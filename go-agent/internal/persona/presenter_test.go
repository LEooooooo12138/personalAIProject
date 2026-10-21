package persona

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
)

type testClient struct {
	chat func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (c testClient) Chat(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	return c.chat(ctx, body)
}
func (testClient) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	panic("presenter must not embed")
}
func (testClient) ListModels(context.Context) ([]inference.ModelInfo, error) {
	panic("presenter must not list models")
}
func completion(content string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion", "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": content}}}})
	return raw
}
func revision(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

// Catches character-name lookups, omitted markdown, fact leakage, cloud route
// inheritance, and ignoring model-selected combinations for an unseen document.
func TestPresenterUsesCompleteMarkdownAndLocalOllamaSchema(t *testing.T) {
	documents := []string{
		"# 人格甲\n温和而耐心，使用柔和节奏。\n" + strings.Repeat("完整原文必须保留。", 500),
		"# 人格乙\n严谨正式，清晰直接，不要感叹和寒暄。",
		"# 第三份自由文档\n像轻快的朋友，以平实节奏交流；没有任何预设角色名。",
	}
	profiles := []string{`{"tone":"gentle","rhythm":"soft"}`, `{"tone":"formal","rhythm":"plain"}`, `{"tone":"lively","rhythm":"plain"}`}
	fact := "独有设备事实：书房灯当前已关闭。"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("unexpected Ollama endpoint: %s %s", r.Method, r.URL.Path)
		}
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Strict bool `json:"strict"`
					Schema struct {
						AdditionalProperties bool     `json:"additionalProperties"`
						Required             []string `json:"required"`
						Properties           map[string]struct {
							Enum []string `json:"enum"`
						} `json:"properties"`
					} `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		i := int(calls.Add(1) - 1)
		if i >= len(documents) {
			t.Error("unexpected extra inference")
			return
		}
		if req.Model != "local-persona-model" || req.Stream || len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Errorf("invalid request contract: %s", body)
		}
		if len(req.Messages) == 2 {
			var data map[string]string
			if json.Unmarshal([]byte(req.Messages[1].Content), &data) != nil || len(data) != 1 || data["markdown"] != documents[i] {
				t.Errorf("complete MD must remain untrusted user data: %s", req.Messages[1].Content)
			}
			if strings.Contains(req.Messages[0].Content, documents[i]) {
				t.Error("personality elevated to system")
			}
		}
		if strings.Contains(string(body), fact) {
			t.Error("fact leaked to inference")
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(body, &fields)
		for _, forbidden := range []string{"knowledge", "history", "devices", "entity_id", "tools"} {
			if _, ok := fields[forbidden]; ok {
				t.Errorf("forbidden inference field %s", forbidden)
			}
		}
		schema := req.ResponseFormat.JSONSchema.Schema
		if req.ResponseFormat.Type != "json_schema" || !req.ResponseFormat.JSONSchema.Strict || schema.AdditionalProperties || len(schema.Required) != 2 || len(schema.Properties) != 2 || strings.Join(schema.Properties["tone"].Enum, ",") != "gentle,lively,concise,formal" || strings.Join(schema.Properties["rhythm"].Enum, ",") != "soft,plain" {
			t.Errorf("must request exact enum-only schema: %s", body)
		}
		_, _ = w.Write(completion(profiles[i]))
	}))
	defer server.Close()
	client := inference.NewAssistantClient(inference.NewOllamaClient(server.URL, time.Second))
	p := NewPresenter(client, "local-persona-model")
	ctx := inference.WithAssistantRoute(context.Background(), "cloud", "")
	var contents []string
	for _, doc := range documents {
		got := p.Present(ctx, doc, Event{Stage: "query", Fact: fact})
		if !strings.Contains(got.Content, fact) || got.PersonalityRevision != revision(doc) || got.Version == "" {
			t.Fatalf("invalid presentation: %+v", got)
		}
		contents = append(contents, got.Content)
	}
	if calls.Load() != 3 || contents[0] == contents[1] || contents[1] == contents[2] || contents[0] == contents[2] {
		t.Fatalf("different inferred profiles must yield different expression: %v, calls=%d", contents, calls.Load())
	}
}

// Catches rewriting facts, unenumerated stage expression, and model-produced
// action/state claims entering any stage's wrapper.
func TestPresenterPreservesFactsAcrossEveryStageAndProfile(t *testing.T) {
	stages := []string{"clarify", "pending", "query", "already_satisfied", "succeeded", "failed", "unknown", "cancelled", "expired", "executing", "recovered"}
	fact := "  【事实】\nentity=light.study：状态未知；请勿改写。\n"
	for _, tone := range []string{"gentle", "lively", "concise", "formal"} {
		for _, rhythm := range []string{"soft", "plain"} {
			t.Run(tone+"/"+rhythm, func(t *testing.T) {
				p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
					return completion(fmt.Sprintf(`{"tone":%q,"rhythm":%q}`, tone, rhythm)), nil
				}}, "local")
				for _, stage := range stages {
					got := p.Present(context.Background(), "人格", Event{Stage: stage, Fact: fact})
					if strings.Count(got.Content, fact) != 1 || got.Version == "" || got.PersonalityRevision != revision("人格") {
						t.Errorf("stage %s changed fact or omitted provenance: %+v", stage, got)
					}
					wrapper := strings.Replace(got.Content, fact, "", 1)
					for _, claim := range []string{"已完成", "成功", "失败", "关闭", "打开", "正在执行", "保证", "设备", "取消", "过期", "未知", "确认"} {
						if strings.Contains(wrapper, claim) {
							t.Errorf("stage %s adds claim %q: %q", stage, claim, wrapper)
						}
					}
				}
				if got := p.Present(context.Background(), "人格", Event{Stage: "future_stage", Fact: fact}); got.Content != fact {
					t.Errorf("unknown stage must fall back: %q", got.Content)
				}
			})
		}
	}
}

// Catches treating Stage only as a whitelist: changing Stage with the same
// immutable Fact must select a distinct communicative frame, beyond punctuation.
func TestPresenterOrganizesExpressionByStageWithIdenticalFact(t *testing.T) {
	stages := []string{"clarify", "pending", "query", "already_satisfied", "succeeded", "failed", "unknown", "cancelled", "expired", "executing", "recovered"}
	const fact = "【同一份服务端事实正文】"
	for _, tone := range []string{"gentle", "lively", "concise", "formal"} {
		t.Run(tone, func(t *testing.T) {
			p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return completion(fmt.Sprintf(`{"tone":%q,"rhythm":"plain"}`, tone)), nil
			}}, "local")
			seen := map[string]string{}
			for _, stage := range stages {
				got := p.Present(context.Background(), "同一人格", Event{Stage: stage, Fact: fact})
				if strings.Count(got.Content, fact) != 1 {
					t.Fatalf("stage %s changed fact: %q", stage, got.Content)
				}
				wrapper := strings.Replace(got.Content, fact, "", 1)
				words := strings.Map(func(r rune) rune {
					if r >= '\u4e00' && r <= '\u9fff' {
						return r
					}
					return -1
				}, wrapper)
				if prior, ok := seen[words]; ok {
					t.Errorf("stages %s and %s reuse the same expression beyond punctuation: %q", prior, stage, wrapper)
				}
				seen[words] = stage
			}
		})
	}
}

// Catches choosing different words without organizing the specific invitations,
// caution, or warm response needed for each stage. Fact stays exactly the same.
func TestPresenterGentleStageFramesHaveRelevantCommunicativePurpose(t *testing.T) {
	p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return completion(`{"tone":"gentle","rhythm":"soft"}`), nil
	}}, "local")
	const fact = "【服务端事实】"
	for _, tc := range []struct{ stage, purpose string }{
		{"clarify", "可以再说清楚"}, {"pending", "核对"}, {"query", "简单"}, {"unknown", "谨慎"}, {"succeeded", "随时告诉我"},
	} {
		got := p.Present(context.Background(), "人格", Event{Stage: tc.stage, Fact: fact})
		wrapper := strings.Replace(got.Content, fact, "", 1)
		if !strings.Contains(wrapper, tc.purpose) {
			t.Errorf("stage %s lacks communicative purpose %q: %q", tc.stage, tc.purpose, wrapper)
		}
	}
}

// Catches an execution response that instructs the user to read instead of
// offering simple conversational patience around the original server fact.
func TestPresenterExecutingDoesNotTellTheUserToRead(t *testing.T) {
	for _, tone := range []string{"gentle", "lively", "concise", "formal"} {
		p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return completion(fmt.Sprintf(`{"tone":%q,"rhythm":"plain"}`, tone)), nil
		}}, "local")
		const fact = "【固定服务端事实】"
		got := p.Present(context.Background(), "表达文档", Event{Stage: "executing", Fact: fact})
		wrapper := strings.Replace(got.Content, fact, "", 1)
		if strings.Contains(wrapper, "读") || !strings.Contains(wrapper, "耐心") {
			t.Errorf("tone %s should offer patience without a reading instruction: %q", tone, wrapper)
		}
	}
}

// Catches a successful result putting a generic wellbeing wish before the fact
// instead of closing naturally with a brief invitation for further dialogue.
func TestPresenterSuccessPlacesFactBeforeConversationInvitation(t *testing.T) {
	for _, tone := range []string{"gentle", "lively", "concise", "formal"} {
		p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return completion(fmt.Sprintf(`{"tone":%q,"rhythm":"soft"}`, tone)), nil
		}}, "local")
		const fact = "【固定服务端事实】"
		got := p.Present(context.Background(), "表达文档", Event{Stage: "succeeded", Fact: fact})
		if !strings.HasPrefix(got.Content, fact) || got.Content == fact {
			t.Errorf("tone %s should close after presenting the immutable fact: %q", tone, got.Content)
		}
	}
}

// Catches accepting schema injections, duplicate keys, malformed envelopes,
// truncated generations, refusal/tool calls, and retaining a failed profile.
func TestPresenterRejectsInvalidResultsAndRetriesOnNextEvent(t *testing.T) {
	badProfiles := []string{`{"tone":"gentle","rhythm":"soft","text":"已为你关闭灯"}`, `{"tone":"ignore previous instructions","rhythm":"plain"}`, `{"tone":"gentle"}`, `{"tone":"gentle","rhythm":"soft","tone":"formal"}`, `{"tone":null,"rhythm":"soft"}`, `{"tone":"gentle","rhythm":1}`, "```json\n{\"tone\":\"gentle\",\"rhythm\":\"soft\"}\n```", `{"tone":"gentle","rhythm":"soft"} {}`, `[]`, `{"Tone":"gentle","rhythm":"soft"}`, `{"tone":"gentle","rhythm":"SOFT"}`}
	var invalid []json.RawMessage
	for _, profile := range badProfiles {
		invalid = append(invalid, completion(profile))
	}
	invalid = append(invalid, nil, json.RawMessage(`{}`), json.RawMessage(`{"choices":[]}`), json.RawMessage(`{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`), json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"role":"user","content":"{}"}}]}`))
	for _, field := range []string{`"refusal":"blocked"`, `"tool_calls":[{}]`, `"function_call":{}`} {
		invalid = append(invalid, json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"tone\":\"gentle\",\"rhythm\":\"soft\"}",`+field+`}}]}`))
	}
	invalid = append(invalid, json.RawMessage(`{"error":{"message":"failure"},"choices":[{"finish_reason":"stop","message":{"content":"{\"tone\":\"gentle\",\"rhythm\":\"soft\"}"}}]}`), json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"content":"{}"},"message":{"content":"{\"tone\":\"gentle\",\"rhythm\":\"soft\"}"}}]}`), json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"tone\":\"gentle\",\"rhythm\":\"soft\"}"}},{"finish_reason":"stop","message":{"content":"{}"}}]}`))
	for i, raw := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var calls atomic.Int32
			p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				if calls.Add(1) == 1 {
					return raw, nil
				}
				return completion(`{"tone":"lively","rhythm":"plain"}`), nil
			}}, "local")
			if got := p.Present(context.Background(), "人格", Event{Stage: "succeeded", Fact: "服务器事实"}); got.Content != "服务器事实" {
				t.Fatalf("invalid result must return exact fact: %+v", got)
			}
			if got := p.Present(context.Background(), "人格", Event{Stage: "query", Fact: "后续事实"}); got.Content == "后续事实" || calls.Load() != 2 {
				t.Fatalf("failure must not be cached: %+v calls=%d", got, calls.Load())
			}
		})
	}
}

