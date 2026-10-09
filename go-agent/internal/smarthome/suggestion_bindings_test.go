package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeneratedSuggestionHasStructuredIntent(t *testing.T) {
	store, _ := NewDeviceStore(t.TempDir())
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	store.SaveHistory(patternHistory(start, 14))
	report, err := NewAnalyzer(store, zap.NewNop()).Analyze(start.AddDate(0, 0, 14), 14)
	if err != nil || len(report.Suggestions) != 1 {
		t.Fatalf("suggestions=%#v %v", report, err)
	}
	data, _ := json.Marshal(report.Suggestions[0])
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if raw["intent"] == nil || raw["missing_bindings"] == nil {
		t.Fatalf("generated suggestion has no structured binding intent: %s", data)
	}
}

type suggestionBinder interface {
	BindSuggestion(context.Context, string, SuggestionBindings) (*RuleSuggestion, error)
}

func generatedManager(t *testing.T) (*Manager, *atomic.Int32, *atomic.Value) {
	t.Helper()
	writes := &atomic.Int32{}
	zone := &atomic.Value{}
	zone.Store("Asia/Shanghai")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/config":
			json.NewEncoder(w).Encode(map[string]string{"time_zone": zone.Load().(string)})
		case r.URL.Path == "/api/states":
			json.NewEncoder(w).Encode([]EntityState{{EntityID: "light.a", State: "on"}, {EntityID: "binary_sensor.house_occupied", State: "off"}, {EntityID: "group.household", State: "home"}, {EntityID: "binary_sensor.broken", State: "unavailable"}})
		case r.Method == "POST":
			writes.Add(1)
			if strings.Contains(r.URL.Path, "/config/") {
				var a AutomationConfig
				if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
					t.Error(err)
				}
				if len(a.Condition) != 1 || a.Condition[0].EntityID != "binary_sensor.house_occupied" || a.Condition[0].State != "on" || a.Trigger[0].At != "18:00" || a.Action[0].Service != "light.turn_on" {
					t.Errorf("unsafe payload=%#v", a)
				}
			}
			fmt.Fprint(w, "{}")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := midnight.AddDate(0, 0, -14).Add(17 * time.Hour)
	if err = m.store.SaveHistory(patternHistory(start, 14)); err != nil {
		t.Fatal(err)
	}
	return m, writes, zone
}
func TestBindingCreatesImmutableSuggestion(t *testing.T) {
	m, writes, _ := generatedManager(t)
	report, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Suggestions) != 1 {
		t.Fatalf("generated=%#v", report)
	}
	source := report.Suggestions[0]
	if _, err = m.ConfirmSuggestion(context.Background(), source.ID); !errors.Is(err, ErrUnsupportedRule) {
		t.Fatalf("unbound confirm=%v", err)
	}
	binder, ok := any(m).(suggestionBinder)
	if !ok {
		t.Fatal("manager missing immutable binding API")
	}
	binding := SuggestionBindings{Presence: &PresenceBinding{EntityID: "binary_sensor.house_occupied", State: "on"}}
	revision, err := binder.BindSuggestion(context.Background(), source.ID, binding)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 0 {
		t.Fatal("binding wrote HA")
	}
	if revision.ID == source.ID || revision.SourceSuggestionID != source.ID || revision.Automation == nil || len(revision.MissingBindings) != 0 {
		t.Fatalf("revision=%#v", revision)
	}
	again, err := binder.BindSuggestion(context.Background(), source.ID, binding)
	if err != nil || again.ID != revision.ID {
		t.Fatalf("retry=%#v %v", again, err)
	}
	if _, err = binder.BindSuggestion(context.Background(), source.ID, SuggestionBindings{Presence: &PresenceBinding{EntityID: "group.household", State: "home"}}); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("changed superseded source=%v", err)
	}
	stored, _ := m.store.GetSuggestions()
	if len(stored) != 2 || stored[0].Status != "superseded" || stored[0].SupersededBy != revision.ID || stored[0].PresenceBinding != nil {
		t.Fatalf("non-atomic revisions=%#v", stored)
	}
	if _, err = m.ConfirmSuggestion(context.Background(), source.ID); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("superseded confirm=%v", err)
	}
	confirmed, err := m.ConfirmSuggestion(context.Background(), revision.ID)
	if err != nil || confirmed.Status != "confirmed" {
		t.Fatalf("confirm=%#v %v", confirmed, err)
	}
	if writes.Load() != 2 {
		t.Fatalf("writes=%d", writes.Load())
	}
	if data, err := os.ReadFile(filepath.Join(m.store.basePath, "rules", revision.ID+".md")); err != nil || !strings.Contains(string(data), "binary_sensor.house_occupied") {
		t.Fatalf("archive=%s %v", data, err)
	}
	if _, err = binder.BindSuggestion(context.Background(), revision.ID, binding); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("confirmed mutation=%v", err)
	}
	if _, err = m.TriggerAnalysis(context.Background(), 14); err != nil {
		t.Fatal(err)
	}
	stored, _ = m.store.GetSuggestions()
	if stored[0].Status != "superseded" || stored[1].Status != "confirmed" {
		t.Fatal("reanalysis revived old rule")
	}
}
func TestHomeTimezoneUnavailableBlocksAnalysis(t *testing.T) {
	m, _, zone := generatedManager(t)
	for _, value := range []string{"", "not/a-zone"} {
		zone.Store(value)
		if _, err := m.TriggerAnalysis(context.Background(), 14); err == nil {
			t.Fatalf("invalid HA timezone %q silently accepted", value)
		}
	}
	zone.Store("UTC")
	m.cfg.TimeZone = "Asia/Shanghai"
	if _, err := m.TriggerAnalysis(context.Background(), 14); err == nil {
		t.Fatal("configured timezone overrode HA")
	}
}

