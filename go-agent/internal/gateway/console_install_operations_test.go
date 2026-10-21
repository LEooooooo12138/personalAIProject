package gateway

import (
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seedInstallOperation(t *testing.T, dir string, op smarthome.InstallOperation) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "operations": []smarthome.InstallOperation{op}, "managed": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "rules", "install-state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestConsoleInstallOperationAuthorizationProjectionAndReadOnlyReconcile(t *testing.T) {
	s, writes, dir := suggestionsFixtureWithArchive(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("pending", "pending")})
	now := time.Now().UTC()
	op := smarthome.InstallOperation{SchemaVersion: 1, ID: strings.Repeat("a", 32), SuggestionID: "pending", SuggestionVersion: "pending", ConfigKey: "pending", ExpectedDigest: strings.Repeat("b", 64), Status: "unknown", Phase: "reload", CreatedAt: now, UpdatedAt: now, OwnAttempt: true, Create: smarthome.InstallStage{AttemptedAt: &now, Status: "acknowledged"}, Reload: smarthome.InstallStage{AttemptedAt: &now, Status: "unknown", ErrorCode: "PRIVATE_PROVIDER_BODY"}, Archive: smarthome.InstallArchive{Status: "not_ready"}, ErrorCode: "PRIVATE_PROVIDER_BODY"}
	seedInstallOperation(t, dir, op)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	path := consolePrefix + "/admin/install-operations/" + op.ID
	consoleJSON(t, consoleRequest(s, "GET", path, "", member, "", "", ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", path+"/reconcile", `{}`, admin, "", testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", path+"/reconcile", `{"create":true}`, admin, csrf, testConsoleOrigin, ""), 422)
	got := consoleJSON(t, consoleRequest(s, "GET", path, "", admin, "", "", ""), 200)["operation"].(map[string]any)
	if got["status"] != "unknown" || got["config_status"] != "saved" || got["load_status"] != "unknown" {
		t.Fatalf("facts %+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "raw_config") {
		t.Fatal("unsafe operation DTO")
	}
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	view := list["suggestions"].([]any)[0].(map[string]any)
	if view["status"] != "unknown" || view["installation"] == nil || view["can_confirm"] != false || view["can_bind"] != false || view["can_revise"] != false || view["can_ignore"] != false {
		t.Fatalf("stale pending capabilities %+v", view)
	}
	before := writes.Load()
	consoleJSON(t, consoleRequest(s, "POST", path+"/reconcile", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	if writes.Load() != before {
		t.Fatal("reconciliation wrote HA")
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/install-operations/missing", "", admin, "", "", ""), 404)
}
func TestConsoleInstallLegacyConfirmedIsUnverified(t *testing.T) {
	s, _ := suggestionsFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("old", "confirmed")})
	admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	result := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)["suggestions"].([]any)[0].(map[string]any)
	if result["installation_state"] != "legacy_unverified" || result["installation"] != nil || result["can_confirm"] != false {
		t.Fatalf("legacy fabricated proof %+v", result)
	}
}
func TestConsoleInstallCorruptJournalCannotAdvertiseWrites(t *testing.T) {
	s, writes, dir := suggestionsFixtureWithArchive(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("pending", "pending")})
	os.WriteFile(filepath.Join(dir, "rules", "install-state.json"), []byte(`null`), 0600)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 503)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 503)
	if writes.Load() != 0 {
		t.Fatal("corrupt journal wrote HA")
	}
}

func TestConsoleInstallUnknownDurableOutcomeBlocksNewShare(t *testing.T) {
	s, _, dir := suggestionsFixtureWithArchive(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("shared", "confirmed")})
	now := time.Now().UTC()
	op := smarthome.InstallOperation{SchemaVersion: 1, ID: strings.Repeat("a", 32), SuggestionID: "shared", SuggestionVersion: "shared", ConfigKey: "shared", ExpectedDigest: strings.Repeat("b", 64), Status: "unknown", Phase: "reconcile", CreatedAt: now, UpdatedAt: now, OwnAttempt: true, Create: smarthome.InstallStage{AttemptedAt: &now, Status: "acknowledged"}}
	seedInstallOperation(t, dir, op)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	sharing, _ := s.consoleStore.GetSharing()
	body, _ := json.Marshal(map[string]any{"revision": sharing.Revision, "suggestion_ids": []string{"shared"}})
	consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", string(body), admin, csrf, testConsoleOrigin, ""), 422)
}

