package inference

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var ErrCloudUnavailable = errors.New("cloud inference unavailable")

type UpstreamError struct{ Status int }

func (e *UpstreamError) Error() string { return fmt.Sprintf("inference: upstream status %d", e.Status) }

type assistantRouteKey struct{}
type assistantRoute struct{ mode, key, endpoint string }

// WithAssistantRoute grants cloud access only to explicitly authorized server flows.
// Provider metadata supplied in a request body never grants this permission.
func WithAssistantRoute(ctx context.Context, mode, key string, endpoint ...string) context.Context {
	selected := ""
	if len(endpoint) > 0 {
		selected = endpoint[0]
	}
	return context.WithValue(ctx, assistantRouteKey{}, assistantRoute{mode, key, selected})
}

type AssistantClient struct {
	local               Client
	cloudEndpoint       string
	hc                  *http.Client
	localAttemptTimeout time.Duration
}

func NewAssistantClient(local Client) *AssistantClient {
	return &AssistantClient{local: local, localAttemptTimeout: 60 * time.Second, hc: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *AssistantClient) Chat(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	route, _ := ctx.Value(assistantRouteKey{}).(assistantRoute)
	if route.mode == "cloud" {
		return c.cloud(ctx, body, route.key)
	}
	localContext := ctx
	cancelLocal := func() {}
	if route.mode == "hybrid" {
		localContext, cancelLocal = context.WithTimeout(ctx, c.localAttemptTimeout)
	}
	result, err := c.local.Chat(localContext, body)
	cancelLocal()
	if err == nil || route.mode != "hybrid" {
		return result, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var upstream *UpstreamError
	if (errors.As(err, &upstream) && upstream.Status >= 500) || assistantTransientNetwork(err) {
		return c.cloud(ctx, body, route.key)
	}
	return nil, err
}

func assistantTransientNetwork(err error) bool {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) || errors.As(err, &verify) || errors.As(err, &record) {
		return false
	}
	var network net.Error
	var operation *net.OpError
	return (errors.As(err, &network) && network.Timeout()) || errors.As(err, &operation) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
func (c *AssistantClient) cloud(ctx context.Context, body json.RawMessage, key string) (result json.RawMessage, callErr error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.Join(ErrCloudUnavailable, ErrKeyUnavailable)
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(body, &input); err != nil || input == nil {
		return nil, ErrInvalidRequest
	}
	output := map[string]any{"model": "mimo-v2.6-flash", "stream": false, "thinking": map[string]string{"type": "disabled"}}
	for _, field := range []string{"messages", "temperature", "top_p", "response_format", "tools", "tool_choice", "max_completion_tokens"} {
		if v, ok := input[field]; ok {
			output[field] = v
		}
	}
	// MiMo documents JSON object output; the caller's strict decoder still
	// validates every schema field before a structured result can be used.
	if raw, ok := input["response_format"]; ok {
		var format struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &format) == nil && format.Type == "json_schema" {
			output["response_format"] = map[string]string{"type": "json_object"}
		}
	}
	if _, ok := output["max_completion_tokens"]; !ok {
		if v, ok := input["max_tokens"]; ok {
			output["max_completion_tokens"] = v
		}
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	route, _ := ctx.Value(assistantRouteKey{}).(assistantRoute)
	endpoint, err := assistantconfig.MiMoBaseURL(route.endpoint, key)
	if err != nil {
		return nil, err
	}
	if c.cloudEndpoint != "" {
		endpoint = c.cloudEndpoint + "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, ErrCloudUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", key)
	callContext, recorder := startCallTrace(ctx, int64(len(payload)))
	defer func() { recorder.finish(callErr) }()
	req = req.WithContext(callContext)
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, transportError{cause: err}
	}
	defer resp.Body.Close()
	if recorder != nil {
		recorder.mu.Lock()
		recorder.trace.StatusCode = resp.StatusCode
		recorder.mu.Unlock()
		resp.Body = &callTraceBody{ReadCloser: resp.Body, recorder: recorder}
	}
	if resp.StatusCode != 200 {
		return nil, &UpstreamError{Status: resp.StatusCode}
	}
	result, err = io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return nil, transportError{cause: err}
	}
	if len(result) > 4*1024*1024 {
		return nil, ErrInvalidResponse
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(result, &envelope) != nil || len(envelope.Choices) == 0 || envelope.Choices[0].FinishReason != "stop" || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return nil, ErrInvalidResponse
	}
	if recorder != nil {
		// Usage is optional and cannot affect the existing response acceptance rules.
		var usage struct {
			Usage struct {
				Input  *int64 `json:"prompt_tokens"`
				Output *int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(result, &usage) == nil {
			recorder.mu.Lock()
			if usage.Usage.Input != nil && *usage.Usage.Input >= 0 {
				recorder.trace.InputTokens = usage.Usage.Input
			}
			if usage.Usage.Output != nil && *usage.Usage.Output >= 0 {
				recorder.trace.OutputTokens = usage.Usage.Output
			}
			recorder.mu.Unlock()
		}
	}
	return result, nil
}
func (c *AssistantClient) Embed(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	return c.local.Embed(ctx, body)
}
func (c *AssistantClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return c.local.ListModels(ctx)
}
func (c *AssistantClient) ChatStream(ctx context.Context, body json.RawMessage) (<-chan StreamChunk, error) {
	route, _ := ctx.Value(assistantRouteKey{}).(assistantRoute)
	if route.mode == "" || route.mode == "local" {
		if s, ok := c.local.(interface {
			ChatStream(context.Context, json.RawMessage) (<-chan StreamChunk, error)
		}); ok {
			return s.ChatStream(ctx, body)
		}
	}
	result, err := c.Chat(ctx, body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(result, &r) != nil || len(r.Choices) == 0 {
		return nil, ErrInvalidResponse
	}
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Text: r.Choices[0].Message.Content, Done: true}
	close(ch)
	return ch, nil
}
