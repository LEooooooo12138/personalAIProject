package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func diagnosisCheck(t *testing.T, s DiagnosticSnapshot, key string) DiagnosticCheck {
	t.Helper()
	for _, c := range s.Checks {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("missing check %s: %+v", key, s)
	return DiagnosticCheck{}
}
func TestAutomationDiagnosticsEvidenceBoundaries(t *testing.T) {
	ref := AutomationRef{Kind: "external", ID: "automation.rule"}
	for _, tc := range []struct {
		name, key, status, reason string
		alter                     func(*AutomationEvidence)
	}{
		{"all_unknown", "enabled", "unknown", "state_unavailable", func(e *AutomationEvidence) {}},
		{"disabled_normal", "enabled", "pass", "disabled_observed", func(e *AutomationEvidence) { e.State.SourceStatus = SourceAvailable; e.State.State = "off" }},
		{"enabled", "enabled", "pass", "enabled_observed", func(e *AutomationEvidence) { e.State.SourceStatus = SourceAvailable; e.State.State = "on" }},
		{"unreachable", "existence", "unknown", "existence_unavailable", func(e *AutomationEvidence) { e.Config.ErrorCode = "ha_request_failed" }},
		{"absent", "existence", "finding", "config_missing", func(e *AutomationEvidence) { b := false; e.Config.SourceStatus = SourceAvailable; e.Config.Exists = &b }},
		{"no_trace", "execution", "unknown", "run_evidence_missing", func(e *AutomationEvidence) {}},
		{"failure", "run:trace:run1", "finding", "action_failed", func(e *AutomationEvidence) {
			e.Trace.Runs = []AutomationRunEvidence{{Ref: "trace:run1", Start: evidenceWindow.Start, Outcome: "action_failed"}}
		}},
		{"condition_normal", "run:trace:run1", "pass", "conditions_not_met", func(e *AutomationEvidence) {
			e.Trace.Runs = []AutomationRunEvidence{{Ref: "trace:run1", Start: evidenceWindow.Start, Outcome: "conditions_not_met"}}
		}},
		{"no_causality", "causality", "unknown", "causality_unproven", func(e *AutomationEvidence) { e.Activity.SourceStatus = SourceAvailable }},
		{"old_failure_outside_window", "execution", "unknown", "run_evidence_missing", func(e *AutomationEvidence) {
			e.Trace.Runs = []AutomationRunEvidence{{Ref: "trace:old", Start: evidenceWindow.Start.Add(-time.Hour), Outcome: "action_failed"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := AutomationEvidence{Ref: ref, Window: evidenceWindow}
			tc.alter(&e)
			s := EvaluateAutomationDiagnostics(ref, e)
			c := diagnosisCheck(t, s, tc.key)
			if c.Status != tc.status || c.Reason != tc.reason {
				t.Fatalf("%+v", c)
			}
			if tc.name == "all_unknown" && s.Summary != "unknown" {
				t.Fatal(s.Summary)
			}
			if tc.name == "disabled_normal" && c.Category == "fault" {
				t.Fatal("disabled is not a fault")
			}
		})
	}
}
func TestAutomationDiagnosticsConflictEvidence(t *testing.T) {
	a := AutomationConfig{ID: "a", Trigger: []AutomationTrigger{{Platform: "time", At: "20:00"}}, Action: []AutomationAction{{Service: "light.turn_on", Target: AutomationTarget{EntityID: "light.room"}}}}
	for _, tc := range []struct {
		name, status, reason string
		alter                func(*AutomationConfig)
	}{
		{"same_scope", "finding", "opposite_actions_same_scope", func(b *AutomationConfig) {}},
		{"different_time", "pass", "no_conflict_in_comparison", func(b *AutomationConfig) { b.Trigger[0].At = "21:00" }},
		{"incomparable_trigger", "unknown", "possible_conflict", func(b *AutomationConfig) {
			b.Trigger = []AutomationTrigger{{Platform: "state", EntityID: "binary_sensor.door", From: "off", To: "on"}}
		}},
		{"exclusive", "pass", "no_conflict_in_comparison", func(b *AutomationConfig) {
			b.Condition = []AutomationCondition{{Condition: "state", EntityID: "input_boolean.home", State: "off"}}
		}},
		{"unknown_sun", "unknown", "possible_conflict", func(b *AutomationConfig) { b.Condition = []AutomationCondition{{Condition: "sun", After: "sunset"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aa := a
			aa.Condition = []AutomationCondition{{Condition: "state", EntityID: "input_boolean.home", State: "on"}}
			raw, _ := json.Marshal(a)
			var b AutomationConfig
			json.Unmarshal(raw, &b)
			b.ID = "b"
			b.Action[0].Service = "light.turn_off"
			tc.alter(&b)
			ref := AutomationRef{Kind: "external", ID: "automation.a"}
			peer := AutomationRef{Kind: "external", ID: "automation.b"}
			e := AutomationEvidence{Window: evidenceWindow, Analysis: AutomationDiagnosticContext{Config: &aa, Peers: []DiagnosticPeer{{Ref: peer, Config: &b, Source: DiagnosticSource{SourceStatus: SourceAvailable}}}}}
			s := EvaluateAutomationDiagnostics(ref, e)
			c := diagnosisCheck(t, s, "conflict:external:automation.b")
			if c.Status != tc.status || c.Reason != tc.reason {
				t.Fatalf("%+v", c)
			}
			if len(c.EvidenceRefs) != 2 || c.EvidenceRefs[1].RuleRef != peer {
				t.Fatal("peer scope lost")
			}
		})
	}
}
func TestAutomationDiagnosticsSummary(t *testing.T) {
	for _, tc := range []struct {
		s    []string
		want string
	}{{nil, "unknown"}, {[]string{"unknown"}, "unknown"}, {[]string{"pass"}, "pass"}, {[]string{"pass", "unknown"}, "partial"}, {[]string{"finding", "unknown"}, "attention"}} {
		var c []DiagnosticCheck
		for _, s := range tc.s {
			c = append(c, DiagnosticCheck{Status: s})
		}
		if got := DiagnosticSummary(c); got != tc.want {
			t.Fatalf("%v: %s", tc.s, got)
		}
	}
}

type diagnosisFixture struct {
	configs               map[string]string
	state                 string
	writes                atomic.Int32
	stateReads            atomic.Int32
	configReads           atomic.Int32
	zone                  string
	version               string
	registryError         string
	registryReads         atomic.Int32
	profileReads          atomic.Int32
	automationStateStatus int
}

func diagnosisConfig(id, service string) string {
	return fmt.Sprintf(`{"id":%q,"alias":"PRIVATE alias","description":"PRIVATE text","trigger":[{"platform":"time","at":"20:00"}],"condition":[{"condition":"state","entity_id":"input_boolean.home","state":"on"}],"action":[{"service":%q,"target":{"entity_id":"light.room"}}],"mode":"single"}`, id, service)
}
func newDiagnosisFixture(t *testing.T) (*diagnosisFixture, *Manager) {
	t.Helper()
	f := &diagnosisFixture{configs: map[string]string{"a": diagnosisConfig("a", "light.turn_on"), "b": diagnosisConfig("b", "light.turn_off")}, state: "unavailable", zone: "America/New_York", version: "2026.7.2"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			f.writes.Add(1)
			w.WriteHeader(500)
			return
		}
		switch {
		case r.URL.Path == "/api/config":
			f.profileReads.Add(1)
			fmt.Fprintf(w, `{"version":%q,"components":["config","automation"],"time_zone":%q}`, f.version, f.zone)
		case strings.HasPrefix(r.URL.Path, "/api/config/automation/config/"):
			f.configReads.Add(1)
			raw, ok := f.configs[strings.TrimPrefix(r.URL.Path, "/api/config/automation/config/")]
			if !ok {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, raw)
		case strings.HasPrefix(r.URL.Path, "/api/states/"):
			f.stateReads.Add(1)
			id := strings.TrimPrefix(r.URL.Path, "/api/states/")
			if strings.HasPrefix(id, "automation.") && f.automationStateStatus != 0 {
				w.WriteHeader(f.automationStateStatus)
				return
			}
			state := "on"
			if id == "light.room" {
				state = f.state
			}
			json.NewEncoder(w).Encode(map[string]any{"entity_id": id, "state": state, "attributes": map[string]string{"private": "PRIVATE"}})
		case r.URL.Path == "/api/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.WriteJSON(map[string]any{"type": "auth_required"})
			var auth any
			if conn.ReadJSON(&auth) != nil {
				return
			}
			conn.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				var req map[string]any
				if conn.ReadJSON(&req) != nil {
					return
				}
				res := map[string]any{"id": req["id"], "type": "result", "success": true}
				switch req["type"] {
				case "config/entity_registry/list":
					f.registryReads.Add(1)
					if f.registryError != "" {
						res["success"] = false
						res["error"] = map[string]string{"code": f.registryError, "message": "PRIVATE"}
						break
					}
					entities := []map[string]string{}
					for id := range f.configs {
						entities = append(entities, map[string]string{"entity_id": "automation." + id, "platform": "automation", "unique_id": id})
					}
					res["result"] = entities
				case "automation/config":
					id := strings.TrimPrefix(req["entity_id"].(string), "automation.")
					res["result"] = map[string]any{"config": json.RawMessage(f.configs[id])}
				case "trace/list":
					res["result"] = []any{}
				default:
					f.writes.Add(1)
					t.Errorf("unexpected WS command %v", req["type"])
					return
				}
				if conn.WriteJSON(res) != nil {
					return
				}
			}
		default:
			f.writes.Add(1)
			t.Errorf("unexpected GET %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{client: NewHomeAssistantClient(srv.URL, "fixture", time.Second), store: store, serviceCtx: context.Background()}
	t.Cleanup(func() {
		m.client.Close()
		if f.writes.Load() != 0 {
			t.Fatal("write or unbounded state scan")
		}
	})
	return f, m
}
func TestAutomationDiagnosticsRealCompositionAndSafeLatest(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	ref := AutomationRef{Kind: "external", ID: "automation.a"}
	s, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosisCheck(t, s, "entity:light.room").Reason != "entity_unavailable" || diagnosisCheck(t, s, "conflict:external:automation.b").Status != "finding" {
		t.Fatalf("missing real facts: %+v", s)
	}
	if s.SchemaVersion != 1 || len(s.ID) != 32 || s.CheckedAt.IsZero() || s.HomeTimezone != "America/New_York" || s.Sources["peers"].CoverageStatus != "bounded_registry_candidates" {
		t.Fatalf("missing scope: %+v", s)
	}
	got, err := m.GetLatestAutomationDiagnostic(ref)
	if err != nil || got.ID != s.ID {
		t.Fatalf("latest %v %+v", err, got)
	}
	files, err := os.ReadDir(filepath.Join(m.store.basePath, "diagnostics"))
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	data, _ := os.ReadFile(filepath.Join(m.store.basePath, "diagnostics", files[0].Name()))
	for _, secret := range []string{"PRIVATE", "raw_config", "attributes", "description", "turn_on"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("private config persisted: %s", secret)
		}
	}
	again, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
	if err != nil || len(again.Changes) != 0 {
		t.Fatalf("repeat renotified: %v %+v", err, again.Changes)
	}
	f.state = "on"
	recovered, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Changes) != 1 || recovered.Changes[0].Kind != "recovered" || recovered.Changes[0].Check.Key != "entity:light.room" {
		t.Fatalf("recovery %+v", recovered.Changes)
	}
}
func TestAutomationDiagnosticsStoreUnknownDoesNotRecover(t *testing.T) {
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := AutomationRef{Kind: "external", ID: "automation.a"}
	s := DiagnosticSnapshot{SchemaVersion: 1, ID: strings.Repeat("a", 32), Ref: ref, Window: evidenceWindow, CheckedAt: time.Now().UTC(), Checks: []DiagnosticCheck{{Key: "entity:light.room", Status: "finding", Severity: "warning", Reason: "entity_unavailable"}}}
	s, err = store.saveDiagnosticSnapshot(s)
	if err != nil || len(s.Changes) != 1 {
		t.Fatalf("%v %+v", err, s)
	}
	s.Checks[0].Status = "unknown"
	s, err = store.saveDiagnosticSnapshot(s)
	if err != nil || len(s.Changes) != 0 {
		t.Fatal("unknown falsely recovered")
	}
	s.Checks[0].Status = "finding"
	s, err = store.saveDiagnosticSnapshot(s)
	if err != nil || len(s.Changes) != 0 {
		t.Fatal("same finding reappeared after unknown")
	}
	s.Checks[0].Severity = "error"
	s, err = store.saveDiagnosticSnapshot(s)
	if err != nil || len(s.Changes) != 1 || s.Changes[0].Kind != "severity_changed" {
		t.Fatal("severity change lost")
	}
}
func TestAutomationDiagnosticsBoundsAndComplexConfig(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("peer_%02d", i)
		f.configs[id] = diagnosisConfig(id, "light.turn_off")
	}
	f.configs["b"] = `{"id":"b","trigger":[{"platform":"time","at":"20:00"}],"action":[{"service":"light.turn_off","target":{"entity_id":"light.room"}}],"condition":[{"condition":"template","value_template":"PRIVATE"}]}`
	s, err := m.DiagnoseAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.a"}, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	if f.configReads.Load() > 9 {
		t.Fatalf("unbounded peer reads: %d", f.configReads.Load())
	}
	if diagnosisCheck(t, s, "conflict:external:automation.b").Status != "unknown" || s.Sources["peers"].CoverageStatus != "read_limit" {
		t.Fatal("complex/truncated peers overclaimed")
	}
}
func TestAutomationDiagnosticsDSTAndManual(t *testing.T) {
	for _, tc := range []struct {
		name, zone, platform, at, want string
		w                              TimeWindow
	}{
		{"spring_gap", "America/New_York", "time", "02:30", "schedule_dst_ambiguous", TimeWindow{Start: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}},
		{"fall_repeat", "America/New_York", "time", "01:30", "schedule_dst_ambiguous", TimeWindow{Start: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}},
		{"manual", "UTC", "event", "", "schedule_not_comparable", evidenceWindow},
		{"state", "UTC", "state", "", "state_trigger_history_unproven", evidenceWindow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := AutomationEvidence{Window: tc.w, Analysis: AutomationDiagnosticContext{HomeTimezone: tc.zone, Config: &AutomationConfig{Trigger: []AutomationTrigger{{Platform: tc.platform, At: tc.at}}}}}
			c := diagnosisCheck(t, EvaluateAutomationDiagnostics(AutomationRef{}, e), "expected_run")
			if c.Status != "unknown" || c.Reason != tc.want {
				t.Fatalf("%+v", c)
			}
		})
	}
}

func TestAutomationDiagnosticsRejectsIdentityAndTemplates(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"identity", diagnosisConfig("wrong", "light.turn_off")},
		{"template_state", strings.ReplaceAll(diagnosisConfig("b", "light.turn_off"), `"state":"on"`, `"state":"{{ secret }}"`)},
		{"duplicate_key", strings.ReplaceAll(diagnosisConfig("b", "light.turn_off"), `"id":"b"`, `"id":"wrong","id":"b"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exists := true
			o := AutomationConfigObservation{SourceStatus: SourceAvailable, Exists: &exists, ConfigKey: "b", RawConfig: json.RawMessage(tc.raw)}
			if supportedDiagnosticConfig(o) != nil {
				t.Fatal("ambiguous config interpreted as trusted semantics")
			}
		})
	}
}
func TestAutomationDiagnosticsReferencedEntityReadLimit(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	var c AutomationConfig
	json.Unmarshal([]byte(f.configs["a"]), &c)
	for i := 0; i < 25; i++ {
		c.Action = append(c.Action, AutomationAction{Service: "light.turn_on", Target: AutomationTarget{EntityID: fmt.Sprintf("light.target_%02d", i)}})
	}
	raw, _ := json.Marshal(c)
	f.configs["a"] = string(raw)
	s, err := m.DiagnoseAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.a"}, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	if s.Sources["entities"].CoverageStatus != "read_limit" || f.stateReads.Load() > 20 {
		t.Fatalf("unbounded entity reads %d %+v", f.stateReads.Load(), s.Sources["entities"])
	}
	if diagnosisCheck(t, s, "references_coverage").Status != "unknown" {
		t.Fatal("truncated entity coverage absent from applicable checks")
	}
}
func TestAutomationDiagnosticsCurrentConditionsAreNormalFacts(t *testing.T) {
	_, m := newDiagnosisFixture(t)
	s, err := m.DiagnoseAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.a"}, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	c := diagnosisCheck(t, s, "condition:input_boolean.home")
	if c.Status != "pass" || c.Reason != "current_condition_met" {
		t.Fatalf("%+v", c)
	}
}
func TestAutomationDiagnosticsInvalidAndCancelledDoNotRead(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, r := range []AutomationRef{{Kind: "external", ID: "../../private"}, {Kind: "external", ID: "automation.a"}} {
		if _, err := m.DiagnoseAutomation(ctx, r, evidenceWindow); err == nil {
			t.Fatal("invalid/cancelled accepted")
		}
	}
	if f.configReads.Load() != 0 || f.stateReads.Load() != 0 {
		t.Fatal("read after cancellation")
	}
	m.lifecycleMu.Lock()
	m.stopped = true
	m.lifecycleMu.Unlock()
	if _, err := m.DiagnoseAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.a"}, evidenceWindow); err == nil {
		t.Fatal("read after stop")
	}
}

func TestAutomationDiagnosticsPartialAndPersistenceContracts(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	ref := AutomationRef{Kind: "external", ID: "automation.a"}
	if _, err := m.GetLatestAutomationDiagnostic(ref); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatal("missing snapshot accepted")
	}
	f.zone = ""
	s, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
	if err == nil || !strings.Contains(err.Error(), "diagnostic_sources_partial") || s.ID == "" {
		t.Fatalf("partial result indistinguishable: %s %v", s.ID, err)
	}
	latest, err := m.GetLatestAutomationDiagnostic(ref)
	if err != nil || latest.ID != s.ID {
		t.Fatal("partial snapshot lost")
	}
	if err := os.WriteFile(m.store.diagnosticPath(ref), []byte(`{"schema_version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetLatestAutomationDiagnostic(ref); !errors.Is(err, ErrDiagnosticStore) {
		t.Fatal("corrupt latest accepted")
	}
	if _, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow); !errors.Is(err, ErrDiagnosticStore) {
		t.Fatal("persistence failure swallowed")
	}
}
func TestAutomationDiagnosticsManagedDigestUsesDurableProvenance(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	_, digest, err := canonicalAutomationConfig([]byte(f.configs["a"]))
	if err != nil {
		t.Fatal(err)
	}
	op, err := newInstallOperation(RuleSuggestion{ID: "a"}, digest)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	op.OwnAttempt = true
	op.Create.AttemptedAt = &now
	op.ManagedAutomationID = strings.Repeat("b", 32)
	op.Status = "confirmed"
	observed, err := m.ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.a"})
	if err != nil || !observed.RuntimeLoadProof.Verified {
		t.Fatalf("fixture install proof: %v", err)
	}
	op.Observation = &observed
	managed := ManagedAutomation{SchemaVersion: 1, ID: op.ManagedAutomationID, OperationID: op.ID, SuggestionID: "a", SuggestionVersion: "a", ConfigKey: "a", EntityID: "automation.a", ExpectedDigest: digest, MappingSource: "config/entity_registry/list"}
	managed.LastObservation = observed
	if err := m.store.saveInstallManaged(op, managed); err != nil {
		t.Fatal(err)
	}
	f.configs["a"] = strings.Replace(f.configs["a"], "PRIVATE text", "PRIVATE changed description", 1)
	s, err := m.DiagnoseAutomation(context.Background(), AutomationRef{Kind: "managed", ID: managed.ID}, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	if s.Ownership != "managed" || diagnosisCheck(t, s, "drift").Reason != "config_drift" {
		t.Fatal("managed complete-config drift lost")
	}
}

func TestAutomationDiagnosticsStaleFactsCannotRecover(t *testing.T) {
	_, m := newDiagnosisFixture(t)
	ref := AutomationRef{Kind: "external", ID: "automation.a"}
	s, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	old := diagnosisCheck(t, s, "entity:light.room")
	old.Status = "pass"
	old.Reason = "entity_available"
	old.EvidenceRefs[0].ObservedAt = old.EvidenceRefs[0].ObservedAt.Add(-time.Hour)
	s.CheckedAt = s.CheckedAt.Add(time.Second)
	s.Checks = []DiagnosticCheck{old}
	s, err = m.store.saveDiagnosticSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Changes) != 0 {
		t.Fatal("stale entity observation falsely recovered")
	}
}

func TestAutomationDiagnosticsRejectsUnselectedScopeBeforeComposition(t *testing.T) {
	for _, version := range []string{"2026.7.2", "2026.8.0"} {
		for _, selection := range []string{"absent", "registry_failure"} {
			t.Run(version+"/"+selection, func(t *testing.T) {
				f, m := newDiagnosisFixture(t)
				f.version = version
				ref := AutomationRef{Kind: "external", ID: "automation.absent"}
				if selection == "registry_failure" {
					ref.ID = "automation.a"
					f.registryError = "not_allowed"
				}
				snapshot, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
				if err == nil || errors.Is(err, ErrDiagnosticSourcesPartial) || snapshot.ID != "" {
					t.Fatalf("unselected scope accepted as completed partial diagnostic: id=%q err=%v", snapshot.ID, err)
				}
				if selection == "absent" && HAErrorCode(err) != "ha_target_not_observed" {
					t.Fatalf("lost explicit absence: %v", err)
				}
				if _, err := m.GetLatestAutomationDiagnostic(ref); !errors.Is(err, ErrDiagnosticNotFound) {
					t.Fatalf("unselected scope persisted: %v", err)
				}
				if f.profileReads.Load() != 1 || f.registryReads.Load() != 1 || f.stateReads.Load() != 0 || f.configReads.Load() != 0 {
					t.Fatalf("composition ran without selection: profile=%d registry=%d state=%d config=%d", f.profileReads.Load(), f.registryReads.Load(), f.stateReads.Load(), f.configReads.Load())
				}
			})
		}
	}
}
func TestAutomationDiagnosticsSelectedUnsupportedProfileRetainsPartial(t *testing.T) {
	for _, stateStatus := range []int{0, http.StatusForbidden} {
		t.Run(fmt.Sprint(stateStatus), func(t *testing.T) {
			f, m := newDiagnosisFixture(t)
			f.version = "2026.8.0"
			f.automationStateStatus = stateStatus
			ref := AutomationRef{Kind: "external", ID: "automation.a"}
			snapshot, err := m.DiagnoseAutomation(context.Background(), ref, evidenceWindow)
			if !errors.Is(err, ErrDiagnosticSourcesPartial) || snapshot.ID == "" {
				t.Fatalf("selected scope lost usable partial: id=%q err=%v", snapshot.ID, err)
			}
			if got, err := m.GetLatestAutomationDiagnostic(ref); err != nil || got.ID != snapshot.ID {
				t.Fatalf("selected partial not saved: %v", err)
			}
			if stateStatus == http.StatusForbidden && diagnosisCheck(t, snapshot, "enabled").Status != "unknown" {
				t.Fatal("failed selected state read overclaimed")
			}
		})
	}
}
