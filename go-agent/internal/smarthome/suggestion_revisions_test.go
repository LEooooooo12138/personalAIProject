package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func revisionFixture(t *testing.T) (*Manager, RuleSuggestion, *atomic.Int32, *catalogFake) {
	t.Helper()
	writes := &atomic.Int32{}
	states := []EntityState{{EntityID: "light.a", State: "off"}, {EntityID: "switch.b", State: "on"}, {EntityID: "fan.c", State: "off"}, {EntityID: "binary_sensor.house", State: "on"}, {EntityID: "light.hidden", State: "on"}, {EntityID: "light.disabled", State: "on"}, {EntityID: "light.unavailable", State: "unavailable"}, {EntityID: "sensor.unsupported", State: "on"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
			t.Errorf("revision made HA write: %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(w).Encode(map[string]string{"time_zone": "UTC"})
		case "/api/states":
			json.NewEncoder(w).Encode(states)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	fake := &catalogFake{states: states}
	for _, st := range states {
		fake.reg.Entities = append(fake.reg.Entities, RegistryEntity{EntityID: st.EntityID, Name: cp("same name")})
	}
	fake.reg.Entities[4].HiddenBy = cp("user")
	fake.reg.Entities[5].DisabledBy = cp("user")
	m.catalog.Close()
	m.catalog = NewCatalogService(context.Background(), fake)
	s := RuleSuggestion{ID: "original", Status: "pending", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Intent: &SuggestionIntent{SchemaVersion: 1, Kind: "time", EntityID: "light.a", At: "18:00", TimeZone: "UTC", RequiredConditions: []string{"presence_home"}}, PresenceBinding: &PresenceBinding{EntityID: "binary_sensor.house", State: "on"}, Evidence: []SuggestionEvidence{{ID: "historical", SampleSize: 11}}}
	renderSuggestion(&s)
	if err = m.store.SaveSuggestions([]RuleSuggestion{s}); err != nil {
		t.Fatal(err)
	}
	return m, s, writes, fake
}
func revisionRequest(s RuleSuggestion) SuggestionRevisionRequest {
	i := *s.Intent
	i.RequiredConditions = append([]string{}, i.RequiredConditions...)
	i.At = "19:30"
	i.EntityID = "switch.b"
	return SuggestionRevisionRequest{ExpectedVersion: s.ID, Intent: i, PresenceBinding: &PresenceBinding{EntityID: "binary_sensor.house", State: "on"}}
}
func TestSuggestionRevisionIsImmutableAndRestricted(t *testing.T) {
	m, s, writes, _ := revisionFixture(t)
	r, err := m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s))
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == s.ID || r.SourceSuggestionID != s.ID || r.Status != "pending" || len(r.Evidence) != 0 || r.Confidence != 0 {
		t.Fatalf("invalid revision: %+v", r)
	}
	original, err := m.findSuggestion(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.Intent, s.Intent) || !reflect.DeepEqual(original.Evidence, s.Evidence) || !original.CreatedAt.Equal(s.CreatedAt) || !reflect.DeepEqual(original.PresenceBinding, s.PresenceBinding) {
		t.Fatal("revision mutated source content")
	}
	if original.Status != "superseded" || original.SupersededBy != r.ID {
		t.Fatal("head not advanced")
	}
	config, err := BuildAutomation(*r)
	if err != nil || config.Action[0].Service != "switch.turn_on" || config.Trigger[0].At != "19:30" || config.Condition[0].EntityID != "binary_sensor.house" {
		t.Fatalf("lost restricted semantics: %+v %v", config, err)
	}
	if _, err = m.ConfirmSuggestion(context.Background(), s.ID); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("old confirmation=%v", err)
	}
	if _, err = m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s)); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("stale revision=%v", err)
	}
	if err = m.store.SaveSuggestions([]RuleSuggestion{s}); err != nil {
		t.Fatal(err)
	}
	original, _ = m.findSuggestion(s.ID)
	if original.Status != "superseded" {
		t.Fatal("rediscovery revived original")
	}
	reopened, _ := NewDeviceStore(m.store.basePath)
	stored, err := reopened.GetSuggestions()
	if err != nil || len(stored) != 2 {
		t.Fatalf("not durable: %+v %v", stored, err)
	}
	if writes.Load() != 0 {
		t.Fatal("unconfirmed revision wrote HA")
	}
	// Request/response pointers cannot mutate persisted immutable versions.
	r.Intent.At = "01:00"
	again, _ := m.findSuggestion(stored[1].ID)
	if again.Intent.At != "19:30" {
		t.Fatal("mutable alias")
	}
}
func TestSuggestionRevisionRejectsInvalidRequests(t *testing.T) {
	cases := map[string]func(*SuggestionRevisionRequest){
		"missing_version": func(r *SuggestionRevisionRequest) { r.ExpectedVersion = "" },
		"wrong_version":   func(r *SuggestionRevisionRequest) { r.ExpectedVersion = "other" },
		"schema":          func(r *SuggestionRevisionRequest) { r.Intent.SchemaVersion = 2 },
		"kind":            func(r *SuggestionRevisionRequest) { r.Intent.Kind = "correlation" },
		"zone":            func(r *SuggestionRevisionRequest) { r.Intent.TimeZone = "Asia/Shanghai" },
		"condition":       func(r *SuggestionRevisionRequest) { r.Intent.RequiredConditions = nil },
		"extra_condition": func(r *SuggestionRevisionRequest) {
			r.Intent.RequiredConditions = append(r.Intent.RequiredConditions, "other")
		},
		"missing_presence":   func(r *SuggestionRevisionRequest) { r.PresenceBinding = nil },
		"guessed_presence":   func(r *SuggestionRevisionRequest) { r.PresenceBinding.EntityID = "light.a" },
		"missing_target":     func(r *SuggestionRevisionRequest) { r.Intent.EntityID = "light.absent" },
		"hidden_target":      func(r *SuggestionRevisionRequest) { r.Intent.EntityID = "light.hidden" },
		"disabled_target":    func(r *SuggestionRevisionRequest) { r.Intent.EntityID = "light.disabled" },
		"unavailable_target": func(r *SuggestionRevisionRequest) { r.Intent.EntityID = "light.unavailable" },
		"unsupported_target": func(r *SuggestionRevisionRequest) { r.Intent.EntityID = "sensor.unsupported" },
		"invalid_time":       func(r *SuggestionRevisionRequest) { r.Intent.At = "25:30" },
		"related_time":       func(r *SuggestionRevisionRequest) { r.Intent.RelatedID = "fan.c" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, s, writes, _ := revisionFixture(t)
			req := revisionRequest(s)
			mutate(&req)
			if _, err := m.ReviseSuggestion(context.Background(), s.ID, req); err == nil {
				t.Fatal("invalid accepted")
			}
			stored, _ := m.store.GetSuggestions()
			if len(stored) != 1 || stored[0].Status != "pending" || writes.Load() != 0 {
				t.Fatal("invalid request mutated state")
			}
		})
	}
}
func TestSuggestionRevisionConcurrencyAndAtomicFailure(t *testing.T) {
	t.Run("concurrent", func(t *testing.T) {
		m, s, _, _ := revisionFixture(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for n := 0; n < 2; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s))
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		a, b := <-results, <-results
		if (a == nil) == (b == nil) {
			t.Fatalf("two winners/losers: %v %v", a, b)
		}
		if a != nil && !errors.Is(a, ErrSuggestionConflict) || b != nil && !errors.Is(b, ErrSuggestionConflict) {
			t.Fatalf("wrong conflict %v %v", a, b)
		}
	})
	t.Run("store_failure", func(t *testing.T) {
		m, s, _, _ := revisionFixture(t)
		path := filepath.Join(m.store.basePath, "rules", "suggestions.json")
		before, _ := os.ReadFile(path)
		// An unreadable destination must fail without changing the saved original.
		if err := os.Rename(path, path+".saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s)); err == nil {
			t.Fatal("store failure accepted")
		}
		after, _ := os.ReadFile(path + ".saved")
		if string(before) != string(after) {
			t.Fatal("original changed after failure")
		}
	})
}
func TestSuggestionRevisionLegacyAndNonPendingReadOnly(t *testing.T) {
	for _, status := range []string{"confirmed", "ignored", "applying", "failed"} {
		t.Run(status, func(t *testing.T) {
			m, s, _, _ := revisionFixture(t)
			s.Status = status
			if err := m.store.writeSuggestions([]RuleSuggestion{s}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s)); !errors.Is(err, ErrSuggestionConflict) {
				t.Fatalf("%s revision=%v", status, err)
			}
		})
	}
	for _, version := range []int{0, 9} {
		t.Run("legacy", func(t *testing.T) {
			m, s, _, _ := revisionFixture(t)
			req := revisionRequest(s)
			s.Intent.SchemaVersion = version
			if version == 0 {
				s.Intent = nil
			}
			if err := m.store.writeSuggestions([]RuleSuggestion{s}); err != nil {
				t.Fatal(err)
			}
			if _, err := m.ReviseSuggestion(context.Background(), s.ID, req); !errors.Is(err, ErrUnsupportedRule) {
				t.Fatalf("legacy revision=%v", err)
			}
		})
	}
}
func TestSuggestionRevisionCorrelationAndStaleCatalog(t *testing.T) {
	m, s, _, fake := revisionFixture(t)
	s.Intent = &SuggestionIntent{SchemaVersion: 1, Kind: "correlation", EntityID: "light.a", RelatedID: "switch.b", TimeZone: "UTC", RequiredConditions: []string{"sunset_to_midnight"}}
	s.PresenceBinding = nil
	if err := m.store.writeSuggestions([]RuleSuggestion{s}); err != nil {
		t.Fatal(err)
	}
	req := SuggestionRevisionRequest{ExpectedVersion: s.ID, Intent: *s.Intent}
	req.Intent.EntityID = "switch.b"
	req.Intent.RelatedID = "fan.c"
	r, err := m.ReviseSuggestion(context.Background(), s.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	a, err := BuildAutomation(*r)
	if err != nil || a.Trigger[0].EntityID != "switch.b" || a.Trigger[0].From != "off" || a.Condition[0].After != "sunset" || a.Action[0].Service != "fan.turn_on" {
		t.Fatalf("correlation changed contract: %+v %v", a, err)
	}
	fake.mu.Lock()
	fake.fail = true
	fake.mu.Unlock()
	m.catalog.mu.Lock()
	old := time.Now().Add(-time.Hour)
	m.catalog.snapshot.Meta.ObservedAt = &old
	m.catalog.mu.Unlock()
	req.ExpectedVersion = r.ID
	req.Intent.RelatedID = "light.a"
	if _, err = m.ReviseSuggestion(context.Background(), r.ID, req); !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("stale catalog accepted: %v", err)
	}
}

