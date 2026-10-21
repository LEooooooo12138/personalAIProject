package smarthome

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
)

// DiagnosticSource separates endpoint availability from temporal completeness.
// CoveredWindows is empty unless a source actually proves continuous coverage.
// A retained trace range is never such proof.
type DiagnosticSource struct {
	ReadLimit      int          `json:"read_limit,omitempty"`
	ReadCount      int          `json:"read_count,omitempty"`
	SourceStatus   SourceStatus `json:"source_status"`
	ErrorCode      string       `json:"error_code,omitempty"`
	ObservedAt     time.Time    `json:"observed_at"`
	CoverageStatus string       `json:"coverage_status"`
	CoveredWindows []TimeWindow `json:"covered_windows"`
	EventRefs      []string     `json:"event_refs"`
}
type AutomationStateEvidence struct {
	DiagnosticSource
	State string `json:"state,omitempty"`
}
type AutomationTraceEvidence struct {
	DiagnosticSource
	Runs []AutomationRunEvidence `json:"runs"`
	// Span of the retained run timestamps, NOT an interval of complete coverage.
	ObservedSpan *TimeWindow `json:"observed_span,omitempty"`
}

// AutomationRunEvidence is an allowlisted historical fact, not current load,
// physical success, a raw trace download, or proof of natural trigger origin.
type AutomationRunEvidence struct {
	Ref          string       `json:"ref"`
	RunID        string       `json:"run_id"`
	Start        time.Time    `json:"start"`
	Finish       *time.Time   `json:"finish,omitempty"`
	Outcome      string       `json:"outcome"`
	ConfigDigest string       `json:"config_digest,omitempty"`
	ConfigStatus SourceStatus `json:"config_status"`
}
type AutomationEvidence struct {
	// Analysis is transient bounded composition, never raw member-facing output.
	Analysis  AutomationDiagnosticContext `json:"-"`
	Ref       AutomationRef               `json:"ref"`
	Window    TimeWindow                  `json:"window"`
	Ownership string                      `json:"ownership"`
	EntityID  string                      `json:"entity_id,omitempty"`
	State     AutomationStateEvidence     `json:"state"`
	// RawConfig remains transient and JSON-omitted, for a later bounded evaluator.
	Config           AutomationConfigObservation `json:"config"`
	RuntimeLoadProof RuntimeLoadProof            `json:"runtime_load_proof"`
	Trace            AutomationTraceEvidence     `json:"trace"`
	Activity         DiagnosticSource            `json:"activity"`
}

const maxDiagnosticRuns = 32
const maxDiagnosticList = 256
const maxDiagnosticBytes = 8 << 20

func emptyDiagnosticSource(status SourceStatus, code, coverage string) DiagnosticSource {
	return DiagnosticSource{SourceStatus: status, ErrorCode: code, CoverageStatus: coverage, CoveredWindows: []TimeWindow{}, EventRefs: []string{}}
}

