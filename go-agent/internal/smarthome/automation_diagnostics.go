package smarthome

import (
	"sort"
	"strings"
	"time"
)

// All text in durable findings is a fixed reason/action code. Entity and rule
// scopes travel with every piece of evidence for A10's independent projection.
type DiagnosticEvidenceRef struct {
	Source     string        `json:"source"`
	Ref        string        `json:"ref,omitempty"`
	RuleRef    AutomationRef `json:"rule_ref"`
	EntityIDs  []string      `json:"entity_ids"`
	ObservedAt time.Time     `json:"observed_at"`
	Window     *TimeWindow   `json:"window,omitempty"`
}
type DiagnosticCheck struct {
	Key                 string                  `json:"key"`
	Status              string                  `json:"status"`
	Category            string                  `json:"category"`
	Severity            string                  `json:"severity"`
	Reason              string                  `json:"reason"`
	ConfidenceBasis     string                  `json:"confidence_basis"`
	SuggestedNextAction string                  `json:"suggested_next_action"`
	EvidenceRefs        []DiagnosticEvidenceRef `json:"evidence_refs"`
}
type DiagnosticChange struct {
	Kind  string          `json:"kind"`
	Check DiagnosticCheck `json:"check"`
}
type DiagnosticSnapshot struct {
	ManagedVersions map[string]DiagnosticManagedVersion `json:"managed_versions,omitempty"`
	SchemaVersion   int                                 `json:"schema_version"`
	ID              string                              `json:"id"`
	Ref             AutomationRef                       `json:"ref"`
	Ownership       string                              `json:"ownership"`
	CheckedAt       time.Time                           `json:"checked_at"`
	Window          TimeWindow                          `json:"window"`
	HomeTimezone    string                              `json:"home_timezone,omitempty"`
	Sources         map[string]DiagnosticSource         `json:"sources"`
	Checks          []DiagnosticCheck                   `json:"checks"`
	Summary         string                              `json:"summary"`
	Changes         []DiagnosticChange                  `json:"changes"`
}

// Internal provenance for one serialized observation generation. This is never
// a member-facing grant, content digest, or proof of physical execution.
type DiagnosticManagedVersion struct {
	OperationID       string `json:"operation_id"`
	SuggestionVersion string `json:"suggestion_version"`
}
type DiagnosticEntity struct {
	EntityID            string
	Source              DiagnosticSource
	Availability        string
	ConditionReferenced bool
	ConditionMet        *bool
}
type DiagnosticPeer struct {
	Ref      AutomationRef
	EntityID string
	Source   DiagnosticSource
	Config   *AutomationConfig
}
type AutomationDiagnosticContext struct {
	CheckedAt      time.Time
	Config         *AutomationConfig
	Entities       []DiagnosticEntity
	EntitySource   DiagnosticSource
	Peers          []DiagnosticPeer
	PeerSource     DiagnosticSource
	HomeTimezone   string
	TimezoneSource DiagnosticSource
}

// DiagnosticSummary is exported so permission projections can recompute it
// after removing inaccessible checks; filtering must also recompute Changes.
func DiagnosticSummary(checks []DiagnosticCheck) string {
	pass, unknown := false, false
	for _, c := range checks {
		switch c.Status {
		case "finding":
			return "attention"
		case "pass":
			pass = true
		default:
			unknown = true
		}
	}
	if pass && unknown {
		return "partial"
	}
	if pass {
		return "pass"
	}
	return "unknown"
}

