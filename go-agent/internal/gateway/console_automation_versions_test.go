package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsoleAutomationLegacyUnboundDiagnosticIsHistorical(t *testing.T) {
	var dir string
	s, writes := newAutomationProjectionFixture(t, &dir)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("legacy_diagnostic", "pending")})
	if _, e := s.smartHome.ConfirmSuggestion(context.Background(), "legacy_diagnostic"); e != nil {
		t.Fatal(e)
	}
	op, _ := s.smartHome.GetInstallOperationForSuggestion("legacy_diagnostic")
	snapshot := diagnoseProjectionFixture(t, s, op.ManagedAutomationID)
	if len(snapshot.ManagedVersions) == 0 {
		t.Fatal("new snapshot has no binding")
	}
	hash := sha256.Sum256([]byte("managed:" + op.ManagedAutomationID))
	path := filepath.Join(dir, "diagnostics", hex.EncodeToString(hash[:])+".json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var record map[string]any
	if e = json.Unmarshal(b, &record); e != nil {
		t.Fatal(e)
	}
	delete(record["snapshot"].(map[string]any), "managed_versions")
	delete(record, "active_versions")
	b, e = json.Marshal(record)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	count := writes.Load()
	for _, endpoint := range []string{consolePrefix + "/automations/" + op.ManagedAutomationID + "/diagnostics/latest", consolePrefix + "/automations"} {
		response := consoleRequest(s, "GET", endpoint, "", admin, "", "", "")
		payload := consoleJSON(t, response, 200)
		var diag map[string]any
		if rows, ok := payload["automations"]; ok {
			diag = rows.([]any)[0].(map[string]any)["diagnostic"].(map[string]any)
		} else {
			diag = payload["diagnostic"].(map[string]any)
		}
		if diag["historical"] != true || diag["summary"] != "unknown" || len(diag["checks"].([]any)) != 0 {
			t.Fatalf("legacy treated as current: %+v", diag)
		}
		if strings.Contains(response.Body.String(), "managed_versions") || strings.Contains(response.Body.String(), "active_versions") {
			t.Fatal("internal binding leaked")
		}
	}
	if writes.Load() != count {
		t.Fatal("legacy read wrote HA")
	}
}

func TestConsoleAutomationPeerGenerationCapturedAndFiltered(t *testing.T) {
	s, _ := newAutomationProjectionFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("selected", "pending"), persistedTimeRule("peer", "pending")})
	ids := map[string]string{}
	for _, name := range []string{"selected", "peer"} {
		if _, e := s.smartHome.ConfirmSuggestion(context.Background(), name); e != nil {
			t.Fatal(e)
		}
		op, _ := s.smartHome.GetInstallOperationForSuggestion(name)
		ids[name] = op.ManagedAutomationID
	}
	snapshot := diagnoseProjectionFixture(t, s, ids["selected"])
	peerBefore, _, e := s.smartHome.GetManagedAutomationVersion(ids["peer"])
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.ManagedVersions[ids["peer"]].OperationID != peerBefore.OperationID {
		t.Fatalf("same-observation peer was not captured: %+v", snapshot.ManagedVersions)
	}
	hasPeer := false
	for _, check := range snapshot.Checks {
		for _, ev := range check.EvidenceRefs {
			if ev.RuleRef.ID == ids["peer"] {
				hasPeer = true
			}
		}
	}
	if !hasPeer {
		t.Fatal("fixture has no peer checks")
	}
	current := updateProjectionFixtureRule(t, s, ids["peer"], "19:00")
	admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	response := consoleRequest(s, "GET", consolePrefix+"/automations/"+ids["selected"]+"/diagnostics/latest", "", admin, "", "", "")
	diag := consoleJSON(t, response, 200)["diagnostic"].(map[string]any)
	if diag["historical"] == true {
		t.Fatal("peer replacement invalidated primary")
	}
	if strings.Contains(response.Body.String(), ids["peer"]) {
		t.Fatal("changed peer evidence survived filtering")
	}
	persisted, e := s.smartHome.GetLatestAutomationDiagnostic(snapshot.Ref)
	if e != nil || persisted.ManagedVersions[ids["peer"]].OperationID != peerBefore.OperationID {
		t.Fatal("old snapshot upgraded at response time")
	}
	next := diagnoseProjectionFixture(t, s, ids["selected"])
	if next.ManagedVersions[ids["peer"]].OperationID != current.OperationID || snapshot.ManagedVersions[ids["peer"]].OperationID != peerBefore.OperationID {
		t.Fatal("new capture did not preserve immutable generations")
	}
}

