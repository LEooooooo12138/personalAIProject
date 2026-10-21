package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudFailureDiagnoses(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          int
		body, key, want string
	}{
		{"missing key", 200, `{}`, "", "key_unavailable"},
		{"malformed response", 200, `private not json`, "key", "inference_invalid_response"},
		{"empty content", 200, `{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`, "key", "inference_invalid_response"},
		{"auth", 401, `private provider token`, "key", "inference_auth_failed"},
		{"forbidden", 403, `private provider token`, "key", "inference_forbidden"},
		{"quota", 402, `private provider token`, "key", "inference_quota_exceeded"},
		{"rate limit", 429, `private provider token`, "key", "inference_rate_limited"},
		{"outage", 503, `private provider token`, "key", "inference_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			c := NewAssistantClient(NewOllamaClient(server.URL, time.Second))
			c.cloudEndpoint = server.URL
			_, err := c.Chat(WithAssistantRoute(context.Background(), "cloud", tc.key), json.RawMessage(`{"messages":[]}`))
			if ErrorCode(err) != tc.want {
				t.Fatalf("code=%q err=%v want=%q", ErrorCode(err), err, tc.want)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("provider detail leaked")
			}
			if calls > 1 {
				t.Fatal("request retried")
			}
		})
	}
}

func TestCloudClientTimeoutKeepsDiagnosis(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(50 * time.Millisecond) }))
	defer server.Close()
	c := NewAssistantClient(nil)
	c.cloudEndpoint = server.URL
	c.hc.Timeout = 20 * time.Millisecond
	_, err := c.Chat(WithAssistantRoute(context.Background(), "cloud", "key"), json.RawMessage(`{"messages":[]}`))
	if ErrorCode(err) != "inference_timeout" {
		t.Fatalf("timeout lost: %v", err)
	}
}

func TestOllamaStreamStatusIsTypedAndSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("private provider token"))
	}))
	defer server.Close()
	_, err := NewOllamaClient(server.URL, time.Second).ChatStream(context.Background(), json.RawMessage(`{"messages":[]}`))
	if ErrorCode(err) != "inference_auth_failed" || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe status: %v", err)
	}
}

func TestOllamaStreamProviderErrorNeverDisclosesPayload(t *testing.T) {
	for _, payload := range []string{`data: {"error":{"message":"private credential"}}` + "\n\n", "data: private not json\n\n"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(payload)) }))
		ch, err := NewOllamaClient(server.URL, time.Second).ChatStream(context.Background(), json.RawMessage(`{"messages":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		chunk := <-ch
		server.Close()
		if chunk.Error == nil || ErrorCode(chunk.Error) != "inference_invalid_response" || strings.Contains(chunk.Error.Error(), "private") {
			t.Fatalf("unsafe provider failure: %v", chunk.Error)
		}
	}
}

func TestOfficialHTTPErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{421, "inference_content_filtered"}, {408, "inference_timeout"}, {504, "inference_timeout"}} {
		if got := ErrorCode(&UpstreamError{Status: tc.status}); got != tc.want {
			t.Errorf("status %d: %s want %s", tc.status, got, tc.want)
		}
	}
}