// ReadAutomationEvidence inherits ObserveAutomation's fresh, server-observed
// selection boundary (including future A7 managed-record resolution). It never
// accepts a URL, config key, or run ID directly from a request.
func (m *Manager) ReadAutomationEvidence(ctx context.Context, ref AutomationRef, window TimeWindow) (AutomationEvidence, error) {
	e := AutomationEvidence{Ref: ref, Window: TimeWindow{Start: window.Start.UTC(), End: window.End.UTC()}, Ownership: "unverified",
		State:    AutomationStateEvidence{DiagnosticSource: emptyDiagnosticSource(SourceUnavailable, "", "point_in_time")},
		Config:   AutomationConfigObservation{SourceStatus: SourceUnavailable, Source: "config/automation/config"},
		Trace:    AutomationTraceEvidence{DiagnosticSource: emptyDiagnosticSource(SourceUnavailable, "ha_mapping_unknown", "retention_unknown"), Runs: []AutomationRunEvidence{}},
		Activity: emptyDiagnosticSource(SourceUnsupported, "ha_activity_attribution_unsupported", "unknown")}
	if validateHistoryWindow(window) != nil || window.End.Sub(window.Start) > 14*24*time.Hour {
		return e, newHAError("ha_invalid_window")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	observation, observationErr := m.ObserveAutomation(ctx, ref)
	e.Ownership = observation.Ownership
	e.EntityID = observation.EntityID
	e.Config = observation.Config
	e.RuntimeLoadProof = observation.RuntimeLoadProof
	e.State.State = observation.State
	e.State.SourceStatus = observation.StateStatus
	e.State.ErrorCode = observation.StateErrorCode
	e.State.ObservedAt = observation.StateObservedAt
	// Automation state is a closed vocabulary; arbitrary upstream values must
	// not turn a summary field into an unfiltered private-text channel.
	if e.State.SourceStatus == SourceAvailable {
		switch e.State.State {
		case "on", "off", "unknown", "unavailable":
		default:
			e.State.State = ""
			e.State.SourceStatus = SourceUnavailable
			e.State.ErrorCode = "ha_invalid_response"
		}
	}
	// Mapping is independent of current config/runtime availability, but its
	// exact reviewed profile and unique tuple must be established before tracing.
	if observation.MappingStatus != SourceAvailable || observation.MappingErrorCode != "" || observation.MappingSource != "config/entity_registry/list" || !validAutomationKey(observation.Config.ConfigKey) {
		if observationErr != nil {
			e.Trace.ErrorCode = HAErrorCode(observationErr)
			e.Trace.SourceStatus = observationStatus(observationErr)
		}
		return e, observationErr
	}
	var err error
	e.Trace, err = m.client.readAutomationTraceEvidence(ctx, observation.Config.ConfigKey, e.Window)
	return e, errors.Join(observationErr, err)
}

func (c *HomeAssistantClient) readAutomationTraceEvidence(ctx context.Context, key string, window TimeWindow) (AutomationTraceEvidence, error) {
	out := AutomationTraceEvidence{DiagnosticSource: emptyDiagnosticSource(SourceUnavailable, "", "retention_unknown"), Runs: []AutomationRunEvidence{}}
	fail := func(err error) (AutomationTraceEvidence, error) {
		out.SourceStatus = observationStatus(err)
		if HAErrorCode(err) == "ha_not_found" {
			out.SourceStatus = SourceRetentionGap
			out.CoverageStatus = "retention_gap"
		}
		out.ErrorCode = HAErrorCode(err)
		out.ObservedAt = time.Now().UTC()
		return out, err
	}
	release, err := c.acquireObservation(ctx)
	if err != nil {
		return fail(err)
	}
	defer release()
	if _, err = c.automationProfile(ctx); err != nil {
		return fail(err)
	}
	session, closeSession, err := c.registrySession(ctx)
	if err != nil {
		return fail(err)
	}
	defer closeSession()
	var raw json.RawMessage
	if err = session.request("trace/list", map[string]any{"domain": "automation", "item_id": key}, &raw); err != nil {
		return fail(err)
	}
	used := len(raw)
	var list []traceRecord
	if err = decodeDiagnosticJSON(raw, &list); err != nil || list == nil {
		return fail(newHAError("ha_invalid_response"))
	}
	if len(list) > maxDiagnosticList {
		out.CoverageStatus = "read_limit"
		return fail(newHAError("ha_trace_read_limit"))
	}
	seen := map[string]bool{}
	selected := []traceRecord{}
	for _, item := range list {
		if !validTraceIdentity(item, key) || seen[item.RunID] {
			return fail(newHAError("ha_invalid_response"))
		}
		seen[item.RunID] = true
		if !item.Timestamp.Start.Before(window.Start) && item.Timestamp.Start.Before(window.End) {
			selected = append(selected, item)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Timestamp.Start.After(selected[j].Timestamp.Start) })
	out.SourceStatus = SourceAvailable
	if len(selected) > maxDiagnosticRuns {
		selected = selected[:maxDiagnosticRuns]
		out.CoverageStatus = "read_limit"
	}
	var readErrors []error
	for _, item := range selected {
		if err = session.request("trace/get", map[string]any{"domain": "automation", "item_id": key, "run_id": item.RunID}, &raw); err != nil {
			if HAErrorCode(err) == "ha_not_found" {
				out.SourceStatus = SourceRetentionGap
				out.CoverageStatus = "retention_gap"
				out.ErrorCode = "ha_not_found"
				readErrors = append(readErrors, err)
				continue
			}
			return fail(err)
		}
		used += len(raw)
		if used > maxDiagnosticBytes {
			out.CoverageStatus = "read_limit"
			return fail(newHAError("ha_trace_read_limit"))
		}
		var detail traceRecord
		if err = decodeDiagnosticJSON(raw, &detail); err != nil || !validTraceIdentity(detail, key) || detail.RunID != item.RunID || !detail.Timestamp.Start.Equal(item.Timestamp.Start) || detail.Trace == nil {
			return fail(newHAError("ha_invalid_response"))
		}
		run := summarizeAutomationTrace(detail, key)
		out.Runs = append(out.Runs, run)
		out.EventRefs = append(out.EventRefs, run.Ref)
		end := run.Start
		if run.Finish != nil {
			end = *run.Finish
		}
		if end.After(window.End) {
			end = window.End
		}
		if out.ObservedSpan == nil {
			out.ObservedSpan = &TimeWindow{Start: run.Start, End: end}
		} else {
			if run.Start.Before(out.ObservedSpan.Start) {
				out.ObservedSpan.Start = run.Start
			}
			if end.After(out.ObservedSpan.End) {
				out.ObservedSpan.End = end
			}
		}
	}
	out.ObservedAt = time.Now().UTC()
	return out, errors.Join(readErrors...)
}

// Private wire types discard variables, trigger descriptions, context/user IDs,
// blueprint inputs and arbitrary results. Error strings are presence-only facts.
type traceRecord struct {
	RunID        string  `json:"run_id"`
	Domain       string  `json:"domain"`
	ItemID       string  `json:"item_id"`
	State        string  `json:"state"`
	Execution    string  `json:"script_execution"`
	NotTriggered bool    `json:"not_triggered"`
	Error        *string `json:"error"`
	Timestamp    struct {
		Start  time.Time  `json:"start"`
		Finish *time.Time `json:"finish"`
	} `json:"timestamp"`
	Config json.RawMessage `json:"config"`
	Trace  map[string][]struct {
		Error          *string  `json:"error"`
		TemplateErrors []string `json:"template_errors"`
	} `json:"trace"`
}

func validTraceIdentity(t traceRecord, key string) bool {
	if t.Domain != "automation" || t.ItemID != key || len(t.RunID) != 32 || t.Timestamp.Start.IsZero() || (t.State != "running" && t.State != "stopped") {
		return false
	}
	if _, err := hex.DecodeString(t.RunID); err != nil {
		return false
	}
	if t.Timestamp.Finish != nil && t.Timestamp.Finish.Before(t.Timestamp.Start) {
		return false
	}
	return t.State != "stopped" || t.Timestamp.Finish != nil
}
func decodeDiagnosticJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if _, err := decodeAutomationJSON(d, 0); err != nil {
		return newHAError("ha_invalid_response")
	}
	if _, err := d.Token(); err != io.EOF {
		return newHAError("ha_invalid_response")
	}
	if json.Unmarshal(raw, out) != nil {
		return newHAError("ha_invalid_response")
	}
	return nil
}
func summarizeAutomationTrace(t traceRecord, key string) AutomationRunEvidence {
	r := AutomationRunEvidence{Ref: "trace:" + t.RunID, RunID: t.RunID, Start: t.Timestamp.Start.UTC(), Finish: t.Timestamp.Finish, Outcome: "unknown", ConfigStatus: SourceUnavailable}
	if t.Timestamp.Finish != nil {
		finish := t.Timestamp.Finish.UTC()
		r.Finish = &finish
	}
	if raw, digest, err := canonicalAutomationConfig(t.Config); err == nil && configHasID(raw, key) {
		r.ConfigStatus = SourceAvailable
		r.ConfigDigest = digest
	}
	actionError, otherError := false, t.Error != nil
	for path, nodes := range t.Trace {
		for _, node := range nodes {
			if node.Error != nil || len(node.TemplateErrors) > 0 {
				if strings.HasPrefix(path, "action/") {
					actionError = true
				} else {
					otherError = true
				}
			}
		}
	}
	switch {
	case actionError:
		r.Outcome = "action_failed"
	case otherError:
		r.Outcome = "execution_error"
	case t.NotTriggered:
		r.Outcome = "not_triggered"
	case t.State == "running":
		r.Outcome = "running"
	case t.Execution == "failed_conditions":
		r.Outcome = "conditions_not_met"
	case t.Execution == "error":
		r.Outcome = "execution_error"
	case t.Execution == "finished":
		r.Outcome = "completed"
	case t.Execution == "cancelled":
		r.Outcome = "cancelled"
	case t.Execution == "failed_single" || t.Execution == "failed_max_runs":
		r.Outcome = "concurrency_blocked"
	}
	return r
}
