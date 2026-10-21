package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

type consoleManagementFixture struct {
	mu            sync.Mutex
	config        json.RawMessage
	state         string
	reads, writes int
	loseResponse  bool
	onRead        func()
	beforeWrite   func()
	archivePath   string
	payload       map[string]any
}

func (f *consoleManagementFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads, f.writes
}

func newConsoleManagementFixture(t *testing.T) (*Server, *consoleManagementFixture, smarthome.ManagedAutomation) {
	t.Helper()
	f := &consoleManagementFixture{state: "on"}
	states := []smarthome.EntityState{{EntityID: "light.room", State: "off"}, {EntityID: "switch.source", State: "on"}, {EntityID: "binary_sensor.house", State: "on"}}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		var hook func()
		if r.Method == "GET" {
			f.reads++
			hook = f.onRead
			f.onRead = nil
		} else {
			hook = f.beforeWrite
		}
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		if r.URL.Path == "/api/websocket" {
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
				res := map[string]any{"id": req["id"], "type": "result", "success": true}
				f.mu.Lock()
				f.reads++
				switch req["type"] {
				case "config/entity_registry/list":
					entries := []map[string]any{{"entity_id": "light.room"}, {"entity_id": "switch.source"}, {"entity_id": "binary_sensor.house"}}
					if f.config != nil {
						entries = append(entries, map[string]any{"entity_id": "automation.managed_fixture", "platform": "automation", "unique_id": "managed-source"})
					}
					res["result"] = entries
				case "config/area_registry/list", "config/device_registry/list", "config/label_registry/list":
					res["result"] = []any{}
				case "automation/config":
					if f.config == nil {
						res["success"] = false
						res["error"] = map[string]string{"code": "not_found"}
					} else {
						res["result"] = map[string]any{"config": json.RawMessage(append([]byte(nil), f.config...))}
					}
				default:
					t.Errorf("unexpected management WS %v", req["type"])
				}
				f.mu.Unlock()
				if c.WriteJSON(res) != nil {
					return
				}
			}
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != "GET" {
			f.writes++
			var body map[string]any
			if r.Body != nil {
				json.NewDecoder(r.Body).Decode(&body)
			}
			f.payload = body
			switch r.URL.Path {
			case "/api/config/automation/config/managed-source":
				if r.Method == "DELETE" {
					f.config = nil
				} else {
					f.config, _ = json.Marshal(body)
				}
			case "/api/services/automation/reload":
			case "/api/services/automation/turn_on":
				f.state = "on"
			case "/api/services/automation/turn_off":
				f.state = "off"
			default:
				t.Errorf("unexpected management write %s %s", r.Method, r.URL.Path)
			}
			if f.loseResponse {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.Write([]byte(`{}`))
			return
		}
		switch r.URL.Path {
		case "/api/config":
			w.Write([]byte(`{"version":"2026.7.2","components":["automation","config"],"time_zone":"UTC"}`))
		case "/api/config/automation/config/managed-source":
			if f.config == nil {
				w.WriteHeader(404)
			} else {
				w.Write(f.config)
			}
		case "/api/states":
			json.NewEncoder(w).Encode(states)
		case "/api/states/automation.managed_fixture":
			if f.config == nil {
				w.WriteHeader(404)
			} else {
				json.NewEncoder(w).Encode(smarthome.EntityState{EntityID: "automation.managed_fixture", State: f.state})
			}
		default:
			for _, state := range states {
				if r.URL.Path == "/api/states/"+state.EntityID {
					json.NewEncoder(w).Encode(state)
					return
				}
			}
			if strings.HasPrefix(r.URL.Path, "/api/config/automation/config/") {
				w.WriteHeader(404)
				return
			}
			t.Errorf("unexpected management GET %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(ha.Close)
	f.archivePath = t.TempDir()
	manager, e := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "fixture-only", AgentVaultPath: f.archivePath}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(manager.Stop)
	s := consoleTestServer(t, testConsoleOrigin)
	s.smartHome = manager
	consoleBootstrap(t, s)
	if e = manager.GetStore().SaveSuggestions([]smarthome.RuleSuggestion{persistedTimeRule("managed-source", "pending")}); e != nil {
		t.Fatal(e)
	}
	if _, e = manager.ConfirmSuggestion(context.Background(), "managed-source"); e != nil {
		t.Fatal(e)
	}
	install, e := manager.GetInstallOperationForSuggestion("managed-source")
	if e != nil || install.Status != "confirmed" {
		t.Fatalf("fixture installation %+v %v", install, e)
	}
	managed, e := manager.GetManagedAutomation(install.ManagedAutomationID)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.reads = 0
	f.writes = 0
	f.mu.Unlock()
	return s, f, managed
}
