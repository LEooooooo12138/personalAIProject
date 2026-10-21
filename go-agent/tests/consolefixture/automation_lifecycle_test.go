package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

func lifecycleRequest(t *testing.T, endpoint, capability, method, path, body string, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, endpoint+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if capability != "" {
		req.Header.Set("Authorization", "Bearer "+capability)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s err=%v", method, path, res.StatusCode, want, raw, err)
	}
	return raw
}

func lifecycleDependencies(t *testing.T) (*fixtureDependencies, *lifecycleControl) {
	t.Helper()
	d := newFixtureDependencies()
	t.Cleanup(d.Close)
	c, err := d.startLifecycleControl(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d, c
}

func TestLifecycleControlRequiresCapabilityAndBoundsModes(t *testing.T) {
	d, c := lifecycleDependencies(t)
	raw, err := os.ReadFile(c.descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor struct {
		URL, Capability string
		SchemaVersion   int `json:"schema_version"`
	}
	if json.Unmarshal(raw, &descriptor) != nil || descriptor.URL != c.url || descriptor.Capability != c.capability || len(descriptor.Capability) != 64 || descriptor.SchemaVersion != 1 {
		t.Fatalf("descriptor %s", raw)
	}
	u, _ := url.Parse(descriptor.URL)
	if net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "18081" {
		t.Fatalf("control listener %s", descriptor.URL)
	}
	for _, cap := range []string{"", "wrong"} {
		lifecycleRequest(t, c.url, cap, "PUT", "/mode", `{"history":"pattern"}`, 403)
	}
	for _, body := range []string{`{"status":"confirmed"}`, `{"history":"real"}`, `{"history_delay_ms":5001}`, `{"read_delay_ms":-1}`, `{"fault":"write_arbitrary"}`, `{"history":"pattern"} {}`, `null`, strings.Repeat("x", 4097)} {
		lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", body, 400)
	}
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "http://example.invalid") }, func(r *http.Request) { r.Host = "example.invalid" }} {
		req, _ := http.NewRequest("GET", c.url+"/state", nil)
		req.Header.Set("Authorization", "Bearer "+c.capability)
		mutate(req)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("cross-origin/host accepted %d", res.StatusCode)
		}
	}
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"history":"pattern"}`, 200)
	if d.writeAttempts.snapshot().Total != 0 {
		t.Fatal("scenario control wrote HA")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(c.descriptorPath), "install-state.json")); !os.IsNotExist(err) {
		t.Fatal("control injected Console state")
	}
}

func TestLifecycleControlClosesAndPreservesForeignDescriptor(t *testing.T) {
	d, c := lifecycleDependencies(t)
	address := strings.TrimPrefix(c.url, "http://")
	d.Close()
	if _, err := os.Stat(c.descriptorPath); !os.IsNotExist(err) {
		t.Fatalf("descriptor retained %v", err)
	}
	if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		conn.Close()
		t.Fatal("control listener survived Close")
	}
	d2 := newFixtureDependencies()
	defer d2.Close()
	root := t.TempDir()
	path := filepath.Join(root, "automation-lifecycle.json")
	if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d2.startLifecycleControl(root); err == nil {
		t.Fatal("overwrote existing descriptor")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "foreign" {
		t.Fatal("foreign descriptor changed")
	}
}

func TestLifecycleHistoryUsesRealClientAndDeclaredEntityWindow(t *testing.T) {
	d, c := lifecycleDependencies(t)
	client := smarthome.NewHomeAssistantClient(d.ha.URL, fixtureHAToken, 2*time.Second)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := end.AddDate(0, 0, -14)
	entries, err := client.GetHistoryForEntities(context.Background(), []string{"switch.channel_1"}, start, end)
	if err != nil || len(entries) != 0 {
		t.Fatalf("default history %+v %v", entries, err)
	}
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"history":"pattern"}`, 200)
	entries, err = client.GetHistoryForEntities(context.Background(), []string{"switch.channel_1"}, start, end)
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	for _, entry := range entries {
		if entry.EntityID != "switch.channel_1" || entry.Timestamp.Before(start) || !entry.Timestamp.Before(end) {
			t.Fatalf("escaped declared query %+v", entry)
		}
		if entry.State == "on" {
			hits++
			if entry.Timestamp.In(loc).Hour() != 18 || entry.Timestamp.In(loc).Minute() != 5 {
				t.Fatalf("wrong household time %+v", entry)
			}
		}
	}
	if hits != 11 {
		t.Fatalf("hits=%d, require unchanged 11/14 threshold", hits)
	}
	entries, err = client.GetHistoryForEntities(context.Background(), []string{"sensor.temperature"}, start, end)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unapproved synthetic history %+v %v", entries, err)
	}
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"history":"forbidden"}`, 200)
	if _, err = client.GetHistoryForEntities(context.Background(), []string{"switch.channel_1"}, start, end); smarthome.HAErrorCode(err) != "ha_forbidden" {
		t.Fatalf("forbidden history %v", err)
	}
}

func TestLifecycleCountsRejectedAndUnknownWriteAttempts(t *testing.T) {
	d, c := lifecycleDependencies(t)
	lifecycleRequest(t, d.ha.URL, "wrong", "POST", "/api/config/automation/config/key", `{}`, 405)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/config/automation/config/key", `{}`, 422)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "DELETE", "/api/config/automation/config/key", ``, 404)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/turn_on", `{"entity_id":"automation.absent"}`, 404)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/turn_off", `{"entity_id":"automation.absent","stop_actions":true}`, 404)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/light/turn_on", `{}`, 405)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "PATCH", "/unsupported", `{}`, 405)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/reload", `{}`, 200)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(d.ha.URL, "http")+"/api/websocket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	var response map[string]any
	ws.ReadJSON(&response)
	ws.WriteJSON(map[string]any{"type": "auth", "access_token": fixtureHAToken})
	ws.ReadJSON(&response)
	ws.WriteJSON(map[string]any{"id": 1, "type": "call_service", "domain": "automation", "service": "trigger"})
	ws.ReadJSON(&response)
	got := d.writeAttempts.snapshot()
	if got.Total != 9 || got.Config != 2 || got.Reload != 1 || got.Enable != 1 || got.Disable != 1 || got.Delete != 1 || got.Device != 1 || got.Other != 2 {
		t.Fatalf("missing write attempt categories %+v", got)
	}
	raw := lifecycleRequest(t, c.url, c.capability, "GET", "/state", ``, 200)
	if bytes.Contains(raw, []byte(fixtureHAToken)) || bytes.Contains(raw, []byte(c.capability)) {
		t.Fatal("metrics leaked credentials")
	}
}

func TestLifecycleCountsInvalidIDWriteAttempts(t *testing.T) {
	for _, lifecycle := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			duplicate bool
			write     bool
		}{
			{"zero_write", false, true},
			{"duplicate_write", true, true},
			{"zero_read", false, false},
			{"duplicate_read", true, false},
		} {
			mode := "default/"
			if lifecycle {
				mode = "lifecycle/"
			}
			t.Run(mode+tc.name, func(t *testing.T) {
				d := newFixtureDependencies()
				defer d.Close()
				if lifecycle {
					if _, err := d.startLifecycleControl(t.TempDir()); err != nil {
						t.Fatal(err)
					}
				}
				ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(d.ha.URL, "http")+"/api/websocket", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer ws.Close()
				if err = ws.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Fatal(err)
				}
				var response map[string]any
				if err = ws.ReadJSON(&response); err != nil {
					t.Fatal(err)
				}
				if err = ws.WriteJSON(map[string]any{"type": "auth", "access_token": fixtureHAToken}); err != nil {
					t.Fatal(err)
				}
				if err = ws.ReadJSON(&response); err != nil || response["type"] != "auth_ok" {
					t.Fatalf("authentication response=%v err=%v", response, err)
				}
				id := 0
				if tc.duplicate {
					if err = ws.WriteJSON(map[string]any{"id": 1, "type": "config/area_registry/list"}); err != nil {
						t.Fatal(err)
					}
					if err = ws.ReadJSON(&response); err != nil || response["success"] != true {
						t.Fatalf("initial read response=%v err=%v", response, err)
					}
					if got := d.writeAttempts.snapshot(); got.Total != 0 {
						t.Fatalf("valid read counted %+v", got)
					}
					id = 1
				}
				kind := "config/area_registry/list"
				if tc.write {
					kind = "call_service"
				}
				if err = ws.WriteJSON(map[string]any{"id": id, "type": kind, "domain": "automation", "service": "trigger"}); err != nil {
					t.Fatal(err)
				}
				if err = ws.ReadJSON(&response); err == nil {
					t.Fatalf("invalid protocol ID accepted %v", response)
				}
				want := fixtureWriteSnapshot{}
				if tc.write {
					want.Total = 1
					want.Other = 1
				}
				if got := d.writeAttempts.snapshot(); got != want {
					t.Fatalf("authenticated invalid-ID attempt counts got=%+v want=%+v", got, want)
				}
			})
		}
	}
}

func TestLifecycleLostReloadConvergesThroughRealManagerWithoutReplay(t *testing.T) {
	t.Setenv("CONSOLE_FIXTURE_TEMP_ROOT", t.TempDir())
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081", false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := f.dependencies.lifecycleControl
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"fault":"lose_reload"}`, 200)
	m := f.app.SmartHome
	s, err := m.BindSuggestion(context.Background(), "fixture_time", smarthome.SuggestionBindings{Presence: &smarthome.PresenceBinding{EntityID: "group.family_home", State: "on"}})
	if err != nil {
		t.Fatal(err)
	}
	s, err = m.ConfirmSuggestion(context.Background(), s.ID)
	if err != nil || s.Status != "unknown" {
		t.Fatalf("lost reload hidden %+v %v", s, err)
	}
	op, err := m.GetInstallOperationForSuggestion(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := f.dependencies.writeAttempts.snapshot()
	if before.Total != 2 || before.Config != 1 || before.Reload != 1 {
		t.Fatalf("writes %+v", before)
	}
	m.ConfirmSuggestion(context.Background(), s.ID)
	op, err = m.ReconcileInstallation(context.Background(), op.ID)
	if err != nil || op.Status != "confirmed" || op.Observation == nil || !op.Observation.RuntimeLoadProof.Verified {
		t.Fatalf("reconcile %+v %v", op, err)
	}
	if after := f.dependencies.writeAttempts.snapshot(); after != before {
		t.Fatalf("replayed unknown/reconcile before=%+v after=%+v", before, after)
	}
	window := smarthome.TimeWindow{Start: time.Now().UTC().Add(-time.Hour), End: time.Now().UTC()}
	for _, mode := range []struct{ name, want string }{{"finished", "available"}, {"forbidden", "forbidden"}, {"unsupported", "unsupported"}, {"retention_gap", "retention_gap"}} {
		lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"trace":"`+mode.name+`"}`, 200)
		evidence, _ := m.ReadAutomationEvidence(context.Background(), smarthome.AutomationRef{Kind: "managed", ID: op.ManagedAutomationID}, window)
		if string(evidence.Trace.SourceStatus) != mode.want {
			t.Fatalf("trace mode=%s got=%+v", mode.name, evidence.Trace)
		}
		if mode.name == "finished" && (len(evidence.Trace.Runs) != 1 || evidence.Trace.Runs[0].Outcome != "completed") {
			t.Fatalf("trace failed real parser %+v", evidence.Trace)
		}
	}
	if after := f.dependencies.writeAttempts.snapshot(); after != before {
		t.Fatal("diagnostics wrote HA")
	}
	for _, mode := range []struct {
		body  string
		check func(smarthome.AutomationObservation) bool
	}{
		{`{"profile":"unknown"}`, func(o smarthome.AutomationObservation) bool {
			return o.Config.ErrorCode == "ha_version_unknown" && !o.RuntimeLoadProof.Verified
		}},
		{`{"config":"forbidden"}`, func(o smarthome.AutomationObservation) bool {
			return o.Config.SourceStatus == smarthome.SourceForbidden
		}},
		{`{"registry":"forbidden"}`, func(o smarthome.AutomationObservation) bool { return o.MappingStatus == smarthome.SourceForbidden }},
		{`{"registry":"missing"}`, func(o smarthome.AutomationObservation) bool { return o.MappingErrorCode == "ha_mapping_unknown" }},
		{`{"runtime":"missing"}`, func(o smarthome.AutomationObservation) bool {
			return !o.RuntimeLoadProof.Verified && o.RuntimeLoadProof.ErrorCode == "ha_not_found"
		}},
		{`{"runtime":"drift"}`, func(o smarthome.AutomationObservation) bool {
			return !o.RuntimeLoadProof.Verified && o.RuntimeLoadProof.ObservedDigest != op.ExpectedDigest
		}},
		{`{"state":"unavailable"}`, func(o smarthome.AutomationObservation) bool {
			return o.State == "unavailable" && !o.RuntimeLoadProof.Verified
		}},
	} {
		lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", mode.body, 200)
		observation, _ := m.ObserveAutomation(context.Background(), smarthome.AutomationRef{Kind: "managed", ID: op.ManagedAutomationID})
		if !mode.check(observation) {
			t.Fatalf("mode %s observation %+v", mode.body, observation)
		}
	}
	if after := f.dependencies.writeAttempts.snapshot(); after != before {
		t.Fatal("observation mode wrote HA")
	}
}

func TestLifecycleHistoryDelayCancelsAndControllerCloseReleases(t *testing.T) {
	d, c := lifecycleDependencies(t)
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"history":"pattern","history_delay_ms":5000}`, 200)
	ctx, cancel := context.WithCancel(context.Background())
	client := smarthome.NewHomeAssistantClient(d.ha.URL, fixtureHAToken, 6*time.Second)
	done := make(chan error, 1)
	go func() {
		end := time.Now().UTC().Add(-time.Hour)
		_, err := client.GetHistoryForEntities(ctx, []string{"switch.channel_1"}, end.Add(-time.Hour), end)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for d.lifecycle.historyReads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if d.lifecycle.historyReads.Load() == 0 {
		t.Fatal("history request did not reach fixture")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel ignored")
	}
	start := time.Now()
	c.Close()
	if time.Since(start) > time.Second {
		t.Fatal("control cleanup blocked on response delay")
	}
}

