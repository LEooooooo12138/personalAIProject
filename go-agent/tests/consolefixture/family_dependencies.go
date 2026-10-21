package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

const fixtureHAToken = "fixture-ha-secret"

// Only these two disposable loopback servers are reachable by the fixture.
// The HA mock accepts directory/state reads, explicit automation installs, and
// on/off for one configured synthetic switch.
// No requests leave loopback; only the approved synthetic switch accepts on/off.
// The Ollama mock lists names only; chat and embedding remain canned/offline.
type fixtureDependencies struct {
	automationWrites  atomic.Int32
	automationReloads atomic.Int32
	deviceWrites      atomic.Int32
	writeAttempts     fixtureWriteAttempts
	lifecycle         *fixtureLifecycle
	lifecycleControl  *lifecycleControl
	stateMu           sync.Mutex
	ha                *httptest.Server
	tags              *httptest.Server
}

func fixtureString(value string) *string { return &value }

func newFixtureDependencies(extended ...bool) *fixtureDependencies {
	registry := smarthome.RegistrySnapshot{
		Areas: []smarthome.RegistryArea{
			{ID: "b1", Name: "地下室B1", Aliases: []string{"地下室"}},
			{ID: "living", Name: "客厅"},
			{ID: "kitchen", Name: "厨房"},
			{ID: "empty", Name: "空房间"},
			{ID: "other", Name: "其他"},
		},
		Devices: []smarthome.RegistryDevice{
			{ID: "living-control", Name: fixtureString("客厅三路开关"), AreaID: fixtureString("living")},
			{ID: "covered-control", Name: fixtureString("实体已覆盖的控制器"), AreaID: fixtureString("living")},
			{ID: "soft-empty", Name: fixtureString("软件设备"), AreaID: fixtureString("other")},
		},
		Entities: []smarthome.RegistryEntity{
			{EntityID: "group.family_home", Name: fixtureString("全家有人（模拟聚合）"), AreaID: fixtureString("other")},
			{EntityID: "switch.channel_1", Name: fixtureString("开关 1"), DeviceID: fixtureString("living-control")},
			{EntityID: "switch.channel_2", Name: fixtureString("开关 2"), DeviceID: fixtureString("living-control"), AreaID: fixtureString("kitchen")},
			{EntityID: "sensor.diagnostic", Name: fixtureString("诊断信息"), DeviceID: fixtureString("living-control"), DisabledBy: fixtureString("integration"), HiddenBy: fixtureString("user"), Category: fixtureString("diagnostic")},
			{EntityID: "switch.covered", Name: fixtureString("被覆盖的通道"), DeviceID: fixtureString("covered-control"), AreaID: fixtureString("kitchen")},
			{EntityID: "sensor.temperature", Name: fixtureString("独立温度计"), AreaID: fixtureString("other")},
		},
		Labels: []smarthome.RegistryLabel{},
	}
	changed := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	states := []smarthome.EntityState{
		{EntityID: "group.family_home", State: "on", LastChanged: changed, LastUpdated: changed},
		{EntityID: "switch.channel_1", State: "on", Attributes: map[string]interface{}{"fixture_secret": fixtureHAToken}, LastChanged: changed, LastUpdated: changed},
		{EntityID: "switch.channel_2", State: "off", LastChanged: changed, LastUpdated: changed},
		{EntityID: "switch.covered", State: "unavailable", LastChanged: changed, LastUpdated: changed},
		{EntityID: "sensor.temperature", State: "23.5", Attributes: map[string]interface{}{"unit_of_measurement": "°C"}, LastChanged: changed, LastUpdated: changed},
	}
	extraControls := len(extended) > 0 && extended[0]
	if extraControls {
		for _, target := range []struct{ id, name, state string }{{"binary_sensor.audit_door", "审计门磁", "on"}, {"switch.delayed_success", "延迟成功开关", "off"}, {"switch.delayed_unknown", "延迟待核开关", "off"}} {
			registry.Entities = append(registry.Entities, smarthome.RegistryEntity{EntityID: target.id, Name: fixtureString(target.name), AreaID: fixtureString("other")})
			attributes := map[string]interface{}{}
			if target.id == "binary_sensor.audit_door" {
				attributes["device_class"] = "door"
				attributes["fixture_secret"] = fixtureHAToken
			}
			states = append(states, smarthome.EntityState{EntityID: target.id, State: target.state, Attributes: attributes, LastChanged: changed, LastUpdated: changed})
		}
	}
	upgrader := websocket.Upgrader{}
	deps := &fixtureDependencies{}
	storedAutomations := map[string]json.RawMessage{}
	loadedAutomations := map[string]json.RawMessage{}
	automationEntity := func(key string) string { return fmt.Sprintf("automation.fixture_%x", sha256.Sum256([]byte(key))) }
	deps.lifecycle = newFixtureLifecycle(deps, storedAutomations, loadedAutomations, automationEntity)
	deps.ha = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deps.writeAttempts.recordHTTP(r)
		if deps.lifecycle.handleHTTP(w, r) {
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Authorization") == "Bearer "+fixtureHAToken {
			if r.URL.Path == "/api/services/switch/turn_on" || r.URL.Path == "/api/services/switch/turn_off" {
				var payload struct {
					EntityID string `json:"entity_id"`
				}
				decoder := json.NewDecoder(r.Body)
				decoder.DisallowUnknownFields()
				if decoder.Decode(&payload) != nil || (payload.EntityID != "switch.channel_1" && (!extraControls || (payload.EntityID != "switch.delayed_success" && payload.EntityID != "switch.delayed_unknown"))) {
					http.Error(w, "unsupported fixture target", 422)
					return
				}
				if strings.HasPrefix(payload.EntityID, "switch.delayed_") {
					deps.deviceWrites.Add(1)
					timer := time.NewTimer(1200 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-r.Context().Done():
						return
					}
					if payload.EntityID == "switch.delayed_unknown" {
						http.Error(w, "ambiguous fixture response", 503)
						return
					}
				}
				deps.stateMu.Lock()
				for i := range states {
					if states[i].EntityID == payload.EntityID {
						if strings.HasSuffix(r.URL.Path, "turn_on") {
							states[i].State = "on"
						} else {
							states[i].State = "off"
						}
						states[i].LastChanged = time.Now().UTC()
						states[i].LastUpdated = states[i].LastChanged
					}
				}
				if !strings.HasPrefix(payload.EntityID, "switch.delayed_") {
					deps.deviceWrites.Add(1)
				}
				deps.stateMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[]`))
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/config/automation/config/") {
				_, lifecycleEnabled := deps.lifecycle.snapshot()
				var config smarthome.AutomationConfig
				var raw json.RawMessage
				decodeErr := json.NewDecoder(r.Body).Decode(&raw)
				if decodeErr == nil {
					if lifecycleEnabled {
						// HA stores full configuration, including fields outside our
						// application builder's supported subset. Keep the raw bytes.
						var identity struct {
							ID string `json:"id"`
						}
						decodeErr = json.Unmarshal(raw, &identity)
						config.ID = identity.ID
					} else {
						decodeErr = json.Unmarshal(raw, &config)
					}
				}
				if decodeErr != nil || config.ID != strings.TrimPrefix(r.URL.Path, "/api/config/automation/config/") {
					http.Error(w, "invalid fixture config", 422)
					return
				}
				deps.stateMu.Lock()
				storedAutomations[config.ID] = append([]byte(nil), raw...)
				if _, loaded := loadedAutomations[config.ID]; lifecycleEnabled && loaded {
					// Model HA's targeted update load only for an existing runtime.
					// New installs still require the explicit reload fixture path.
					loadedAutomations[config.ID] = append([]byte(nil), raw...)
				}
				deps.stateMu.Unlock()
				deps.automationWrites.Add(1)
				if deps.lifecycle.consumeFault("lose_config") {
					loseFixtureResponse(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{}`))
				return
			}
			if r.URL.Path == "/api/services/automation/reload" {
				deps.automationReloads.Add(1)
				deps.stateMu.Lock()
				for key, raw := range storedAutomations {
					loadedAutomations[key] = append([]byte(nil), raw...)
				}
				deps.stateMu.Unlock()
				if deps.lifecycle.consumeFault("lose_reload") {
					loseFixtureResponse(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[]`))
				return
			}
		}
		if r.Method != http.MethodGet {
			http.Error(w, "fixture is read-only", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/api/websocket" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			if conn.WriteJSON(map[string]any{"type": "auth_required", "ha_version": "fixture"}) != nil {
				return
			}
			var auth struct {
				Type  string `json:"type"`
				Token string `json:"access_token"`
			}
			if conn.ReadJSON(&auth) != nil {
				return
			}
			if auth.Type != "auth" || auth.Token != fixtureHAToken {
				_ = conn.WriteJSON(map[string]any{"type": "auth_invalid"})
				return
			}
			if conn.WriteJSON(map[string]any{"type": "auth_ok", "ha_version": "fixture"}) != nil {
				return
			}
			lastID := 0
			for {
				var request fixtureWSRequest
				if conn.ReadJSON(&request) != nil {
					return
				}
				if !fixtureWSRead(request.Type) {
					deps.writeAttempts.record("other")
				}
				if request.ID <= lastID {
					return
				}
				lastID = request.ID
				if result, code, handled := deps.lifecycle.handleWS(request, registry); handled {
					response := map[string]any{"id": request.ID, "type": "result", "success": code == ""}
					if code == "" {
						response["result"] = result
					} else {
						response["error"] = map[string]string{"code": code}
					}
					if conn.WriteJSON(response) != nil {
						return
					}
					continue
				}
				var result any
				switch request.Type {
				case "config/area_registry/list":
					result = registry.Areas
				case "config/device_registry/list":
					result = registry.Devices
				case "config/entity_registry/list":
					entities := append([]smarthome.RegistryEntity(nil), registry.Entities...)
					deps.stateMu.Lock()
					for key := range loadedAutomations {
						entities = append(entities, smarthome.RegistryEntity{EntityID: automationEntity(key), Platform: fixtureString("automation"), UniqueID: fixtureString(key)})
					}
					deps.stateMu.Unlock()
					result = entities
				case "automation/config":
					deps.stateMu.Lock()
					var raw json.RawMessage
					for key, config := range loadedAutomations {
						if request.EntityID == automationEntity(key) {
							raw = append([]byte(nil), config...)
							break
						}
					}
					deps.stateMu.Unlock()
					if raw == nil {
						conn.WriteJSON(map[string]any{"id": request.ID, "type": "result", "success": false, "error": map[string]string{"code": "not_found"}})
						continue
					}
					result = map[string]any{"config": raw}
				case "config/label_registry/list":
					result = registry.Labels
				default:
					if conn.WriteJSON(map[string]any{"id": request.ID, "type": "result", "success": false, "error": map[string]string{"code": "fixture_read_only"}}) != nil {
						return
					}
					continue
				}
				if conn.WriteJSON(map[string]any{"id": request.ID, "type": "result", "success": true, "result": result}) != nil {
					return
				}
			}
		}
		if r.Header.Get("Authorization") != "Bearer "+fixtureHAToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/history/period/") {
			// Empty successful read is a request fact, not retained event coverage.
			_ = json.NewEncoder(w).Encode([][]smarthome.EntityState{})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/config/automation/config/") {
			key := strings.TrimPrefix(r.URL.Path, "/api/config/automation/config/")
			deps.stateMu.Lock()
			raw, exists := storedAutomations[key]
			deps.stateMu.Unlock()
			if !exists {
				http.NotFound(w, r)
				return
			}
			w.Write(raw)
			return
		}
		switch r.URL.Path {
		case "/api/states":
			deps.stateMu.Lock()
			_ = json.NewEncoder(w).Encode(deps.lifecycle.appendAutomationStates(states))
			deps.stateMu.Unlock()
		case "/api/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"time_zone": "Asia/Shanghai", "version": "2026.7.2", "components": []string{"automation", "config"}})
		default:
			if strings.HasPrefix(r.URL.Path, "/api/states/") {
				id := strings.TrimPrefix(r.URL.Path, "/api/states/")
				deps.stateMu.Lock()
				defer deps.stateMu.Unlock()
				for key := range loadedAutomations {
					if id == automationEntity(key) {
						json.NewEncoder(w).Encode(smarthome.EntityState{EntityID: id, State: deps.lifecycle.automationState(key), Attributes: map[string]interface{}{}})
						return
					}
				}
				for _, state := range states {
					if state.EntityID == id {
						json.NewEncoder(w).Encode(state)
						return
					}
				}
			}
			http.NotFound(w, r)
		}
	}))
	deps.tags = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/tags" {
			http.Error(w, "fixture supports tags only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "gemma4:12b"}}})
	}))
	return deps
}

func (d *fixtureDependencies) Close() {
	if d.lifecycleControl != nil {
		d.lifecycleControl.Close()
	}
	if d.lifecycle != nil {
		d.lifecycle.cancel()
	}
	d.ha.Close()
	d.tags.Close()
}
