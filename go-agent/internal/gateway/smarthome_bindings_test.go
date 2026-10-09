package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVisitorCannotBindOrConfirm(t *testing.T) {
	_, server := auditServer(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error { return nil }))
	cookie := browserCookie(t, server)
	for _, action := range []string{"bindings", "confirm"} {
		req, _ := http.NewRequest("POST", server.URL+"/internal/smarthome/suggestions/x/"+action, strings.NewReader(`{}`))
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("visitor %s status=%d", action, resp.StatusCode)
		}
	}
}
func TestSmartHomeBindingsStrictHTTPContract(t *testing.T) {
	var writes atomic.Int32
	var unavailable atomic.Bool
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "offline", 503)
			return
		}
		switch r.URL.Path {
		case "/api/config":
			fmt.Fprint(w, `{"time_zone":"UTC"}`)
		case "/api/states":
			fmt.Fprint(w, `[{"entity_id":"light.a","state":"on"},{"entity_id":"binary_sensor.occupied","state":"on"}]`)
		default:
			writes.Add(1)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer ha.Close()
	s, server := auditServer(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error { return nil }))
	m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	s.smartHome = m
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var entries []smarthome.HistoryEntry
	for day := 1; day <= 14; day++ {
		at := midnight.AddDate(0, 0, -day).Add(18 * time.Hour)
		entries = append(entries, smarthome.HistoryEntry{EntityID: "light.a", State: "off", Timestamp: at.Add(-time.Minute), ObservationKind: "initial"}, smarthome.HistoryEntry{EntityID: "light.a", State: "on", Timestamp: at, ObservationKind: "change"})
	}
	if err = m.GetStore().SaveHistory(entries); err != nil {
		t.Fatal(err)
	}
	report, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil || len(report.Suggestions) != 1 {
		t.Fatalf("analysis=%#v %v", report, err)
	}
	id := report.Suggestions[0].ID
	post := func(id, body string, want int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest("POST", server.URL+"/internal/smarthome/suggestions/"+id+"/bindings", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got map[string]any
		json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != want {
			t.Fatalf("body=%s status=%d want=%d response=%#v", body, resp.StatusCode, want, got)
		}
		return got
	}
	for _, body := range []string{`{"automation":{}}`, `{"presence":{"entity_id":"binary_sensor.occupied","state":"on","extra":true}}`, `{"presence":4}`, `{} {}`, `null`} {
		post(id, body, 400)
	}
	post(id, `{}`, 422)
	post("missing", `{"presence":{"entity_id":"binary_sensor.occupied","state":"on"}}`, 404)
	unavailable.Store(true)
	post(id, `{"presence":{"entity_id":"binary_sensor.occupied","state":"on"}}`, 502)
	unavailable.Store(false)
	got := post(id, `{"presence":{"entity_id":"binary_sensor.occupied","state":"on"}}`, 200)
	if got["id"] == id {
		t.Fatal("binding changed source in place")
	}
	post(id, `{"presence":{"entity_id":"binary_sensor.other","state":"on"}}`, 409)
	if writes.Load() != 0 {
		t.Fatal("binding performed HA mutation")
	}
}
