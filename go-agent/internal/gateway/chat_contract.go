package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// Reject unsupported semantics instead of acknowledging options that are ignored.
func parseChatRequest(body []byte) (chatRequest, error) {
	var req chatRequest
	if err := json.Unmarshal(body, &req.Raw); err != nil || req.Raw == nil {
		return req, fmt.Errorf("invalid request JSON")
	}
	allowed := map[string]bool{"model": true, "messages": true, "metadata": true, "stream": true, "temperature": true, "top_p": true, "max_tokens": true, "stop": true, "seed": true, "presence_penalty": true, "frequency_penalty": true, "reasoning_effort": true, "reasoning": true}
	for key := range req.Raw {
		if !allowed[key] {
			return req, fmt.Errorf("unsupported field: %s", key)
		}
	}
	validEffort := func(v interface{}) bool {
		value, ok := v.(string)
		if !ok {
			return false
		}
		switch value {
		case "none", "minimal", "low", "medium", "high", "xhigh", "ultra", "max":
			return true
		}
		return false
	}
	if value, ok := req.Raw["reasoning_effort"]; ok && !validEffort(value) {
		return req, fmt.Errorf("invalid reasoning_effort")
	}
	if value, ok := req.Raw["reasoning"]; ok {
		options, valid := value.(map[string]interface{})
		if !valid || len(options) != 1 || !validEffort(options["effort"]) {
			return req, fmt.Errorf("invalid reasoning.effort")
		}
	}
	if value, ok := req.Raw["model"]; ok {
		var valid bool
		req.Model, valid = value.(string)
		if !valid {
			return req, fmt.Errorf("model must be a string")
		}
	}
	if value, ok := req.Raw["stream"]; ok {
		var valid bool
		req.Stream, valid = value.(bool)
		if !valid {
			return req, fmt.Errorf("stream must be a boolean")
		}
	}
	msgs, ok := req.Raw["messages"].([]interface{})
	if !ok || len(msgs) == 0 {
		return req, fmt.Errorf("messages must be a nonempty array")
	}
	hasUser := false
	for _, value := range msgs {
		m, ok := value.(map[string]interface{})
		if !ok {
			return req, fmt.Errorf("invalid message")
		}
		role, _ := m["role"].(string)
		if role != "system" && role != "developer" && role != "user" && role != "assistant" {
			return req, fmt.Errorf("unsupported message role")
		}
		for key := range m {
			if key != "role" && key != "content" && key != "name" {
				return req, fmt.Errorf("unsupported message field: %s", key)
			}
		}
		content := m["content"]
		switch v := content.(type) {
		case string:
			if role == "user" && strings.TrimSpace(v) != "" {
				hasUser = true
			}
		case []interface{}:
			if len(v) == 0 {
				return req, fmt.Errorf("empty content parts")
			}
			for _, part := range v {
				p, ok := part.(map[string]interface{})
				if !ok {
					return req, fmt.Errorf("invalid content part")
				}
				kind, _ := p["type"].(string)
				if kind != "text" && kind != "image_url" {
					return req, fmt.Errorf("unsupported content part")
				}
			}
			if role == "user" {
				hasUser = true
			}
		default:
			return req, fmt.Errorf("content must be text or multimodal parts")
		}
	}
	if !hasUser {
		return req, fmt.Errorf("a user message is required")
	}
	messages, _ := json.Marshal(msgs)
	if err := json.Unmarshal(messages, &req.Messages); err != nil {
		return req, err
	}
	req.Metadata = make(map[string]string)
	if v, exists := req.Raw["metadata"]; exists {
		metadata, ok := v.(map[string]interface{})
		if !ok {
			return req, fmt.Errorf("metadata must be an object")
		}
		for key, v := range metadata {
			switch x := v.(type) {
			case string:
				req.Metadata[key] = x
			case bool:
				req.Metadata[key] = fmt.Sprint(x)
			case float64:
				req.Metadata[key] = fmt.Sprint(x)
			default:
				return req, fmt.Errorf("metadata values must be scalar")
			}
		}
	}
	if skill := req.Metadata["skill"]; skill != "" && skill != "wiki-query" {
		return req, fmt.Errorf("unsupported skill: %s", skill)
	}
	for _, key := range []string{"temperature", "top_p", "max_tokens", "seed", "presence_penalty", "frequency_penalty"} {
		if value, exists := req.Raw[key]; exists {
			v, ok := value.(float64)
			if !ok {
				return req, fmt.Errorf("%s must be a number", key)
			}
			if key == "max_tokens" && (v <= 0 || v != float64(int64(v))) {
				return req, fmt.Errorf("max_tokens must be a positive integer")
			}
		}
	}
	// Gateway emits OpenAI SSE after a successful buffered generation; upstream fields remain intact.
	req.Raw["stream"] = false
	return req, nil
}

func writeChatSSE(c *gin.Context, resp map[string]interface{}) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	data, _ := json.Marshal(resp["choices"])
	var choices []map[string]interface{}
	_ = json.Unmarshal(data, &choices)
	for _, choice := range choices {
		chunk := map[string]interface{}{"id": resp["id"], "object": "chat.completion.chunk", "created": resp["created"], "model": resp["model"], "choices": []map[string]interface{}{{"index": choice["index"], "delta": choice["message"], "finish_reason": nil}}}
		b, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		c.Writer.Flush()
		chunk["choices"] = []map[string]interface{}{{"index": choice["index"], "delta": map[string]interface{}{}, "finish_reason": choice["finish_reason"]}}
		b, _ = json.Marshal(chunk)
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		c.Writer.Flush()
	}
	_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}
