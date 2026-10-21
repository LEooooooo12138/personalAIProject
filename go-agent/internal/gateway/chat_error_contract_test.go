package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
)

func TestClassifierHTTPFailureReachesChatFrame(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{
		{401, "inference_auth_failed"}, {403, "inference_forbidden"}, {402, "inference_quota_exceeded"}, {429, "inference_rate_limited"}, {503, "inference_unavailable"}, {400, "inference_invalid_request"}, {404, "inference_model_unavailable"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, "private provider key and prompt")
			}))
			defer upstream.Close()
			s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { t.Error("unexpected ordinary generation"); return nil }))
			ha := attachRejectedControlIntent(t, s, nil)
			s.controlChat = core.NewControlChat(s.control, chain.NewHAIntentParser(inference.NewOllamaClient(upstream.URL, time.Second), "local"))
			admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
			conn := dialConsole(t, ts, admin)
			if err := conn.WriteJSON(clientMessage{RequestID: "classified-error", Content: "hello"}); err != nil {
				t.Fatal(err)
			}
			var event map[string]any
			if err := readChatMessage(conn, &event); err != nil {
				t.Fatal(err)
			}
			event = nil
			if err := readChatMessage(conn, &event); err != nil {
				t.Fatal(err)
			}
			if event["code"] != tc.code || event["outcome"] != "failed" || event["request_id"] != "classified-error" || ha.writes.Load() != 0 {
				t.Fatalf("lost safe failure: %+v", event)
			}
			if strings.Contains(fmt.Sprint(event), "private") {
				t.Fatal("provider data leaked")
			}
		})
	}
}

func TestOrdinaryChatInferenceFailureIsDefiniteAndSafe(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("fail", func(context.Context, *chain.ChainState) error {
		return fmt.Errorf("private credential: %w", &inference.UpstreamError{Status: 401})
	}))
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	conn := dialConsole(t, ts, admin)
	if err := conn.WriteJSON(clientMessage{RequestID: "ordinary-failure", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := readChatMessage(conn, &event); err != nil {
		t.Fatal(err)
	}
	event = nil
	if err := readChatMessage(conn, &event); err != nil {
		t.Fatal(err)
	}
	if event["code"] != "inference_auth_failed" || event["outcome"] != "failed" {
		t.Fatalf("lost generation failure: %+v", event)
	}
	if strings.Contains(fmt.Sprint(event), "private") {
		t.Fatal("provider text leaked")
	}
}

type deadlineIntentInference struct{ inference.Client }

func (deadlineIntentInference) Chat(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestClassifierDeadlineRemainsDefiniteThroughControlFlow(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		s, _ := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { return nil }))
		attachRejectedControlIntent(t, s, nil)
		control := core.NewControlChat(s.control, chain.NewHAIntentParser(deadlineIntentInference{}, "local"))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		var err error
		actor := smarthome.ControlActor{UserID: "owner", SessionID: "timeout"}
		if cloud {
			_, err = control.HandleConversation(ctx, actor, "deadline", "hello")
		} else {
			_, err = control.Handle(ctx, actor, "deadline", "hello")
		}
		cancel()
		if chatErrorCode(err, "unknown") != "inference_timeout" || controlFailureOutcome(err) != "failed" {
			t.Fatalf("classifier timeout was ambiguous: cloud=%v code=%s outcome=%s", cloud, chatErrorCode(err, "unknown"), controlFailureOutcome(err))
		}
	}
	if controlFailureOutcome(context.DeadlineExceeded) != "unknown" {
		t.Fatal("post-classification deadline was assumed safe")
	}
}
