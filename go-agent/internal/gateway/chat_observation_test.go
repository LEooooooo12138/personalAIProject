package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
)

type observationIntent func(context.Context, string, chain.HACandidates) (chain.HAIntent, error)

func (f observationIntent) Parse(ctx context.Context, text string, c chain.HACandidates) (chain.HAIntent, error) {
	return f(ctx, text, c)
}

// Catch stages emitted speculatively, missing identity, or generation before classification.
func TestChatStagesFollowActualWork(t *testing.T) {
	classify, generate := make(chan struct{}), make(chan struct{})
	defer close(classify)
	defer close(generate)
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
		select {
		case <-generate:
		case <-ctx.Done():
			return ctx.Err()
		}
		state.FinalAnswer = "safe answer"
		return nil
	}))
	attachRejectedControlIntent(t, s, nil)
	s.controlChat = core.NewControlChat(s.control, observationIntent(func(ctx context.Context, _ string, _ chain.HACandidates) (chain.HAIntent, error) {
		select {
		case <-classify:
		case <-ctx.Done():
			return chain.HAIntent{}, ctx.Err()
		}
		return chain.HAIntent{Kind: "chat"}, nil
	}))
	_, cookie := consoleMemberLogin(t, s, "alice")
	conn := dialConsole(t, ts, cookie)
	if err := conn.WriteJSON(clientMessage{RequestID: "stage-request", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	var stages []string
	sid := ""
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		switch frame["type"] {
		case "session":
			sid, _ = frame["session_id"].(string)
		case "chat_stage":
			stage, _ := frame["stage"].(string)
			stages = append(stages, stage)
			if frame["session_id"] != sid || sid == "" || frame["request_id"] != "stage-request" || len(frame) != 4 {
				t.Fatalf("invalid stage frame: %v", frame)
			}
			if stage == "classifying" {
				classify <- struct{}{}
			}
			if stage == "generating" {
				generate <- struct{}{}
			}
		case "response":
			if !reflect.DeepEqual(stages, []string{"queued", "preparing", "classifying", "generating", "saving"}) {
				t.Fatalf("stages=%v", stages)
			}
			return
		default:
			t.Fatalf("unexpected frame: %v", frame)
		}
	}
}

// Catch acquisition using socket context instead of the whole-round deadline.
func TestChatRoundBudgetIncludesQueue(t *testing.T) {
	var calls atomic.Int32
	s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
		calls.Add(1)
		state.FinalAnswer = "late"
		return nil
	}))
	logCore, logs := observer.New(zap.InfoLevel)
	s.logger = zap.New(logCore)
	const sid = "queue-budget"
	if _, err := s.sessionStore.BrowserSession("console", sid, "owner", true); err != nil {
		t.Fatal(err)
	}
	release, err := s.sessionMgr.AcquireTurn(context.Background(), "console", sid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	finished := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		conn, err := u.Upgrade(writer, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(finished)
		w := &wsConn{conn: conn, ctx: r.Context(), owner: "owner", sessionID: sid, requestID: "budget", logger: s.logger, sessionMgr: s.sessionMgr, sessionStore: s.sessionStore, chainExecutor: s.chainExecutor, chainRouter: s.chainRouter, filter: s.filterChain, options: webChatOptions{ChannelID: "console", VaultName: "agent"}}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Millisecond)
		defer cancel()
		w.handleChatContext(ctx, "hello")
	}))
	defer ts.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-finished:
	case <-time.After(250 * time.Millisecond):
		release()
		t.Fatal("round deadline did not bound queue")
	}
	if calls.Load() != 0 {
		t.Fatal("cancelled queue started inference")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var frame map[string]any
	for {
		frame = nil
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame["type"] != "chat_stage" {
			break
		}
	}
	if frame["code"] != "inference_timeout" {
		t.Fatalf("missing terminal timeout: %v", frame)
	}
	entries := logs.FilterMessage("chat round timing").All()
	if len(entries) != 1 || entries[0].ContextMap()["publish_ms"] == nil {
		t.Fatalf("terminal timeout publication was not timed: %v", entries)
	}
}

// Catch publishing a late stage/answer after the real login is revoked.
func TestChatStageRevocationSuppressesPublish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
		t.Error("revoked classifier started generation")
		state.FinalAnswer = "late"
		return nil
	}))
	attachRejectedControlIntent(t, s, nil)
	s.controlChat = core.NewControlChat(s.control, observationIntent(func(ctx context.Context, _ string, _ chain.HACandidates) (chain.HAIntent, error) {
		close(entered)
		<-release
		return chain.HAIntent{Kind: "chat"}, nil
	}))
	_, cookie := consoleMemberLogin(t, s, "alice")
	conn := dialConsole(t, ts, cookie)
	conn.WriteJSON(clientMessage{RequestID: "revoke", Content: "hello"})
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame["stage"] == "classifying" {
			break
		}
	}
	<-entered
	if err := s.consoleStore.Logout(cookie.Value); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	var frame json.RawMessage
	if err := conn.ReadJSON(&frame); err == nil {
		t.Fatalf("published after revocation: %s", frame)
	}
}

