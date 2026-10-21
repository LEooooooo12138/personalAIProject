package speech

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTokenPlanSpeechUsesMatchingOfficialEndpoint(t *testing.T) {
	e := NewEngine(Config{ASRMode: "cloud", APIKey: "ttp-test-only"})
	e.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://token-plan-cn.xiaomimimo.com/v1/chat/completions" {
			t.Errorf("wrong credential destination: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"hello"}}]}`))}, nil
	})
	if _, err := e.Transcribe(context.Background(), wav(1), "audio/wav"); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitSpeechRegionControlsRequestHost(t *testing.T) {
	e := NewEngine(Config{ASRMode: "cloud", APIKey: "ttp-test-only", MiMoEndpoint: "token_sgp"})
	e.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "token-plan-sgp.xiaomimimo.com" {
			t.Errorf("explicit region lost: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"hello"}}]}`))}, nil
	})
	if _, err := e.Transcribe(context.Background(), wav(1), "audio/wav"); err != nil {
		t.Fatal(err)
	}
}