func TestSuggestionRevisionChainRetainsEvidenceAndBindingContract(t *testing.T) {
	m, s, _, _ := revisionFixture(t)
	first, err := m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s))
	if err != nil {
		t.Fatal(err)
	}
	req := SuggestionRevisionRequest{ExpectedVersion: first.ID, Intent: *s.Intent, PresenceBinding: s.PresenceBinding}
	second, err := m.ReviseSuggestion(context.Background(), first.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == s.ID || second.ID == first.ID || second.SourceSuggestionID != first.ID {
		t.Fatal("reverting intent reused an immutable version ID")
	}
	evidence := SuggestionEvidence{RunID: "0123456789abcdef0123456789abcdef", Window: TimeWindow{Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)}, AlgorithmVersion: "deterministic-v1", SampleSize: 11, PeriodDays: 14, Denominator: 14, DenominatorType: "requested_days"}
	if err = m.store.AppendSuggestionEvidence(s.ID, evidence); err != nil {
		t.Fatal(err)
	}
	original, _ := m.findSuggestion(s.ID)
	latest, _ := m.findSuggestion(second.ID)
	middle, _ := m.findSuggestion(first.ID)
	if len(original.Evidence) != 2 || len(latest.Evidence) != 0 || original.Status != "superseded" || middle.Status != "superseded" || !reflect.DeepEqual(latest.Intent, s.Intent) {
		t.Fatal("evidence append rewrote revision chain")
	}
	// Legacy binding API must still reject arbitrary intent edits.
	changed := *latest
	changed.Intent = &SuggestionIntent{SchemaVersion: 1, Kind: "time", EntityID: "fan.c", At: "20:00", TimeZone: "UTC", RequiredConditions: []string{"presence_home"}}
	changed.SourceSuggestionID = latest.ID
	changed.ID = stableSuggestionID(changed)
	if _, err = m.store.SaveBoundSuggestion(latest.ID, changed); !errors.Is(err, ErrUnsupportedRule) {
		t.Fatalf("binding API broadened: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	req.ExpectedVersion = latest.ID
	req.Intent.At = "21:00"
	if _, err = m.ReviseSuggestion(cancelled, latest.ID, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled revision=%v", err)
	}
}

func TestSuggestionRevisionStoreCompareAndSwap(t *testing.T) {
	m, s, _, _ := revisionFixture(t)
	if _, err := m.store.changeSuggestion(s.ID, "ignored", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.saveSuggestionRevision(s.ID, revisionRequest(s)); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("store committed stale intent: %v", err)
	}
}

func TestSuggestionRevisionAndBindingRejectDurableInstallAttempt(t *testing.T) {
	for _, mode := range []string{"attempted", "corrupt"} {
		for _, operation := range []string{"revise", "bind"} {
			t.Run(mode+"/"+operation, func(t *testing.T) {
				m, s, writes, _ := revisionFixture(t)
				if mode == "attempted" {
					op, err := newInstallOperation(s, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
					if err != nil {
						t.Fatal(err)
					}
					at := time.Now().UTC()
					op.OwnAttempt = true
					op.Status = "unknown"
					op.Phase = "create"
					op.Create = InstallStage{Status: "unknown", AttemptedAt: &at}
					if err = m.store.saveInstallOperation(op); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(filepath.Join(m.store.basePath, "rules", "install-state.json"), []byte("{broken"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if operation == "revise" {
					_, err = m.ReviseSuggestion(context.Background(), s.ID, revisionRequest(s))
				} else {
					_, err = m.BindSuggestion(context.Background(), s.ID, SuggestionBindings{Presence: s.PresenceBinding})
				}
				if err == nil {
					t.Fatal("uncertain install allowed new version")
				}
				if mode == "attempted" && !errors.Is(err, ErrSuggestionConflict) {
					t.Fatalf("wrong operation conflict: %v", err)
				}
				stored, _ := m.store.GetSuggestions()
				if len(stored) != 1 || stored[0].Status != "pending" || writes.Load() != 0 {
					t.Fatal("durable operation guard mutated state")
				}
			})
		}
	}
}