// readChatMessage consumes only valid nonterminal progress frames. Existing
// terminal/identity assertions still see every other frame, including errors.
// Revocation tests drain expected progress BEFORE revoking, then use raw reads.
func readChatMessage(conn *websocket.Conn, target any) error {
	for {
		var raw json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			return err
		}
		var header struct {
			Type      string `json:"type"`
			Stage     string `json:"stage"`
			SessionID string `json:"session_id"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return err
		}
		if header.Type != "chat_stage" {
			return json.Unmarshal(raw, target)
		}
		if header.SessionID == "" || header.RequestID == "" {
			return fmt.Errorf("stage missing identity: %s", raw)
		}
		switch header.Stage {
		case "queued", "preparing", "classifying", "generating", "saving":
		default:
			return fmt.Errorf("unknown stage: %s", raw)
		}
	}
}
func readStagesThrough(t *testing.T, conn *websocket.Conn, stage string) {
	t.Helper()
	for {
		var frame serverMessage
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != "chat_stage" {
			t.Fatalf("expected progress before %s, got %+v", stage, frame)
		}
		if string(frame.Stage) == stage {
			return
		}
	}
}

// Runs the real shared chat path with a short caller-owned budget and a real
// socket. The only replaced work is the externally supplied model step/parser.
func runObservedRound(t *testing.T, s *Server, channel string, budget time.Duration, configure func(*wsConn)) []serverMessage {
	t.Helper()
	const sid = "observed-round"
	if _, err := s.sessionStore.BrowserSession(channel, sid, "owner", true); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		defer close(finished)
		u := websocket.Upgrader{}
		conn, err := u.Upgrade(writer, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		w := &wsConn{conn: conn, ctx: r.Context(), owner: "owner", sessionID: sid, requestID: "PRIVATE_CLIENT_REQUEST", logger: s.logger, sessionMgr: s.sessionMgr, sessionStore: s.sessionStore, chainExecutor: s.chainExecutor, chainRouter: s.chainRouter, filter: s.filterChain, options: webChatOptions{ChannelID: channel, VaultName: "agent", ControlChat: s.controlChat}}
		if configure != nil {
			configure(w)
		}
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		w.handleChatContext(ctx, "PRIVATE_USER_TEXT")
	}))
	defer ts.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frames []serverMessage
	for {
		var frame serverMessage
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		frames = append(frames, frame)
	}
	<-finished
	return frames
}
func TestChatStagesClassifierDeadlineStopsGeneration(t *testing.T) {
	var calls atomic.Int32
	s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error { calls.Add(1); return nil }))
	attachRejectedControlIntent(t, s, nil)
	s.controlChat = core.NewControlChat(s.control, chain.NewHAIntentParser(deadlineIntentInference{}, "local"))
	frames := runObservedRound(t, s, "console", 40*time.Millisecond, nil)
	var stages []string
	for _, frame := range frames {
		if frame.Type == "chat_stage" {
			stages = append(stages, string(frame.Stage))
		}
	}
	if !reflect.DeepEqual(stages, []string{"queued", "preparing", "classifying"}) || calls.Load() != 0 {
		t.Fatalf("deadline entered generation: %v calls=%d", stages, calls.Load())
	}
	last := frames[len(frames)-1]
	if last.Type != "error" || last.Code != "inference_timeout" || last.Outcome != "failed" {
		t.Fatalf("timeout terminal=%+v", last)
	}
}
func TestChatStagesDeviceTerminalSkipsOrdinaryWork(t *testing.T) {
	s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error {
		t.Error("device clarification entered ordinary generation")
		return nil
	}))
	attachRejectedControlIntent(t, s, chain.ErrHAIntentInvalid)
	frames := runObservedRound(t, s, "console", time.Second, nil)
	var stages []string
	for _, frame := range frames {
		if frame.Type == "chat_stage" {
			stages = append(stages, string(frame.Stage))
		}
	}
	if !reflect.DeepEqual(stages, []string{"queued", "preparing", "classifying"}) {
		t.Fatalf("device fabricated stages: %v", stages)
	}
	if frames[len(frames)-1].Type != "response" {
		t.Fatalf("device terminal missing: %+v", frames)
	}
}
func TestChatStagesFilterAndPersistFailureNeverPublishSuccess(t *testing.T) {
	for _, failure := range []string{"filter", "persist"} {
		t.Run(failure, func(t *testing.T) {
			channel := "console"
			if failure == "filter" {
				channel = "wechat"
			}
			var s *Server
			s, _ = consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
				state.FinalAnswer = "<br>" // Existing WeChat platform filter removes all markup.
				if failure == "persist" {
					path := filepath.Join(s.cfg.Vaults.Agent, "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte(channel+":observed-round"))))
					if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
						return err
					}
					if err := os.Mkdir(path, 0700); err != nil {
						return err
					}
				}
				return nil
			}))
			frames := runObservedRound(t, s, channel, time.Second, nil)
			for _, frame := range frames {
				if frame.Type == "response" {
					t.Fatalf("%s failure published success: %+v", failure, frame)
				}
			}
			code := "chat_filter_empty"
			if failure == "persist" {
				code = "chat_publish_failed"
			}
			if len(frames) == 0 || frames[len(frames)-1].Code != code {
				t.Fatalf("%s failure terminal: %+v", failure, frames)
			}
		})
	}
}
func TestChatObservationCompletesEachTimingOnce(t *testing.T) {
	logCore, logs := observer.New(zap.InfoLevel)
	ctx, o := startChatObservation(context.Background(), zap.New(logCore))
	done := measureChat(ctx, "queue")
	done()
	first := o.timings["queue"]
	time.Sleep(time.Millisecond)
	done()
	if o.timings["queue"] != first {
		t.Fatal("duplicate timer completion changed measurement")
	}
	o.finish(ctx)
	o.finish(ctx)
	entries := logs.FilterMessage("chat round timing").All()
	if len(entries) != 1 {
		t.Fatalf("round completed %d times", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["queue_ms"] == nil || fields["generate_ms"] != nil || fields["terminal_code"] != "incomplete" {
		t.Fatalf("missing vs observed timing: %v", fields)
	}
}
func TestChatObservationCorrelatesSafeActualProviderCalls(t *testing.T) {
	logCore, logs := observer.New(zap.InfoLevel)
	original := http.DefaultTransport
	var calls atomic.Int32
	http.DefaultTransport = consoleModelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		payload := `{"choices":[{"message":{"content":"safe answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})
	defer func() { http.DefaultTransport = original }()
	client := inference.NewAssistantClient(nil)
	call := func(ctx context.Context) error {
		_, err := client.Chat(inference.WithAssistantRoute(ctx, "cloud", "PRIVATE_KEY"), json.RawMessage(`{"messages":[{"role":"user","content":"PRIVATE_PROMPT"}]}`))
		return err
	}
	s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
		state.FinalAnswer = "safe answer"
		return call(ctx)
	}))
	s.logger = zap.New(logCore)
	attachRejectedControlIntent(t, s, nil)
	s.controlChat = core.NewControlChat(s.control, observationIntent(func(ctx context.Context, _ string, _ chain.HACandidates) (chain.HAIntent, error) {
		err := call(ctx)
		return chain.HAIntent{Kind: "chat"}, err
	}))
	frames := runObservedRound(t, s, "console", time.Second, nil)
	if frames[len(frames)-1].Type != "response" {
		t.Fatalf("no response: %+v", frames)
	}
	rounds := logs.FilterMessage("chat round timing").All()
	providers := logs.FilterMessage("chat provider timing").All()
	if len(rounds) != 1 || len(providers) != 2 || calls.Load() != 2 {
		t.Fatalf("rounds=%d provider records=%d calls=%d", len(rounds), len(providers), calls.Load())
	}
	summary := rounds[0].ContextMap()
	trace := summary["trace_id"]
	if trace == "" || summary["terminal_code"] != "success" || summary["provider_calls"] != int64(2) {
		t.Fatalf("round=%v", summary)
	}
	for i, entry := range providers {
		fields := entry.ContextMap()
		if fields["trace_id"] != trace || fields["call_sequence"] != int64(i+1) || fields["phase"] != []string{"classify", "generate"}[i] || fields["total_ms"] == nil || fields["dns_ms"] != nil {
			t.Fatalf("provider=%v", fields)
		}
	}
	for _, entry := range logs.All() {
		if strings.Contains(fmt.Sprint(entry.ContextMap()), "PRIVATE_") {
			t.Fatalf("observation leaked content: %v", entry.ContextMap())
		}
	}
}

// A still-live socket context is not authorization. This catches a stage writer
// that checks ctx.Err() but bypasses the real WithSession revocation guard.
func TestChatStageRevocationWithLiveSocketContext(t *testing.T) {
	s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(context.Context, *chain.ChainState) error {
		t.Error("revoked login entered generation")
		return nil
	}))
	_, cookie := consoleMemberLogin(t, s, "alice")
	attachRejectedControlIntent(t, s, nil)
	s.controlChat = core.NewControlChat(s.control, observationIntent(func(ctx context.Context, _ string, _ chain.HACandidates) (chain.HAIntent, error) {
		if err := s.consoleStore.Logout(cookie.Value); err != nil {
			t.Error(err)
		}
		if ctx.Err() != nil {
			t.Error("fixture lost live socket context")
		}
		return chain.HAIntent{Kind: "chat"}, nil
	}))
	frames := runObservedRound(t, s, "console", time.Second, func(w *wsConn) {
		w.options.WithSession = func(ctx context.Context, fn func(context.Context) error) error {
			return s.consoleStore.WithSession(ctx, cookie.Value, fn)
		}
	})
	if len(frames) != 3 || frames[2].Stage != "classifying" {
		t.Fatalf("published after revocation: %+v", frames)
	}
}
