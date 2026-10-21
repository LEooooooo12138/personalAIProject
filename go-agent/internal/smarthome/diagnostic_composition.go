package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxDiagnosticEntities = 16
const maxDiagnosticPeers = 8
const maxDiagnosticConfigBytes = 64 << 10

// A non-nil error matching this sentinel is accompanied by a usable, durably
// saved snapshot. Other failures (invalid target, cancellation, store failure)
// must not be treated as completed diagnostics by API consumers.
var ErrDiagnosticSourcesPartial = errors.New("diagnostic_sources_partial")

// supportedDiagnosticConfig reuses the strict builder's semantic model. Unknown
// fields (including templates, devices, action data and plural syntax) are not
// dropped to manufacture comparability. Complete digests stay in observation.
func supportedDiagnosticConfig(o AutomationConfigObservation) *AutomationConfig {
	if o.SourceStatus != SourceAvailable || o.Exists == nil || !*o.Exists || len(o.RawConfig) > maxDiagnosticConfigBytes {
		return nil
	}
	var c AutomationConfig
	canonical, _, err := canonicalAutomationConfig(o.RawConfig)
	if err != nil || !configHasID(canonical, o.ConfigKey) || json.Unmarshal(canonical, &c) != nil || validateAutomation(c) != nil || len(c.Trigger) > 32 || len(c.Condition) > 32 || len(c.Action) > 32 {
		return nil
	}
	for _, tr := range c.Trigger {
		if strings.ContainsAny(tr.From+tr.To, "{}") || len(tr.From) > 255 || len(tr.To) > 255 {
			return nil
		}
	}
	for _, condition := range c.Condition {
		if strings.ContainsAny(condition.State, "{}") || len(condition.State) > 255 {
			return nil
		}
	}
	c.Alias = ""
	c.Description = ""
	return &c
}

// DiagnoseAutomation is synchronous, lifecycle/caller-cancellable and read-only.
// It never accepts peer IDs, URLs, config keys or trace run IDs from callers.
// Failure of a source preserves independent usable facts in the saved snapshot.
func (m *Manager) DiagnoseAutomation(ctx context.Context, ref AutomationRef, window TimeWindow) (DiagnosticSnapshot, error) {
	if !validDiagnosticRef(ref) || validateHistoryWindow(window) != nil || window.End.Sub(window.Start) > 14*24*time.Hour {
		return DiagnosticSnapshot{}, newHAError("ha_invalid_target")
	}
	ctx, finish, err := m.acquireJob(JobScope{Context: ctx, OwnerID: "diagnostic"})
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	releaseAction, err := m.acquireDiagnosticAction(ctx)
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	defer releaseAction()
	e, readErr := m.ReadAutomationEvidence(ctx, ref, window)
	// On an unreviewed profile ObserveAutomation returns the profile error,
	// while recording registry absence separately in State.ErrorCode. Never
	// let that earlier error hide rejected selection and create a saved scope.
	if e.State.ErrorCode == "ha_target_not_observed" {
		return DiagnosticSnapshot{}, errors.Join(newHAError("ha_target_not_observed"), readErr)
	}
	if code := HAErrorCode(readErr); code == "ha_invalid_target" || code == "ha_target_not_observed" || code == "ha_managed_record_unavailable" {
		return DiagnosticSnapshot{}, readErr
	}
	if !diagnosticScopeSelected(ref, e) {
		return DiagnosticSnapshot{}, errors.Join(newHAError("ha_target_selection_unavailable"), readErr)
	}
	compositionErr := m.composeAutomationDiagnosis(ctx, &e)
	e.Analysis.CheckedAt = time.Now().UTC()
	snapshot := EvaluateAutomationDiagnostics(ref, e)
	if err = m.bindDiagnosticVersions(&snapshot, e.Analysis.Peers); err != nil {
		return snapshot, err
	}
	snapshot.ID, err = randomAnalysisID()
	if err != nil {
		return DiagnosticSnapshot{}, errors.New("diagnostic_id_unavailable")
	}
	if err = ctx.Err(); err != nil {
		return snapshot, err
	}
	snapshot, saveErr := m.store.saveDiagnosticSnapshot(snapshot)
	if saveErr != nil {
		return snapshot, saveErr
	}
	if sourceErr := errors.Join(readErr, compositionErr); sourceErr != nil {
		return snapshot, errors.Join(ErrDiagnosticSourcesPartial, sourceErr)
	}
	return snapshot, nil
}

func diagnosticScopeSelected(ref AutomationRef, e AutomationEvidence) bool {
	if e.Ref != ref {
		return false
	}
	if ref.Kind == "managed" {
		return e.Ownership == "managed"
	}
	// Supported-profile observation assigns external ownership only after an
	// exact registry match. On an unsupported profile it does not assign that
	// ownership, but it attempts a state read only after the same exact match.
	// The attempt timestamp survives a denied/failed state read, preserving
	// useful partial diagnostics without equating state availability to scope.
	return e.Ownership == "external" || !e.State.ObservedAt.IsZero()
}

