package inference

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const traceResponse = `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`

// A completion recorded at headers loses the body wait; replacing a context trace loses caller hooks.
func TestCallTraceSeparatesHeadersAndBody(t *testing.T) {
	headers, body := make(chan struct{}), make(chan struct{})
	received, firstHeader := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(received)
		<-headers
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-body
		io.WriteString(w, traceResponse)
	}))
	defer server.Close()
	client := NewAssistantClient(nil)
	client.cloudEndpoint = server.URL
	observed := make(chan CallTrace, 2)
	base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := httptrace.WithClientTrace(base, &httptrace.ClientTrace{GotFirstResponseByte: func() { close(firstHeader) }})
	ctx = WithCallObserver(WithAssistantRoute(ctx, "cloud", "private-auth"), func(trace CallTrace) { observed <- trace })
	done := make(chan error, 1)
	go func() { _, err := client.Chat(ctx, json.RawMessage(`{"messages":[]}`)); done <- err }()
	select {
	case <-received:
	case err := <-done:
		t.Fatalf("call never reached server: %v", err)
	case <-base.Done():
		t.Fatal("call never reached server")
	}
	select {
	case <-observed:
		t.Error("completed before headers")
	default:
	}
	close(headers)
	select {
	case <-firstHeader:
	case <-time.After(5 * time.Second):
		close(body)
		t.Fatal("existing context hook lost")
	}
	select {
	case <-observed:
		t.Error("completed before body")
	default:
	}
	// Allow coarse Windows clocks to advance while the body is held; assertions
	// use ordering only, with no latency threshold.
	time.Sleep(20 * time.Millisecond)
	close(body)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var trace CallTrace
	select {
	case trace = <-observed:
	case <-base.Done():
		t.Fatal("observer never completed")
	}
	if trace.TotalMS == nil || trace.FirstResponseByteMS == nil || trace.FirstBodyByteMS == nil || *trace.TotalMS <= *trace.FirstResponseByteMS || *trace.FirstBodyByteMS < *trace.FirstResponseByteMS {
		t.Fatalf("body timing lost: %+v", trace)
	}
	if trace.StatusCode != 200 || trace.ResponseBytes != int64(len(traceResponse)) || trace.RequestBytes == 0 || trace.InputTokens == nil || *trace.InputTokens != 11 || trace.OutputTokens == nil || *trace.OutputTokens != 7 || trace.ErrorCode != "" {
		t.Fatalf("incomplete trace: %+v", trace)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", calls.Load())
	}
	select {
	case <-observed:
		t.Fatal("observer completed twice")
	default:
	}
}

// A reused connection has no fresh DNS/TCP/TLS samples; absent events must not become zero.
func TestCallTraceMissingHooksAreUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, traceResponse) }))
	defer server.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := NewAssistantClient(nil)
	client.cloudEndpoint, client.hc.Transport = server.URL, transport
	var traces []CallTrace
	ctx := WithCallObserver(WithAssistantRoute(context.Background(), "cloud", "key"), func(trace CallTrace) { traces = append(traces, trace) })
	for range 2 {
		if _, err := client.Chat(ctx, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(traces) != 2 {
		t.Fatalf("traces=%d", len(traces))
	}
	reused := traces[1]
	if !reused.Reused || reused.DNSMS != nil || reused.TCPMS != nil || reused.TLSMS != nil {
		t.Fatalf("missing hooks must stay unknown: %+v", reused)
	}
}

type traceRoundTripper func(*http.Request) (*http.Response, error)

func (f traceRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Upstream text and request IDs cannot enter metrics; failure must still complete exactly once.
func TestCallTraceErrorsArePrivate(t *testing.T) {
	const secret = "PRIVATE_TRACE_PAYLOAD_157"
	for _, tc := range []struct {
		name     string
		status   int
		response string
		failure  error
		want     string
	}{
		{"upstream", 401, secret, nil, "upstream_status"},
		{"invalid", 200, secret, nil, "invalid_response"},
		{"transport", 0, "", errors.New(secret), "transport_error"},
		{"cancel", 0, "", context.Canceled, "canceled"},
		{"timeout", 0, "", context.DeadlineExceeded, "deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, observed := 0, 0
			client := NewAssistantClient(nil)
			client.cloudEndpoint = "http://fixture.invalid?" + secret
			client.hc.Transport = traceRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.failure != nil {
					return nil, tc.failure
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.response)), Header: make(http.Header)}, nil
			})
			var trace CallTrace
			ctx := WithCallObserver(WithAssistantRoute(context.Background(), "cloud", secret), func(v CallTrace) { observed++; trace = v })
			_, err := client.Chat(ctx, json.RawMessage(`{"messages":[{"role":"user","content":"`+secret+`"}],"request_id":"`+secret+`"}`))
			if err == nil || calls != 1 || observed != 1 || trace.ErrorCode != tc.want || trace.TotalMS == nil {
				t.Fatalf("calls=%d observed=%d trace=%+v err=%v", calls, observed, trace, err)
			}
			raw, _ := json.Marshal(trace)
			if strings.Contains(string(raw), secret) {
				t.Fatal("payload leaked into trace")
			}
			if trace.DNSMS != nil || trace.TCPMS != nil || trace.TLSMS != nil || trace.InputTokens != nil || trace.OutputTokens != nil {
				t.Fatalf("unobserved data invented: %+v", trace)
			}
		})
	}
}

// DNS callbacks may overlap; keep completed durations and tolerate missing completions.
func TestCallTraceConcurrentHooks(t *testing.T) {
	client := NewAssistantClient(nil)
	client.cloudEndpoint = "http://fixture.invalid"
	client.hc.Transport = traceRoundTripper(func(r *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(r.Context())
		done := make(chan struct{}, 2)
		for range 2 {
			go func() {
				trace.DNSStart(httptrace.DNSStartInfo{})
				trace.DNSDone(httptrace.DNSDoneInfo{})
				done <- struct{}{}
			}()
		}
		<-done
		<-done
		trace.ConnectStart("tcp", "fixture")
		trace.ConnectDone("tcp", "fixture", nil)
		trace.TLSHandshakeStart()
		trace.TLSHandshakeDone(tls.ConnectionState{}, nil)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(traceResponse)), Header: make(http.Header)}, nil
	})
	var got CallTrace
	ctx := WithCallObserver(WithAssistantRoute(context.Background(), "cloud", "key"), func(v CallTrace) { got = v })
	if _, err := client.Chat(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if got.DNSMS == nil || got.TCPMS == nil || got.TLSMS == nil {
		t.Fatalf("completed hooks lost: %+v", got)
	}
}
