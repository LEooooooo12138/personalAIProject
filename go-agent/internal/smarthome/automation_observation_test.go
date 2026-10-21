package smarthome

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const observationConfig = `{"id":"rule-1","alias":"Shared name","triggers":[],"actions":[],"unknown":{"large":9007199254740993,"values":[null,true,"1",1]}}`

type observationFixture struct {
	version       string
	components    []string
	stored        string
	storedStatus  int
	entities      string
	runtime       string
	runtimeCode   string
	state         string
	writes        atomic.Int32
	configReads   atomic.Int32
	runtimeReads  atomic.Int32
	stateReads    atomic.Int32
	registryReads atomic.Int32
}

func newObservationFixture(t *testing.T) (*observationFixture, *HomeAssistantClient) {
	t.Helper()
	f := &observationFixture{version: "2026.7.2", components: []string{"automation", "config"}, stored: observationConfig, entities: `[{"entity_id":"automation.rule","platform":"automation","unique_id":"rule-1","name":"Shared name"}]`, runtime: observationConfig, state: "on"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			f.writes.Add(1)
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(w).Encode(map[string]any{"version": f.version, "components": f.components})
		case "/api/config/automation/config/rule-1":
			f.configReads.Add(1)
			if f.storedStatus != 0 {
				w.WriteHeader(f.storedStatus)
				io.WriteString(w, "private-body")
				return
			}
			io.WriteString(w, f.stored)
		case "/api/states/automation.not_observed":
			f.stateReads.Add(1)
			w.WriteHeader(http.StatusNotFound)
		case "/api/states/automation.rule":
			f.stateReads.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"entity_id": "automation.rule", "state": f.state, "attributes": map[string]any{}})
		case "/api/websocket":
			conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if e != nil {
				return
			}
			defer conn.Close()
			conn.WriteJSON(map[string]any{"type": "auth_required", "ha_version": f.version})
			var auth map[string]any
			if conn.ReadJSON(&auth) != nil {
				return
			}
			conn.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				var req map[string]any
				if conn.ReadJSON(&req) != nil {
					return
				}
				result := map[string]any{"id": req["id"], "type": "result", "success": true}
				switch req["type"] {
				case "config/entity_registry/list":
					f.registryReads.Add(1)
					result["result"] = json.RawMessage(f.entities)
				case "automation/config":
					if req["entity_id"] != "automation.rule" {
						t.Error("runtime request must use verified entity")
					}
					f.runtimeReads.Add(1)
					if f.runtimeCode != "" {
						result["success"] = false
						result["error"] = map[string]any{"code": f.runtimeCode, "message": "private-body"}
					} else {
						result["result"] = map[string]any{"config": json.RawMessage(f.runtime)}
					}
				default:
					f.writes.Add(1)
					t.Errorf("unexpected command %v", req["type"])
					return
				}
				if conn.WriteJSON(result) != nil {
					return
				}
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		if f.writes.Load() != 0 {
			t.Fatalf("observation issued %d writes/unexpected commands", f.writes.Load())
		}
	})
	return f, NewHomeAssistantClient(srv.URL, "fixture-secret", time.Second)
}
func TestAutomationObservationDistinguishesMissingAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		version string
		want    SourceStatus
		missing bool
	}{
		{"missing", 404, "2026.7.2", SourceAvailable, true}, {"forbidden", 403, "2026.7.2", SourceForbidden, false},
		{"unreviewed", 404, "2026.7.3", SourceUnavailable, false}, {"no_version", 404, "", SourceUnavailable, false}, {"server_error", 500, "2026.7.2", SourceUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newObservationFixture(t)
			f.storedStatus = tc.status
			f.version = tc.version
			got, err := c.ReadAutomationConfig(context.Background(), "rule-1")
			if got.SourceStatus != tc.want || (got.Exists != nil) != tc.missing {
				t.Fatalf("got %+v, %v", got, err)
			}
			if tc.missing && (*got.Exists || err != nil) {
				t.Fatalf("missing: %+v %v", got, err)
			}
			if !tc.missing && err == nil {
				t.Fatal("failure lacks error")
			}
			if tc.version != "2026.7.2" && f.configReads.Load() != 0 {
				t.Fatal("unknown profile probed config route")
			}
			if err != nil && strings.Contains(err.Error(), "private-body") {
				t.Fatal("leaked response")
			}
		})
	}
	t.Run("network", func(t *testing.T) {
		s := httptest.NewServer(http.NotFoundHandler())
		s.Close()
		got, err := NewHomeAssistantClient(s.URL, "secret", time.Second).ReadAutomationConfig(context.Background(), "rule-1")
		if err == nil || got.SourceStatus != SourceUnavailable || got.Exists != nil {
			t.Fatalf("%+v %v", got, err)
		}
	})
}
func TestAutomationObservationDoesNotInferOwnership(t *testing.T) {
	for _, entities := range []string{
		`[{"entity_id":"automation.rule","name":"Shared name"}]`,
		`[{"entity_id":"automation.rule","platform":"automation","unique_id":"rule-1"}]`,
		`[{"entity_id":"automation.rule","platform":"other","unique_id":"rule-1"}]`,
		`[{"entity_id":"automation.rule","platform":"automation","unique_id":"rule-1"},{"entity_id":"automation.duplicate","platform":"automation","unique_id":"rule-1"}]`,
	} {
		t.Run(entities, func(t *testing.T) {
			f, c := newObservationFixture(t)
			f.entities = entities
			got, _ := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"})
			if got.Ownership == "managed" {
				t.Fatal("registry/name conferred ownership")
			}
			if strings.Contains(entities, `"name"`) && (got.Config.ConfigKey != "" || got.RuntimeLoadProof.Verified) {
				t.Fatal("name created mapping")
			}
			if strings.Contains(entities, "duplicate") && (got.RuntimeLoadProof.Verified || f.configReads.Load() != 0) {
				t.Fatal("ambiguous tuple selected")
			}
		})
	}
	f, c := newObservationFixture(t)
	got, err := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "managed", ID: "legacy-confirmed-suggestion"})
	if err == nil || got.Ownership != "unverified" || f.configReads.Load() != 0 {
		t.Fatalf("unproven managed ref: %+v %v", got, err)
	}
}
func TestConfigMatchIsNotRuntimeProof(t *testing.T) {
	for _, tc := range []struct {
		name, config, state, code string
		want                      bool
	}{
		{"current_on", observationConfig, "on", "", true}, {"current_off", observationConfig, "off", "", true},
		{"unavailable_entity", observationConfig, "unavailable", "", false}, {"unknown_entity", observationConfig, "unknown", "", false},
		{"missing_runtime", "null", "on", "", false}, {"unsupported_runtime", observationConfig, "on", "unknown_command", false},
		{"forbidden_runtime", observationConfig, "on", "unauthorized", false},
		{"id_drift", strings.Replace(observationConfig, `"rule-1"`, `"rule-2"`, 1), "on", "", false},
		{"alias_drift", strings.Replace(observationConfig, "Shared name", "Changed", 1), "on", "", false},
		{"unknown_drift", strings.Replace(observationConfig, "9007199254740993", "9007199254740992", 1), "on", "", false},
		{"blueprint_expansion", `{"id":"rule-1","use_blueprint":{"path":"example.yaml","input":{}}}`, "on", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newObservationFixture(t)
			f.runtime = tc.config
			f.state = tc.state
			f.runtimeCode = tc.code
			got, _ := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"})
			if got.RuntimeLoadProof.Verified != tc.want {
				t.Fatalf("proof %+v", got)
			}
			if got.Config.Exists == nil || !*got.Config.Exists || got.Config.Digest == "" {
				t.Fatalf("lost stored observation %+v", got)
			}
			if tc.want && (got.MappingSource != "config/entity_registry/list" || got.RuntimeLoadProof.Source != "automation/config" || got.RuntimeLoadProof.ObservedAt.IsZero()) {
				t.Fatalf("missing provenance %+v", got)
			}
		})
	}
}
func TestAutomationObservationPreservesCompleteConfig(t *testing.T) {
	f, c := newObservationFixture(t)
	a, err := c.ReadAutomationConfig(context.Background(), "rule-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a.RawConfig), "9007199254740993") || !strings.Contains(string(a.RawConfig), `[null,true,"1",1]`) {
		t.Fatal("config truncated or scalar types changed")
	}
	serialized, _ := json.Marshal(a)
	if strings.Contains(string(serialized), "Shared name") || strings.Contains(string(serialized), "9007199254740993") {
		t.Fatal("raw config leaked into DTO")
	}
	f.stored = `{ "unknown":{"values":[null,true,"1",1],"large":9007199254740993},"actions":[],"triggers":[],"alias":"Shared name","id":"rule-1" }`
	b, err := c.ReadAutomationConfig(context.Background(), "rule-1")
	if err != nil || a.Digest != b.Digest {
		t.Fatalf("encoding/key order changed digest: %v", err)
	}
	for _, changed := range []string{
		strings.Replace(observationConfig, `"alias":"Shared name",`, "", 1), strings.Replace(observationConfig, `"id":"rule-1",`, "", 1),
		strings.Replace(observationConfig, `[null,true,"1",1]`, `[true,null,"1",1]`, 1), strings.Replace(observationConfig, `"triggers"`, `"trigger"`, 1),
	} {
		f.stored = changed
		b, err = c.ReadAutomationConfig(context.Background(), "rule-1")
		if err != nil || a.Digest == b.Digest {
			t.Fatalf("normalized away meaningful data %s, %v", changed, err)
		}
	}
}
func TestAutomationObservationRejectsUnsafeAndUnobservedTargets(t *testing.T) {
	for _, key := range []string{"", "..", "../rule-1", "https://other/rule-1", "a/b", "a\\b", "a?b", "a%b"} {
		f, c := newObservationFixture(t)
		_, err := c.ReadAutomationConfig(context.Background(), key)
		if err == nil || f.configReads.Load() != 0 {
			t.Fatalf("unsafe key %q", key)
		}
	}
	f, c := newObservationFixture(t)
	got, err := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.not_observed"})
	if err == nil || got.RuntimeLoadProof.Verified || f.configReads.Load() != 0 {
		t.Fatalf("unobserved target %+v %v", got, err)
	}
}

