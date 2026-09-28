package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

func TestManagementAndBrowserRoutesRejectAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, key := range []string{"test-key", ""} {
		for _, path := range []string{"/internal/vault/search", "/internal/smarthome/suggestions/x/confirm", "/sessions/webchat/alice/messages", "/channels/webchat/ws", "/v1/chat/completions"} {
			t.Run(key+path, func(t *testing.T) {
				r := gin.New()
				r.Use(AuthMiddleware(key))
				r.GET(path, func(c *gin.Context) { c.Status(204) })
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous status=%d, want 401", w.Code)
				}
			})
		}
	}
}

func auditServer(t *testing.T, step chain.Step) (*Server, *httptest.Server) {
	t.Helper()
	log := zap.NewNop()
	cr := chain.NewChainRouter()
	cr.Register("chat", chain.NewChain("chat", "", step))
	mgr := core.NewSessionManager(core.DefaultSessionConfig(), log)
	s := &Server{cfg: &core.Config{Server: core.ServerConfig{InternalKey: "test-key", ChainTimeout: time.Second}, Inference: core.InferenceConfig{Models: core.ModelsConfig{Local: "local-test", Vision: "vision-test"}}}, logger: log, filterChain: filter.NewChain(), sessionMgr: mgr, sessionStore: core.NewSessionStore(mgr, t.TempDir(), nil, log), chainRouter: cr, chainExecutor: chain.NewChainExecutor(log, cr), router: core.NewModelRouter("local-test", "vision-test")}
	s.setupRoutes()
	ts := httptest.NewServer(s.engine)
	t.Cleanup(ts.Close)
	return s, ts
}
func browserCookie(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	resp, err := http.Post(ts.URL+"/auth/browser", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("browser identity status=%d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.HttpOnly && c.SameSite == http.SameSiteStrictMode {
			return c
		}
	}
	t.Fatal("no HttpOnly SameSite=Strict identity cookie")
	return nil
}
func dialBrowser(t *testing.T, ts *httptest.Server, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	h.Set("Cookie", cookie.String())
	h.Set("Origin", ts.URL)
	c, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1)+"/channels/webchat/ws", h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	return c
}
func TestBrowserIdentityCannotManageOrReadOtherConversations(t *testing.T) {
	_, ts := auditServer(t, chain.NewFuncStep("answer", func(_ context.Context, s *chain.ChainState) error {
		if s.Vault != "agent" {
			return errors.New("external vault escaped")
		}
		s.FinalAnswer = "token=fake-value"
		return nil
	}))
	alice := browserCookie(t, ts)
	bob := browserCookie(t, ts)
	req, _ := http.NewRequest("GET", ts.URL+"/internal/vault/search?q=x", nil)
	req.AddCookie(alice)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("browser has management access: %d", resp.StatusCode)
	}
	conn := dialBrowser(t, ts, alice)
	conn.WriteJSON(clientMessage{Content: "hello"})
	var identity, answer serverMessage
	if err := conn.ReadJSON(&identity); err != nil {
		t.Fatal(err)
	}
	if err := conn.ReadJSON(&answer); err != nil {
		t.Fatal(err)
	}
	if answer.Type != "response" || strings.Contains(answer.Content, "fake-value") {
		t.Fatalf("unsafe reply: %+v", answer)
	}
	conn.Close()
	req, _ = http.NewRequest("GET", ts.URL+"/sessions/webchat/"+identity.SessionID+"/messages", nil)
	req.AddCookie(bob)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("foreign history status=%d", resp.StatusCode)
	}
	other := dialBrowser(t, ts, bob)
	other.WriteJSON(clientMessage{SessionID: identity.SessionID, Content: "steal"})
	var denied serverMessage
	other.ReadJSON(&denied)
	if denied.Type != "error" {
		t.Fatalf("foreign session resumed: %+v", denied)
	}
	resumed := dialBrowser(t, ts, alice)
	resumed.WriteJSON(clientMessage{SessionID: identity.SessionID, Content: "again"})
	resumed.ReadJSON(&identity)
	resumed.ReadJSON(&answer)
	req, _ = http.NewRequest("GET", ts.URL+"/sessions/webchat/"+identity.SessionID+"/messages", nil)
	req.AddCookie(alice)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var history struct{ Messages []core.Message }
	json.NewDecoder(resp.Body).Decode(&history)
	if len(history.Messages) != 4 {
		t.Fatalf("reconnect lost history: %d messages", len(history.Messages))
	}
}
func TestBrowserBootstrapRejectsCrossOrigin(t *testing.T) {
	_, ts := auditServer(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error { return nil }))
	req, _ := http.NewRequest("POST", ts.URL+"/auth/browser", nil)
	req.Header.Set("Origin", "https://attacker.invalid")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("cross-origin bootstrap=%d", r.StatusCode)
	}
}
func TestRESTPreservesFullRequestAndPropagatesFailure(t *testing.T) {
	_, ts := auditServer(t, chain.NewFuncStep("capture", func(_ context.Context, s *chain.ChainState) error {
		req, ok := s.Data["chat_request"].(map[string]interface{})
		if !ok {
			return errors.New("request missing")
		}
		messages, _ := req["messages"].([]interface{})
		if len(messages) != 3 || req["model"] != "named-local" || req["temperature"] != float64(0) || s.Metadata["sensitive"] != "true" {
			return errors.New("request fields lost")
		}
		s.FinalAnswer = "preserved"
		return nil
	}))
	body := `{"model":"named-local","messages":[{"role":"system","content":"system"},{"role":"assistant","content":"previous"},{"role":"user","content":"next"}],"metadata":{"sensitive":true},"temperature":0}`
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "preserved") {
		t.Fatalf("request contract status=%d body=%s", resp.StatusCode, b)
	}
	_, failTS := auditServer(t, chain.NewFuncStep("fail", func(context.Context, *chain.ChainState) error { return errors.New("synthetic failure") }))
	req, _ = http.NewRequest("POST", failTS.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("chain failure status=%d", resp.StatusCode)
	}
}