func TestConsoleInstallProjectionFailureCanReconcileApplyingWithoutRestart(t *testing.T) {
	s, _, dir := suggestionsFixtureWithArchive(t)
	suggestionPath := filepath.Join(dir, "rules", "suggestions.json")
	var failProjection sync.Once
	var writes atomic.Int32
	ha := httptest.NewServer(gatewayInstallationProofHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		switch r.URL.Path {
		case "/api/config":
			// Confirm already read the pending suggestion. Fail the subsequent real
			// projectInstallSuggestion read, after its durable journal has been saved.
			failProjection.Do(func() {
				if err := os.WriteFile(suggestionPath, []byte(`invalid projection fixture`), 0600); err != nil {
					t.Error(err)
				}
			})
			fmt.Fprint(w, `{"time_zone":"UTC"}`)
		case "/api/states":
			fmt.Fprint(w, `[{"entity_id":"light.room","state":"off"},{"entity_id":"binary_sensor.house","state":"on"}]`)
		default:
			t.Errorf("unexpected HA request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ha.Close)
	manager, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, AgentVaultPath: dir}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	s.smartHome = manager
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("pending", "pending")})
	original, err := os.ReadFile(suggestionPath)
	if err != nil {
		t.Fatal(err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 503)
	op, err := manager.GetInstallOperationForSuggestion("pending")
	if err != nil || op.Status != "applying" || !op.OwnAttempt || op.Create.AttemptedAt == nil || op.Create.Status != "unknown" || writes.Load() != 0 {
		t.Fatalf("projection failure did not preserve uncertain durable attempt: %+v %v writes=%d", op, err, writes.Load())
	}
	if err = os.WriteFile(suggestionPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	view := list["suggestions"].([]any)[0].(map[string]any)
	if view["status"] != "applying" || view["can_confirm"] != false || view["can_revise"] != false || view["can_bind"] != false || view["can_ignore"] != false || view["installation"].(map[string]any)["can_reconcile"] != true {
		t.Fatalf("abandoned applying operation must offer only read-only recovery: %+v", view)
	}
	endpoint := consolePrefix + "/admin/install-operations/" + op.ID
	dto := consoleJSON(t, consoleRequest(s, "GET", endpoint, "", admin, "", "", ""), 200)["operation"].(map[string]any)
	if dto["can_reconcile"] != true {
		t.Fatalf("operation endpoint lost recovery: %+v", dto)
	}
	recovered := consoleJSON(t, consoleRequest(s, "POST", endpoint+"/reconcile", `{}`, admin, csrf, testConsoleOrigin, ""), 200)["operation"].(map[string]any)
	if recovered["status"] != "unknown" || recovered["config_status"] != "missing" || recovered["ownership"] != "unverified" || recovered["archive_status"] != "not_ready" {
		t.Fatalf("invented installation/dispatch: %+v", recovered)
	}
	after, err := manager.GetInstallOperation(op.ID)
	if err != nil || after.ID != op.ID || after.ExpectedDigest != op.ExpectedDigest || after.Create.Status != op.Create.Status || !after.Create.AttemptedAt.Equal(*op.Create.AttemptedAt) {
		t.Fatalf("attempt history changed: %+v %v", after, err)
	}
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	if writes.Load() != 0 {
		t.Fatal("recovery or repeated confirmation dispatched HA writes")
	}
}

func TestConsoleInstallApplyingReconcilePreservesOwnershipBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		owned  bool
		create string
		want   bool
	}{
		{"owned unknown attempt", true, "unknown", true},
		{"unowned legacy", false, "unknown", false},
		{"rejected", true, "rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := installOperationProjection(smarthome.InstallOperation{Status: "applying", OwnAttempt: tc.owned, Create: smarthome.InstallStage{Status: tc.create}})
			if view.CanReconcile != tc.want {
				t.Fatalf("can_reconcile=%v want %v", view.CanReconcile, tc.want)
			}
		})
	}
}