func TestBindingNeverCallsHAWriteForInvalidEntities(t *testing.T) {
	for _, id := range []string{"binary_sensor.absent", "binary_sensor.broken"} {
		t.Run(id, func(t *testing.T) {
			m, writes, _ := generatedManager(t)
			report, err := m.TriggerAnalysis(context.Background(), 14)
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.BindSuggestion(context.Background(), report.Suggestions[0].ID, SuggestionBindings{Presence: &PresenceBinding{EntityID: id, State: "on"}})
			if !errors.Is(err, ErrUnsupportedRule) {
				t.Fatalf("invalid entity=%v", err)
			}
			if writes.Load() != 0 {
				t.Fatal("invalid binding called HA write")
			}
			stored, _ := m.store.GetSuggestions()
			if len(stored) != 1 || stored[0].Status != "pending" {
				t.Fatal("invalid binding mutated source")
			}
		})
	}
}
func TestGeneratedSuggestionTimezoneChangeBlocksConfirm(t *testing.T) {
	m, writes, zone := generatedManager(t)
	r, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := m.BindSuggestion(context.Background(), r.Suggestions[0].ID, SuggestionBindings{Presence: &PresenceBinding{EntityID: "binary_sensor.house_occupied", State: "on"}})
	if err != nil {
		t.Fatal(err)
	}
	zone.Store("UTC")
	if _, err = m.ConfirmSuggestion(context.Background(), bound.ID); !errors.Is(err, ErrUnsupportedRule) {
		t.Fatalf("timezone change accepted: %v", err)
	}
	if writes.Load() != 0 {
		t.Fatal("timezone mismatch wrote HA")
	}
}
func TestGeneratedCorrelationUsesSunsetUntilMidnight(t *testing.T) {
	store, _ := NewDeviceStore(t.TempDir())
	start := time.Date(2026, 9, 1, 17, 0, 0, 0, time.UTC)
	entries := patternHistory(start, 14)
	for day := 0; day < 14; day++ {
		at := start.AddDate(0, 0, day)
		entries = append(entries, HistoryEntry{EntityID: "switch.b", State: "off", Timestamp: at, ObservationKind: "initial"}, HistoryEntry{EntityID: "switch.b", State: "on", Timestamp: at.Add(time.Hour + time.Minute), ObservationKind: "change"})
	}
	store.SaveHistory(entries)
	r, err := NewAnalyzer(store, zap.NewNop()).Analyze(start.AddDate(0, 0, 14), 14)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Suggestions {
		if s.Intent.Kind != "correlation" {
			continue
		}
		a, err := BuildAutomation(s)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Condition) != 1 || a.Condition[0].Condition != "sun" || a.Condition[0].After != "sunset" || a.Condition[0].State != "" || a.Trigger[0].From != "off" || a.Action[0].Service != "switch.turn_on" {
			t.Fatalf("sunset condition weakened=%#v", a)
		}
		return
	}
	t.Fatal("actual correlation produced no executable suggestion")
}
func TestBindingAndConfirmationSerialize(t *testing.T) {
	m, _, _ := generatedManager(t)
	r, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := m.BindSuggestion(context.Background(), r.Suggestions[0].ID, SuggestionBindings{Presence: &PresenceBinding{EntityID: "binary_sensor.house_occupied", State: "on"}})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := m.ConfirmSuggestion(context.Background(), bound.ID); results <- err }()
	go func() {
		<-start
		_, err := m.BindSuggestion(context.Background(), bound.ID, SuggestionBindings{Presence: &PresenceBinding{EntityID: "group.household", State: "home"}})
		results <- err
	}()
	close(start)
	e1, e2 := <-results, <-results
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("exactly one competing mutation must succeed: %v / %v", e1, e2)
	}
	if e1 != nil && !errors.Is(e1, ErrSuggestionConflict) || e2 != nil && !errors.Is(e2, ErrSuggestionConflict) {
		t.Fatalf("wrong conflicts %v %v", e1, e2)
	}
}

