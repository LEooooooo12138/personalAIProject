package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

type suggestionConfirmer interface {
	ConfirmSuggestion(context.Context, string) (*RuleSuggestion, error)
	IgnoreSuggestion(string) (*RuleSuggestion, error)
}

func confirmationManager(t *testing.T, handler http.HandlerFunc) (*Manager, suggestionConfirmer, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: dir}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	api, ok := interface{}(m).(suggestionConfirmer)
	if !ok {
		t.Fatal("manager has no confirmation state transition API")
	}
	if err := m.store.SaveSuggestions([]RuleSuggestion{decodeSuggestion(t, executableSuggestion)}); err != nil {
		t.Fatal(err)
	}
	return m, api, dir
}

// Concurrent confirmation must create one HA rule and preserve a durable confirmed state.
func TestConfirmSuggestionIsConcurrentAndDurableIdempotent(t *testing.T) {
	var requests atomic.Int32
	m, api, dir := confirmationManager(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`{}`)) })
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := api.ConfirmSuggestion(context.Background(), "rule-kitchen")
			if err != nil {
				t.Error(err)
				return
			}
			if s.Status != "confirmed" || s.HAAutomationID != "rule-kitchen" {
				t.Errorf("confirmation=%#v", s)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 2 {
		t.Errorf("HA calls=%d want one create and reload", requests.Load())
	}
	stored, err := m.store.GetSuggestions()
	if err != nil || len(stored) != 1 || stored[0].Status != "confirmed" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "rules", "rule-kitchen.md")); err != nil {
		t.Fatalf("rule doc missing: %v", err)
	}
	// Restart/reopen uses persisted state and must not make another HA request.
	reopened, err := NewManager(HAConfig{BaseURL: m.cfg.BaseURL, AgentVaultPath: dir}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	reopenedAPI := interface{}(reopened).(suggestionConfirmer)
	if _, err := reopenedAPI.ConfirmSuggestion(context.Background(), "rule-kitchen"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Errorf("restart repeated HA calls=%d", requests.Load())
	}
	if _, err := api.IgnoreSuggestion("rule-kitchen"); err == nil {
		t.Error("confirmed rule can be ignored without deleting HA rule")
	} else if !errors.Is(err, ErrSuggestionConflict) {
		t.Errorf("wrong conflict sentinel: %v", err)
	}
}

func TestIgnoredSuggestionCannotBeConfirmed(t *testing.T) {
	var calls atomic.Int32
	_, api, _ := confirmationManager(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for i := 0; i < 2; i++ {
		s, err := api.IgnoreSuggestion("rule-kitchen")
		if err != nil || s.Status != "ignored" {
			t.Fatalf("ignore=%#v %v", s, err)
		}
	}
	if _, err := api.ConfirmSuggestion(context.Background(), "rule-kitchen"); err == nil {
		t.Error("ignored rule confirmed")
	}
	if _, err := api.IgnoreSuggestion("missing"); err == nil {
		t.Error("unknown suggestion silently ignored")
	} else if !errors.Is(err, ErrSuggestionNotFound) {
		t.Errorf("wrong not-found sentinel: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("unexpected device requests=%d", calls.Load())
	}
}

func TestConfirmationFailureIsPersistedAndCanBeRetried(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var calls atomic.Int32
	m, api, _ := confirmationManager(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := api.ConfirmSuggestion(context.Background(), "rule-kitchen"); err == nil {
		t.Error("HA failure was hidden")
	} else if !errors.Is(err, ErrHARequest) {
		t.Errorf("wrong upstream sentinel: %v", err)
	}
	stored, err := m.store.GetSuggestions()
	if err != nil || len(stored) != 1 || stored[0].Status != "failed" || stored[0].LastError == "" {
		t.Fatalf("failed state=%#v %v", stored, err)
	}
	if _, err := api.IgnoreSuggestion("rule-kitchen"); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("uncertain HA outcome can be ignored: %v", err)
	}
	fail.Store(false)
	s, err := api.ConfirmSuggestion(context.Background(), "rule-kitchen")
	if err != nil || s.Status != "confirmed" || s.LastError != "" {
		t.Fatalf("retry=%#v %v", s, err)
	}
	if calls.Load() != 3 {
		t.Errorf("requests=%d", calls.Load())
	}
}

func TestAutomationRejectsUnknownFieldsInsteadOfDroppingConstraints(t *testing.T) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(executableSuggestion), &data); err != nil {
		t.Fatal(err)
	}
	a := data["automation"].(map[string]interface{})
	conditions := a["condition"].([]interface{})
	conditions[0].(map[string]interface{})["for"] = "00:05:00"
	raw, _ := json.Marshal(data)
	var suggestion RuleSuggestion
	if err := json.Unmarshal(raw, &suggestion); err == nil {
		t.Fatal("unsupported condition duration silently discarded")
	}
}

