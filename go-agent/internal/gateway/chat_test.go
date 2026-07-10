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