// EvaluateAutomationDiagnostics has no client, store, clock or command access.
// Current config, loaded config, historical execution and physical causality
// intentionally remain independent checks.
func EvaluateAutomationDiagnostics(ref AutomationRef, e AutomationEvidence) DiagnosticSnapshot {
	s := DiagnosticSnapshot{SchemaVersion: 1, Ref: ref, Ownership: e.Ownership, CheckedAt: e.Analysis.CheckedAt.UTC(), Window: e.Window, HomeTimezone: e.Analysis.HomeTimezone, Checks: []DiagnosticCheck{}, Changes: []DiagnosticChange{}, Sources: map[string]DiagnosticSource{
		"state": e.State.DiagnosticSource, "config": {SourceStatus: e.Config.SourceStatus, ObservedAt: e.Config.ObservedAt, CoverageStatus: "point_in_time", ErrorCode: e.Config.ErrorCode}, "trace": e.Trace.DiagnosticSource, "activity": e.Activity, "entities": e.Analysis.EntitySource, "peers": e.Analysis.PeerSource, "timezone": e.Analysis.TimezoneSource,
		"runtime": {SourceStatus: e.RuntimeLoadProof.SourceStatus, ObservedAt: e.RuntimeLoadProof.ObservedAt, CoverageStatus: "point_in_time", ErrorCode: e.RuntimeLoadProof.ErrorCode}}}
	ev := func(source string, at time.Time, ids ...string) DiagnosticEvidenceRef {
		return DiagnosticEvidenceRef{Source: source, RuleRef: ref, EntityIDs: ids, ObservedAt: at.UTC()}
	}
	add := func(key, status, category, severity, reason, basis string, refs ...DiagnosticEvidenceRef) {
		next := "review_evidence"
		if status == "unknown" {
			next = "refresh_sources"
		}
		s.Checks = append(s.Checks, DiagnosticCheck{Key: key, Status: status, Category: category, Severity: severity, Reason: reason, ConfidenceBasis: basis, SuggestedNextAction: next, EvidenceRefs: refs})
	}
	configRef := ev("config", e.Config.ObservedAt, e.EntityID)
	stateRef := ev("state", e.State.ObservedAt, e.EntityID)
	status, reason := "unknown", "existence_unavailable"
	if e.Config.SourceStatus == SourceAvailable && e.Config.Exists != nil {
		if *e.Config.Exists {
			status, reason = "pass", "config_exists"
		} else {
			status, reason = "finding", "config_missing"
		}
	}
	add("existence", status, "configuration", "warning", reason, "current_config", configRef)
	status, reason = "unknown", "state_unavailable"
	if e.State.SourceStatus == SourceAvailable {
		switch e.State.State {
		case "on":
			status, reason = "pass", "enabled_observed"
		case "off":
			status, reason = "pass", "disabled_observed"
		case "unavailable":
			status, reason = "finding", "automation_unavailable"
		}
	}
	add("enabled", status, "state", "info", reason, "current_state", stateRef)
	status, reason = "unknown", "installed_version_unproven"
	if e.Ownership == "managed" && e.Config.SourceStatus == SourceAvailable && e.Config.Exists != nil && *e.Config.Exists && e.Config.Digest != "" && e.RuntimeLoadProof.ExpectedDigest != "" {
		status, reason = "pass", "installed_version_matches"
		if e.Config.Digest != e.RuntimeLoadProof.ExpectedDigest {
			status, reason = "finding", "config_drift"
		}
	}
	add("drift", status, "configuration", "warning", reason, "installed_digest", configRef)
	status, reason = "unknown", "runtime_version_unproven"
	if e.RuntimeLoadProof.Verified {
		status, reason = "pass", "runtime_version_matches"
	}
	add("runtime", status, "configuration", "info", reason, "current_loaded_config", ev("runtime", e.RuntimeLoadProof.ObservedAt, e.EntityID))
	if len(e.Analysis.Entities) == 0 {
		add("references", "unknown", "reference", "info", "references_unavailable", "current_config", configRef)
	}
	for _, entity := range e.Analysis.Entities {
		status, reason = "unknown", "entity_state_unavailable"
		if entity.Source.SourceStatus == SourceAvailable {
			switch entity.Availability {
			case "available":
				status, reason = "pass", "entity_available"
			case "unavailable":
				status, reason = "finding", "entity_unavailable"
			case "missing":
				status, reason = "finding", "entity_missing"
			}
		}
		add("entity:"+entity.EntityID, status, "reference", "warning", reason, "current_entity_state", ev("entity", entity.Source.ObservedAt, entity.EntityID))
		if entity.ConditionReferenced {
			status, reason = "unknown", "current_condition_unavailable"
			if entity.ConditionMet != nil {
				status, reason = "pass", "current_condition_not_met"
				if *entity.ConditionMet {
					reason = "current_condition_met"
				}
			}
			add("condition:"+entity.EntityID, status, "condition", "info", reason, "current_condition_state", configRef, ev("entity", entity.Source.ObservedAt, entity.EntityID))
		}
	}
	if e.Analysis.EntitySource.CoverageStatus == "read_limit" {
		add("references_coverage", "unknown", "coverage", "info", "reference_scope_incomplete", "bounded_entity_selection", configRef)
	}
	runs := 0
	for _, r := range e.Trace.Runs {
		if r.Start.Before(e.Window.Start) || !r.Start.Before(e.Window.End) {
			continue
		}
		runs++
		status, reason = "unknown", "run_outcome_unknown"
		severity := "info"
		switch r.Outcome {
		case "action_failed", "execution_error":
			status, reason, severity = "finding", r.Outcome, "error"
		case "completed", "conditions_not_met", "not_triggered":
			status, reason = "pass", r.Outcome
		case "running", "cancelled", "concurrency_blocked":
			reason = r.Outcome
		}
		er := ev("trace", r.Start, e.EntityID)
		er.Ref = r.Ref
		w := e.Window
		er.Window = &w
		add("run:"+r.Ref, status, "execution", severity, reason, "historical_trace", er)
	}
	if runs == 0 {
		add("execution", "unknown", "execution", "info", "run_evidence_missing", "historical_trace", ev("trace", e.Trace.ObservedAt, e.EntityID))
	}
	// No trusted enabled/delay/cadence contract exists in this version. Retained
	// traces also never prove a complete execution journal, even when empty.
	reason = "expectation_contract_missing"
	if e.Analysis.Config != nil {
		reason = scheduleDiagnosticReason(*e.Analysis.Config, e.Analysis.HomeTimezone, e.Window)
	}
	add("expected_run", "unknown", "schedule", "info", reason, "schedule_prerequisites", configRef, ev("timezone", e.Analysis.TimezoneSource.ObservedAt))
	add("causality", "unknown", "causality", "info", "causality_unproven", "activity_attribution", ev("activity", e.Activity.ObservedAt, e.EntityID))
	for _, p := range e.Analysis.Peers {
		status, reason = "unknown", "comparison_unavailable"
		if e.Analysis.Config != nil && p.Source.SourceStatus == SourceAvailable && p.Config != nil {
			status, reason = compareDiagnosticConfigs(*e.Analysis.Config, *p.Config)
		}
		pe := DiagnosticEvidenceRef{Source: "peer_config", RuleRef: p.Ref, EntityIDs: diagnosticConfigEntities(p.Config), ObservedAt: p.Source.ObservedAt.UTC()}
		own := configRef
		own.EntityIDs = diagnosticConfigEntities(e.Analysis.Config)
		add("conflict:"+p.Ref.Kind+":"+p.Ref.ID, status, "conflict", "warning", reason, "static_configuration", own, pe)
	}
	add("conflict_coverage", "unknown", "coverage", "info", "peer_scope_incomplete", "bounded_peer_selection", ev("peers", e.Analysis.PeerSource.ObservedAt))
	s.Summary = DiagnosticSummary(s.Checks)
	return s
}

