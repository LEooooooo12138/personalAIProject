package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAssistantCloudRequiresTrustedContextAndUsesMiMoContract(t *testing.T) {
	localCalls, cloudCalls := 0, 0
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localCalls++
		w.Write([]byte(`{"choices":[{"message":{"content":"local"}}]}`))
	}))
	defer local.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudCalls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("api-key") != "test-secret" {
			t.Error("wrong MiMo auth/path")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "mimo-v2.6-flash" || body["max_completion_tokens"] != float64(200) || body["max_tokens"] != nil || body["metadata"] != nil {
			t.Errorf("wrong MiMo payload: %v", body)
		}
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"cloud"}}]}`))
	}))
	defer cloud.Close()
	client := NewAssistantClient(NewOllamaClient(local.URL, time.Second))
	client.cloudEndpoint = cloud.URL
	body := json.RawMessage(`{"model":"mimo-v2.6-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":200,"metadata":{"provider":"cloud"}}`)
	if _, err := client.Chat(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if cloudCalls != 0 || localCalls != 1 {
		t.Fatal("caller JSON enabled cloud")
	}
	got, err := client.Chat(WithAssistantRoute(context.Background(), "cloud", "test-secret"), body)
	if err != nil || !strings.Contains(string(got), "cloud") || cloudCalls != 1 {
		t.Fatalf("cloud call failed %s %v", got, err)
	}
}

func TestAssistantHybridOnlyFallsBackOnTransientFailure(t *testing.T) {
	for _, status := range []int{400, 401, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				w.Write([]byte("secret error detail"))
			}))
			defer local.Close()
			n := 0
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n++
				w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`))
			}))
			defer cloud.Close()
			c := NewAssistantClient(NewOllamaClient(local.URL, time.Second))
			c.cloudEndpoint = cloud.URL
			_, err := c.Chat(WithAssistantRoute(context.Background(), "hybrid", "key"), json.RawMessage(`{"messages":[]}`))
			if status >= 500 {
				if err != nil || n != 1 {
					t.Fatalf("expected single fallback %v %d", err, n)
				}
			} else if err == nil || n != 0 {
				t.Fatal("invalid/auth failure retried in cloud")
			}
		})
	}
}

func TestAssistantCancellationAndMissingKeyNeverCallCloud(t *testing.T) {
	c := NewAssistantClient(NewOllamaClient("http://127.0.0.1:1", time.Second))
	_, err := c.Chat(WithAssistantRoute(context.Background(), "cloud", ""), json.RawMessage(`{}`))
	if !errors.Is(err, ErrCloudUnavailable) {
		t.Fatalf("missing key %v", err)
	}
	ctx, cancel := context.WithCancel(WithAssistantRoute(context.Background(), "hybrid", "secret"))
	cancel()
	if _, err := c.Chat(ctx, json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestAssistantCloudErrorsDoNotLeakKeyOrUpstreamBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("api-key: test-secret"))
	}))
	defer upstream.Close()
	c := NewAssistantClient(NewOllamaClient("http://127.0.0.1:1", time.Second))
	c.cloudEndpoint = upstream.URL
	_, err := c.Chat(WithAssistantRoute(context.Background(), "cloud", "test-secret"), json.RawMessage(`{"messages":[]}`))
	if err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("unsafe error %v", err)
	}
}

func TestAssistantHybridDoesNotFallbackForInvalidLocalProtocol(t *testing.T) {
	calls := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`))
	}))
	defer cloud.Close()
	c := NewAssistantClient(NewOllamaClient("file:///invalid-local-provider", time.Second))
	c.cloudEndpoint = cloud.URL
	_, err := c.Chat(WithAssistantRoute(context.Background(), "hybrid", "key"), json.RawMessage(`{"messages":[]}`))
	if err == nil || calls != 0 {
		t.Fatalf("invalid local configuration sent cloud request: err=%v calls=%d", err, calls)
	}
}

func TestAssistantHybridLocalDeadlineLeavesTimeForCloud(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer local.Close()
	calls := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"fallback"}}]}`))
	}))
	defer cloud.Close()
	c := NewAssistantClient(NewOllamaClient(local.URL, time.Second))
	c.cloudEndpoint = cloud.URL
	c.localAttemptTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(WithAssistantRoute(context.Background(), "hybrid", "key"), time.Second)
	defer cancel()
	result, err := c.Chat(ctx, json.RawMessage(`{"messages":[]}`))
	if err != nil || calls != 1 || !strings.Contains(string(result), "fallback") || ctx.Err() != nil {
		t.Fatalf("local timeout consumed cloud budget: %s %v calls%d", result, err, calls)
	}
}
