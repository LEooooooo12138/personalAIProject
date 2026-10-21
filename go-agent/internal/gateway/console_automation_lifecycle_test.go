package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// HTTP/WS fixture uses the public Manager install/update/delete APIs and real
// stores. It never constructs an installation journal or managed draft by hand.
func newAutomationProjectionFixture(t *testing.T, roots ...*string) (*Server, *atomic.Int32) {
	t.Helper()
	s, _, dir := suggestionsFixtureWithArchive(t)
	if len(roots) > 0 {
		*roots[0] = dir
	}
	var mu sync.Mutex
	configs := map[string]json.RawMessage{}
	automationStates := map[string]string{}
	externalConfig, err := smarthome.BuildAutomation(persistedTimeRule("outside", "confirmed"))
	if err != nil {
		t.Fatal(err)
	}
	configs["outside"], _ = json.Marshal(externalConfig)
	writes := &atomic.Int32{}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/config/automation/config/"
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/config":
			json.NewEncoder(w).Encode(map[string]any{"version": "2026.7.2", "components": []string{"automation", "config"}, "time_zone": "UTC"})
		case strings.HasPrefix(r.URL.Path, prefix):
			key := strings.TrimPrefix(r.URL.Path, prefix)
			mu.Lock()
			defer mu.Unlock()
			switch r.Method {
			case "GET":
				if raw, ok := configs[key]; ok {
					w.Write(raw)
				} else {
					http.NotFound(w, r)
				}
			case "POST":
				var raw json.RawMessage
				if json.NewDecoder(r.Body).Decode(&raw) != nil {
					t.Error("invalid config")
					return
				}
				configs[key] = raw
				writes.Add(1)
				w.Write([]byte(`{}`))
			case "DELETE":
				delete(configs, key)
				writes.Add(1)
				w.Write([]byte(`{}`))
			default:
				t.Errorf("unexpected write %s", r.Method)
				w.WriteHeader(400)
			}
		case r.URL.Path == "/api/services/automation/reload" && r.Method == "POST":
			writes.Add(1)
			w.Write([]byte(`[]`))
		case r.Method == "POST" && (r.URL.Path == "/api/services/automation/turn_on" || r.URL.Path == "/api/services/automation/turn_off"):
			var body struct {
				EntityID string `json:"entity_id"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid service body")
				return
			}
			state := "on"
			if strings.HasSuffix(r.URL.Path, "turn_off") {
				state = "off"
			}
			mu.Lock()
			automationStates[body.EntityID] = state
			mu.Unlock()
			writes.Add(1)
			w.Write([]byte(`[]`))
		case r.URL.Path == "/api/states":
			states := []map[string]any{{"entity_id": "light.room", "state": "off"}, {"entity_id": "switch.source", "state": "on"}, {"entity_id": "binary_sensor.house", "state": "on"}}
			mu.Lock()
			for key := range configs {
				states = append(states, map[string]any{"entity_id": "automation." + key, "state": "on", "attributes": map[string]any{"friendly_name": "fixture rule"}})
			}
			mu.Unlock()
			json.NewEncoder(w).Encode(states)
		case strings.HasPrefix(r.URL.Path, "/api/states/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/states/")
			exists := true
			state := "on"
			if strings.HasPrefix(id, "automation.") {
				mu.Lock()
				_, exists = configs[strings.TrimPrefix(id, "automation.")]
				if observed := automationStates[id]; observed != "" {
					state = observed
				}
				mu.Unlock()
			}
			if !exists {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"entity_id": id, "state": state, "attributes": map[string]any{"private": "PRIVATE_ATTRIBUTE"}})
		case r.URL.Path == "/api/websocket":
			c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if e != nil {
				return
			}
			defer c.Close()
			c.WriteJSON(map[string]any{"type": "auth_required"})
			var auth any
			if c.ReadJSON(&auth) != nil {
				return
			}
			c.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				var req map[string]any
				if c.ReadJSON(&req) != nil {
					return
				}
				out := map[string]any{"id": req["id"], "type": "result", "success": true}
				switch req["type"] {
				case "config/entity_registry/list":
					entries := []map[string]any{{"entity_id": "light.room"}, {"entity_id": "switch.source"}, {"entity_id": "binary_sensor.house"}}
					mu.Lock()
					for key := range configs {
						entries = append(entries, map[string]any{"entity_id": "automation." + key, "platform": "automation", "unique_id": key})
					}
					mu.Unlock()
					out["result"] = entries
				case "config/area_registry/list", "config/device_registry/list", "config/label_registry/list":
					out["result"] = []any{}
				case "automation/config":
					mu.Lock()
					raw, ok := configs[strings.TrimPrefix(req["entity_id"].(string), "automation.")]
					mu.Unlock()
					if ok {
						out["result"] = map[string]any{"config": raw}
					} else {
						out["success"] = false
						out["error"] = map[string]any{"code": "not_found"}
					}
				case "trace/list":
					out["success"] = false
					out["error"] = map[string]any{"code": "unauthorized", "message": "PRIVATE_TRACE_ERROR"}
				default:
					t.Errorf("unexpected WS %v", req["type"])
					return
				}
				if c.WriteJSON(out) != nil {
					return
				}
			}
		default:
			if r.Method != "GET" {
				writes.Add(1)
			}
			t.Errorf("unexpected HA route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ha.Close)
	m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "fixture", AgentVaultPath: dir}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	s.smartHome = m
	s.areaCatalog = m
	return s, writes
}

func TestConsoleAutomationCurrentVersionAndDeletion(t *testing.T) {
	s, writes := newAutomationProjectionFixture(t)
	original := persistedTimeRule("original", "pending")
	seedSuggestions(t, s, []smarthome.RuleSuggestion{original})
	if _, err := s.smartHome.ConfirmSuggestion(context.Background(), original.ID); err != nil {
		t.Fatal(err)
	}
	op, _ := s.smartHome.GetInstallOperationForSuggestion(original.ID)
	managed, _ := s.smartHome.GetManagedAutomation(op.ManagedAutomationID)
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	grant := func(ids []string) {
		t.Helper()
		sh, _ := s.consoleStore.GetSharing()
		if _, e := s.consoleStore.PutSharing(sh.Revision, ids, []string{original.ID}); e != nil {
			t.Fatal(e)
		}
	}
	grant([]string{"light.room", "binary_sensor.house"})
	ctx := smarthome.WithManagementAuthority(context.Background(), smarthome.ManagementAuthority{UserID: "admin", SessionID: "test", Check: func(context.Context) error { return nil }})
	intent := *original.Intent
	intent.EntityID = "switch.source"
	intent.At = "19:30"
	draft, err := s.smartHome.ReviseManagedAutomation(ctx, managed.ID, smarthome.SuggestionRevisionRequest{ExpectedVersion: original.ID, Intent: intent, PresenceBinding: original.PresenceBinding})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, e := s.smartHome.ConfirmAutomationAction(ctx, prepared.ID); e != nil || got.Status != "confirmed" {
		t.Fatalf("update %+v %v", got, e)
	}
	for _, route := range []string{"/suggestions", "/automations"} {
		body := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+route, "", member, "", "", ""), 200)
		key := "suggestions"
		if route == "/automations" {
			key = "automations"
		}
		if len(body[key].([]any)) != 0 {
			t.Fatalf("unshared current target visible: %+v", body)
		}
	}
	// Explicitly grant current target, removing original target entirely.
	grant([]string{"switch.source", "binary_sensor.house"})
	for _, route := range []string{"/suggestions", "/automations"} {
		response := consoleRequest(s, "GET", consolePrefix+route, "", member, "", "", "")
		body := consoleJSON(t, response, 200)
		key := "suggestions"
		if route == "/automations" {
			key = "automations"
		}
		rows := body[key].([]any)
		if len(rows) != 1 {
			t.Fatalf("current grants rejected %+v", body)
		}
		row := rows[0].(map[string]any)
		currentJSON, _ := json.Marshal(row)
		if row["current_version"] != draft.ID || strings.Contains(string(currentJSON), "light.room") || !strings.Contains(string(currentJSON), "switch.source") || !strings.Contains(string(currentJSON), "19:30") {
			t.Fatalf("old payload/current permission mismatch %s", response.Body.String())
		}
		if route == "/suggestions" {
			history, _ := json.Marshal(body["shared_results"])
			if !strings.Contains(string(history), "light.room") || strings.Contains(string(history), "switch.source") || strings.Contains(string(history), "current_version") || strings.Contains(string(history), "diagnostic") || strings.Contains(string(history), "observed_state") {
				t.Fatalf("immutable shared result mixed current payload: %s", history)
			}
		}
	}
	old := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/install-operations/"+op.ID, "", admin, "", "", ""), 200)["operation"].(map[string]any)
	if old["historical"] != true || old["can_reconcile"] != false {
		t.Fatalf("old operation controls %+v", old)
	}
	managed, _, err = s.smartHome.GetManagedAutomationVersion(managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: managed.ID, Action: "delete", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	if got, e := s.smartHome.ConfirmAutomationAction(ctx, deletion.ID); e != nil || got.Status != "confirmed" {
		t.Fatalf("delete %+v %v", got, e)
	}
	before := writes.Load()
	body := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	row := body["suggestions"].([]any)[0].(map[string]any)
	installation := row["installation"].(map[string]any)
	if row["installation_state"] != "deleted" || installation["can_reconcile"] != false || installation["historical"] != true || installation["deleted_at"] == nil {
		t.Fatalf("tombstone is not historical %+v", row)
	}
	for _, id := range []string{op.ID, managed.OperationID} {
		detail := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/install-operations/"+id, "", admin, "", "", ""), 200)["operation"].(map[string]any)
		if detail["can_reconcile"] != false || detail["deleted_at"] == nil {
			t.Fatal(detail)
		}
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/install-operations/"+id+"/reconcile", `{}`, admin, csrf, testConsoleOrigin, ""), 409)
	}
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)
	counts := list["counts"].(map[string]any)
	if counts["installed"] != float64(0) || counts["archived"] != float64(1) || counts["attention"] != float64(0) {
		t.Fatal(counts)
	}
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automations/"+managed.ID+"/diagnostics", `{}`, admin, csrf, testConsoleOrigin, ""), 404)
	if writes.Load() != before {
		t.Fatal("historical projection/reconcile dispatched HA writes")
	}
}

func TestConsoleAutomationDiagnosticRoutePersistsSafePartialSnapshot(t *testing.T) {
	s, writes := newAutomationProjectionFixture(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("diagnose", "pending")})
	if _, e := s.smartHome.ConfirmSuggestion(context.Background(), "diagnose"); e != nil {
		t.Fatal(e)
	}
	op, _ := s.smartHome.GetInstallOperationForSuggestion("diagnose")
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	before := writes.Load()
	end := time.Now().UTC()
	raw, _ := json.Marshal(map[string]any{"window": smarthome.TimeWindow{Start: end.Add(-24 * time.Hour), End: end}})
	response := consoleRequest(s, "POST", consolePrefix+"/admin/automations/"+op.ManagedAutomationID+"/diagnostics", string(raw), admin, csrf, testConsoleOrigin, "")
	body := consoleJSON(t, response, 200)
	if body["partial"] != true || strings.Contains(response.Body.String(), "PRIVATE") {
		t.Fatalf("unsafe partial diagnostic %s", response.Body.String())
	}
	snapshot, e := s.smartHome.GetLatestAutomationDiagnostic(smarthome.AutomationRef{Kind: "managed", ID: op.ManagedAutomationID})
	if e != nil || snapshot.ID == "" {
		t.Fatalf("missing persisted snapshot %v", e)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations/"+op.ManagedAutomationID+"/diagnostics/latest", "", admin, "", "", ""), 200)
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)
	candidates := list["external_candidates"].([]any)
	if len(candidates) != 1 || candidates[0].(map[string]any)["ref"].(map[string]any)["id"] != "automation.outside" {
		t.Fatalf("server selection %+v", candidates)
	}
	externalBody, _ := json.Marshal(map[string]any{"ref": smarthome.AutomationRef{Kind: "external", ID: "automation.outside"}, "window": snapshot.Window})
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automation-diagnostics", string(externalBody), admin, csrf, testConsoleOrigin, ""), 200)
	if _, e = s.smartHome.GetInstallOperationForSuggestion("outside"); !errors.Is(e, smarthome.ErrInstallOperationNotFound) {
		t.Fatalf("external diagnosis adopted rule: %v", e)
	}
	if writes.Load() != before {
		t.Fatal("read-only diagnostics dispatched writes")
	}
	if _, e = s.smartHome.GetInstallOperationForSuggestion("not-selected"); !errors.Is(e, smarthome.ErrInstallOperationNotFound) {
		t.Fatal(e)
	}
}
