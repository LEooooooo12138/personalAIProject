package gateway

import (
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
)

func TestChatBudgetCloudPrivacyBeforeCap(t *testing.T) {
	s := &core.Session{}
	for _, r := range []core.Message{{Role: "assistant", Content: "legacy"}, {Role: "assistant", Content: "local", AssistantMode: "local"}, {Role: "assistant", Content: "attachment", AssistantMode: "cloud", Attachments: []core.MessageAttachment{{Kind: "control"}}}, {Role: "assistant", Content: "device", AssistantMode: "cloud", DeviceResult: &smarthome.DeviceQueryResult{}}, {Role: "assistant", Content: "event", AssistantMode: "cloud", EventID: "e"}} {
		s.Messages = append(s.Messages, core.Message{Role: "user", Content: "private"}, r)
	}
	for i := 0; i < 11; i++ {
		s.Messages = append(s.Messages, core.Message{Role: "user", Content: "public"}, core.Message{Role: "assistant", Content: "answer", AssistantMode: "cloud"})
	}
	s.Messages = append(s.Messages, core.Message{Role: "user", Content: "current"})
	h := buildCloudHistory(s)
	if len(h) != 22 {
		t.Fatalf("eligible history capped before budget: %d", len(h))
	}
	r, e := chain.ApplyChatBudget("", "", "current", h, chain.ChatBudget{MaxHistoryMessages: 20})
	if e != nil || r.DroppedPairs != 1 || len(r.History) != 20 {
		t.Fatalf("wrong omission %+v %v", r, e)
	}
	for _, m := range r.History {
		if m["content"] != "public" && m["content"] != "answer" {
			t.Fatal("private history survived")
		}
	}
	if c := chatErrorCode(chain.ErrContextBudgetExceeded, "chat_generation_failed"); c != "context_budget_exceeded" {
		t.Fatalf("unsafe error: %s", c)
	}
}