func updateProjectionFixtureRule(t *testing.T, s *Server, id, at string) smarthome.ManagedAutomation {
	t.Helper()
	ctx := smarthome.WithManagementAuthority(context.Background(), smarthome.ManagementAuthority{UserID: "admin", SessionID: "test", Check: func(context.Context) error { return nil }})
	managed, current, e := s.smartHome.GetManagedAutomationVersion(id)
	if e != nil {
		t.Fatal(e)
	}
	intent := *current.Intent
	intent.At = at
	draft, e := s.smartHome.ReviseManagedAutomation(ctx, id, smarthome.SuggestionRevisionRequest{ExpectedVersion: current.ID, Intent: intent, PresenceBinding: current.PresenceBinding})
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: id, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
	if e != nil {
		t.Fatal(e)
	}
	if result, e := s.smartHome.ConfirmAutomationAction(ctx, op.ID); e != nil || result.Status != "confirmed" {
		t.Fatalf("update %+v %v", result, e)
	}
	managed, _, e = s.smartHome.GetManagedAutomationVersion(id)
	if e != nil {
		t.Fatal(e)
	}
	return managed
}
func diagnoseProjectionFixture(t *testing.T, s *Server, id string) smarthome.DiagnosticSnapshot {
	t.Helper()
	end := time.Now().UTC()
	snapshot, e := s.smartHome.DiagnoseAutomation(context.Background(), smarthome.AutomationRef{Kind: "managed", ID: id}, smarthome.TimeWindow{Start: end.Add(-time.Hour), End: end})
	if e != nil && !errors.Is(e, smarthome.ErrDiagnosticSourcesPartial) {
		t.Fatal(e)
	}
	return snapshot
}

func TestConsoleAutomationOldVersionDiagnosticIsHistorical(t *testing.T) {
	for _, sameContent := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed_content", true: "same_content_new_generation"}[sameContent], func(t *testing.T) {
			s, writes := newAutomationProjectionFixture(t)
			seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("versioned", "pending")})
			if _, e := s.smartHome.ConfirmSuggestion(context.Background(), "versioned"); e != nil {
				t.Fatal(e)
			}
			install, _ := s.smartHome.GetInstallOperationForSuggestion("versioned")
			before := updateProjectionFixtureRule(t, s, install.ManagedAutomationID, "19:00")
			snapshot := diagnoseProjectionFixture(t, s, before.ID)
			current := updateProjectionFixtureRule(t, s, before.ID, "20:00")
			if sameContent {
				current = updateProjectionFixtureRule(t, s, before.ID, "19:00")
				if current.ExpectedDigest != before.ExpectedDigest {
					t.Fatal("fixture does not represent same-content new generation")
				}
			}
			if current.OperationID == before.OperationID || current.SuggestionVersion == before.SuggestionVersion {
				t.Fatal("update did not advance generation")
			}
			admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			writeCount := writes.Load()
			latest := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations/"+before.ID+"/diagnostics/latest", "", admin, "", "", ""), 200)["diagnostic"].(map[string]any)
			if latest["historical"] != true || latest["summary"] != "unknown" || len(latest["checks"].([]any)) != 0 {
				t.Fatalf("old version returned as current: %+v", latest)
			}
			list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)
			row := list["automations"].([]any)[0].(map[string]any)
			at, e := time.Parse(time.RFC3339Nano, row["observed_at"].(string))
			if e != nil || !at.Equal(current.LastObservation.StateObservedAt) {
				t.Fatalf("old snapshot %s overwrote newer observation: %+v", snapshot.ID, row)
			}
			if row["diagnostic"].(map[string]any)["historical"] != true {
				t.Fatal("list mislabels old snapshot")
			}
			if writes.Load() != writeCount {
				t.Fatal("list/latest wrote HA")
			}
		})
	}
}