func TestBindingRejectsActionDeviceAsPresence(t *testing.T) {
	m, _, _ := generatedManager(t)
	r, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.BindSuggestion(context.Background(), r.Suggestions[0].ID, SuggestionBindings{Presence: &PresenceBinding{EntityID: "light.a", State: "on"}}); !errors.Is(err, ErrUnsupportedRule) {
		t.Fatalf("action device substituted for aggregate presence: %v", err)
	}
}
func TestHomeTimezoneRejectsHostLocal(t *testing.T) {
	m, _, zone := generatedManager(t)
	zone.Store("Local")
	if _, err := m.TriggerAnalysis(context.Background(), 14); !errors.Is(err, ErrUnsupportedRule) {
		t.Fatalf("host timezone accepted: %v", err)
	}
}
func TestSummaryConflictBreaksDurationContinuity(t *testing.T) {
	a := NewAnalyzer(nil, zap.NewNop())
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r := a.buildSummaries(map[string][]HistoryEntry{"light.a": {{EntityID: "light.a", State: "off", Timestamp: start, ObservationKind: "initial"}, {EntityID: "light.a", State: "off", Timestamp: start.Add(time.Hour), ObservationKind: "change"}, {EntityID: "light.a", State: "on", Timestamp: start.Add(time.Hour), ObservationKind: "change"}}}, 1, start, start.Add(2*time.Hour))
	if r[0].TotalOnTime != 0 {
		t.Fatalf("conflicting state guessed on duration: %#v", r)
	}
}

func TestGeneratedCorrelationConfirmsAndArchives(t *testing.T) {
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			fmt.Fprint(w, `{"time_zone":"UTC"}`)
		case "/api/states":
			fmt.Fprint(w, `[{"entity_id":"light.a","state":"on"},{"entity_id":"switch.b","state":"off"}]`)
		case "/api/services/automation/reload":
			fmt.Fprint(w, `{}`)
		default:
			creates.Add(1)
			var a AutomationConfig
			if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
				t.Error(err)
			}
			if len(a.Condition) != 1 || a.Condition[0].After != "sunset" || a.Condition[0].Condition != "sun" || a.Action[0].Service != "switch.turn_on" {
				t.Errorf("HA correlation=%#v", a)
			}
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day()-14, 17, 0, 0, 0, time.UTC)
	entries := patternHistory(start, 14)
	for day := 0; day < 14; day++ {
		at := start.AddDate(0, 0, day)
		entries = append(entries, HistoryEntry{EntityID: "switch.b", State: "off", Timestamp: at, ObservationKind: "initial"}, HistoryEntry{EntityID: "switch.b", State: "on", Timestamp: at.Add(time.Hour + time.Minute), ObservationKind: "change"})
	}
	if err = m.store.SaveHistory(entries); err != nil {
		t.Fatal(err)
	}
	report, err := m.TriggerAnalysis(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range report.Suggestions {
		if s.Intent.Kind != "correlation" {
			continue
		}
		confirmed, err := m.ConfirmSuggestion(context.Background(), s.ID)
		if err != nil || confirmed.Status != "confirmed" {
			t.Fatalf("confirm=%#v %v", confirmed, err)
		}
		if creates.Load() != 1 {
			t.Fatal("missing HA create")
		}
		data, err := os.ReadFile(filepath.Join(m.store.basePath, "rules", s.ID+".md"))
		if err != nil || !strings.Contains(string(data), `"after": "sunset"`) {
			t.Fatalf("sunset archive=%s %v", data, err)
		}
		return
	}
	t.Fatal("no generated correlation")
}
func TestUnknownIntentFieldsCannotBeSilentlyDropped(t *testing.T) {
	var s RuleSuggestion
	err := json.Unmarshal([]byte(`{"id":"fixture","intent":{"schema_version":1,"kind":"time","entity_id":"light.a","at":"18:00","time_zone":"UTC","required_conditions":["presence_home"],"extra_condition":"must stay"}}`), &s)
	if err == nil {
		t.Fatal("unknown intent condition silently discarded")
	}
}
