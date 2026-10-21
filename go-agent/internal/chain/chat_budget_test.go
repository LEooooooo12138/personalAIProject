package chain

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestChatBudgetPreservesRequiredContext(t *testing.T) {
	h := []map[string]string{{"role": "user", "content": "旧"}, {"role": "assistant", "content": "答"}}
	before, _ := json.Marshal(h)
	r, e := ApplyChatBudget("系", "人", "问", h, ChatBudget{MaxInputBytes: 15, MaxHistoryMessages: 20})
	if e != nil || r.InputBytes != 15 || r.DroppedPairs != 0 {
		t.Fatalf("UTF8 boundary: %+v %v", r, e)
	}
	after, _ := json.Marshal(h)
	if !bytes.Equal(before, after) {
		t.Fatal("input changed")
	}
	_, e = ApplyChatBudget("系", "人", "问", h, ChatBudget{MaxInputBytes: 8})
	if !errors.Is(e, ErrContextBudgetExceeded) {
		t.Fatalf("required truncated: %v", e)
	}
}
func TestChatBudgetDropsOldestPairs(t *testing.T) {
	h := []map[string]string{{"role": "user", "content": "old"}, {"role": "assistant", "content": "reply"}, {"role": "user", "content": "新"}, {"role": "assistant", "content": "答"}}
	for _, b := range []ChatBudget{{MaxInputBytes: 9}, {MaxHistoryMessages: 3}} {
		r, e := ApplyChatBudget("s", "p", "q", h, b)
		if e != nil || r.DroppedPairs != 1 || r.InputBytes != 9 || !reflect.DeepEqual(r.History, h[2:]) {
			t.Fatalf("pair broken: %+v %v", r, e)
		}
	}
	r, e := ApplyChatBudget("s", "p", "q", h, ChatBudget{})
	if e != nil || r.DroppedPairs != 0 || len(r.History) != 4 {
		t.Fatalf("disabled budget: %+v %v", r, e)
	}
	r, e = ApplyChatBudget("s", "p", "q", h, ChatBudget{MaxHistoryMessages: 1})
	if e != nil || r.DroppedPairs != 2 || len(r.History) != 0 {
		t.Fatalf("split pair: %+v %v", r, e)
	}
}
func TestChatBudgetDoesNotFabricatePairs(t *testing.T) {
	h := []map[string]string{{"role": "assistant", "content": "orphan"}, {"role": "user", "content": "orphan"}, {"role": "system", "content": "gap"}, {"role": "assistant", "content": "orphan"}, {"role": "user", "content": "u"}, {"role": "assistant", "content": "a"}, {"role": "user", "content": "unfinished"}}
	r, e := ApplyChatBudget("", "", "q", h, ChatBudget{})
	if e != nil || r.DroppedPairs != 0 || r.InputBytes != 3 || !reflect.DeepEqual(r.History, h[4:6]) {
		t.Fatalf("fabricated pair: %+v %v", r, e)
	}
}

// Catch required reference/current truncation and changes to the identity switch.
func TestChatBudgetRequestPreservesRequiredContext(t *testing.T) {
	for _, p := range []string{"", " # 人格\n <&>\"保留原文"} {
		s := NewChainState(" 当前\n<&>问题 ", "agent", map[string]string{"channel": "console"})
		s.Set("assistant_personality", p)
		before, e := buildChatRequest(s, "fixture", 0.7, 1024, false)
		if e != nil {
			t.Fatal(e)
		}
		var req struct{ Messages []map[string]string }
		_ = json.Unmarshal(before, &req)
		n := 0
		for _, m := range req.Messages {
			n += len(m["content"])
		}
		s.Set("conversation_history", []map[string]string{{"role": "user", "content": "older"}, {"role": "assistant", "content": "reply"}})
		s.Set("chat_budget", ChatBudget{MaxInputBytes: n, MaxHistoryMessages: 20})
		after, e := buildChatRequest(s, "fixture", 0.7, 1024, false)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("required context changed or history remained")
		}
		if s.Data["omitted_history_pairs"] != 1 || s.Data["chat_input_bytes"] != n {
			t.Fatal("inaccurate budget metadata")
		}
		s.Set("chat_budget", ChatBudget{MaxInputBytes: n - 1})
		if _, e := buildChatRequest(s, "fixture", 0.7, 1024, false); !errors.Is(e, ErrContextBudgetExceeded) {
			t.Fatalf("missing safe error: %v", e)
		}
	}
	s := NewChainState("q", "agent", map[string]string{"channel": "console"})
	s.Set("chat_budget", ChatBudget{MaxInputBytes: 1})
	raw, e := buildChatRequest(s, "fixture", 0.7, 1024, false)
	if e != nil {
		t.Fatal(e)
	}
	var req struct{ Messages []map[string]string }
	_ = json.Unmarshal(raw, &req)
	if len(req.Messages) != 1 || req.Messages[0]["content"] != "q" {
		t.Fatal("identity enabled without snapshot")
	}
}