func (m *Manager) composeAutomationDiagnosis(ctx context.Context, e *AutomationEvidence) error {
	a := &e.Analysis
	a.Config = supportedDiagnosticConfig(e.Config)
	a.EntitySource = emptyDiagnosticSource(SourceUnsupported, "diagnostic_config_unsupported", "unknown")
	a.PeerSource = emptyDiagnosticSource(SourceUnavailable, "", "unknown")
	a.TimezoneSource = emptyDiagnosticSource(SourceUnavailable, "", "point_in_time")
	var errs []error
	release, err := m.client.acquireObservation(ctx)
	if err != nil {
		return err
	}
	loc, err := m.homeLocation(ctx)
	a.TimezoneSource.ObservedAt = time.Now().UTC()
	if err == nil {
		a.HomeTimezone = loc.String()
		a.TimezoneSource.SourceStatus = SourceAvailable
	} else {
		a.TimezoneSource.ErrorCode = "diagnostic_timezone_unavailable"
		errs = append(errs, newHAError("diagnostic_timezone_unavailable"))
	}
	if a.Config != nil {
		a.EntitySource = emptyDiagnosticSource(SourceAvailable, "", "selected_config_references")
		a.EntitySource.ReadLimit = maxDiagnosticEntities
		ids := diagnosticConfigEntities(a.Config)
		if len(ids) > maxDiagnosticEntities {
			ids = ids[:maxDiagnosticEntities]
			a.EntitySource.CoverageStatus = "read_limit"
		}
		for _, id := range ids {
			source := emptyDiagnosticSource(SourceAvailable, "", "point_in_time")
			availability := "unknown"
			state, err := m.client.GetState(ctx, id)
			source.ObservedAt = time.Now().UTC()
			if err != nil {
				source.SourceStatus = observationStatus(err)
				source.ErrorCode = HAErrorCode(err)
				var httpErr *haHTTPStatusError
				if errors.As(err, &httpErr) && httpErr.status == http.StatusNotFound {
					source.SourceStatus = SourceAvailable
					availability = "missing"
				} else {
					errs = append(errs, err)
				}
			} else {
				switch state.State {
				case "unknown", "":
					availability = "unknown"
				case "unavailable":
					availability = "unavailable"
				default:
					availability = "available"
				}
			}
			entity := DiagnosticEntity{EntityID: id, Source: source, Availability: availability}
			for _, condition := range a.Config.Condition {
				if condition.Condition == "state" && condition.EntityID == id {
					entity.ConditionReferenced = true
					if availability == "available" {
						matched := state.State == condition.State
						if entity.ConditionMet != nil {
							matched = matched && *entity.ConditionMet
						}
						entity.ConditionMet = &matched
					}
				}
			}
			a.Entities = append(a.Entities, entity)
		}
		a.EntitySource.ObservedAt = time.Now().UTC()
		a.EntitySource.ReadCount = len(a.Entities)
	}
	// Registry is the existing bounded server-observed source (10k entries / 4MiB
	// WS frame). Fetch at most eight current peer configs; never scan all states.
	registry, err := m.client.readAutomationRegistry(ctx)
	release()
	a.PeerSource.ObservedAt = time.Now().UTC()
	if err != nil {
		a.PeerSource.SourceStatus = observationStatus(err)
		a.PeerSource.ErrorCode = HAErrorCode(err)
		return errors.Join(append(errs, err)...)
	}
	sort.Slice(registry, func(i, j int) bool { return registry[i].EntityID < registry[j].EntityID })
	candidates := []RegistryEntity{}
	for _, r := range registry {
		if r.EntityID != e.EntityID {
			candidates = append(candidates, r)
		}
	}
	a.PeerSource.SourceStatus = SourceAvailable
	a.PeerSource.ReadLimit = maxDiagnosticPeers
	a.PeerSource.CoverageStatus = "bounded_registry_candidates"
	if len(candidates) > maxDiagnosticPeers {
		candidates = candidates[:maxDiagnosticPeers]
		a.PeerSource.CoverageStatus = "read_limit"
	}
	for _, candidate := range candidates {
		ref := AutomationRef{Kind: "external", ID: candidate.EntityID}
		o, err := m.ObserveAutomation(ctx, ref)
		peer := DiagnosticPeer{Ref: m.managedDiagnosticPeerRef(ref, o), EntityID: o.EntityID, Config: supportedDiagnosticConfig(o.Config), Source: DiagnosticSource{SourceStatus: o.Config.SourceStatus, ObservedAt: o.Config.ObservedAt, CoverageStatus: "point_in_time", ErrorCode: o.Config.ErrorCode}}
		if peer.Config == nil && peer.Source.SourceStatus == SourceAvailable {
			peer.Source.SourceStatus = SourceUnsupported
			peer.Source.ErrorCode = "diagnostic_config_unsupported"
		}
		a.Peers = append(a.Peers, peer)
		a.PeerSource.ReadCount = len(a.Peers)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validDiagnosticRef(ref AutomationRef) bool {
	if ref.Kind == "external" || ref.Kind == "unverified" {
		return validAutomationEntity(ref.ID)
	}
	return ref.Kind == "managed" && ref.ID != "" && len(ref.ID) <= 255 && validateHistoryScope(ref.ID) == nil
}
