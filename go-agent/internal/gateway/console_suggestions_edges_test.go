package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type blockedSuggestionCatalog struct {
	snapshot         smarthome.CatalogSnapshot
	entered, release chan struct{}
}

func (b blockedSuggestionCatalog) Catalog(ctx context.Context) (smarthome.CatalogSnapshot, error) {
	close(b.entered)
	<-b.release
	return b.snapshot, nil
}
func TestConsoleSharingRevokedWhileCatalogPendingDoesNotCommit(t *testing.T) {
	s, _ := suggestionsFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("ready", "confirmed")})
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	before, _ := s.consoleStore.GetSharing()
	block := blockedSuggestionCatalog{s.areaCatalog.(testAreaCatalog).snapshot, make(chan struct{}), make(chan struct{})}
	s.areaCatalog = block
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", fmt.Sprintf(`{"revision":%d,"suggestion_ids":["ready"]}`, before.Revision), admin, csrf, testConsoleOrigin, "")
	}()
	<-block.entered
	logout := consoleRequest(s, "POST", consolePrefix+"/auth/logout", `{}`, admin, csrf, testConsoleOrigin, "")
	if logout.Code != 204 {
		close(block.release)
		t.Fatalf("logout failed: %d", logout.Code)
	}
	close(block.release)
	response := <-done
	if response.Code != 401 || strings.Contains(response.Body.String(), "suggestion_ids") {
		t.Fatal("revoked request published policy:", response.Body.String())
	}
	after, _ := s.consoleStore.GetSharing()
	if after.Revision != before.Revision || len(after.SuggestionIDs) != 0 {
		t.Fatal("revoked request changed sharing")
	}
}
func TestConsoleSuggestionLateMemberReadAfterLogoutIsSuppressed(t *testing.T) {
	s, _ := suggestionsFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("shared", "confirmed")})
	sharing, _ := s.consoleStore.GetSharing()
	s.consoleStore.PutSharing(sharing.Revision, nil, []string{"shared"})
	_, member := consoleMemberLogin(t, s, "Member")
	block := blockedSuggestionCatalog{s.areaCatalog.(testAreaCatalog).snapshot, make(chan struct{}), make(chan struct{})}
	s.areaCatalog = block
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- consoleRequest(s, "GET", consolePrefix+"/suggestions", "", member, "", "", "") }()
	<-block.entered
	// Direct Store logout is the same revocation operation as the authenticated
	// route; member CSRF is deliberately not inferred from another principal.
	if err := s.consoleStore.Logout(member.Value); err != nil {
		t.Fatal(err)
	}
	close(block.release)
	response := <-done
	if response.Code != 401 || strings.Contains(response.Body.String(), "shared") {
		t.Fatalf("late member data published: %d %s", response.Code, response.Body.String())
	}
}
func TestConsoleAnalyzeConcurrentRequestReturnsBusyCode(t *testing.T) {
	s, _ := suggestionsFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" {
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		fmt.Fprint(w, `{"time_zone":"UTC"}`)
	}))
	defer ha.Close()
	m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	s.smartHome = m
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- consoleRequest(s, "POST", consolePrefix+"/admin/analyze", `{"days":14}`, admin, csrf, testConsoleOrigin, "")
	}()
	<-entered
	second := consoleRequest(s, "POST", consolePrefix+"/admin/analyze", `{"days":14}`, admin, csrf, testConsoleOrigin, "")
	close(release)
	result := consoleJSON(t, second, 409)
	if result["error"].(map[string]any)["code"] != "busy" {
		t.Fatal("busy response indistinguishable:", result)
	}
	first := consoleJSON(t, <-done, 200)
	if first["suggestion_count"] != float64(0) || first["period_start"] == nil || first["period_end"] == nil {
		t.Fatal(first)
	}
}
func TestConsoleAnalyzeProducesUnboundSuggestionWithoutInstalling(t *testing.T) {
	s, writes := suggestionsFixture(t)
	end := time.Now().UTC().Truncate(24 * time.Hour)
	var entries []smarthome.HistoryEntry
	for day := 14; day > 0; day-- {
		at := end.AddDate(0, 0, -day).Add(18 * time.Hour)
		entries = append(entries, smarthome.HistoryEntry{EntityID: "light.room", State: "off", Timestamp: at.Add(-time.Minute), ObservationKind: "initial"}, smarthome.HistoryEntry{EntityID: "light.room", State: "on", Timestamp: at, ObservationKind: "change"})
	}
	if err := s.smartHome.GetStore().SaveHistory(entries); err != nil {
		t.Fatal(err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	result := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/analyze", `{"days":14}`, admin, csrf, testConsoleOrigin, ""), 200)
	if result["suggestion_count"] != float64(1) || writes.Load() != 0 {
		t.Fatalf("analysis installs or lacks real pattern: %v", result)
	}
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	view := list["suggestions"].([]any)[0].(map[string]any)
	if view["can_bind"] != true || view["can_confirm"] != false || view["rule"] != nil {
		t.Fatal(view)
	}
	id := view["id"].(string)
	before, _ := s.smartHome.GetStore().GetSuggestions()
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/"+id+"/bindings", `{"presence":{"entity_id":"binary_sensor.absent","state":"on"}}`, admin, csrf, testConsoleOrigin, ""), 422)
	after, _ := s.smartHome.GetStore().GetSuggestions()
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) || writes.Load() != 0 {
		t.Fatal("invalid binding mutated source")
	}
}
func TestConsoleSharingOfflineRemovalAllowedButNewShareRejected(t *testing.T) {
	s, _ := suggestionsFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("existing", "confirmed"), persistedTimeRule("new", "confirmed")})
	before, _ := s.consoleStore.GetSharing()
	before, err := s.consoleStore.PutSharing(before.Revision, []string{"legacy.entity"}, []string{"existing"})
	if err != nil {
		t.Fatal(err)
	}
	stale := s.areaCatalog.(testAreaCatalog)
	stale.snapshot.Meta.Freshness = "stale"
	s.areaCatalog = stale
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	result := consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", fmt.Sprintf(`{"revision":%d,"suggestion_ids":[]}`, before.Revision), admin, csrf, testConsoleOrigin, ""), 200)
	if len(result["suggestion_ids"].([]any)) != 0 {
		t.Fatal("offline revocation failed")
	}
	after, _ := s.consoleStore.GetSharing()
	if len(after.EntityIDs) != 1 || after.EntityIDs[0] != "legacy.entity" {
		t.Fatal("legacy data changed")
	}
	consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", fmt.Sprintf(`{"revision":%d,"suggestion_ids":["new"]}`, after.Revision), admin, csrf, testConsoleOrigin, ""), 503)
}
func TestConsoleSuggestionUpstreamAuthorizationIs502NotConsole401(t *testing.T) {
	s, _ := suggestionsFixture(t)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected write %s", r.URL.Path)
		}
		http.Error(w, "PRIVATE_HA_TOKEN_OR_PATH", 401)
	}))
	defer ha.Close()
	m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "fixture", AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	s.smartHome = m
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("pending", "pending")})
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	w := consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/confirm", `{}`, admin, csrf, testConsoleOrigin, "")
	result := consoleJSON(t, w, 502)
	if result["error"].(map[string]any)["code"] != "ha_auth_required" || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatalf("lost safe upstream cause: %s", w.Body.String())
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", admin, "", "", ""), 200)
}
