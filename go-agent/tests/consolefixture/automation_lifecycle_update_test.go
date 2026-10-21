package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

// A same-key update must become observable without a test-issued reload. Leaving
// the loaded map stale, loading every stored key, or replaying a lost POST must
// fail this test through the public HA endpoints and real Manager observation.
func TestLifecycleSameKeyUpdateLoadsWithoutReload(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		lifecycle, loseResponse bool
	}{
		{"lifecycle", true, false}, {"lost_response", true, true}, {"default_retains_old_runtime", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newFixtureDependencies()
			defer d.Close()
			var control *lifecycleControl
			if tc.lifecycle {
				var err error
				control, err = d.startLifecycleControl(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
			}
			config := func(key, alias string) string {
				raw := `{"id":"` + key + `","alias":"` + alias + `","trigger":[{"platform":"time","at":"18:05"}],"action":[{"service":"switch.turn_on","target":{"entity_id":"switch.channel_1"}}]`
				if tc.lifecycle {
					raw += `,"fixture_unknown":{"large":9007199254740993,"values":[null,true,"1",1]}`
				}
				return raw + `}`
			}
			const key, other, pending = "updated_rule", "untouched_rule", "not_loaded_rule"
			original, updated, unrelated := config(key, "before"), config(key, "after"), config(other, "unrelated")
			for _, raw := range []struct{ key, body string }{{key, original}, {other, unrelated}} {
				lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/config/automation/config/"+raw.key, raw.body, 200)
			}
			lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/reload", `{}`, 200)
			manager, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: d.ha.URL, Token: fixtureHAToken, AgentVaultPath: t.TempDir()}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Stop()
			client := smarthome.NewHomeAssistantClient(d.ha.URL, fixtureHAToken, 2*time.Second)
			defer client.Close()
			registry, err := client.GetRegistry(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			entities := map[string]string{}
			for _, item := range registry.Entities {
				if item.UniqueID != nil {
					entities[*item.UniqueID] = item.EntityID
				}
			}
			if entities[key] == "" || entities[other] == "" {
				t.Fatal("missing initial mappings")
			}
			observe := func(rule string) smarthome.AutomationObservation {
				t.Helper()
				got, err := manager.ObserveAutomation(context.Background(), smarthome.AutomationRef{Kind: "external", ID: entities[rule]})
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			oldTarget, oldOther := observe(key), observe(other)
			if !oldTarget.RuntimeLoadProof.Verified || !oldOther.RuntimeLoadProof.Verified {
				t.Fatal("setup was not loaded")
			}
			if tc.lifecycle {
				lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/services/automation/turn_off", `{"entity_id":"`+entities[key]+`","stop_actions":false}`, 200)
			}
			lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/config/automation/config/"+pending, config(pending, "pending"), 200)
			if tc.loseResponse {
				lifecycleRequest(t, control.url, control.capability, "PUT", "/mode", `{"fault":"lose_config"}`, 200)
			}
			before, reloads, accepted := d.writeAttempts.snapshot(), d.automationReloads.Load(), d.automationWrites.Load()
			if tc.loseResponse {
				req, err := http.NewRequest("POST", d.ha.URL+"/api/config/automation/config/"+key, strings.NewReader(updated))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+fixtureHAToken)
				response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
				if response != nil {
					response.Body.Close()
				}
				if err == nil {
					t.Fatal("lost config response unexpectedly succeeded")
				}
			} else {
				lifecycleRequest(t, d.ha.URL, fixtureHAToken, "POST", "/api/config/automation/config/"+key, updated, 200)
			}
			want := before
			want.Total++
			want.Config++
			assertAttempts := func() {
				t.Helper()
				if got := d.writeAttempts.snapshot(); got != want || d.automationReloads.Load() != reloads || d.automationWrites.Load() != accepted+1 {
					t.Fatalf("update/reconciliation replayed or reloaded: got=%+v want=%+v reloads=%d accepted=%d", got, want, d.automationReloads.Load(), d.automationWrites.Load())
				}
			}
			assertAttempts()
			for i := 0; i < 2; i++ {
				got := observe(key)
				if !bytes.Equal(lifecycleRequest(t, d.ha.URL, fixtureHAToken, "GET", "/api/config/automation/config/"+key, ``, 200), []byte(updated)) || got.Config.Digest == oldTarget.Config.Digest {
					t.Fatalf("stored update missing: %+v", got.Config)
				}
				if tc.lifecycle {
					if !got.RuntimeLoadProof.Verified || got.RuntimeLoadProof.ObservedDigest != got.Config.Digest || got.State != "off" {
						t.Errorf("same-key update did not expose new loaded config with preserved state: %+v", got)
					}
				} else if got.RuntimeLoadProof.Verified || got.RuntimeLoadProof.ObservedDigest != oldTarget.Config.Digest {
					t.Errorf("default fixture update unexpectedly loaded: %+v", got)
				}
				otherObservation := observe(other)
				if !otherObservation.RuntimeLoadProof.Verified || otherObservation.Config.Digest != oldOther.Config.Digest || otherObservation.State != "on" {
					t.Fatal("unrelated rule changed")
				}
			}
			registry, err = client.GetRegistry(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range registry.Entities {
				if item.UniqueID != nil && *item.UniqueID == pending {
					t.Fatal("same-key update loaded unrelated new config")
				}
			}
			if tc.lifecycle {
				for _, mode := range []string{"missing", "drift"} {
					lifecycleRequest(t, control.url, control.capability, "PUT", "/mode", `{"runtime":"`+mode+`"}`, 200)
					got, _ := manager.ObserveAutomation(context.Background(), smarthome.AutomationRef{Kind: "external", ID: entities[key]})
					if got.RuntimeLoadProof.Verified {
						t.Fatalf("runtime %s manufactured load proof", mode)
					}
				}
				lifecycleRequest(t, control.url, control.capability, "PUT", "/mode", `{}`, 200)
				if got := observe(key); !got.RuntimeLoadProof.Verified {
					t.Fatal("restored runtime did not converge")
				}
				device, err := client.GetState(context.Background(), "switch.channel_1")
				if err != nil || device.State != "on" {
					t.Fatal("update changed synthetic physical state")
				}
			}
			assertAttempts()
		})
	}
}
