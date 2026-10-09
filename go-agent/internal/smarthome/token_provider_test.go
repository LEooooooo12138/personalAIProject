package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func oauthFixture(t *testing.T, base string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "oauth.json")
	b, _ := json.Marshal(map[string]any{"base_url": base, "client_id": "http://console.local/", "refresh_token": "refresh-secret", "access_token": "expired-secret", "expires_at": "2000-01-01T00:00:00Z"})
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestOAuthConcurrentRefreshAndExpiry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/token" || r.Method != "POST" {
			t.Errorf("wrong request %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh-secret" || r.FormValue("client_id") != "http://console.local/" {
			t.Error("invalid grant")
		}
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-token", "expires_in": 1800, "token_type": "Bearer"})
	}))
	defer srv.Close()
	p, e := NewOAuthTokenProvider(srv.URL, oauthFixture(t, srv.URL), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := p.Token(context.Background())
			if e != nil || s != "fresh-token" {
				t.Errorf("token %q %v", s, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("refresh count %d", calls.Load())
	}
	p.mu.Lock()
	p.expiresAt = time.Now().Add(30 * time.Second)
	p.mu.Unlock()
	if _, e = p.Token(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatalf("expiry not refreshed: %d", calls.Load())
	}
	p.Invalidate("old-token")
	if _, e = p.Token(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("late invalidation cleared newer token")
	}
}
func TestOAuthFailureCooldownCancellationAndSafeErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte("refresh-secret private-url"))
	}))
	defer srv.Close()
	p, e := NewOAuthTokenProvider(srv.URL, oauthFixture(t, srv.URL), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	for i := 0; i < 3; i++ {
		_, e = p.Token(context.Background())
		if HAErrorCode(e) != "ha_auth_required" || strings.Contains(e.Error(), "secret") {
			t.Fatalf("unsafe error %v", e)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh storm %d", calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = p.Token(ctx); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel %v", e)
	}
	p.Close()
	if _, e = p.Token(context.Background()); e == nil {
		t.Fatal("closed provider issued token")
	}
}
func TestOAuthRejectsCredentialOriginAndRedirect(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer origin.Close()
	if _, e := NewOAuthTokenProvider(origin.URL, oauthFixture(t, target.URL), time.Second); e == nil {
		t.Fatal("accepted mismatch")
	}
	p, e := NewOAuthTokenProvider(origin.URL, oauthFixture(t, origin.URL), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if _, e = p.Token(context.Background()); e == nil {
		t.Fatal("accepted redirect")
	}
	if leaked.Load() {
		t.Fatal("credential leaked on redirect")
	}
}
func TestOAuthWaiterCancellationDoesNotCancelSharedRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"access_token":"fresh","expires_in":1800,"token_type":"Bearer"}`))
	}))
	defer srv.Close()
	p, e := NewOAuthTokenProvider(srv.URL, oauthFixture(t, srv.URL), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := p.Token(ctx); done <- e }()
	<-entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(release)
	if token, e := p.Token(context.Background()); e != nil || token != "fresh" {
		t.Fatalf("shared work lost %q %v", token, e)
	}
}

type testTokenProvider struct {
	token         string
	invalidations int
}

func (p *testTokenProvider) Token(context.Context) (string, error) { return p.token, nil }
func (p *testTokenProvider) Invalidate(token string)               { p.invalidations++; p.token = "renewed" }
func (p *testTokenProvider) Close()                                {}
func TestClientTokenRetriesReadOnlyAndSafeErrors(t *testing.T) {
	var gets, posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.WriteHeader(401)
			return
		}
		gets.Add(1)
		if r.Header.Get("Authorization") != "Bearer renewed" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte("secret"))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewHomeAssistantClient(srv.URL, "", time.Second)
	p := &testTokenProvider{token: "expired"}
	c.tokenProvider = p
	if _, e := c.GetStates(context.Background()); e != nil {
		t.Fatal(e)
	}
	if gets.Load() != 2 || p.invalidations != 1 {
		t.Fatal("GET was not refreshed once")
	}
	if e := c.CallService(context.Background(), "light", "turn_on", nil); HAErrorCode(e) != "ha_auth_required" {
		t.Fatal(e)
	}
	if posts.Load() != 1 {
		t.Fatal("write replayed")
	}
}

func TestOAuthTimeoutAndForbiddenAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{403, "ha_forbidden"}, {200, "ha_timeout"}} {
		t.Run(tc.want, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 200 {
					io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			p, e := NewOAuthTokenProvider(srv.URL, oauthFixture(t, srv.URL), 30*time.Millisecond)
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			_, e = p.Token(context.Background())
			if HAErrorCode(e) != tc.want {
				t.Fatalf("%v", e)
			}
		})
	}
}
func TestClientTokenRepeated401IsBoundedAndDoesNotLeak(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		_, _ = w.Write([]byte("secret-response-body"))
	}))
	defer srv.Close()
	c := NewHomeAssistantClient(srv.URL, "", time.Second)
	c.tokenProvider = &testTokenProvider{token: "bad"}
	_, e := c.GetStates(context.Background())
	if calls.Load() != 2 || HAErrorCode(e) != "ha_auth_required" || strings.Contains(e.Error(), "secret") {
		t.Fatalf("%d %v", calls.Load(), e)
	}
}
func TestOAuthCloseCancelsRefresh(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer srv.Close()
	p, e := NewOAuthTokenProvider(srv.URL, oauthFixture(t, srv.URL), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := p.Token(context.Background()); done <- e }()
	<-entered
	p.Close()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stuck")
	}
}
