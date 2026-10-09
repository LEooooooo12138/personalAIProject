package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

// Missing business registration, unsafe projection or implicit confirmation must
// fail this real Cookie/CSRF/Manager/store contract, not a mocked handler.
func suggestionsFixture(t *testing.T) (*Server, *atomic.Int32) {
	s, writes, _ := suggestionsFixtureWithArchive(t)
	return s, writes
}
func suggestionsFixtureWithArchive(t *testing.T) (*Server, *atomic.Int32, string) {
	t.Helper()
	writes := &atomic.Int32{}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/config":
			fmt.Fprint(w, `{"time_zone":"UTC"}`)
		case r.Method == "GET" && r.URL.Path == "/api/states":
			fmt.Fprint(w, `[{"entity_id":"light.room","state":"off"},{"entity_id":"switch.source","state":"on"},{"entity_id":"binary_sensor.house","state":"on"}]`)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/config/automation/config/"):
			var a smarthome.AutomationConfig
			if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
				t.Error(err)
			}
			if len(a.Condition) != 1 || a.Condition[0].EntityID != "binary_sensor.house" {
				t.Errorf("lost explicit presence: %+v", a)
			}
			writes.Add(1)
			fmt.Fprint(w, `{}`)
		case r.Method == "POST" && r.URL.Path == "/api/services/automation/reload":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected mock HA request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ha.Close)
	archive := t.TempDir()
	manager, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "fixture-only", AgentVaultPath: archive}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	s := consoleTestServer(t, testConsoleOrigin)
	s.smartHome = manager
	consoleBootstrap(t, s)
	// A local source feeds the real CatalogService without real HA connections.
	catalog := smarthome.NewCatalogService(context.Background(), suggestionCatalogSource{})
	t.Cleanup(catalog.Close)
	snapshot, err := catalog.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.areaCatalog = testAreaCatalog{snapshot: snapshot}
	registered := false
	for _, route := range s.engine.Routes() {
		if route.Path == consolePrefix+"/suggestions" {
			registered = true
		}
	}
	if !registered {
		if reg, ok := any(s).(interface{ setupConsoleSmartHomeRoutes(*gin.RouterGroup) }); ok {
			reg.setupConsoleSmartHomeRoutes(s.engine.Group(consolePrefix))
		}
	}
	return s, writes, archive
}

type suggestionCatalogSource struct{}