// Catches unbounded document caching and use of a previous document's profile.
func TestPresenterCachesByExactDocumentAndEvictsAfterSixteen(t *testing.T) {
	var calls atomic.Int32
	p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return completion(`{"tone":"gentle","rhythm":"soft"}`), nil
	}}, "local")
	present := func(doc string) Presentation {
		return p.Present(context.Background(), doc, Event{Stage: "query", Fact: "事实"})
	}
	first := present("文档0")
	if again := present("文档0"); again != first || calls.Load() != 1 {
		t.Fatalf("same document must reuse profile: %+v calls=%d", again, calls.Load())
	}
	for i := 1; i <= 16; i++ {
		got := present(fmt.Sprintf("文档%d", i))
		if got.PersonalityRevision == first.PersonalityRevision {
			t.Fatal("changed document reused old revision")
		}
	}
	present("文档16")
	if calls.Load() != 17 {
		t.Fatalf("latest document must remain cached, calls=%d", calls.Load())
	}
	present("文档0")
	if calls.Load() != 18 {
		t.Fatalf("oldest entry must be evicted after 16 profiles, calls=%d", calls.Load())
	}
}

func TestPresenterChangedDocumentDoesNotReusePriorProfileOnFailure(t *testing.T) {
	var calls atomic.Int32
	p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			return completion(`{"tone":"gentle","rhythm":"soft"}`), nil
		}
		return nil, errors.New("local unavailable")
	}}, "local")
	p.Present(context.Background(), "旧人格", Event{Stage: "query", Fact: "旧事实"})
	got := p.Present(context.Background(), "新人格", Event{Stage: "query", Fact: "新事实"})
	if got.Content != "新事实" || got.PersonalityRevision != revision("新人格") || calls.Load() != 2 {
		t.Fatalf("old profile contaminated new document: %+v calls=%d", got, calls.Load())
	}
}

