package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"go.uber.org/zap"
)

// Only Chat is used; the offline transport records actual assembled JSON.
type contextCaptureClient struct {
	inference.Client
	mu   sync.Mutex
	body json.RawMessage
}

func (c *contextCaptureClient) Chat(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = append(c.body[:0], body...)
	return json.RawMessage(`{"choices":[{"message":{"content":"synthetic answer"}}]}`), nil
}
func (c *contextCaptureClient) requestBody() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.body...)
}

func TestChatBudgetResponseMatchesActualOmission(t *testing.T) {
	for _, mode := range []assistantconfig.Mode{assistantconfig.Cloud, assistantconfig.Hybrid} {
		t.Run(string(mode), func(t *testing.T) {
			c := &contextCaptureClient{}
			step := chain.NewLLMSimpleAnswerStep(c, "synthetic", zap.NewNop())
			s, ts := consoleChatFixture(t, step)
			s.chainRouter.Register("direct", chain.NewChain("direct", "", step))
			_, err := s.assistantConfig.Update(assistantconfig.Update{LLMMode: mode, ASRMode: assistantconfig.Local, TTSMode: assistantconfig.Local})
			if err != nil {
				t.Fatal(err)
			}
			cookie, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
			conn := dialConsole(t, ts, cookie)
			for i := 0; i < 12; i++ {
				if err := conn.WriteJSON(clientMessage{Content: fmt.Sprintf("question %d", i), Entry: ChatEntryChat, RequestID: fmt.Sprintf("budget-%d", i)}); err != nil {
					t.Fatal(err)
				}
				var frame serverMessage
				if err := readChatMessage(conn, &frame); err != nil {
					t.Fatal(err)
				}
				if frame.Type == "session" {
					if err := readChatMessage(conn, &frame); err != nil {
						t.Fatal(err)
					}
				}
				want := 0
				if i == 11 {
					want = 1
				}
				if frame.Type != "response" || frame.OmittedHistoryPairs != want {
					t.Fatalf("turn %d: %+v", i, frame)
				}
			}
			var req struct{ Messages []map[string]string }
			if err := json.Unmarshal(c.requestBody(), &req); err != nil {
				t.Fatal(err)
			}
			if len(req.Messages) != 22 || req.Messages[1]["content"] != "question 1" || req.Messages[21]["content"] != "question 11" {
				t.Fatalf("actual model history differs from omission count: %+v", req.Messages)
			}
		})
	}
}

// Measures the actual direct-chat assembly and JSON serialization plus a tiny
// offline response decode. No provider, filesystem personality, or network calls.
func BenchmarkChatContextAssembly(b *testing.B) {
	for _, historyCount := range []int{0, 20} {
		for _, personalityBytes := range []int{11 * 1024, 256 * 1024} {
			b.Run(fmt.Sprintf("history_%d/personality_%d", historyCount, personalityBytes), func(b *testing.B) {
				session := &core.Session{}
				for i := 0; i < historyCount/2; i++ {
					session.Messages = append(session.Messages, core.Message{Role: "user", Content: "synthetic prior question"}, core.Message{Role: "assistant", Content: "synthetic prior answer", AssistantMode: "cloud"})
				}
				personality := strings.Repeat("p", personalityBytes)
				c := &contextCaptureClient{}
				step := chain.NewLLMSimpleAnswerStep(c, "synthetic", zap.NewNop())
				assemble := func() *chain.ChainState {
					s := chain.NewChainState("synthetic current question", "agent", map[string]string{"channel": "console"})
					s.Set("conversation_history", buildCloudHistory(session))
					s.Set("assistant_personality", personality)
					s.Set("chat_budget", chain.ChatBudget{MaxHistoryMessages: 20})
					if err := step.Run(context.Background(), s); err != nil {
						b.Fatal(err)
					}
					return s
				}
				s := assemble()
				contentBytes := s.Data["chat_input_bytes"].(int)
				requestBytes := len(c.requestBody())
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					assemble()
				}
				b.StopTimer()
				b.ReportMetric(float64(contentBytes), "content_bytes/op")
				b.ReportMetric(float64(requestBytes), "request_bytes/op")
			})
		}
	}
}