func TestExecuteRulePreservesStateTrigger(t *testing.T) {
	s := decodeSuggestion(t, executableSuggestion)
	s.Automation.Trigger = []AutomationTrigger{{Platform: "state", EntityID: "binary_sensor.door", From: "off", To: "on"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config/automation/config/rule-kitchen" {
			var a AutomationConfig
			if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
				t.Error(err)
				return
			}
			if len(a.Trigger) != 1 || a.Trigger[0].Platform != "state" || a.Trigger[0].EntityID != "binary_sensor.door" || a.Trigger[0].From != "off" || a.Trigger[0].To != "on" {
				t.Errorf("changed trigger: %#v", a.Trigger)
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	if err := ExecuteRule(context.Background(), NewHomeAssistantClient(server.URL, "", time.Second), s); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedSuggestionCannotBeReplacedUnderSameID(t *testing.T) {
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSuggestion(t, executableSuggestion)
	if err := store.SaveSuggestions([]RuleSuggestion{s}); err != nil {
		t.Fatal(err)
	}
	s.Automation.Action[0].Service = "switch.turn_off"
	if err := store.SaveSuggestions([]RuleSuggestion{s}); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("published action replaced: %v", err)
	}
	stored, err := store.GetSuggestions()
	if err != nil || stored[0].Automation.Action[0].Service != "switch.turn_on" {
		t.Fatalf("stored action changed: %#v %v", stored, err)
	}
}

func TestConfirmationReturnsRuleArchiveFailure(t *testing.T) {
	m, api, dir := confirmationManager(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	if err := os.Mkdir(filepath.Join(dir, "rules", "rule-kitchen.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ConfirmSuggestion(context.Background(), "rule-kitchen"); err == nil {
		t.Fatal("rule document failure reported as success")
	}
	stored, err := m.store.GetSuggestions()
	if err != nil || stored[0].Status == "confirmed" {
		t.Fatalf("archive failure marked confirmed: %#v %v", stored, err)
	}
}

func TestUnsupportedAutomationNeverReachesHA(t *testing.T) {
	cases := []struct {
		name  string
		alter func(map[string]interface{})
	}{
		{"unmapped condition", func(a map[string]interface{}) { delete(a, "condition") }},
		{"wrong service domain", func(a map[string]interface{}) {
			a["action"] = []interface{}{map[string]interface{}{"service": "light.turn_on", "target": map[string]interface{}{"entity_id": "switch.kitchen"}}}
		}},
		{"unsupported trigger", func(a map[string]interface{}) {
			a["trigger"] = []interface{}{map[string]interface{}{"platform": "template"}}
		}},
		{"unsupported condition", func(a map[string]interface{}) {
			a["condition"] = []interface{}{map[string]interface{}{"condition": "sun", "entity_id": "sun.sun", "state": "above_horizon"}}
		}},
		{"invalid time", func(a map[string]interface{}) {
			a["trigger"] = []interface{}{map[string]interface{}{"platform": "time", "at": "25:80"}}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(executableSuggestion), &data); err != nil {
				t.Fatal(err)
			}
			tt.alter(data["automation"].(map[string]interface{}))
			raw, _ := json.Marshal(data)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer server.Close()
			if err := ExecuteRule(context.Background(), NewHomeAssistantClient(server.URL, "", time.Second), decodeSuggestion(t, string(raw))); err == nil {
				t.Error("unsupported rule accepted")
			}
			if calls.Load() != 0 {
				t.Errorf("unsupported rule made %d HA calls", calls.Load())
			}
		})
	}
}

func TestAnalyzeIncludesKnownStateAtWindowBoundaries(t *testing.T) {
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	if err := store.SaveHistory([]HistoryEntry{
		{EntityID: "light.before", State: "on", Timestamp: start.Add(-time.Hour)},
		{EntityID: "light.before", State: "off", Timestamp: start.Add(time.Hour)},
		{EntityID: "light.ongoing", State: "on", Timestamp: end.Add(-2 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	report, err := NewAnalyzer(store, zap.NewNop()).Analyze(end, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Devices) != 2 || report.Devices[0].TotalOnTime != 1 || report.Devices[1].TotalOnTime != 2 {
		t.Fatalf("window summaries=%#v", report.Devices)
	}
}

func TestManagerDefaultPollingCanStartAndStop(t *testing.T) {
	states := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			states <- struct{}{}
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if m.collector.interval <= 0 {
		t.Fatal("omitted polling interval creates invalid ticker")
	}
	m.Start(context.Background())
	defer m.Stop()
	select {
	case <-states:
	case <-time.After(time.Second):
		t.Fatal("initial collection did not start")
	}
	m.Stop()
	m.Stop()
}

func TestAnalysisReturnsPersistedDecision(t *testing.T) {
	m, err := NewManager(HAConfig{AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now()
	var history []HistoryEntry
	for day := 1; day <= 7; day++ {
		at := end.AddDate(0, 0, -day)
		at = time.Date(at.Year(), at.Month(), at.Day(), 18, 0, 0, 0, at.Location())
		history = append(history, HistoryEntry{EntityID: "light.kitchen", State: "on", Timestamp: at})
	}
	if err := m.store.SaveHistory(history); err != nil {
		t.Fatal(err)
	}
	report, err := m.TriggerAnalysis(context.Background(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Suggestions) != 1 {
		t.Fatalf("suggestions=%#v", report.Suggestions)
	}
	api := interface{}(m).(suggestionConfirmer)
	if _, err := api.IgnoreSuggestion(report.Suggestions[0].ID); err != nil {
		t.Fatal(err)
	}
	report, err = m.TriggerAnalysis(context.Background(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if report.Suggestions[0].Status != "ignored" {
		t.Fatalf("analysis reset visible decision: %#v", report.Suggestions[0])
	}
}