func TestConsoleAutomationNewerSameGenerationStateWins(t *testing.T) {
	s, _ := newAutomationProjectionFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("stateful", "pending")})
	if _, e := s.smartHome.ConfirmSuggestion(context.Background(), "stateful"); e != nil {
		t.Fatal(e)
	}
	install, _ := s.smartHome.GetInstallOperationForSuggestion("stateful")
	managed, _ := s.smartHome.GetManagedAutomation(install.ManagedAutomationID)
	diagnoseProjectionFixture(t, s, managed.ID)
	ctx := smarthome.WithManagementAuthority(context.Background(), smarthome.ManagementAuthority{UserID: "admin", SessionID: "test", Check: func(context.Context) error { return nil }})
	stop := false
	op, e := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: managed.ID, Action: "disable", ExpectedDigest: managed.ExpectedDigest, StopActions: &stop})
	if e != nil {
		t.Fatal(e)
	}
	if result, e := s.smartHome.ConfirmAutomationAction(ctx, op.ID); e != nil || result.Status != "confirmed" {
		t.Fatalf("disable %+v %v", result, e)
	}
	current, _ := s.smartHome.GetManagedAutomation(managed.ID)
	if current.OperationID != managed.OperationID || current.LastObservation.State != "off" {
		t.Fatal("fixture did not create newer same-generation observation")
	}
	admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	row := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)["automations"].([]any)[0].(map[string]any)
	if row["observed_state"] != "off" || row["observed_at"] != current.LastObservation.StateObservedAt.Format(time.RFC3339Nano) || row["stale"] != false {
		t.Fatalf("older diagnostic replaced newer disabled observation %+v", row)
	}
}
func TestConsoleAutomationFailedNewerReadbackPreservesReliableObservation(t *testing.T) {
	s, _ := newAutomationProjectionFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("stateful", "pending")})
	if _, e := s.smartHome.ConfirmSuggestion(context.Background(), "stateful"); e != nil {
		t.Fatal(e)
	}
	install, _ := s.smartHome.GetInstallOperationForSuggestion("stateful")
	managed, _ := s.smartHome.GetManagedAutomation(install.ManagedAutomationID)
	snapshot := diagnoseProjectionFixture(t, s, managed.ID)
	var wantTime string
	for _, c := range snapshot.Checks {
		if c.Reason == "enabled_observed" {
			wantTime = c.EvidenceRefs[0].ObservedAt.Format("2006-01-02T15:04:05.999999999Z07:00")
		}
	}
	if wantTime == "" {
		t.Fatal("no reliable original observation")
	}
	client := s.smartHome.GetClient()
	upstream, e := url.Parse(client.BaseURL)
	if e != nil {
		t.Fatal(e)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var fail atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() && strings.HasPrefix(r.URL.Path, "/api/states/automation.") {
			w.WriteHeader(503)
			return
		}
		proxy.ServeHTTP(w, r)
		if r.Method == "POST" && r.URL.Path == "/api/services/automation/turn_off" {
			fail.Store(true)
		}
	}))
	t.Cleanup(endpoint.Close)
	client.BaseURL = endpoint.URL
	ctx := smarthome.WithManagementAuthority(context.Background(), smarthome.ManagementAuthority{UserID: "admin", SessionID: "test", Check: func(context.Context) error { return nil }})
	stop := false
	op, e := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: managed.ID, Action: "disable", ExpectedDigest: managed.ExpectedDigest, StopActions: &stop})
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.smartHome.ConfirmAutomationAction(ctx, op.ID)
	if e != nil || result.Status != "unknown" {
		t.Fatalf("expected failed readback: %+v %v", result, e)
	}
	current, _ := s.smartHome.GetManagedAutomation(managed.ID)
	if current.OperationID != managed.OperationID || current.LastObservation.StateStatus == smarthome.SourceAvailable || current.LastObservation.StateObservedAt.IsZero() {
		t.Fatal("fixture did not record newer failed same-generation observation")
	}
	admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	row := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)["automations"].([]any)[0].(map[string]any)
	if row["observed_state"] != "on" || row["observed_at"] != wantTime || row["stale"] != true {
		t.Fatalf("failed readback displaced reliable observation: state=%v time=%v stale=%v; want on %s stale=true", row["observed_state"], row["observed_at"], row["stale"], wantTime)
	}
	// A later successful diagnostic can establish the actual off state even
	// though durable LastObservation still contains the earlier failed attempt.
	fail.Store(false)
	fresh := diagnoseProjectionFixture(t, s, managed.ID)
	var freshTime string
	for _, check := range fresh.Checks {
		if check.Reason == "disabled_observed" {
			freshTime = check.EvidenceRefs[0].ObservedAt.Format(time.RFC3339Nano)
		}
	}
	if freshTime == "" {
		t.Fatal("new diagnosis did not observe off")
	}
	row = consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)["automations"].([]any)[0].(map[string]any)
	if row["observed_state"] != "off" || row["observed_at"] != freshTime || row["stale"] != false {
		t.Fatalf("fresh reliable state failed to supersede old failed attempt: %+v", row)
	}
}