func (suggestionCatalogSource) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	return smarthome.RegistrySnapshot{Entities: []smarthome.RegistryEntity{{EntityID: "light.room"}, {EntityID: "switch.source"}, {EntityID: "binary_sensor.house"}}}, nil
}
func (suggestionCatalogSource) GetStates(context.Context) ([]smarthome.EntityState, error) {
	return []smarthome.EntityState{{EntityID: "light.room", State: "off"}, {EntityID: "switch.source", State: "on"}, {EntityID: "binary_sensor.house", State: "on"}}, nil
}
func persistedTimeRule(id, status string) smarthome.RuleSuggestion {
	return smarthome.RuleSuggestion{ID: id, Status: status, CreatedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), Title: "RAW_TITLE_SECRET", Description: "RAW_DESCRIPTION_SECRET", DataSource: "930 secrets, guess not allowed", LastError: "RAW_ERROR_SECRET", Confidence: .8, Intent: &smarthome.SuggestionIntent{SchemaVersion: 1, Kind: "time", EntityID: "light.room", At: "18:00", TimeZone: "UTC", RequiredConditions: []string{"presence_home"}}, PresenceBinding: &smarthome.PresenceBinding{EntityID: "binary_sensor.house", State: "on"}}
}
func TestConsoleSuggestionBindingConfirmationIsExplicitAndIdempotent(t *testing.T) {
	s, writes, archive := suggestionsFixtureWithArchive(t)
	pending := persistedTimeRule("pending", "pending")
	pending.PresenceBinding = nil
	if err := s.smartHome.GetStore().SaveSuggestions([]smarthome.RuleSuggestion{pending}); err != nil {
		t.Fatal(err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	before := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	view := before["suggestions"].([]any)[0].(map[string]any)
	if view["can_bind"] != true || view["can_confirm"] != false || view["rule"] != nil {
		t.Fatalf("unbound rule claims executable: %v", view)
	}
	bound := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/bindings", `{"presence":{"entity_id":"binary_sensor.house","state":"on"}}`, admin, csrf, testConsoleOrigin, ""), 200)["suggestion"].(map[string]any)
	id := bound["id"].(string)
	if id == "pending" || bound["source_suggestion_id"] != "pending" || bound["can_confirm"] != true || writes.Load() != 0 {
		t.Fatalf("binding implicitly confirmed or lost revision: %v writes=%d", bound, writes.Load())
	}
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 409)
	versions := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	for _, raw := range versions["suggestions"].([]any) {
		old := raw.(map[string]any)
		if old["id"] == "pending" && (old["status"] != "superseded" || old["can_bind"] != false || old["can_confirm"] != false || old["superseded_by"] != id) {
			t.Fatalf("superseded version advertises mutation: %v", old)
		}
	}

	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/"+id+"/confirm", `{}`, admin, csrf, testConsoleOrigin, "")
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		v := consoleJSON(t, result, 200)["suggestion"].(map[string]any)
		if v["status"] != "confirmed" {
			t.Fatal(v)
		}
	}
	if writes.Load() != 1 {
		t.Fatalf("duplicate HA install: %d", writes.Load())
	}
	document, err := os.ReadFile(filepath.Join(archive, "rules", id+".md"))
	if err != nil || !strings.Contains(string(document), "binary_sensor.house") {
		t.Fatalf("confirmed rule archive missing: %v", err)
	}

}
func TestConsoleSuggestionSharedMemberProjectionChecksEveryEntity(t *testing.T) {
	s, _ := suggestionsFixture(t)
	good := persistedTimeRule("shared", "confirmed")
	missing := persistedTimeRule("missing-condition", "confirmed")
	missing.PresenceBinding.EntityID = "binary_sensor.absent"
	hidden := persistedTimeRule("not-shared", "confirmed")
	unknown := smarthome.RuleSuggestion{ID: "legacy", Status: "confirmed", Title: "RAW_LEGACY_SECRET"}
	seedSuggestions(t, s, []smarthome.RuleSuggestion{good, missing, hidden, unknown})
	sharing, _ := s.consoleStore.GetSharing()
	if _, err := s.consoleStore.PutSharing(sharing.Revision, []string{"legacy.entity"}, []string{"shared", "missing-condition", "legacy"}); err != nil {
		t.Fatal(err)
	}
	_, member := consoleMemberLogin(t, s, "Member")
	w := consoleRequest(s, "GET", consolePrefix+"/suggestions", "", member, "", "", "")
	result := consoleJSON(t, w, 200)
	items := result["suggestions"].([]any)
	if len(items) != 1 || result["count"] != float64(1) {
		t.Fatalf("unauthorized rule leaked into items/count: %v", result)
	}
	view := items[0].(map[string]any)
	if view["id"] != "shared" || view["can_confirm"] != false || len(view["entity_ids"].([]any)) != 2 {
		t.Fatal(view)
	}
	if _, ok := view["source_suggestion_id"]; ok {
		t.Fatal("member sees version chain")
	}
	if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "930") {
		t.Fatal("raw free text leaked")
	}
	evidence := view["evidence"].(map[string]any)
	if evidence["sample_size"] != nil || evidence["period_days"] != nil {
		t.Fatal("invented evidence")
	}
	stale := s.areaCatalog.(testAreaCatalog)
	stale.snapshot.Meta.Freshness = "stale"
	s.areaCatalog = stale
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", member, "", "", ""), 503)
}
func TestConsoleSharingRevisionAndLegacyEntityIDs(t *testing.T) {
	s, _ := suggestionsFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("ready", "confirmed"), persistedTimeRule("another", "confirmed"), persistedTimeRule("pending", "pending")})
	sharing, _ := s.consoleStore.GetSharing()
	sharing, err := s.consoleStore.PutSharing(sharing.Revision, []string{"legacy.entity"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	get := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/sharing", "", admin, "", "", ""), 200)
	if _, ok := get["entity_ids"]; ok {
		t.Fatal("restored device ACL")
	}
	put := fmt.Sprintf(`{"revision":%d,"suggestion_ids":["ready"]}`, sharing.Revision)
	consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", put, admin, csrf, testConsoleOrigin, ""), 200)
	consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", put, admin, csrf, testConsoleOrigin, ""), 409)
	actual, _ := s.consoleStore.GetSharing()
	if len(actual.EntityIDs) != 1 || actual.EntityIDs[0] != "legacy.entity" {
		t.Fatal("destroyed existing EntityIDs")
	}
	invalid := fmt.Sprintf(`{"revision":%d,"suggestion_ids":["pending"]}`, actual.Revision)
	consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", invalid, admin, csrf, testConsoleOrigin, ""), 422)
	stale := s.areaCatalog.(testAreaCatalog)
	stale.snapshot.Meta.Freshness = "stale"
	s.areaCatalog = stale
	consoleJSON(t, consoleRequest(s, "PUT", consolePrefix+"/admin/sharing", fmt.Sprintf(`{"revision":%d,"suggestion_ids":["ready","another"]}`, actual.Revision), admin, csrf, testConsoleOrigin, ""), 503)
}
func TestConsoleSuggestionManagementAuthorizationAndCollectionUnknown(t *testing.T) {
	s, writes := suggestionsFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	for _, part := range []string{"/suggestions/guess/bindings", "/suggestions/guess/confirm", "/suggestions/guess/ignore", "/analyze"} {
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin"+part, `{}`, member, csrf, testConsoleOrigin, ""), 403)
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin"+part, `{}`, admin, "", testConsoleOrigin, ""), 403)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/collection", "", member, "", "", ""), 403)
	state := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/collection", "", admin, "", "", ""), 200)
	for _, key := range []string{"last_attempt_at", "last_success_at", "last_failure_at", "snapshot_at", "checkpoint"} {
		if state[key] != nil {
			t.Fatalf("new process manufactured %s: %v", key, state)
		}
	}
	if state["phase"] != "idle" || writes.Load() != 0 {
		t.Fatal(state)
	}
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/analyze", `{"days":1}`, admin, csrf, testConsoleOrigin, ""), 422)

}

func seedSuggestions(t *testing.T, s *Server, items []smarthome.RuleSuggestion) {
	t.Helper()
	store := s.smartHome.GetStore()
	if err := store.SaveSuggestions(items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status == "confirmed" {
			if err := store.UpdateSuggestionStatus(item.ID, "applying"); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateSuggestionStatus(item.ID, "confirmed"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
