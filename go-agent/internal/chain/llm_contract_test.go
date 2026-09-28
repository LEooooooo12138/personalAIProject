package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
	"go.uber.org/zap"
)

func TestLLMStepsPreserveCompleteChatRequestAndResponse(t *testing.T) {
	for _, kind := range []string{"answer", "simple"} {
		t.Run(kind, func(t *testing.T) {
			received := make(chan map[string]interface{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]interface{}
				json.NewDecoder(r.Body).Decode(&req)
				received <- req
				fmt.Fprint(w, `{"id":"upstream-id","model":"chosen","usage":{"total_tokens":9},"choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			client := inference.NewOllamaClient(server.URL, time.Second)
			var step Step = NewLLMAnswerStep(client, "default", 0.7, zap.NewNop())
			if kind == "simple" {
				step = NewLLMSimpleAnswerStep(client, "default", zap.NewNop())
			}
			state := NewChainState("last text", "personal", nil)
			state.Set("system_prompt", "vault context")
			var original map[string]interface{}
			json.Unmarshal([]byte(`{"model":"chosen","temperature":0,"max_tokens":17,"top_p":0.25,"stop":["END"],"messages":[{"role":"system","content":"caller system"},{"role":"assistant","content":"prior answer"},{"role":"user","content":[{"type":"text","text":"last text"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`), &original)
			state.Set("chat_request", original)
			if err := step.Run(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			req := <-received
			if req["model"] != "chosen" || req["temperature"] != float64(0) || req["max_tokens"] != float64(17) || req["top_p"] != 0.25 {
				t.Errorf("request options lost: %#v", req)
			}
			encoded, _ := json.Marshal(req["messages"])
			if !strings.Contains(string(encoded), "caller system") || !strings.Contains(string(encoded), "prior answer") || !strings.Contains(string(encoded), "image_url") {
				t.Errorf("messages lost: %s", encoded)
			}
			if strings.Count(string(encoded), "last text") != 1 {
				t.Errorf("current message duplicated/lost: %s", encoded)
			}
			raw, ok := state.Data["chat_response"].(json.RawMessage)
			if !ok || !strings.Contains(string(raw), "upstream-id") || !strings.Contains(string(raw), "total_tokens") {
				t.Errorf("upstream response lost: %s", raw)
			}
		})
	}
}

func TestLLMBufferedHonorsCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"should not happen"}}]}`)
	}))
	defer server.Close()
	step := NewLLMAnswerStep(inference.NewOllamaClient(server.URL, time.Second), "local", 0.7, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := step.Run(ctx, NewChainState("hello", "personal", nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request continued: %v", err)
	}
}

type stepStreamClient struct {
	started chan struct{}
	chunks  []inference.StreamChunk
}

func (c *stepStreamClient) Chat(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("unexpected buffered call")
}
func (c *stepStreamClient) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("unexpected embedding call")
}
func (c *stepStreamClient) ListModels(context.Context) ([]inference.ModelInfo, error) {
	return nil, nil
}
func (c *stepStreamClient) ChatStream(context.Context, json.RawMessage) (<-chan inference.StreamChunk, error) {
	ch := make(chan inference.StreamChunk, len(c.chunks))
	for _, chunk := range c.chunks {
		ch <- chunk
	}
	close(ch)
	if c.started != nil {
		close(c.started)
	}
	return ch, nil
}

func TestLLMStreamWaitsForConsumerWithoutDroppingTokens(t *testing.T) {
	client := &stepStreamClient{started: make(chan struct{}), chunks: []inference.StreamChunk{{Text: "one"}, {Text: "two"}, {Text: "onetwo", Done: true}}}
	step := NewLLMAnswerStep(client, "local", 0.7, zap.NewNop())
	state := NewChainState("hello", "personal", nil)
	tokens := make(chan StreamToken)
	state.Stream = tokens
	state.StreamMode = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- step.Run(ctx, state); close(tokens) }()
	<-client.started
	select {
	case err := <-done:
		t.Fatalf("producer completed before consumer; tokens dropped: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	var text strings.Builder
	var completed bool
	for token := range tokens {
		if !token.Done {
			text.WriteString(token.Text)
		}
		completed = completed || token.Done
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if text.String() != "onetwo" || state.FinalAnswer != "onetwo" || !completed {
		t.Fatalf("incomplete stream: %q final=%q done=%t", text.String(), state.FinalAnswer, completed)
	}
}

func TestLLMStreamFailureIsReturnedToChain(t *testing.T) {
	client := &stepStreamClient{chunks: []inference.StreamChunk{{Text: "partial"}, {Error: errors.New("stream interrupted")}}}
	state := NewChainState("hello", "personal", nil)
	state.StreamMode = true
	state.Stream = make(chan StreamToken, 10)
	if err := NewLLMAnswerStep(client, "local", 0.7, zap.NewNop()).Run(context.Background(), state); err == nil {
		t.Fatal("stream failure reported as success")
	}
}

type bufferedOnlyClient struct{ inference.Client }

func TestStreamingFallbackUsesBufferedUpstreamRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] == true {
			http.Error(w, "buffered client requested SSE", 400)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"complete"}}]}`)
	}))
	defer server.Close()
	client := bufferedOnlyClient{inference.NewOllamaClient(server.URL, time.Second)}
	state := NewChainState("hello", "personal", nil)
	state.StreamMode = true
	state.Stream = make(chan StreamToken, 1)
	if err := NewLLMAnswerStep(client, "local", 0.7, zap.NewNop()).Run(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if state.FinalAnswer != "complete" {
		t.Fatalf("fallback lost answer: %q", state.FinalAnswer)
	}
}
