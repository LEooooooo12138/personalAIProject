package smarthome

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientResponseLimitRejectsOverflow(t *testing.T) {
	for _, size := range []int{32 << 20, (32 << 20) + 1} {
		t.Run(string(rune(size-(32<<20)+'0')), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(w, io.LimitReader(&constantReader{}, int64(size)))
			}))
			defer srv.Close()
			raw, err := NewHomeAssistantClient(srv.URL, "secret", 5*time.Second).do(context.Background(), http.MethodGet, "/api/config", nil)
			if size == 32<<20 {
				if err != nil || len(raw) != size {
					t.Fatalf("exact limit rejected %d %v", len(raw), err)
				}
			} else if HAErrorCode(err) != "ha_response_too_large" || raw != nil {
				t.Fatalf("truncated response accepted %d %v", len(raw), err)
			}
		})
	}
}

type constantReader struct{}

func (*constantReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func TestClientRejectsRedirectWithoutForwardingToken(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	_, err := NewHomeAssistantClient(source.URL, "private-token", time.Second).ReadAutomationConfig(context.Background(), "rule-1")
	if err == nil || received.Load() != 0 || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe redirect %d %v", received.Load(), err)
	}
}
func TestAutomationConfigOAuthGETRefreshesOnce(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh", true: "bounded"}[repeat], func(t *testing.T) {
			var reads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("unexpected write")
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/api/config" {
					io.WriteString(w, `{"version":"2026.7.2","components":["config","automation"]}`)
					return
				}
				if r.URL.Path != "/api/config/automation/config/rule-1" {
					t.Error("wrong target")
					return
				}
				reads.Add(1)
				if repeat || r.Header.Get("Authorization") != "Bearer renewed" {
					w.WriteHeader(401)
					return
				}
				io.WriteString(w, observationConfig)
			}))
			defer srv.Close()
			c := NewHomeAssistantClient(srv.URL, "", time.Second)
			p := &testTokenProvider{token: "old"}
			c.tokenProvider = p
			got, err := c.ReadAutomationConfig(context.Background(), "rule-1")
			if reads.Load() != 2 {
				t.Fatalf("GET retry count %d", reads.Load())
			}
			if repeat {
				if HAErrorCode(err) != "ha_auth_required" || got.Exists != nil {
					t.Fatalf("%+v %v", got, err)
				}
			} else if err != nil || got.Exists == nil || !*got.Exists || p.invalidations != 1 {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
