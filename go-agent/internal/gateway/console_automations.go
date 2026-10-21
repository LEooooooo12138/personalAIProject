package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type consoleDiagnosticEvidence struct {
	Source     string                  `json:"source"`
	RuleRef    smarthome.AutomationRef `json:"rule_ref"`
	EntityIDs  []string                `json:"entity_ids"`
	ObservedAt time.Time               `json:"observed_at"`
}
type consoleDiagnosticCheck struct {
	Status       string                      `json:"status"`
	Category     string                      `json:"category"`
	Severity     string                      `json:"severity"`
	Reason       string                      `json:"reason"`
	NextAction   string                      `json:"next_action"`
	EvidenceRefs []consoleDiagnosticEvidence `json:"evidence_refs"`
}
type consoleDiagnosticChange struct {
	Kind  string                 `json:"kind"`
	Check consoleDiagnosticCheck `json:"check"`
}
type consoleDiagnosticSource struct {
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
}
type consoleDiagnosticView struct {
	Historical bool                               `json:"historical"`
	ID         string                             `json:"id"`
	Ref        smarthome.AutomationRef            `json:"ref"`
	CheckedAt  time.Time                          `json:"checked_at"`
	Window     smarthome.TimeWindow               `json:"window"`
	Summary    string                             `json:"summary"`
	Checks     []consoleDiagnosticCheck           `json:"checks"`
	Changes    []consoleDiagnosticChange          `json:"changes"`
	Counts     map[string]int                     `json:"counts"`
	Sources    map[string]consoleDiagnosticSource `json:"sources"`
}
type consoleAutomationView struct {
	// Failed attempts establish staleness, never an observed state/time. Internal only.
	failedStateAt      *time.Time
	ID                 string                    `json:"id"`
	SuggestionID       string                    `json:"suggestion_id"`
	CurrentVersion     string                    `json:"current_version,omitempty"`
	ObservationVersion string                    `json:"observation_version,omitempty"`
	DeletedAt          *time.Time                `json:"deleted_at,omitempty"`
	Rule               *consoleSuggestionRule    `json:"rule,omitempty"`
	Entities           []consoleSuggestionEntity `json:"entities"`
	Ref                *smarthome.AutomationRef  `json:"ref,omitempty"`
	Title              string                    `json:"title"`
	Ownership          string                    `json:"ownership"`
	InstallationState  string                    `json:"installation_state"`
	ObservedState      string                    `json:"observed_state"`
	ObservedAt         *time.Time                `json:"observed_at,omitempty"`
	Stale              bool                      `json:"stale"`
	Diagnostic         *consoleDiagnosticView    `json:"diagnostic,omitempty"`
	CanDiagnose        bool                      `json:"can_diagnose"`
	HAURL              string                    `json:"ha_url,omitempty"`
}
type consoleAutomationCandidate struct {
	Ref   smarthome.AutomationRef `json:"ref"`
	Title string                  `json:"title"`
	HAURL string                  `json:"ha_url,omitempty"`
}
type consoleAutomationScope struct {
	admin      bool
	sharing    console.Sharing
	directory  map[string]consoleSuggestionEntity
	allowed    map[string]map[string]bool
	versions   map[string]smarthome.DiagnosticManagedVersion
	rows       []consoleAutomationView
	candidates []consoleAutomationCandidate
}

func consoleAutomationURL(base string) string {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	// The supported native automation list avoids guessing a config/entity route.
	u.Path = strings.TrimRight(u.Path, "/") + "/config/automation/dashboard"
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return u.String()
}

