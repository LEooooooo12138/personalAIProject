package gateway

import (
	"bytes"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Adds the reviewed HTTP/WS proof contract to older payload-focused tests.
// The supplied handler still checks every actual write and supplies /api/config.
func gatewayInstallationProofHandler(t *testing.T, next http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	var mu sync.Mutex
	stored := map[string]json.RawMessage{}
	loaded := map[string]json.RawMessage{}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/config" {
			rec := httptest.NewRecorder()
			next(rec, r)
			if rec.Code >= 200 && rec.Code < 300 {
				var config map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &config); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				config["version"] = "2026.7.2"
				config["components"] = []string{"automation", "config"}
				json.NewEncoder(w).Encode(config)
				return
			}
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
			return
		}
		const prefix = "/api/config/automation/config/"
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, prefix) {
			key := strings.TrimPrefix(r.URL.Path, prefix)
			mu.Lock()
			raw, ok := stored[key]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write(raw)
			return
		}
		if r.URL.Path == "/api/states/automation.test_rule" && r.Method == http.MethodGet {
			io.WriteString(w, `{"entity_id":"automation.test_rule","state":"on","attributes":{}}`)
			return
		}
		if r.URL.Path == "/api/websocket" && r.Method == http.MethodGet {
			c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
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
				switch req["type"] {
				case "config/entity_registry/list":
					entries := []map[string]any{}
					mu.Lock()
					for key := range stored {
						entries = append(entries, map[string]any{"entity_id": "automation.test_rule", "platform": "automation", "unique_id": key})
					}
					mu.Unlock()
					res["result"] = entries
				case "automation/config":
					if req["entity_id"] != "automation.test_rule" {
						t.Error("runtime used unverified entity")
						return
					}
					mu.Lock()
					var raw json.RawMessage
					for _, v := range loaded {
						raw = v
					}
					mu.Unlock()
					if raw == nil {
						res["success"] = false
						res["error"] = map[string]any{"code": "not_found"}
					} else {
						res["result"] = map[string]any{"config": raw}
					}
				default:
					t.Errorf("unexpected proof WS %v", req["type"])
					return
				}
				if c.WriteJSON(res) != nil {
					return
				}
			}
		}
		if r.Method == http.MethodPost && (strings.HasPrefix(r.URL.Path, prefix) || r.URL.Path == "/api/services/automation/reload") {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			rec := httptest.NewRecorder()
			next(rec, r)
			if rec.Code >= 200 && rec.Code < 300 {
				mu.Lock()
				if strings.HasPrefix(r.URL.Path, prefix) {
					stored[strings.TrimPrefix(r.URL.Path, prefix)] = append([]byte(nil), raw...)
				} else {
					for key, v := range stored {
						loaded[key] = append([]byte(nil), v...)
					}
				}
				mu.Unlock()
			}
			for key, vs := range rec.Header() {
				for _, v := range vs {
					w.Header().Add(key, v)
				}
			}
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes())
			return
		}
		next(w, r)
	}
}
