package gateway

import (
	"encoding/json"
	"testing"
)

func TestMultimodalUnmarshal(t *testing.T) {
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"http://x.com/i.jpg"}}]}]}`)

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	t.Logf("OK: %d messages, model=%s", len(req.Messages), req.Model)
}

func TestChatRequestAcceptsReasoningControls(t *testing.T) {
	for _, option := range []string{`"reasoning_effort":"none"`, `"reasoning_effort":"high"`, `"reasoning":{"effort":"low"}`} {
		body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}],` + option + `}`)
		req, err := parseChatRequest(body)
		if err != nil {
			t.Errorf("legal caller reasoning rejected: %s: %v", option, err)
			continue
		}
		if req.Raw["reasoning_effort"] == nil && req.Raw["reasoning"] == nil {
			t.Errorf("caller preference discarded: %s", option)
		}
	}
	for _, option := range []string{`"reasoning_effort":true`, `"reasoning_effort":"invented"`, `"reasoning":"low"`, `"reasoning":{"effort":false}`, `"reasoning":{"effort":"low","other":"unsupported"}`} {
		if _, err := parseChatRequest([]byte(`{"messages":[{"role":"user","content":"hello"}],` + option + `}`)); err == nil {
			t.Errorf("malformed caller reasoning accepted: %s", option)
		}
	}
}