func TestRESTStreamContractAndUnsupportedFields(t *testing.T) {
	_, ts := auditServer(t, chain.NewFuncStep("answer", func(_ context.Context, s *chain.ChainState) error { s.FinalAnswer = "complete"; return nil }))
	for _, tc := range []struct {
		extra  string
		status int
		stream bool
	}{{`,"stream":true`, 200, true}, {`,"tools":[{"type":"function"}]`, 400, false}, {`,"unexpected_option":true`, 400, false}} {
		body := `{"model":"auto","messages":[{"role":"user","content":"hello"}]` + tc.extra + `}`
		req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-key")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != tc.status {
			t.Errorf("%s status=%d", tc.extra, r.StatusCode)
		}
		if tc.stream && (!strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(b), "data: [DONE]") || !strings.Contains(string(b), `"delta"`)) {
			t.Errorf("not SSE: headers=%v body=%s", r.Header, b)
		}
	}
}

func TestSmartHomeHandlersPreserveRuleStateErrors(t *testing.T) {
	s, ts := auditServer(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error { return nil }))
	sh, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: "http://127.0.0.1:1", PollIntervalSec: 3600, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	s.smartHome = sh
	if err := sh.GetStore().SaveSuggestions([]smarthome.RuleSuggestion{{ID: "unsupported", Status: "pending", Trigger: "sunset"}, {ID: "ignored", Status: "ignored"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.IgnoreSuggestion("ignored"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want int
	}{{"unsupported", 422}, {"ignored", 409}, {"missing", 404}} {
		req, _ := http.NewRequest("POST", ts.URL+"/internal/smarthome/suggestions/"+tc.id+"/confirm", nil)
		req.Header.Set("Authorization", "Bearer test-key")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Errorf("%s=%d want %d", tc.id, r.StatusCode, tc.want)
		}
	}
}

func TestWikiIngestReportsStepFailure(t *testing.T) {
	s, ts := auditServer(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error { return nil }))
	s.chainRouter.Register("wiki-ingest", chain.NewChain("wiki-ingest", "", chain.NewFuncStep("write", func(context.Context, *chain.ChainState) error { return errors.New("synthetic write failure") })))
	req, _ := http.NewRequest("POST", ts.URL+"/internal/wiki/ingest", strings.NewReader(`{"content":"example"}`))
	req.Header.Set("Authorization", "Bearer test-key")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 500 {
		t.Fatalf("ingest failure was hidden: %d", r.StatusCode)
	}
}

func TestWebChatRoundLimitRotatesAfterACompleteExchange(t *testing.T) {
	s, ts := auditServer(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error { state.FinalAnswer = "answer"; return nil }))
	cfg := core.DefaultSessionConfig()
	cfg.MaxRounds, cfg.MaxSessions = 1, 1
	s.sessionMgr = core.NewSessionManager(cfg, zap.NewNop())
	s.sessionStore = core.NewSessionStore(s.sessionMgr, t.TempDir(), nil, zap.NewNop())
	conn := dialBrowser(t, ts, browserCookie(t, ts))
	var previous string
	for _, question := range []string{"first", "second"} {
		if err := conn.WriteJSON(clientMessage{Content: question}); err != nil {
			t.Fatal(err)
		}
		var identity, response serverMessage
		if err := conn.ReadJSON(&identity); err != nil {
			t.Fatal(err)
		}
		if identity.Type != "session" || identity.SessionID == previous {
			t.Fatalf("missing fresh session: %+v", identity)
		}
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if response.Type != "response" {
			t.Fatalf("final round lost its response: %+v", response)
		}
		if previous != "" {
			if msgs := s.sessionStore.GetMessages("webchat", previous); len(msgs) != 2 {
				t.Fatalf("previous exchange was polluted: %d", len(msgs))
			}
		}
		previous = identity.SessionID
	}
}

func TestWebSocketClosesWhenServerContextIsCancelled(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			s, _ := auditServer(t, chain.NewFuncStep("wait", func(ctx context.Context, _ *chain.ChainState) error {
				close(started)
				<-ctx.Done()
				close(stopped)
				return ctx.Err()
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ts := httptest.NewUnstartedServer(s.engine)
			ts.Config.BaseContext = func(net.Listener) context.Context { return ctx }
			ts.Start()
			defer ts.Close()
			conn := dialBrowser(t, ts, browserCookie(t, ts))
			if active {
				conn.WriteJSON(clientMessage{Content: "wait"})
				var identity serverMessage
				if err := conn.ReadJSON(&identity); err != nil {
					t.Fatal(err)
				}
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("chain did not start")
				}
			}
			cancel()
			conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			_, _, err := conn.ReadMessage()
			if err == nil {
				_, _, err = conn.ReadMessage()
			}
			var timeout net.Error
			if err == nil || errors.As(err, &timeout) && timeout.Timeout() {
				t.Fatalf("WebSocket stayed open after cancellation: %v", err)
			}
			if active {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("inflight WebSocket chain was not cancelled")
				}
			}
		})
	}
}
