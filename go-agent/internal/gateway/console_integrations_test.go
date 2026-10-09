package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

// Status checks must use the real tags client against a temporary HTTP service;
// POST chat/embed would be an observable forbidden side effect in this fixture.
func TestFamilyIntegrationsUsesCachedTagsAndReportsMissingModels(t *testing.T) {
	s, _, haReads := areasTestServer(t)
	calls := &atomic.Int32{}
	offline := &atomic.Bool{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/tags" {
			t.Errorf("health invoked model inference: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		if offline.Load() {
			http.Error(w, "MODEL_UPSTREAM_SECRET_321", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"name":"fixture-chat:latest"},{"name":"fixture-embed:latest"},{"name":"UPSTREAM_MODEL_SECRET_321"}]}`)
	}))
	defer model.Close()
	s.infer = inference.NewOllamaClient(model.URL, time.Second)
	s.cfg.Inference.Models.Local = "fixture-chat"
	s.cfg.Inference.Models.Embedding = "fixture-embed"
	s.cfg.Inference.Models.Vision = "missing-vision"
	_, cookie := consoleMemberLogin(t, s, "Reader")
	for i := 0; i < 2; i++ {
		w := consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", cookie, "", "", "")
		body := consoleJSON(t, w, 200)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Error("private service status cached by browser")
		}
		ollama := body["ollama"].(map[string]any)
		if ollama["connection"] != "connected" || ollama["checked_at"] == nil || ollama["error_code"] != "model_missing" {
			t.Errorf("missing models presented as verified: %v", ollama)
		}
		models := ollama["models"].([]any)
		want := map[string]bool{"fixture-chat": true, "fixture-embed": true, "missing-vision": false}
		if len(models) != 3 {
			t.Errorf("unconfigured model leaked: %v", models)
		}
		for _, raw := range models {
			m := raw.(map[string]any)
			if m["available"] != want[m["name"].(string)] {
				t.Errorf("incorrect configured model availability: %v", m)
			}
		}
		if body["ha"].(map[string]any)["connection"] != "connected" {
			t.Errorf("healthy HA misreported: %v", body)
		}
		for _, secret := range []string{model.URL, "UPSTREAM_MODEL_SECRET_321", "HA_TOKEN_SECRET_531", "MODEL_UPSTREAM_SECRET_321"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Errorf("private integration details leaked: %s", w.Body.String())
			}
		}
	}
	if calls.Load() != 1 || haReads.Load() != 2 {
		t.Errorf("status does not reuse service caches: models=%d ha=%d", calls.Load(), haReads.Load())
	}
}

func TestFamilyIntegrationsOfflineAndAuthentication(t *testing.T) {
	s, fail, haReads := areasTestServer(t)
	fail.Store(true)
	calls := &atomic.Int32{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/tags" {
			t.Errorf("unexpected inference health call %s %s", r.Method, r.URL.Path)
		}
		http.Error(w, "MODEL_TOKEN_SECRET_321 http://secret-host", 503)
	}))
	defer model.Close()
	s.infer = inference.NewOllamaClient(model.URL, time.Second)
	s.cfg.Inference.Models.Local = "fixture-chat"
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", nil, "", "", ""), 401)
	_, password, err := s.consoleStore.CreateMember("Restricted", "Restricted")
	if err != nil {
		t.Fatal(err)
	}
	restricted, _, _ := consoleLogin(t, s, "Restricted", password, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", restricted, "", "", ""), 403)
	if calls.Load() != 0 || haReads.Load() != 0 {
		t.Fatal("unauthorized integration request probed upstream")
	}
	_, cookie := consoleMemberLogin(t, s, "Reader")
	w := consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", cookie, "", "", "")
	body := consoleJSON(t, w, 200)
	ha, ollama := body["ha"].(map[string]any), body["ollama"].(map[string]any)
	if ha["connection"] != "unavailable" || ha["freshness"] != "unknown" || ha["observed_at"] != nil || ha["error_code"] != "ha_invalid_response" {
		t.Errorf("failed HA status invented observation: %v", ha)
	}
	if ollama["connection"] != "unavailable" || ollama["error_code"] != "ollama_unavailable" || ollama["models"].([]any)[0].(map[string]any)["available"] != nil {
		t.Errorf("offline model misreported: %v", ollama)
	}
	if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "secret-host") || strings.Contains(w.Body.String(), model.URL) {
		t.Fatalf("integration error leaks upstream: %s", w.Body.String())
	}
}

// The cache's clock is controllable so we test the 30s boundary without sleeps.
type statusProbeClient struct {
	calls     atomic.Int32
	forbidden atomic.Int32
	list      func(context.Context) ([]inference.ModelInfo, error)
}

func (c *statusProbeClient) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	c.calls.Add(1)
	return c.list(ctx)
}
func (c *statusProbeClient) Chat(context.Context, json.RawMessage) (json.RawMessage, error) {
	c.forbidden.Add(1)
	return nil, errors.New("forbidden chat health")
}
func (c *statusProbeClient) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	c.forbidden.Add(1)
	return nil, errors.New("forbidden embed health")
}

func TestOllamaStatusCacheRefreshesAtThirtySecondsAndDoesNotReuseOfflineAvailability(t *testing.T) {
	var tick atomic.Int64
	tick.Store(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC).UnixNano())
	cache := newOllamaStatusCache(func() time.Time { return time.Unix(0, tick.Load()) })
	defer cache.Close()
	client := &statusProbeClient{}
	client.list = func(ctx context.Context) ([]inference.ModelInfo, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
			return nil, fmt.Errorf("health probe must receive10s total deadline")
		}
		if client.calls.Load() > 1 {
			return nil, errors.New("MODEL_TOKEN_SECRET_321")
		}
		return []inference.ModelInfo{{ID: "bge-m3:latest"}}, nil
	}
	health, err := cache.Get(context.Background(), client)
	if err != nil || health.connection != "connected" || !health.installed["bge-m3:latest"] {
		t.Fatalf("initial healthy tags unavailable: %+v %v", health, err)
	}
	tick.Add((30*time.Second - time.Nanosecond).Nanoseconds())
	if _, err := cache.Get(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("cache refresh before30s: %d", client.calls.Load())
	}
	tick.Add(1)
	health, err = cache.Get(context.Background(), client)
	if err != nil || client.calls.Load() != 2 || health.connection != "unavailable" || health.installed["bge-m3:latest"] || health.errorCode == nil || *health.errorCode != "ollama_unavailable" {
		t.Fatalf("failed refresh retains stale model availability: %+v calls=%d err=%v", health, client.calls.Load(), err)
	}
	if _, err := cache.Get(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if client.calls.Load() != 2 || client.forbidden.Load() != 0 {
		t.Fatalf("failed check not cached or health called inference: tags=%d writes=%d", client.calls.Load(), client.forbidden.Load())
	}
}

func TestOllamaStatusSharedProbeSurvivesRequestCancellation(t *testing.T) {
	cache := newOllamaStatusCache(time.Now)
	defer cache.Close()
	started, release := make(chan struct{}), make(chan struct{})
	client := &statusProbeClient{list: func(ctx context.Context) ([]inference.ModelInfo, error) {
		close(started)
		select {
		case <-release:
			return []inference.ModelInfo{{ID: "gemma4:12b"}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := cache.Get(ctx, client); first <- err }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation ignored: %v", err)
	}
	second := make(chan ollamaHealth, 1)
	go func() { health, _ := cache.Get(context.Background(), client); second <- health }()
	close(release)
	health := <-second
	if health.connection != "connected" || !health.installed["gemma4:12b"] || client.calls.Load() != 1 {
		t.Fatalf("one request cancelled shared health refresh: %+v calls=%d", health, client.calls.Load())
	}
}

func TestOllamaStatusCloseCancelsActiveProbe(t *testing.T) {
	cache := newOllamaStatusCache(time.Now)
	started := make(chan struct{})
	finished := make(chan struct{})
	client := &statusProbeClient{list: func(ctx context.Context) ([]inference.ModelInfo, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	go func() { cache.Get(context.Background(), client); close(finished) }()
	<-started
	cache.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("lifecycle close did not join active model probe")
	}
}

func TestFamilyIntegrationRecognizesImplicitLatestButKeepsExplicitTagsDistinct(t *testing.T) {
	s, _, _ := areasTestServer(t)
	s.cfg.Inference.Models.Local = "gemma4:12b"
	s.cfg.Inference.Models.Vision = "llava:13b"
	s.cfg.Inference.Models.Embedding = "bge-m3"
	s.infer = &statusProbeClient{list: func(context.Context) ([]inference.ModelInfo, error) {
		return []inference.ModelInfo{{ID: "gemma4:12b"}, {ID: "llava:7b"}, {ID: "bge-m3:latest"}}, nil
	}}
	_, cookie := consoleMemberLogin(t, s, "Reader")
	body := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", cookie, "", "", ""), 200)
	ollama := body["ollama"].(map[string]any)
	got := map[string]bool{}
	for _, raw := range ollama["models"].([]any) {
		model := raw.(map[string]any)
		got[model["name"].(string)] = model["available"].(bool)
	}
	if got["bge-m3"] != true || got["gemma4:12b"] != true || got["llava:13b"] != false || ollama["error_code"] != "model_missing" {
		t.Fatalf("implicit latest or explicit tag availability incorrect: %v", ollama)
	}
}

// Delay entering Catalog.Get to deterministically model ordinary goroutine
// scheduling after the handler starts. Its shared upstream still gets its real
// ten-second deadline; no cache internals or observation timestamps are edited.
type delayedIntegrationCatalog struct{ manager *smarthome.Manager }

func (p delayedIntegrationCatalog) Catalog(ctx context.Context) (smarthome.CatalogSnapshot, error) {
	select {
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		return smarthome.CatalogSnapshot{}, ctx.Err()
	}
	return p.manager.Catalog(ctx)
}

func TestFamilyIntegrationsRetainsRealCatalogAfterExpiredRefreshTimeout(t *testing.T) {
	var stall atomic.Bool
	blocked := make(chan struct{}, 4)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected HA action %s", r.Method)
			http.Error(w, "unexpected", 400)
			return
		}
		switch r.URL.Path {
		case "/api/states":
			if stall.Load() {
				blocked <- struct{}{}
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		case "/api/websocket":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.WriteJSON(map[string]string{"type": "auth_required", "ha_version": "fixture"})
			var auth map[string]string
			if err = conn.ReadJSON(&auth); err != nil {
				return
			}
			if auth["type"] != "auth" || auth["access_token"] != "temporary-fixture-token" {
				t.Error("invalid fixture auth")
				return
			}
			conn.WriteJSON(map[string]string{"type": "auth_ok", "ha_version": "fixture"})
			for i := 0; i < 4; i++ {
				var cmd struct {
					ID   int    `json:"id"`
					Type string `json:"type"`
				}
				if conn.ReadJSON(&cmd) != nil {
					return
				}
				if !strings.HasPrefix(cmd.Type, "config/") || !strings.HasSuffix(cmd.Type, "_registry/list") {
					t.Errorf("nonregistry HA command %s", cmd.Type)
					return
				}
				conn.WriteJSON(map[string]any{"id": cmd.ID, "type": "result", "success": true, "result": []any{}})
			}
		default:
			t.Errorf("unexpected HA path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer ha.Close()
	manager, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "temporary-fixture-token", AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	defer s.ollamaStatus.Close()
	s.areaCatalog = delayedIntegrationCatalog{manager: manager}
	s.infer = &statusProbeClient{list: func(context.Context) ([]inference.ModelInfo, error) {
		return []inference.ModelInfo{{ID: "gemma4:12b"}}, nil
	}}
	_, cookie := consoleMemberLogin(t, s, "DeadlineReader")
	initial := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", cookie, "", "", ""), 200)["ha"].(map[string]any)
	if initial["freshness"] != "fresh" || initial["observed_at"] == nil || initial["last_success_at"] == nil {
		t.Fatalf("real Catalog never published initial success: %v", initial)
	}
	// Catalog's public service uses wall time; let its actual thirty-second cache
	// expire rather than inventing stale snapshot metadata in the gateway.
	observed, err := time.Parse(time.RFC3339Nano, initial["observed_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	wait := time.Until(observed.Add(30*time.Second + 100*time.Millisecond))
	if wait > 0 {
		time.Sleep(wait)
	}
	stall.Store(true)
	statusDone := make(chan *httptest.ResponseRecorder, 1)
	began := time.Now()
	go func() {
		statusDone <- consoleRequest(s, "GET", consolePrefix+"/integrations/status", "", cookie, "", "", "")
	}()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("expired refresh did not reach actual HA fixture")
	}
	areasDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { areasDone <- consoleRequest(s, "GET", consolePrefix+"/areas", "", cookie, "", "", "") }()
	status := consoleJSON(t, <-statusDone, 200)["ha"].(map[string]any)
	elapsed := time.Since(began)
	areas := consoleJSON(t, <-areasDone, 200)["meta"].(map[string]any)
	for name, meta := range map[string]map[string]any{"status": status, "areas": areas} {
		if meta["freshness"] != "stale" || meta["connection"] != "unavailable" || meta["observed_at"] != initial["observed_at"] || meta["last_success_at"] != initial["last_success_at"] || meta["last_attempt_at"] == initial["last_attempt_at"] {
			t.Errorf("%s lost actual successful Catalog observation after shared timeout: %v; initial=%v", name, meta, initial)
		}
	}
	if elapsed < 9*time.Second || elapsed > 11*time.Second {
		t.Errorf("parallel bounded status elapsed=%v, want about10s", elapsed)
	}
	t.Logf("Real Catalog success aged >30s; blocked HA shared timeout; status/areas observation comparison completed; status duration=%v", elapsed)
	// The incoming request must still cancel waiting promptly without publishing
	// stale business data or cancelling the lifecycle-bound shared refresh.
	req := httptest.NewRequest("GET", consolePrefix+"/integrations/status", nil)
	req.AddCookie(cookie)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	cancelled := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); s.engine.ServeHTTP(w, req); cancelled <- w }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("next shared refresh did not start")
	}
	cancel()
	select {
	case w := <-cancelled:
		if w.Code == 200 || strings.Contains(w.Body.String(), "observed_at") {
			t.Errorf("cancelled read published business JSON: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(time.Second):
		t.Error("caller cancellation did not stop waiting")
	}
}
