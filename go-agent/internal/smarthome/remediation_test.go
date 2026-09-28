package smarthome

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func decodeSuggestion(t *testing.T, raw string) RuleSuggestion {
	t.Helper()
	var s RuleSuggestion
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

const executableSuggestion = `{
	"id":"rule-kitchen", "title":"Kitchen switch", "status":"pending",
	"trigger":"18:00", "condition":"binary_sensor.occupied is on", "action":"switch.kitchen",
	"automation":{
		"trigger":[{"platform":"time","at":"18:00"}],
		"condition":[{"condition":"state","entity_id":"binary_sensor.occupied","state":"on"}],
		"action":[{"service":"switch.turn_on","target":{"entity_id":"switch.kitchen"}}], "mode":"single"
	}
}`

// A missing config_key or silently discarded condition must fail the HA contract.
func TestExecuteRulePreservesConfirmedPayload(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing HA bearer")
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/config/automation/config/rule-kitchen":
			var payload map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			if payload["id"] != "rule-kitchen" {
				t.Errorf("id = %#v", payload["id"])
			}
			conditions, ok := payload["condition"].([]interface{})
			if !ok || len(conditions) != 1 {
				t.Errorf("condition lost: %#v", payload)
				return
			}
			condition := conditions[0].(map[string]interface{})
			if condition["entity_id"] != "binary_sensor.occupied" || condition["state"] != "on" {
				t.Errorf("wrong condition: %#v", condition)
			}
			actions, ok := payload["action"].([]interface{})
			if !ok || len(actions) != 1 {
				t.Errorf("wrong actions: %#v", payload)
				return
			}
			if actions[0].(map[string]interface{})["service"] != "switch.turn_on" {
				t.Errorf("wrong action: %#v", actions)
			}
		case "/api/services/automation/reload":
		default:
			t.Errorf("unexpected HA path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	err := ExecuteRule(context.Background(), NewHomeAssistantClient(server.URL, "fixture-token", time.Second), decodeSuggestion(t, executableSuggestion))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if requests.Load() != 2 {
		t.Errorf("requests = %d, want create + reload", requests.Load())
	}
}

// Natural-language conditions have no verified entity mapping and must not control devices.
func TestExecuteRuleRejectsUnstructuredSuggestionBeforeHA(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	err := ExecuteRule(context.Background(), NewHomeAssistantClient(server.URL, "", time.Second), RuleSuggestion{ID: "rule-unmapped", Trigger: "18:00", Condition: "someone is home", Action: "light.kitchen"})
	if err == nil {
		t.Error("unmapped condition accepted")
	}
	if requests.Load() != 0 {
		t.Errorf("unsafe suggestion made %d HA calls", requests.Load())
	}
}

// Repeated samples while already on must not reset the beginning of the interval.
func TestBuildSummariesCountsStateTransitions(t *testing.T) {
	start := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	a := NewAnalyzer(nil, zap.NewNop())
	got := a.buildSummaries(map[string][]HistoryEntry{"switch.kitchen": {
		{State: "on", Timestamp: start}, {State: "on", Timestamp: start.Add(30 * time.Minute)},
		{State: "off", Timestamp: start.Add(time.Hour)}, {State: "off", Timestamp: start.Add(2 * time.Hour)},
	}}, 1, start, start.Add(24*time.Hour))
	if len(got) != 1 || got[0].TotalOnTime != 1 || got[0].OnCount != 1 || got[0].OffCount != 1 {
		t.Fatalf("summary = %#v; want one on/off transition and one hour", got)
	}
}

func TestSaveSuggestionsKeepsEarlierDecisions(t *testing.T) {
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := decodeSuggestion(t, executableSuggestion)
	if err := store.SaveSuggestions([]RuleSuggestion{first}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSuggestionStatus(first.ID, "ignored"); err != nil {
		t.Fatal(err)
	}
	second := RuleSuggestion{ID: "rule-other", Title: "Other", Status: "pending"}
	if err := store.SaveSuggestions([]RuleSuggestion{first, second}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSuggestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Status != "ignored" {
		t.Fatalf("previous decision lost: %#v", got)
	}
	if err := store.SaveSuggestions(nil); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSuggestions()
	if err != nil || len(got) != 2 {
		t.Fatalf("empty analysis cleared suggestions: %#v %v", got, err)
	}
}

func TestSuggestionIDsSurvivePatternOrderChanges(t *testing.T) {
	a := NewAnalyzer(nil, zap.NewNop())
	first := DetectedPattern{Type: "time", EntityID: "light.first", Description: "Device turned on around 18:00", Confidence: 0.9}
	second := DetectedPattern{Type: "time", EntityID: "switch.second", Description: "Device turned on around 19:00", Confidence: 0.9}
	forward := a.generateSuggestions([]DetectedPattern{first, second})
	reverse := a.generateSuggestions([]DetectedPattern{second, first})
	if len(forward) != 2 || len(reverse) != 2 {
		t.Fatal("missing suggestions")
	}
	if forward[0].ID != reverse[1].ID || forward[1].ID != reverse[0].ID {
		t.Fatalf("IDs depend on iteration order: %#v %#v", forward, reverse)
	}
}

func TestTriggerAnalysisPersistsReport(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(HAConfig{BaseURL: "http://unused.invalid", AgentVaultPath: dir}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "reports", report.PeriodEnd.Format("2006-01-02")+".json")
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("manual analysis not persisted: %v", err)
	}
}
