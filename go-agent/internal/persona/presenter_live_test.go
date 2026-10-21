package persona

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
)

type liveInferenceRecord struct {
	body, response json.RawMessage
	err            error
}
type liveRecordingClient struct {
	local   inference.Client
	mu      sync.Mutex
	records []liveInferenceRecord
}

func (c *liveRecordingClient) Chat(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	response, err := c.local.Chat(ctx, body)
	c.mu.Lock()
	c.records = append(c.records, liveInferenceRecord{append(json.RawMessage(nil), body...), append(json.RawMessage(nil), response...), err})
	c.mu.Unlock()
	return response, err
}
func (*liveRecordingClient) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("persona acceptance must not call embeddings")
}
func (*liveRecordingClient) ListModels(context.Context) ([]inference.ModelInfo, error) {
	return nil, fmt.Errorf("persona acceptance must not enumerate models")
}
func (c *liveRecordingClient) snapshot() []liveInferenceRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]liveInferenceRecord(nil), c.records...)
}

// Explicit local opt-in only. Ordinary test runs do not read a personality
// file, contact Ollama, or access credentials. The caller selects the complete
// uploaded MD with PERSONA_PRESENTER_LIVE_FILE. Facts are synthetic test data;
// the recorded real request additionally verifies that even those stay out of
// inference. This test does not contain or invoke a physical device executor.
func TestPresenterLiveLocalCompleteDocumentAndContrastingStyles(t *testing.T) {
	if os.Getenv("PERSONA_PRESENTER_LIVE") != "1" {
		t.Skip("set PERSONA_PRESENTER_LIVE=1 and PERSONA_PRESENTER_LIVE_FILE for explicit local persona acceptance")
	}
	path := os.Getenv("PERSONA_PRESENTER_LIVE_FILE")
	if path == "" {
		t.Fatal("PERSONA_PRESENTER_LIVE_FILE must explicitly select the complete uploaded Markdown")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatal("uploaded Markdown must not be empty")
	}
	documents := []struct{ name, markdown string }{
		{"uploaded", string(data)},
		{"reserved", "# 交流偏好\n庄重、正式、严谨而克制，使用清晰平实的节奏和礼貌的书面语。保持适当社交距离，直接表达，不撒娇，不热闹，不使用感叹语。"},
		{"energetic", "# 交流偏好\n活泼、热情、轻快，交流充满活力和好奇心，喜欢友好地邀请一起尝试。语气明快而亲近，节奏柔和留有空间，避免正式公文式表达。"},
	}
	const fact = "【本地表达验收固定事实正文；不含任何真实设备信息】"
	stages := []string{"clarify", "pending", "query", "already_satisfied", "succeeded", "failed", "unknown", "cancelled", "expired", "executing", "recovered"}
	client := &liveRecordingClient{local: inference.NewOllamaClient("http://127.0.0.1:11434", 10*time.Second)}
	presenter := NewPresenter(client, "gemma4:12b")
	pending := map[string]string{}
	profiles := map[string]profile{}
	for _, document := range documents {
		t.Run(document.name, func(t *testing.T) {
			before := len(client.snapshot())
			for _, stage := range stages {
				started := time.Now()
				got := presenter.Present(context.Background(), document.markdown, Event{Stage: stage, Fact: fact})
				t.Logf("sample=%s revision=%s stage=%s elapsed=%s\ncontent=%s", document.name, got.PersonalityRevision, stage, time.Since(started), got.Content)
				if strings.Count(got.Content, fact) != 1 || got.PersonalityRevision != revision(document.markdown) {
					t.Fatalf("stage %s changed fact or revision: %+v", stage, got)
				}
				// A valid concise/plain query is deliberately Fact-only. Every other
				// stage has a frame, so raw Fact indicates rejected/timeout inference.
				if stage != "query" && got.Content == fact {
					t.Fatalf("stage %s fell back to Fact; real local classification did not succeed within six seconds", stage)
				}
				if stage == "pending" {
					pending[document.name] = got.Content
				}
			}
			records := client.snapshot()
			if len(records) != before+1 {
				t.Fatalf("one successful inference should serve all stages, calls=%d", len(records)-before)
			}
			record := records[before]
			if record.err != nil {
				t.Fatalf("local classification failed: %v", record.err)
			}
			selected, valid := decodeProfile(record.response)
			if !valid {
				t.Fatal("real local model returned an invalid enum profile")
			}
			profiles[document.name] = selected
			t.Logf("sample=%s real_profile tone=%s rhythm=%s model=gemma4:12b temperature=0 reasoning_effort=none max_tokens=64 profile_prompt_sha256=%s", document.name, selected.tone, selected.rhythm, revision(profileSystemPrompt))
			var request struct {
				Model    string
				Messages []struct{ Role, Content string }
			}
			if json.Unmarshal(record.body, &request) != nil || request.Model != "gemma4:12b" || len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
				t.Fatal("local inference request has unexpected model or roles")
			}
			var envelope map[string]string
			if json.Unmarshal([]byte(request.Messages[1].Content), &envelope) != nil || len(envelope) != 1 || envelope["markdown"] != document.markdown {
				t.Fatal("real request did not receive the complete unchanged Markdown")
			}
			if strings.Contains(string(record.body), fact) {
				t.Fatal("Fact leaked into the real local inference request")
			}
		})
	}
	if t.Failed() {
		return
	}
	if profiles["reserved"] == profiles["energetic"] || pending["reserved"] == pending["energetic"] {
		t.Fatalf("opposite unseen communication documents did not yield different real profiles/presentations: reserved=%+v energetic=%+v", profiles["reserved"], profiles["energetic"])
	}
}
