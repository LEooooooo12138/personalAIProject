package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type fixtureWSRequest struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	EntityID string `json:"entity_id"`
	Domain   string `json:"domain"`
	ItemID   string `json:"item_id"`
	RunID    string `json:"run_id"`
}

func fixtureWSRead(kind string) bool {
	switch kind {
	case "config/area_registry/list", "config/device_registry/list", "config/entity_registry/list", "config/label_registry/list", "automation/config", "trace/list", "trace/get":
		return true
	}
	return false
}
func loseFixtureResponse(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
			return
		}
	}
	http.Error(w, "ambiguous synthetic response", 503)
}
func driftFixtureConfig(raw json.RawMessage) json.RawMessage {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	value["alias"] = json.RawMessage(`"模拟外部修改"`)
	result, _ := json.Marshal(value)
	return result
}

func (l *fixtureLifecycle) handleHTTP(w http.ResponseWriter, r *http.Request) bool {
	modes, enabled := l.snapshot()
	if !enabled || r.Header.Get("Authorization") != "Bearer "+fixtureHAToken {
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path != "/api/websocket" && !strings.HasPrefix(r.URL.Path, "/api/history/period/") && !l.delay(r.Context(), modes.ReadDelayMS) {
		return true
	}
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/history/period/"):
		l.historyReads.Add(1)
		if !l.delay(r.Context(), modes.HistoryDelayMS) {
			return true
		}
		if modes.History == "forbidden" {
			http.Error(w, "synthetic recorder forbidden", 403)
			return true
		}
		if modes.History != "pattern" {
			return false
		}
		l.writeHistory(w, r)
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/api/config" && modes.Profile == "unknown":
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "fixture-unreviewed", "time_zone": "Asia/Shanghai", "components": []string{"config", "automation"}})
		return true
	case strings.HasPrefix(r.URL.Path, "/api/config/automation/config/"):
		key := strings.TrimPrefix(r.URL.Path, "/api/config/automation/config/")
		if r.Method == http.MethodPost && l.consumeFault("reject_config") {
			http.Error(w, "synthetic rejected config", 400)
			return true
		}
		if r.Method == http.MethodGet && modes.Config == "forbidden" {
			http.Error(w, "synthetic config forbidden", 403)
			return true
		}
		if r.Method == http.MethodGet && modes.Config == "drift" {
			l.deps.stateMu.Lock()
			raw, exists := l.stored[key]
			raw = append([]byte(nil), raw...)
			l.deps.stateMu.Unlock()
			if !exists {
				http.NotFound(w, r)
			} else {
				w.Write(driftFixtureConfig(raw))
			}
			return true
		}
		if r.Method == http.MethodDelete {
			l.deps.stateMu.Lock()
			_, exists := l.stored[key]
			if exists {
				delete(l.stored, key)
				delete(l.loaded, key)
				delete(l.states, key)
			}
			l.deps.stateMu.Unlock()
			if !exists {
				http.NotFound(w, r)
			} else if l.consumeFault("lose_management") {
				loseFixtureResponse(w)
			} else {
				w.Write([]byte(`{}`))
			}
			return true
		}
	case r.Method == http.MethodPost && (r.URL.Path == "/api/services/automation/turn_on" || r.URL.Path == "/api/services/automation/turn_off"):
		var payload struct {
			EntityID    string `json:"entity_id"`
			StopActions *bool  `json:"stop_actions"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&payload) != nil || (strings.HasSuffix(r.URL.Path, "turn_off") && payload.StopActions == nil) || (strings.HasSuffix(r.URL.Path, "turn_on") && payload.StopActions != nil) {
			http.Error(w, "invalid synthetic management target", 422)
			return true
		}
		key := ""
		l.deps.stateMu.Lock()
		for candidate := range l.loaded {
			if l.entity(candidate) == payload.EntityID {
				key = candidate
				break
			}
		}
		if key != "" {
			state := "on"
			if strings.HasSuffix(r.URL.Path, "turn_off") {
				state = "off"
			}
			l.states[key] = state
		}
		l.deps.stateMu.Unlock()
		if key == "" {
			http.NotFound(w, r)
		} else if l.consumeFault("lose_management") {
			loseFixtureResponse(w)
		} else {
			w.Write([]byte(`[]`))
		}
		return true
	}
	return false
}

// The fixture generates only declared switch history within the requested
// half-open window. Eleven actual on transitions in fourteen household days
// exercise the unchanged analyzer threshold; no complete-recorder claim exists.
func (l *fixtureLifecycle) writeHistory(w http.ResponseWriter, r *http.Request) {
	start, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(r.URL.Path, "/api/history/period/"))
	end, endErr := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end_time"))
	ids := strings.Split(r.URL.Query().Get("filter_entity_id"), ",")
	if err != nil || endErr != nil || !start.Before(end) || end.Sub(start) > 14*24*time.Hour || len(ids) > 200 {
		http.Error(w, "invalid synthetic history query", 422)
		return
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	first := today.AddDate(0, 0, -14)
	result := [][]smarthome.EntityState{}
	for _, id := range ids {
		if id != "switch.channel_1" {
			continue
		}
		entries := []smarthome.EntityState{}
		for day := 0; day < 14; day++ {
			date := first.AddDate(0, 0, day)
			points := []struct {
				at    time.Time
				state string
			}{{date.Add(17*time.Hour + 59*time.Minute), "off"}}
			if day < 11 {
				points = append(points, struct {
					at    time.Time
					state string
				}{date.Add(18*time.Hour + 5*time.Minute), "on"}, struct {
					at    time.Time
					state string
				}{date.Add(18*time.Hour + 35*time.Minute), "off"})
			}
			for _, point := range points {
				if !point.at.Before(start) && point.at.Before(end) {
					entries = append(entries, smarthome.EntityState{EntityID: id, State: point.state, LastChanged: point.at.UTC(), LastUpdated: point.at.UTC()})
				}
			}
		}
		if len(entries) > 0 {
			result = append(result, entries)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (l *fixtureLifecycle) automationState(key string) string {
	modes, enabled := l.snapshot()
	if !enabled {
		return "on"
	}
	if modes.State != "observed" {
		return modes.State
	}
	if state := l.states[key]; state != "" {
		return state
	}
	return "on"
}
func (l *fixtureLifecycle) handleWS(request fixtureWSRequest, registry smarthome.RegistrySnapshot) (any, string, bool) {
	modes, enabled := l.snapshot()
	if !enabled {
		return nil, "", false
	}
	if !l.delay(l.ctx, modes.ReadDelayMS) {
		return nil, "unknown_error", true
	}
	switch request.Type {
	case "config/entity_registry/list":
		if modes.Registry == "forbidden" {
			return nil, "unauthorized", true
		}
		if modes.Registry == "missing" {
			return registry.Entities, "", true
		}
	case "automation/config":
		if modes.Runtime == "missing" {
			return nil, "not_found", true
		}
		if modes.Runtime == "drift" {
			l.deps.stateMu.Lock()
			defer l.deps.stateMu.Unlock()
			for key, raw := range l.loaded {
				if l.entity(key) == request.EntityID {
					return map[string]any{"config": driftFixtureConfig(raw)}, "", true
				}
			}
			return nil, "not_found", true
		}
	case "trace/list", "trace/get":
		if request.Domain != "automation" {
			return nil, "invalid_format", true
		}
		switch modes.Trace {
		case "forbidden":
			return nil, "unauthorized", true
		case "unsupported":
			return nil, "unknown_command", true
		case "retention_gap":
			return nil, "not_found", true
		}
		l.deps.stateMu.Lock()
		raw, exists := l.loaded[request.ItemID]
		raw = append([]byte(nil), raw...)
		l.deps.stateMu.Unlock()
		if !exists {
			return nil, "not_found", true
		}
		hash := sha256.Sum256([]byte("synthetic-trace:" + request.ItemID))
		run := hex.EncodeToString(hash[:16])
		start := l.traceAt
		finish := start.Add(time.Second)
		item := map[string]any{"run_id": run, "domain": "automation", "item_id": request.ItemID, "state": "stopped", "script_execution": "finished", "timestamp": map[string]any{"start": start, "finish": finish}, "config": raw, "trace": map[string]any{"action/0": []any{map[string]any{"path": "action/0", "timestamp": finish, "changed_variables": map[string]any{"fixture_secret": "LIFECYCLE_PRIVATE_TRACE"}}}}, "context": map[string]string{"user_id": "LIFECYCLE_PRIVATE_USER"}, "trigger": "LIFECYCLE_PRIVATE_TRIGGER"}
		if request.Type == "trace/list" {
			if modes.Trace == "empty" {
				return []any{}, "", true
			}
			return []any{item}, "", true
		}
		if request.RunID != run || modes.Trace == "empty" {
			return nil, "not_found", true
		}
		return item, "", true
	}
	return nil, "", false
}

func (l *fixtureLifecycle) appendAutomationStates(states []smarthome.EntityState) []smarthome.EntityState {
	_, enabled := l.snapshot()
	if !enabled {
		return states
	}
	result := append([]smarthome.EntityState(nil), states...)
	keys := []string{}
	for key := range l.loaded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, smarthome.EntityState{EntityID: l.entity(key), State: l.automationState(key), Attributes: map[string]any{}})
	}
	return result
}
