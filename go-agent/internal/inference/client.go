package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ── Public interface ──

// Client is the unified inference interface.
// Callers (gateway, router) depend on this, not on OllamaClient directly.
type Client interface {
	Chat(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	Embed(ctx context.Context, body json.RawMessage) (json.RawMessage, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// ModelInfo is a lightweight model descriptor returned by ListModels.
type ModelInfo struct {
	ID string `json:"id"`
}

// ── Streaming ──

// StreamChunk is a single token from a streaming chat completion.
type StreamChunk struct {
	Text  string // the delta content for this chunk
	Done  bool   // true for the final chunk
	Error error  // set when the stream fails
}

// ChatStream sends a chat completion request with stream=true and returns
// a channel that receives tokens as they arrive via SSE.
// The caller MUST read the channel until it closes.
func (c *OllamaClient) ChatStream(ctx context.Context, body json.RawMessage) (<-chan StreamChunk, error) {
	// Inject stream:true into the request body.
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("ollama: stream: unmarshal request: %w", err)
	}
	req["stream"] = true

	streamBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: stream: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/v1/chat/completions", bytes.NewReader(streamBody))
	if err != nil {
		return nil, fmt.Errorf("ollama: stream: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.streamHc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama: stream: status %d: %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, 20)
	go c.readSSE(resp, ch)
	return ch, nil
}

// readSSE reads Server-Sent Events from the response body and writes StreamChunks.
// Closes the channel when the stream ends.
func (c *OllamaClient) readSSE(resp *http.Response, ch chan<- StreamChunk) {
	defer resp.Body.Close()
	defer close(ch)

	scanner := bufio.NewScanner(resp.Body)
	// Ollama SSE lines can be long; 4MB buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var accumulated strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// SSE data lines start with "data: ".
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		// "[DONE]" signals end of stream.
		if data == "[DONE]" {
			break
		}

		// Parse the chunk.
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed chunks silently — some SSE lines may be comments.
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta.Content
		if delta != "" {
			accumulated.WriteString(delta)
			select {
			case ch <- StreamChunk{Text: delta}:
			default:
				// Channel full — skip to avoid blocking the SSE reader.
				// The buffer is sized to handle normal cases.
			}
		}

		// Check for finish_reason.
		if chunk.Choices[0].FinishReason != nil {
			break
		}
	}

	// Send final chunk with accumulated text.
	select {
	case ch <- StreamChunk{Text: accumulated.String(), Done: true}:
	default:
	}
}

// ── Ollama implementation ──

// OllamaClient proxies requests to an Ollama-compatible HTTP endpoint.
type OllamaClient struct {
	endpoint string   // e.g. "http://localhost:11434"
	hc       *http.Client
	streamHc *http.Client // no timeout, for SSE streaming
}

// NewOllamaClient creates a client that forwards to the given endpoint.
func NewOllamaClient(endpoint string, timeout time.Duration) *OllamaClient {
	return &OllamaClient{
		endpoint: endpoint,
		hc: &http.Client{
			Timeout: timeout,
		},
		streamHc: &http.Client{
			// No timeout — streaming connections are long-lived by design.
			Timeout: 0,
		},
	}
}

// Chat forwards a chat completion request.
func (c *OllamaClient) Chat(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	return c.post(ctx, "/v1/chat/completions", body)
}

// Embed forwards an embedding request.
func (c *OllamaClient) Embed(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	return c.post(ctx, "/v1/embeddings", body)
}

// ListModels returns available models from the Ollama tags API.
func (c *OllamaClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: list models: %w", err)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: list models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: list models: status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama: list models: decode: %w", err)
	}

	models := make([]ModelInfo, len(result.Models))
	for i, m := range result.Models {
		models[i] = ModelInfo{ID: m.Name}
	}
	return models, nil
}

// ── helpers ──

func (c *OllamaClient) post(ctx context.Context, path string, body json.RawMessage) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: %s: read: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}

	return json.RawMessage(respBody), nil
}