func (s *Server) consoleAutomationScope(ctx context.Context, admin bool) (consoleAutomationScope, error) {
	p := consoleAutomationScope{admin: admin, allowed: map[string]map[string]bool{}, versions: map[string]smarthome.DiagnosticManagedVersion{}, rows: []consoleAutomationView{}, candidates: []consoleAutomationCandidate{}}
	var err error
	p.sharing, err = s.consoleStore.GetSharing()
	if err != nil {
		return p, err
	}
	p.directory, err = s.consoleSuggestionDirectory(ctx)
	if err != nil && !admin {
		return p, smarthome.ErrCatalogUnavailable
	}
	items, err := s.smartHome.GetStore().GetSuggestions()
	if err != nil {
		return p, err
	}
	shared, current := map[string]bool{}, map[string]bool{}
	for _, id := range p.sharing.SuggestionIDs {
		shared[id] = true
	}
	for _, id := range p.sharing.EntityIDs {
		if _, ok := p.directory[id]; ok {
			current[id] = true
		}
	}
	managedEntities := map[string]bool{}
	for _, item := range items {
		view, err := s.consoleSuggestionInstallation(item, suggestionProjection(item, admin, shared[item.ID], p.directory), admin)
		if err != nil {
			return p, err
		}
		op, err := s.smartHome.GetInstallOperationForSuggestion(item.ID)
		if err != nil && !errors.Is(err, smarthome.ErrInstallOperationNotFound) {
			return p, err
		}
		if op.ManagedAutomationID == "" {
			if admin && (view.InstallationState != "" || item.Status == "confirmed") {
				p.rows = append(p.rows, consoleAutomationView{ID: item.ID, SuggestionID: item.ID, Title: view.Title, Ownership: "unverified", InstallationState: "unknown", ObservedState: "unknown", Stale: true})
			}
			continue
		}
		managed, currentRule, err := s.smartHome.GetManagedAutomationVersion(op.ManagedAutomationID)
		if err != nil {
			return p, err
		}
		op, err = s.smartHome.GetInstallOperation(managed.OperationID)
		if err != nil {
			return p, err
		}
		if managed.DeletedAt == nil {
			managedEntities[managed.EntityID] = true
		}
		// Durable ownership retains the original shared suggestion anchor even
		// when a later observation makes installation convergence unknown.
		authorized := suggestionProjection(currentRule, admin, shared[item.ID], p.directory)
		authorized.Status = "confirmed"
		if managed.SuggestionID != item.ID || (!admin && !visibleSuggestion(authorized, current)) {
			continue
		}
		entities := map[string]bool{}
		for _, id := range authorized.EntityIDs {
			entities[id] = true
		}
		entities[managed.EntityID] = true
		if managed.DeletedAt == nil {
			p.allowed[managed.ID] = entities
			p.versions[managed.ID] = smarthome.DiagnosticManagedVersion{OperationID: managed.OperationID, SuggestionVersion: managed.SuggestionVersion}
		}
		ref := smarthome.AutomationRef{Kind: "managed", ID: managed.ID}
		row := consoleAutomationView{ID: managed.ID, SuggestionID: item.ID, CurrentVersion: currentRule.ID, Rule: authorized.Rule, Entities: authorized.Entities, Ref: &ref, Title: authorized.Title, Ownership: "managed", InstallationState: "unknown", ObservedState: "unknown", CanDiagnose: admin}
		generation := sha256.Sum256([]byte(managed.OperationID))
		row.ObservationVersion = hex.EncodeToString(generation[:])
		if op.Status == "confirmed" {
			row.InstallationState = "verified"
		}
		if managed.DeletedAt != nil {
			row.DeletedAt = managed.DeletedAt
			row.InstallationState, row.Ownership, row.CanDiagnose, row.Ref, row.Stale = "deleted", "historical", false, nil, true
		}
		o := managed.LastObservation
		if o.StateStatus == smarthome.SourceAvailable && (o.State == "on" || o.State == "off") && !o.StateObservedAt.IsZero() {
			row.ObservedState = o.State
			at := o.StateObservedAt
			row.ObservedAt = &at
		} else {
			at := o.StateObservedAt
			row.failedStateAt = &at
			row.Stale = true
		}
		if admin && managed.DeletedAt == nil {
			row.HAURL = consoleAutomationURL(s.smartHome.GetClient().BaseURL)
		}
		p.rows = append(p.rows, row)
	}
	if admin {
		for id, entity := range p.directory {
			if entity.Domain == "automation" && strings.HasPrefix(id, "automation.") && !managedEntities[id] {
				p.candidates = append(p.candidates, consoleAutomationCandidate{Ref: smarthome.AutomationRef{Kind: "external", ID: id}, Title: entity.Name, HAURL: consoleAutomationURL(s.smartHome.GetClient().BaseURL)})
			}
		}
		sort.Slice(p.candidates, func(i, j int) bool { return p.candidates[i].Ref.ID < p.candidates[j].Ref.ID })
	}
	return p, nil
}