func TestLifecycleManagementChangesOnlyStoredSyntheticTarget(t *testing.T) {
	d, c := lifecycleDependencies(t)
	const key = "lifecycle_rule"
	const config = `{"id":"lifecycle_rule","alias":"模拟规则","trigger":[{"platform":"time","at":"18:05"}],"action":[{"service":"switch.turn_on","target":{"entity_id":"switch.channel_1"}}],"fixture_unknown":{"preserve":true}}`
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/config/automation/config/"+key, config, 200)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/reload", `{}`, 200)
	client := smarthome.NewHomeAssistantClient(d.ha.URL, fixtureHAToken, time.Second)
	registry, err := client.GetRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entity := ""
	for _, item := range registry.Entities {
		if item.UniqueID != nil && *item.UniqueID == key {
			entity = item.EntityID
		}
	}
	if entity == "" || entity == key {
		t.Fatalf("missing independent entity mapping %+v", registry.Entities)
	}
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/turn_off", `{"entity_id":"`+entity+`"}`, 422)
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/turn_off", `{"entity_id":"`+entity+`","stop_actions":false}`, 200)
	state, err := client.GetState(context.Background(), entity)
	if err != nil || state.State != "off" {
		t.Fatalf("disable state %+v %v", state, err)
	}
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/turn_on", `{"entity_id":"`+entity+`"}`, 200)
	state, err = client.GetState(context.Background(), entity)
	if err != nil || state.State != "on" {
		t.Fatalf("enable state %+v %v", state, err)
	}
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{"config":"drift","runtime":"drift"}`, 200)
	observation, err := client.ReadAutomationConfig(context.Background(), key)
	if err != nil || !bytes.Contains(observation.RawConfig, []byte(`"fixture_unknown":{"preserve":true}`)) || !bytes.Contains(observation.RawConfig, []byte("模拟外部修改")) {
		t.Fatalf("drift replaced unrelated raw config %+v %v", observation, err)
	}
	lifecycleRequest(t, c.url, c.capability, "PUT", "/mode", `{}`, 200)
	before := d.writeAttempts.snapshot()
	lifecycleRequest(t, d.ha.URL, fixtureHAToken, "DELETE", "/api/config/automation/config/"+key, ``, 200)
	if _, err = client.GetState(context.Background(), entity); err == nil {
		t.Fatal("deleted automation state retained")
	}
	observation, err = client.ReadAutomationConfig(context.Background(), key)
	if err != nil || observation.Exists == nil || *observation.Exists {
		t.Fatalf("deleted config retained %+v %v", observation, err)
	}
	if after := d.writeAttempts.snapshot(); after.Total != before.Total+1 || after.Delete != before.Delete+1 {
		t.Fatalf("delete counts before=%+v after=%+v", before, after)
	}
	device, err := client.GetState(context.Background(), "switch.channel_1")
	if err != nil || device.State != "on" {
		t.Fatalf("management touched physical target %+v %v", device, err)
	}
}