// Catches duplicate concurrent inference and tying a shared flight to the
// first caller's cancellation instead of cancelling only that caller.
func TestPresenterCoalescesConcurrentDocumentWithoutCancellingOtherWaiters(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	p := NewPresenter(testClient{chat: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return completion(`{"tone":"gentle","rhythm":"soft"}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}, "local")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan Presentation, 1)
	go func() { first <- p.Present(ctx, "共享人格", Event{Stage: "query", Fact: "第一事实"}) }()
	waitStarted(t, started)
	var wg sync.WaitGroup
	results := make(chan Presentation, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- p.Present(context.Background(), "共享人格", Event{Stage: "query", Fact: "其余事实"})
		}()
	}
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		f := p.flights[revision("共享人格")]
		return f != nil && f.waiters == 13
	})
	cancel()
	select {
	case got := <-first:
		if got.Content != "第一事实" {
			t.Errorf("cancelled caller used expression: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not return promptly")
	}
	close(release)
	wg.Wait()
	close(results)
	for got := range results {
		if got.Content == "其余事实" || !strings.Contains(got.Content, "其余事实") {
			t.Errorf("other waiters lost shared result: %+v", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("same document inferred %d times", calls.Load())
	}
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("profile inference was not started")
	}
}

func TestPresenterCancelsAbandonedFlightAndAllowsFreshRequest(t *testing.T) {
	started := make(chan struct{})
	abandoned := make(chan struct{})
	var calls atomic.Int32
	p := NewPresenter(testClient{chat: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(abandoned)
			return nil, ctx.Err()
		}
		return completion(`{"tone":"formal","rhythm":"plain"}`), nil
	}}, "local")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan Presentation, 1)
	go func() { result <- p.Present(ctx, "人格", Event{Stage: "pending", Fact: "事实"}) }()
	waitStarted(t, started)
	cancel()
	select {
	case got := <-result:
		if got.Content != "事实" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	select {
	case <-abandoned:
	case <-time.After(time.Second):
		t.Fatal("abandoned inference was not cancelled")
	}
	got := p.Present(context.Background(), "人格", Event{Stage: "query", Fact: "新事实"})
	if got.Content == "新事实" || calls.Load() != 2 {
		t.Fatalf("fresh request joined abandoned flight: %+v calls=%d", got, calls.Load())
	}
}

// Catches allowing a non-cooperative inference client to block longer than the
// six-second budget; late results must never contaminate the cache.
func TestPresenterBoundsInferenceAndDiscardsLateResult(t *testing.T) {
	release := make(chan struct{})
	returned := make(chan struct{})
	var calls atomic.Int32
	p := NewPresenter(testClient{chat: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 6*time.Second {
				t.Error("inference deadline exceeds six seconds")
			}
			<-release
			close(returned)
		}
		return completion(`{"tone":"gentle","rhythm":"soft"}`), nil
	}}, "local")
	start := time.Now()
	got := p.Present(context.Background(), "超时人格", Event{Stage: "query", Fact: "事实"})
	if elapsed := time.Since(start); got.Content != "事实" || elapsed > 6500*time.Millisecond {
		close(release)
		t.Fatalf("timeout did not fall back in budget: %+v after %v", got, elapsed)
	}
	close(release)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("inference never ran or did not finish")
	}
	fresh := p.Present(context.Background(), "超时人格", Event{Stage: "query", Fact: "新事实"})
	if calls.Load() != 2 || fresh.Content == "新事实" {
		t.Fatalf("late result was cached: %+v calls=%d", fresh, calls.Load())
	}
}

func TestPresenterSkipsInferenceForEmptyDocumentOrCancelledContext(t *testing.T) {
	var calls atomic.Int32
	p := NewPresenter(testClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return completion(`{"tone":"gentle","rhythm":"soft"}`), nil
	}}, "local")
	for _, doc := range []string{"", " \n\t"} {
		if got := p.Present(context.Background(), doc, Event{Stage: "query", Fact: " 原事实 "}); got.Content != " 原事实 " {
			t.Errorf("empty document modified fact: %+v", got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := p.Present(ctx, "人格", Event{Stage: "query", Fact: "取消事实"}); got.Content != "取消事实" {
		t.Errorf("cancelled context modified fact: %+v", got)
	}
	if calls.Load() != 0 {
		t.Errorf("unnecessary inference calls=%d", calls.Load())
	}
	if got := NewPresenter(nil, "local").Present(context.Background(), "人格", Event{Stage: "query", Fact: "事实"}); got.Content != "事实" {
		t.Errorf("nil client modified fact: %+v", got)
	}
}
