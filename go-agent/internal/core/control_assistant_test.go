package core

import (
	"context"
	"encoding/json"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type assistantTestTransport func(*http.Request) (*http.Response, error)

func (f assistantTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCloudIntentWorksWithoutLocalModelAndDeviceResolutionStaysLocal(t *testing.T) {
	localCalls, cloudCalls := 0, 0
	completion := func(kind, question string) string {
		content, _ := json.Marshal(chain.HAIntent{Kind: kind, Question: question})
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(content)}}}})
		return string(body)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { localCalls++; w.WriteHeader(503) }))
	defer local.Close()
	original := http.DefaultTransport
	cloudKind := "chat"
	http.DefaultTransport = assistantTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.xiaomimimo.com" {
			return original.RoundTrip(r)
		}
		cloudCalls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["response_format"].(map[string]any)["type"] != "json_object" {
			t.Error("MiMo schema not converted")
		}
		messages := body["messages"].([]any)
		var envelope struct {
			Candidates chain.HACandidates `json:"candidates"`
		}
		json.Unmarshal([]byte(messages[1].(map[string]any)["content"].(string)), &envelope)
		if len(envelope.Candidates.Query) != 0 || len(envelope.Candidates.Control) != 0 {
			t.Error("cloud intent exposed local candidates")
		}
		question := ""
		if cloudKind == "clarify" {
			question = "哪个设备？"
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(completion(cloudKind, question)))}, nil
	})
	defer func() { http.DefaultTransport = original }()
	client := inference.NewAssistantClient(inference.NewOllamaClient(local.URL, time.Second))
	svc, err := smarthome.NewControlService(smarthome.ControlConfig{}, downControlCatalog{}, unusedControlHA{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := NewControlChat(svc, chain.NewHAIntentParser(client, "local-model"))
	ctx := inference.WithAssistantRoute(context.Background(), "cloud", "test-key")
	if result, err := control.HandleConversation(ctx, smarthome.ControlActor{}, "one", "hello"); err != nil || result != nil || localCalls != 0 || cloudCalls != 1 {
		t.Fatalf("cloud blocked by local: %v %v %d/%d", result, err, localCalls, cloudCalls)
	}
	cloudKind = "clarify"
	if _, err := control.HandleConversation(ctx, smarthome.ControlActor{}, "two", "打开台灯"); err == nil || localCalls != 1 || cloudCalls != 2 {
		t.Fatalf("device bypassed local: %v %d/%d", err, localCalls, cloudCalls)
	}
}

type preflightIntent struct{ calls int }

func (p *preflightIntent) Parse(_ context.Context, q string, c chain.HACandidates) (chain.HAIntent, error) {
	p.calls++
	if len(c.Query) != 0 || len(c.Control) != 0 {
		panic("preflight exposed candidates")
	}
	if q == "hello" {
		return chain.HAIntent{Kind: "chat"}, nil
	}
	return chain.HAIntent{Kind: "clarify", Question: "device?"}, nil
}
func TestAssistantPreflightOrdinaryChatDoesNotRequireLocalCatalog(t *testing.T) {
	parser := &preflightIntent{}
	c := NewControlChat(nil, parser)
	result, err := c.HandleConversation(context.Background(), smarthome.ControlActor{}, "id", "hello")
	if err != nil || result != nil || parser.calls != 1 {
		t.Fatalf("ordinary preflight %v %v", result, err)
	}
}
func TestAssistantPreflightDeviceStillUsesExistingLocalControl(t *testing.T) {
	svc, err := smarthome.NewControlService(smarthome.ControlConfig{}, downControlCatalog{}, unusedControlHA{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := &preflightIntent{}
	result, err := NewControlChat(svc, parser).HandleConversation(context.Background(), smarthome.ControlActor{}, "id", "打开灯")
	if err != nil || result == nil || result.Proposal != nil || parser.calls != 2 {
		t.Fatalf("device preflight %v %v calls%d", result, err, parser.calls)
	}
}
