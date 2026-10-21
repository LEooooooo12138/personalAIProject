package smarthome

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type managementFixture struct {
	mu                                      sync.Mutex
	stored, runtime                         json.RawMessage
	state, version, entityID                string
	missingMapping, loseResponse, delayLoad bool
	writes                                  []string
	payloads                                []map[string]any
	onRead                                  func()
	beforeWrite                             func()
}

type managementTokenProvider struct {
	token func(context.Context) (string, error)
}

func (p managementTokenProvider) Token(ctx context.Context) (string, error) { return p.token(ctx) }
func (managementTokenProvider) Invalidate(string)                           {}
func (managementTokenProvider) Close()                                      {}

func newManagementFixture(t *testing.T) (*managementFixture, *Manager, ManagedAutomation) {
	t.Helper()
	f := &managementFixture{state: "on", version: "2026.7.2", entityID: "automation.installed"}
	states := []EntityState{{EntityID: "light.a", State: "off"}, {EntityID: "switch.b", State: "on"}, {EntityID: "binary_sensor.house", State: "on"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hook := f.onRead
		if r.Method == "GET" {
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
			c.ReadJSON(&auth)
			c.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				var req map[string]any
				if c.ReadJSON(&req) != nil {
					return
				}
				res := map[string]any{"id": req["id"], "type": "result", "success": true}
				f.mu.Lock()
				switch req["type"] {
				case "config/entity_registry/list":
					res["result"] = []map[string]any{}
					if f.stored != nil && !f.missingMapping {
						res["result"] = []map[string]any{{"entity_id": f.entityID, "platform": "automation", "unique_id": "rule-kitchen"}}
					}
				case "automation/config":
					if f.runtime == nil {
						res["success"] = false
						res["error"] = map[string]any{"code": "not_found"}
					} else {
						res["result"] = map[string]any{"config": json.RawMessage(append([]byte(nil), f.runtime...))}
					}
				default:
					t.Errorf("unexpected WS %v", req["type"])
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
			f.writes = append(f.writes, r.Method+" "+r.URL.Path)
			var payload map[string]any
			if r.Body != nil {
				json.NewDecoder(r.Body).Decode(&payload)
			}
			f.payloads = append(f.payloads, payload)
			switch r.URL.Path {
			case "/api/config/automation/config/rule-kitchen":
				if r.Method == "DELETE" {
					f.stored = nil
					f.runtime = nil
				} else {
					f.stored, _ = json.Marshal(payload)
					if !f.delayLoad {
						f.runtime = append([]byte(nil), f.stored...)
					}
				}
			case "/api/services/automation/reload":
				f.runtime = append([]byte(nil), f.stored...)
			case "/api/services/automation/turn_on":
				f.state = "on"
			case "/api/services/automation/turn_off":
				f.state = "off"
			default:
				t.Errorf("unexpected write %s %s", r.Method, r.URL.Path)
			}
			if f.loseResponse {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			io.WriteString(w, `{}`)
			return
		}
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(w).Encode(map[string]any{"version": f.version, "components": []string{"automation", "config"}, "time_zone": "UTC"})
		case "/api/config/automation/config/rule-kitchen":
			if f.stored == nil {
				w.WriteHeader(404)
			} else {
				w.Write(f.stored)
			}
		case "/api/states":
			json.NewEncoder(w).Encode(states)
		case "/api/states/automation.installed", "/api/states/automation.renamed":
			if f.runtime == nil || strings.TrimPrefix(r.URL.Path, "/api/states/") != f.entityID {
				w.WriteHeader(404)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"entity_id": f.entityID, "state": f.state, "attributes": map[string]any{}})
			}
		default:
			if strings.HasPrefix(r.URL.Path, "/api/states/") {
				for _, s := range states {
					if strings.TrimPrefix(r.URL.Path, "/api/states/") == s.EntityID {
						json.NewEncoder(w).Encode(s)
						return
					}
				}
			}
			t.Errorf("unexpected GET %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	s := RuleSuggestion{ID: "rule-kitchen", Status: "pending", Intent: &SuggestionIntent{SchemaVersion: 1, Kind: "time", EntityID: "light.a", At: "18:00", TimeZone: "UTC", RequiredConditions: []string{"presence_home"}}, PresenceBinding: &PresenceBinding{EntityID: "binary_sensor.house", State: "on"}}
	renderSuggestion(&s)
	if err = m.store.SaveSuggestions([]RuleSuggestion{s}); err != nil {
		t.Fatal(err)
	}
	fake := &catalogFake{states: states}
	for _, s := range states {
		fake.reg.Entities = append(fake.reg.Entities, RegistryEntity{EntityID: s.EntityID, Name: cp("fixture")})
	}
	m.catalog.Close()
	m.catalog = NewCatalogService(context.Background(), fake)
	if _, err = m.ConfirmSuggestion(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	op, err := m.GetInstallOperationForSuggestion(s.ID)
	if err != nil || op.Status != "confirmed" {
		t.Fatalf("fixture install: %+v %v", op, err)
	}
	managed, err := m.GetManagedAutomation(op.ManagedAutomationID)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.writes = nil
	f.payloads = nil
	f.mu.Unlock()
	return f, m, managed
}