func TestAutomationObservationRuntimeErrorsRemainTyped(t *testing.T) {
	for _, tc := range []struct {
		code, want string
		status     SourceStatus
	}{
		{"unknown_command", "ha_unsupported", SourceUnsupported}, {"not_supported", "ha_unsupported", SourceUnsupported},
		{"not_allowed", "ha_forbidden", SourceForbidden}, {"auth_invalid", "ha_auth_required", SourceForbidden},
		{"not_found", "ha_not_found", SourceUnavailable}, {"invalid_format", "ha_invalid_response", SourceUnavailable},
		{"unknown_error", "ha_unavailable", SourceUnavailable}, {"home_assistant_error", "ha_unavailable", SourceUnavailable},
	} {
		t.Run(tc.code, func(t *testing.T) {
			f, c := newObservationFixture(t)
			f.runtimeCode = tc.code
			got, err := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"})
			if HAErrorCode(err) != tc.want || got.RuntimeLoadProof.SourceStatus != tc.status || got.RuntimeLoadProof.Verified || strings.Contains(err.Error(), "private-body") {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
func TestAutomationObservationRejectsMalformedCompleteConfig(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"id":"rule-1","id":"other"}`, `{"unknown":{"x":1,"x":2}}`, `{} {}`, `{"nested":` + strings.Repeat("[", 102) + `0` + strings.Repeat("]", 102) + `}`} {
		f, c := newObservationFixture(t)
		f.stored = raw
		got, err := c.ReadAutomationConfig(context.Background(), "rule-1")
		if HAErrorCode(err) != "ha_invalid_response" || got.Exists != nil || got.Digest != "" || len(got.RawConfig) != 0 {
			t.Fatalf("accepted ambiguous config %q: %+v %v", raw, got, err)
		}
	}
}
func TestAutomationObservationRefreshesVersionAndKeepsPlainState(t *testing.T) {
	f, c := newObservationFixture(t)
	if _, err := c.ReadAutomationConfig(context.Background(), "rule-1"); err != nil {
		t.Fatal(err)
	}
	f.version = "2026.8.0"
	got, err := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.rule"})
	if HAErrorCode(err) != "ha_version_unknown" || got.Config.Exists != nil || got.RuntimeLoadProof.Verified || got.State != "on" || got.StateStatus != SourceAvailable || f.configReads.Load() != 1 || f.runtimeReads.Load() != 0 {
		t.Fatalf("stale profile or lost state %+v %v", got, err)
	}
	f.version = "2026.7.2"
	f.components = []string{"automation"}
	config, err := c.ReadAutomationConfig(context.Background(), "rule-1")
	if HAErrorCode(err) != "ha_capability_unknown" || config.Exists != nil || f.configReads.Load() != 1 {
		t.Fatalf("unsupported registration interpreted as missing %+v %v", config, err)
	}
}
func TestAutomationObservationExpectedDigestDoesNotFollowExternalDrift(t *testing.T) {
	f, c := newObservationFixture(t)
	original, err := c.ReadAutomationConfig(context.Background(), "rule-1")
	if err != nil {
		t.Fatal(err)
	}
	f.stored = strings.Replace(observationConfig, "Shared name", "Changed", 1)
	f.runtime = f.stored
	got, err := (&Manager{client: c}).observeAutomationConfigKey(context.Background(), "rule-1", original.Digest)
	if err != nil || got.RuntimeLoadProof.Verified || got.RuntimeLoadProof.ExpectedDigest != original.Digest || got.Config.Digest == original.Digest || got.Ownership != "unverified" {
		t.Fatalf("drift rewrote expectation %+v %v", got, err)
	}
	f.runtime = observationConfig
	got, err = (&Manager{client: c}).observeAutomationConfigKey(context.Background(), "rule-1", original.Digest)
	if err != nil || !got.RuntimeLoadProof.Verified || got.Config.Digest == original.Digest {
		t.Fatalf("stored/runtime facts conflated %+v %v", got, err)
	}
	f.entities = `[{"entity_id":"automation.rule","platform":"automation","unique_id":"rule-1"},{"entity_id":"automation.rule","platform":"automation","unique_id":"other"}]`
	got, err = (&Manager{client: c}).observeAutomationConfigKey(context.Background(), "rule-1", original.Digest)
	if err == nil || got.RuntimeLoadProof.Verified {
		t.Fatalf("duplicate entity identity was trusted %+v", got)
	}
}

func TestAutomationObservationKnownKeyRetainsConfigWithoutMapping(t *testing.T) {
	f, c := newObservationFixture(t)
	original, err := c.ReadAutomationConfig(context.Background(), "rule-1")
	if err != nil {
		t.Fatal(err)
	}
	f.entities = `[]`
	got, err := (&Manager{client: c}).observeAutomationConfigKey(context.Background(), "rule-1", original.Digest)
	if err == nil || got.Config.Exists == nil || !*got.Config.Exists || got.Config.Digest != original.Digest || got.RuntimeLoadProof.Verified || f.runtimeReads.Load() != 0 {
		t.Fatalf("lost independent stored fact %+v %v", got, err)
	}
}
func TestAutomationObservationConcurrencyWaitIsCancellable(t *testing.T) {
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(503) }))
	defer srv.Close()
	c := NewHomeAssistantClient(srv.URL, "secret", time.Second)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := c.ReadAutomationConfig(context.Background(), "rule-1"); done <- err }()
	}
	<-entered
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.ReadAutomationConfig(ctx, "rule-1")
	select {
	case <-entered:
		t.Error("third simultaneous observation reached HA")
	default:
	}
	close(release)
	<-done
	<-done
	if err != context.DeadlineExceeded {
		t.Fatalf("waiting observation ignored cancellation: %v", err)
	}
}

func TestAutomationObservationUnknownProfileRequiresObservedSelection(t *testing.T) {
	for _, tc := range []struct {
		name, target, entities string
		selected               bool
	}{
		{"unobserved", "automation.not_observed", `[{"entity_id":"automation.rule"}]`, false},
		{"empty_registry", "automation.rule", `[]`, false},
		{"duplicate_identity", "automation.rule", `[{"entity_id":"automation.rule"},{"entity_id":"automation.rule"}]`, false},
		{"unavailable_registry", "automation.rule", `null`, false},
		{"selected_plain_state", "automation.rule", `[{"entity_id":"automation.rule"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newObservationFixture(t)
			f.version = "2026.8.0"
			f.entities = tc.entities
			got, err := (&Manager{client: c}).ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: tc.target})
			if HAErrorCode(err) != "ha_version_unknown" || got.Config.SourceStatus != SourceUnavailable || got.Config.Exists != nil || got.RuntimeLoadProof.Verified || got.MappingSource != "" || f.configReads.Load() != 0 || f.runtimeReads.Load() != 0 {
				t.Fatalf("unknown profile gained version-specific proof %+v %v", got, err)
			}
			if tc.selected {
				if f.stateReads.Load() != 1 || f.registryReads.Load() != 1 || got.State != "on" || got.StateStatus != SourceAvailable {
					t.Fatalf("verified ordinary state lost %+v; states=%d registry=%d", got, f.stateReads.Load(), f.registryReads.Load())
				}
			} else if f.stateReads.Load() != 0 || got.StateStatus != SourceUnavailable {
				t.Fatalf("unverified target caused %d state reads", f.stateReads.Load())
			}
		})
	}
}
