package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLegacyTimedConfirmationRequiresAuthoritativeTimezone(t *testing.T) {
	for _, kind := range []string{"time", "sun"} {
		for _, tc := range []struct {
			name, body string
			status     int
			want       error
		}{
			{"mismatch", `{"time_zone":"UTC"}`, 200, ErrUnsupportedRule},
			{"unreadable", `{}`, 503, ErrHARequest},
			{"missing", `{}`, 200, ErrUnsupportedRule},
			{"Local", `{"time_zone":"Local"}`, 200, ErrUnsupportedRule},
			{"invalid", `{"time_zone":"not/a-zone"}`, 200, ErrUnsupportedRule},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				var writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						writes.Add(1)
						w.Write([]byte(`{}`))
						return
					}
					if r.URL.Path != "/api/config" {
						t.Errorf("unexpected read %s", r.URL.Path)
					}
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				}))
				defer server.Close()
				m, err := NewManager(HAConfig{BaseURL: server.URL, TimeZone: "Asia/Shanghai", AgentVaultPath: t.TempDir()}, zap.NewNop())
				if err != nil {
					t.Fatal(err)
				}
				suggestion := decodeSuggestion(t, executableSuggestion)
				if kind == "sun" {
					suggestion.Automation.Trigger = []AutomationTrigger{{Platform: "state", EntityID: "binary_sensor.door", From: "off", To: "on"}}
					suggestion.Automation.Condition = []AutomationCondition{{Condition: "sun", After: "sunset"}}
				}
				if err = m.store.SaveSuggestions([]RuleSuggestion{suggestion}); err != nil {
					t.Fatal(err)
				}
				if _, err = m.ConfirmSuggestion(context.Background(), suggestion.ID); !errors.Is(err, tc.want) {
					t.Errorf("confirm=%v want %v", err, tc.want)
				}
				if writes.Load() != 0 {
					t.Errorf("invalid timezone allowed %d HA writes", writes.Load())
				}
				stored, err := m.store.GetSuggestions()
				if err != nil || stored[0].Status != "pending" {
					t.Errorf("preflight changed state: %#v %v", stored, err)
				}
			})
		}
	}
}

func TestLegacyConfirmationPreservesPayloadAndStateOnlyCompatibility(t *testing.T) {
	for _, kind := range []string{"time", "sun", "state"} {
		t.Run(kind, func(t *testing.T) {
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads.Add(1)
					if kind == "state" {
						http.Error(w, "timezone offline", 503)
						return
					}
					w.Write([]byte(`{"time_zone":"Asia/Shanghai"}`))
					return
				}
				writes.Add(1)
				if r.URL.Path == "/api/config/automation/config/rule-kitchen" {
					var payload AutomationConfig
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
						return
					}
					if payload.ID != "rule-kitchen" || len(payload.Action) != 1 || payload.Action[0].Service != "switch.turn_on" || payload.Action[0].Target.EntityID != "switch.kitchen" {
						t.Errorf("legacy action changed: %#v", payload)
					}
					if len(payload.Trigger) != 1 || len(payload.Condition) != 1 {
						t.Errorf("legacy rule weakened: %#v", payload)
						return
					}
					if kind == "time" {
						if payload.Trigger[0].Platform != "time" || payload.Trigger[0].At != "18:00" {
							t.Errorf("legacy time changed: %#v", payload.Trigger)
						}
					} else if payload.Trigger[0].Platform != "state" || payload.Trigger[0].EntityID != "binary_sensor.door" || payload.Trigger[0].From != "off" || payload.Trigger[0].To != "on" {
						t.Errorf("legacy state changed: %#v", payload.Trigger)
					}
					if kind == "sun" {
						if payload.Condition[0].Condition != "sun" || payload.Condition[0].After != "sunset" {
							t.Errorf("legacy sun changed: %#v", payload.Condition)
						}
					} else if payload.Condition[0].EntityID != "binary_sensor.occupied" || payload.Condition[0].State != "on" {
						t.Errorf("legacy condition changed: %#v", payload.Condition)
					}
				}
				w.Write([]byte(`{}`))
			}))
			defer server.Close()
			m, err := NewManager(HAConfig{BaseURL: server.URL, TimeZone: "Asia/Shanghai", AgentVaultPath: t.TempDir()}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			suggestion := decodeSuggestion(t, executableSuggestion)
			if kind != "time" {
				suggestion.Automation.Trigger = []AutomationTrigger{{Platform: "state", EntityID: "binary_sensor.door", From: "off", To: "on"}}
			}
			if kind == "sun" {
				suggestion.Automation.Condition = []AutomationCondition{{Condition: "sun", After: "sunset"}}
			}
			if err = m.store.SaveSuggestions([]RuleSuggestion{suggestion}); err != nil {
				t.Fatal(err)
			}
			confirmed, err := m.ConfirmSuggestion(context.Background(), suggestion.ID)
			if err != nil || confirmed.Status != "confirmed" {
				t.Fatalf("confirm=%#v %v", confirmed, err)
			}
			if writes.Load() != 2 {
				t.Errorf("writes=%d", writes.Load())
			}
			wantReads := int32(1)
			if kind == "state" {
				wantReads = 0
			}
			if reads.Load() != wantReads {
				t.Errorf("timezone reads=%d want=%d", reads.Load(), wantReads)
			}
		})
	}
}