func diagnosticReason(code string) string {
	switch code {
	case "existence_unavailable", "config_exists", "config_missing", "state_unavailable", "enabled_observed", "disabled_observed", "automation_unavailable", "installed_version_unproven", "installed_version_matches", "config_drift", "runtime_version_unproven", "runtime_version_matches", "references_unavailable", "entity_state_unavailable", "entity_available", "entity_unavailable", "entity_missing", "current_condition_unavailable", "current_condition_not_met", "current_condition_met", "reference_scope_incomplete", "run_outcome_unknown", "action_failed", "execution_error", "completed", "conditions_not_met", "not_triggered", "running", "cancelled", "concurrency_blocked", "run_evidence_missing", "expectation_contract_missing", "timezone_unavailable", "schedule_not_comparable", "state_trigger_history_unproven", "schedule_dst_ambiguous", "schedule_prerequisites_unproven", "causality_unproven", "comparison_unavailable", "no_conflict_in_comparison", "possible_conflict", "opposite_actions_same_scope", "peer_scope_incomplete":
		return code
	default:
		return "diagnostic_unavailable"
	}
}
func diagnosticValue(value, fallback string, allowed ...string) string {
	for _, item := range allowed {
		if item == value {
			return value
		}
	}
	return fallback
}
func (p consoleAutomationScope) currentDiagnosticVersion(id string, snapshot smarthome.DiagnosticSnapshot) bool {
	current, ok := p.versions[id]
	observed, bound := snapshot.ManagedVersions[id]
	return ok && bound && current.OperationID != "" && current.SuggestionVersion != "" && current == observed
}
func (p consoleAutomationScope) projectCheck(check smarthome.DiagnosticCheck, snapshot smarthome.DiagnosticSnapshot) (consoleDiagnosticCheck, bool) {
	out := consoleDiagnosticCheck{Status: diagnosticValue(check.Status, "unknown", "pass", "finding", "unknown"), Category: diagnosticValue(check.Category, "evidence", "configuration", "state", "reference", "condition", "coverage", "execution", "schedule", "causality", "conflict"), Severity: diagnosticValue(check.Severity, "info", "info", "warning", "error"), Reason: diagnosticReason(check.Reason), NextAction: "review_evidence", EvidenceRefs: []consoleDiagnosticEvidence{}}
	if out.Reason == "diagnostic_unavailable" {
		out.Status = "unknown"
	}
	if out.Status == "unknown" {
		out.NextAction = "refresh_sources"
	}
	if !p.admin && len(check.EvidenceRefs) == 0 {
		return out, false
	}
	for _, evidence := range check.EvidenceRefs {
		if evidence.RuleRef.Kind == "managed" && !p.currentDiagnosticVersion(evidence.RuleRef.ID, snapshot) {
			return out, false
		}
		if !p.admin {
			entities, ok := p.allowed[evidence.RuleRef.ID]
			if evidence.RuleRef.Kind != "managed" || !ok {
				return out, false
			}
			for _, id := range evidence.EntityIDs {
				if !entities[id] {
					return out, false
				}
			}
		}
		source := diagnosticValue(evidence.Source, "unknown", "config", "state", "runtime", "entity", "trace", "timezone", "activity", "peer_config", "peers")
		out.EvidenceRefs = append(out.EvidenceRefs, consoleDiagnosticEvidence{Source: source, RuleRef: evidence.RuleRef, EntityIDs: append([]string{}, evidence.EntityIDs...), ObservedAt: evidence.ObservedAt})
	}
	return out, true
}
func (p consoleAutomationScope) projectDiagnostic(snapshot smarthome.DiagnosticSnapshot) consoleDiagnosticView {
	out := consoleDiagnosticView{ID: snapshot.ID, Ref: snapshot.Ref, CheckedAt: snapshot.CheckedAt, Window: snapshot.Window, Checks: []consoleDiagnosticCheck{}, Changes: []consoleDiagnosticChange{}, Counts: map[string]int{"pass": 0, "finding": 0, "unknown": 0, "changes": 0}, Sources: map[string]consoleDiagnosticSource{}}
	if snapshot.Ref.Kind == "managed" && !p.currentDiagnosticVersion(snapshot.Ref.ID, snapshot) {
		out.Historical = true
		out.Summary = "unknown"
		return out
	}
	visible := []smarthome.DiagnosticCheck{}
	for _, check := range snapshot.Checks {
		if projected, ok := p.projectCheck(check, snapshot); ok {
			out.Checks = append(out.Checks, projected)
			visible = append(visible, smarthome.DiagnosticCheck{Status: projected.Status})
			out.Counts[projected.Status]++
		}
	}
	for _, change := range snapshot.Changes {
		kind := diagnosticValue(change.Kind, "", "new", "severity_changed", "recovered")
		if projected, ok := p.projectCheck(change.Check, snapshot); ok && kind != "" {
			out.Changes = append(out.Changes, consoleDiagnosticChange{Kind: kind, Check: projected})
		}
	}
	out.Counts["changes"] = len(out.Changes)
	out.Summary = smarthome.DiagnosticSummary(visible)
	for _, name := range []string{"state", "config", "runtime", "trace", "activity", "timezone", "entities", "peers"} {
		if !p.admin && (name == "entities" || name == "peers") {
			continue
		}
		if source, ok := snapshot.Sources[name]; ok {
			out.Sources[name] = consoleDiagnosticSource{Status: diagnosticValue(string(source.SourceStatus), "unavailable", "available", "unsupported", "forbidden", "retention_gap", "unavailable"), ObservedAt: source.ObservedAt}
		}
	}
	return out
}
func (s *Server) consoleAutomationJSON(c *gin.Context, ctx context.Context, revision uint64, value any) {
	if !s.consoleSmartHomeSessionValid(c) {
		return
	}
	err := s.consoleStore.WithSessionSharingRevision(ctx, consoleToken(c.Request), revision, func(context.Context) error { c.JSON(200, value); return nil })
	if err != nil {
		consoleStoreError(c, err)
	}
}
func (s *Server) automationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, smarthome.ErrCatalogUnavailable):
		s.consoleSmartHomeFailure(c, 503, "catalog_unavailable", "Cannot verify current household entities")
	case errors.Is(err, smarthome.ErrDiagnosticNotFound), errors.Is(err, smarthome.ErrInstallOperationNotFound):
		s.consoleSmartHomeFailure(c, 404, "not_found", "Automation evidence not found")
	default:
		s.consoleSmartHomeFailure(c, 503, "diagnostic_unavailable", "Automation evidence is unavailable")
	}
}
func (s *Server) handleConsoleAutomations(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, false)
	if !ok {
		return
	}
	defer stop()
	p, err := s.consoleAutomationScope(ctx, requestConsolePrincipal(c.Request).Role == "admin")
	if err != nil {
		s.automationError(c, err)
		return
	}
	counts := map[string]int{"installed": 0, "attention": 0, "unverified": 0, "archived": 0}
	for i := range p.rows {
		row := &p.rows[i]
		if row.DeletedAt != nil {
			counts["archived"]++
			continue
		}
		if row.Ref != nil {
			snapshot, err := s.smartHome.GetLatestAutomationDiagnostic(*row.Ref)
			if err != nil && !errors.Is(err, smarthome.ErrDiagnosticNotFound) {
				s.automationError(c, err)
				return
			}
			if err == nil {
				diagnostic := p.projectDiagnostic(snapshot)
				row.Diagnostic = &diagnostic
				row.Stale = true
				for _, check := range diagnostic.Checks {
					if !diagnostic.Historical && check.Status == "pass" && (check.Reason == "enabled_observed" || check.Reason == "disabled_observed") && len(check.EvidenceRefs) > 0 && !check.EvidenceRefs[0].ObservedAt.IsZero() {
						at := check.EvidenceRefs[0].ObservedAt
						if row.ObservedAt != nil && at.Before(*row.ObservedAt) {
							row.Stale = false
							continue
						}
						row.ObservedState = "on"
						if check.Reason == "disabled_observed" {
							row.ObservedState = "off"
						}
						row.ObservedAt = &at
						row.Stale = false
					}
				}
				// A newer unsuccessful read cannot displace the reliable candidate
				// above or make that older observation current again.
				if row.failedStateAt != nil && (row.ObservedAt == nil || row.failedStateAt.IsZero() || !row.failedStateAt.Before(*row.ObservedAt)) {
					row.Stale = true
				}
			}
		}
		if row.InstallationState == "verified" {
			counts["installed"]++
		} else {
			counts["unverified"]++
		}
		if row.InstallationState != "verified" || row.Diagnostic == nil || row.Diagnostic.Summary != "pass" {
			counts["attention"]++
		}
	}
	s.consoleAutomationJSON(c, ctx, p.sharing.Revision, gin.H{"automations": p.rows, "external_candidates": p.candidates, "counts": counts, "sharing_revision": p.sharing.Revision})
}
func (s *Server) handleConsoleLatestAutomationDiagnostic(c *gin.Context) {
	s.consoleAutomationDiagnostic(c, false, false)
}
func (s *Server) handleConsoleDiagnoseAutomation(c *gin.Context) {
	s.consoleAutomationDiagnostic(c, true, false)
}
func (s *Server) handleConsoleDiagnoseExternalAutomation(c *gin.Context) {
	s.consoleAutomationDiagnostic(c, true, true)
}
func (s *Server) consoleAutomationDiagnostic(c *gin.Context, run, external bool) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, run)
	if !ok {
		return
	}
	defer stop()
	p, err := s.consoleAutomationScope(ctx, requestConsolePrincipal(c.Request).Role == "admin")
	if err != nil {
		s.automationError(c, err)
		return
	}
	ref := smarthome.AutomationRef{Kind: "managed", ID: c.Param("id")}
	var body struct {
		Window smarthome.TimeWindow     `json:"window"`
		Ref    *smarthome.AutomationRef `json:"ref,omitempty"`
	}
	if run && !decodeConsoleJSON(c, &body) {
		return
	}
	if external {
		selected := false
		if body.Ref != nil {
			for _, candidate := range p.candidates {
				if candidate.Ref == *body.Ref {
					ref = candidate.Ref
					selected = true
					break
				}
			}
		}
		if !selected {
			s.automationError(c, smarthome.ErrInstallOperationNotFound)
			return
		}
	} else {
		if body.Ref != nil {
			s.consoleSmartHomeFailure(c, 422, "invalid_request", "Select the route's managed automation")
			return
		}
		if _, ok := p.allowed[ref.ID]; !ok {
			s.automationError(c, smarthome.ErrInstallOperationNotFound)
			return
		}
	}
	var snapshot smarthome.DiagnosticSnapshot
	partial := false
	if run {
		if body.Window.Start.IsZero() || !body.Window.End.After(body.Window.Start) || body.Window.End.Sub(body.Window.Start) > 14*24*time.Hour {
			s.consoleSmartHomeFailure(c, 422, "invalid_request", "Supply a window of at most 14 days")
			return
		}
		snapshot, err = s.smartHome.DiagnoseAutomation(ctx, ref, body.Window)
		partial = errors.Is(err, smarthome.ErrDiagnosticSourcesPartial)
	} else {
		snapshot, err = s.smartHome.GetLatestAutomationDiagnostic(ref)
	}
	if err != nil && !partial {
		s.automationError(c, err)
		return
	}
	s.consoleAutomationJSON(c, ctx, p.sharing.Revision, gin.H{"diagnostic": p.projectDiagnostic(snapshot), "partial": partial})
}
