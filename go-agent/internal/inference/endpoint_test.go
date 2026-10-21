package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type endpointTransport func(*http.Request) (*http.Response, error)

func (f endpointTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMiMoTokenPlanReachesMatchingOfficialEndpoint(t *testing.T) {
	c := NewAssistantClient(nil)
	c.hc.Transport = endpointTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://token-plan-cn.xiaomimimo.com/v1/chat/completions" {
			t.Errorf("wrong credential destination: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"OK"}}]}`))}, nil
	})
	_, err := c.Chat(WithAssistantRoute(context.Background(), "cloud", "tp-test-only"), json.RawMessage(`{"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestMiMoExplicitRegionControlsRequestHost(t *testing.T) {
	for _, tc := range []struct{ region, host string }{{"token_sgp", "token-plan-sgp.xiaomimimo.com"}, {"token_ams", "token-plan-ams.xiaomimimo.com"}} {
		t.Run(tc.region, func(t *testing.T) {
			c := NewAssistantClient(nil)
			c.hc.Transport = endpointTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != tc.host || r.Header.Get("api-key") != "tp-test-only" {
					t.Error("explicit region or credential not propagated")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"OK"}}]}`))}, nil
			})
			if _, err := c.Chat(WithAssistantRoute(context.Background(), "cloud", "tp-test-only", tc.region), json.RawMessage(`{"messages":[]}`)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