func diagnosticConfigEntities(c *AutomationConfig) []string {
	ids := []string{}
	if c == nil {
		return ids
	}
	for _, t := range c.Trigger {
		if t.EntityID != "" {
			ids = append(ids, t.EntityID)
		}
	}
	for _, condition := range c.Condition {
		if condition.EntityID != "" {
			ids = append(ids, condition.EntityID)
		}
	}
	for _, a := range c.Action {
		ids = append(ids, a.Target.EntityID)
	}
	ids = uniqueEntityIDs(ids)
	sort.Strings(ids)
	return ids
}

func compareDiagnosticConfigs(a, b AutomationConfig) (string, string) {
	opposite := false
	for _, x := range a.Action {
		for _, y := range b.Action {
			xDomain, xAction, _ := strings.Cut(x.Service, ".")
			yDomain, yAction, _ := strings.Cut(y.Service, ".")
			if x.Target.EntityID == y.Target.EntityID && xDomain == yDomain && ((xAction == "turn_on" && yAction == "turn_off") || (xAction == "turn_off" && yAction == "turn_on")) {
				opposite = true
			}
		}
	}
	if !opposite {
		return "pass", "no_conflict_in_comparison"
	}
	overlap, incomparable := false, false
	for _, x := range a.Trigger {
		for _, y := range b.Trigger {
			if x.Platform == "time" && y.Platform == "time" {
				tx, ex := parseDiagnosticClock(x.At)
				ty, ey := parseDiagnosticClock(y.At)
				overlap = overlap || (ex == nil && ey == nil && tx == ty)
			} else if x.Platform == "state" && y.Platform == "state" && x.EntityID == y.EntityID {
				overlap = overlap || (x.To == y.To && (x.From == y.From || x.From == "" || y.From == ""))
			} else {
				incomparable = true
			}
		}
	}
	if !overlap {
		if incomparable {
			return "unknown", "possible_conflict"
		}
		return "pass", "no_conflict_in_comparison"
	}
	conditions := map[string]string{}
	for _, set := range [][]AutomationCondition{a.Condition, b.Condition} {
		for _, c := range set {
			if c.Condition != "state" {
				return "unknown", "possible_conflict"
			}
			if old, ok := conditions[c.EntityID]; ok && old != c.State {
				return "pass", "no_conflict_in_comparison"
			}
			conditions[c.EntityID] = c.State
		}
	}
	return "finding", "opposite_actions_same_scope"
}
func parseDiagnosticClock(s string) (int, error) {
	t, err := time.Parse("15:04:05", s)
	if err != nil {
		t, err = time.Parse("15:04", s)
	}
	return t.Hour()*3600 + t.Minute()*60 + t.Second(), err
}
func scheduleDiagnosticReason(c AutomationConfig, zone string, w TimeWindow) string {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return "timezone_unavailable"
	}
	if len(c.Trigger) != 1 {
		return "schedule_not_comparable"
	}
	tr := c.Trigger[0]
	if tr.Platform == "state" {
		return "state_trigger_history_unproven"
	}
	if tr.Platform != "time" {
		return "schedule_not_comparable"
	}
	seconds, err := parseDiagnosticClock(tr.At)
	if err != nil {
		return "schedule_not_comparable"
	}
	// The same wall clock may be skipped/repeated at DST. Enumerate offsets
	// around each local day and require one actual matching instant.
	start := w.Start.In(loc)
	day := time.Date(start.Year(), start.Month(), start.Day(), 12, 0, 0, 0, loc)
	for n := 0; n < 16 && day.Before(w.End.Add(24*time.Hour)); n++ {
		hits := map[int64]bool{}
		y, m, d := day.Date()
		candidate := time.Date(y, m, d, seconds/3600, (seconds%3600)/60, seconds%60, 0, loc)
		for delta := -3 * time.Hour; delta <= 3*time.Hour; delta += 15 * time.Minute {
			t := candidate.Add(delta)
			lt := t.In(loc)
			if lt.Year() == y && lt.Month() == m && lt.Day() == d && lt.Hour()*3600+lt.Minute()*60+lt.Second() == seconds {
				hits[t.Unix()] = true
			}
		}
		if len(hits) != 1 {
			return "schedule_dst_ambiguous"
		}
		day = day.AddDate(0, 0, 1)
	}
	return "schedule_prerequisites_unproven"
}
