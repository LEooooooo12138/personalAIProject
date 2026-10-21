package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

func TestConsoleSuggestionRevisionVersionAndIndependentConfirmation(t *testing.T) {
	var writes atomic.Int32
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
			http.Error(w, "unexpected write", 500)
			return
		}
		switch r.URL.Path {
		case "/api/config":
			fmt.Fprint(w, `{"time_zone":"UTC"}`)
		case "/api/states":
			fmt.Fprint(w, `[{"entity_id":"light.room","state":"off"},{"entity_id":"switch.source","state":"on"},{"entity_id":"binary_sensor.house","state":"on"}]`)
		case "/api/websocket":
			c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			c.WriteJSON(map[string]any{"type": "auth_required"})
			var req map[string]any
			if c.ReadJSON(&req) != nil {
				return
			}
			c.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				if c.ReadJSON(&req) != nil {
					return
				}
				result := []map[string]any{}
				switch req["type"] {
				case "config/entity_registry/list":
					result = []map[string]any{{"entity_id": "light.room"}, {"entity_id": "switch.source"}, {"entity_id": "binary_sensor.house"}}
				case "config/area_registry/list", "config/device_registry/list", "config/label_registry/list":
				default:
					t.Errorf("unexpected WS command %v", req["type"])
				}
				c.WriteJSON(map[string]any{"id": req["id"], "type": "result", "success": true, "result": result})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ha.Close()
	m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "fixture", AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	s := consoleTestServer(t, testConsoleOrigin)
	s.smartHome = m
	consoleBootstrap(t, s)
	source := persistedTimeRule("pending", "pending")
	if err = m.GetStore().SaveSuggestions([]smarthome.RuleSuggestion{source}); err != nil {
		t.Fatal(err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	request := smarthome.SuggestionRevisionRequest{ExpectedVersion: "pending", Intent: *source.Intent, PresenceBinding: source.PresenceBinding}
	request.Intent.At = "19:30"
	request.Intent.EntityID = "switch.source"
	body, _ := json.Marshal(request)
	path := consolePrefix + "/admin/suggestions/pending/revisions"
	consoleJSON(t, consoleRequest(s, "POST", path, string(body), member, csrf, testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", path, string(body), admin, "", testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", path, `{"expected_version":"pending","yaml":"action: turn_off"}`, admin, csrf, testConsoleOrigin, ""), 422)
	request.ExpectedVersion = ""
	missing, _ := json.Marshal(request)
	consoleJSON(t, consoleRequest(s, "POST", path, string(missing), admin, csrf, testConsoleOrigin, ""), 409)
	saved := consoleJSON(t, consoleRequest(s, "POST", path, string(body), admin, csrf, testConsoleOrigin, ""), 200)
	view := saved["suggestion"].(map[string]any)
	if view["id"] == "pending" || view["can_revise"] != true || view["can_confirm"] != true || view["intent"] == nil || len(saved["diff"].([]any)) != 2 {
		t.Fatalf("revision projection %v", saved)
	}
	if view["evidence"].(map[string]any)["sample_size"] != nil {
		t.Fatal("revision inherited historical evidence")
	}
	consoleJSON(t, consoleRequest(s, "POST", path, string(body), admin, csrf, testConsoleOrigin, ""), 409)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/pending/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 409)
	if writes.Load() != 0 {
		t.Fatalf("unconfirmed/stale requests wrote HA %d", writes.Load())
	}
}
