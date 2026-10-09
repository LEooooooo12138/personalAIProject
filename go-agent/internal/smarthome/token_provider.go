package smarthome

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// TokenProvider supplies only short-lived access credentials; callers never see the refresh grant.
type TokenProvider interface {
	Token(context.Context) (string, error)
	Invalidate(string)
	Close()
}

type OAuthTokenProvider struct {
	mu                               sync.Mutex
	ctx                              context.Context
	cancel                           context.CancelFunc
	workers                          sync.WaitGroup
	client                           *http.Client
	endpoint, clientID, refreshToken string
	token                            string
	expiresAt, retryAt               time.Time
	lastErr                          error
	refresh                          chan struct{}
	closed                           bool
}

func normalizedHAURL(raw string) (string, bool) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return strings.TrimRight(u.String(), "/"), true
}

// NewOAuthTokenProvider reuses a pre-existing grant. Access tokens remain in memory.
func NewOAuthTokenProvider(baseURL, credentialsFile string, timeout time.Duration) (*OAuthTokenProvider, error) {
	base, ok := normalizedHAURL(baseURL)
	if !ok {
		return nil, newHAError("ha_auth_required")
	}
	f, e := os.Open(credentialsFile)
	if e != nil {
		return nil, newHAError("ha_auth_required")
	}
	defer f.Close()
	var credentials struct {
		BaseURL      string `json:"base_url"`
		ClientID     string `json:"client_id"`
		RefreshToken string `json:"refresh_token"`
	}
	if e = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&credentials); e != nil {
		return nil, newHAError("ha_auth_required")
	}
	credentialBase, valid := normalizedHAURL(credentials.BaseURL)
	if !valid || credentialBase != base || credentials.RefreshToken == "" || len(credentials.RefreshToken) > 65536 {
		return nil, newHAError("ha_auth_required")
	}
	if _, valid = normalizedHAURL(credentials.ClientID); !valid {
		return nil, newHAError("ha_auth_required")
	}
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &OAuthTokenProvider{ctx: ctx, cancel: cancel, client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: base + "/auth/token", clientID: credentials.ClientID, refreshToken: credentials.RefreshToken}, nil
}
func (p *OAuthTokenProvider) Token(ctx context.Context) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", context.Canceled
	}
	if p.token != "" && time.Until(p.expiresAt) > 60*time.Second {
		token := p.token
		p.mu.Unlock()
		return token, nil
	}
	if time.Now().Before(p.retryAt) && p.lastErr != nil {
		e := p.lastErr
		p.mu.Unlock()
		return "", e
	}
	done := p.refresh
	if done == nil {
		done = make(chan struct{})
		p.refresh = done
		p.workers.Add(1)
		go p.fetch(done)
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-done:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", context.Canceled
	}
	if p.lastErr != nil {
		return "", p.lastErr
	}
	return p.token, nil
}
func (p *OAuthTokenProvider) fetch(done chan struct{}) {
	defer p.workers.Done()
	token, expiry, e := p.exchange()
	p.mu.Lock()
	defer p.mu.Unlock()
	if e != nil {
		p.lastErr = e
		p.retryAt = time.Now().Add(5 * time.Second)
	} else {
		p.token = token
		p.expiresAt = expiry
		p.lastErr = nil
		p.retryAt = time.Time{}
	}
	p.refresh = nil
	close(done)
}
func (p *OAuthTokenProvider) exchange() (string, time.Time, error) {
	data := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {p.refreshToken}, "client_id": {p.clientID}}
	req, e := http.NewRequestWithContext(p.ctx, http.MethodPost, p.endpoint, strings.NewReader(data.Encode()))
	if e != nil {
		return "", time.Time{}, newHAError("ha_auth_required")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := p.client.Do(req)
	if e != nil {
		return "", time.Time{}, safeHAError(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 400 || resp.StatusCode == 401 {
		return "", time.Time{}, newHAError("ha_auth_required")
	}
	if resp.StatusCode == 403 {
		return "", time.Time{}, newHAError("ha_forbidden")
	}
	if resp.StatusCode != 200 {
		return "", time.Time{}, newHAError("ha_unavailable")
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); e != nil || result.AccessToken == "" || len(result.AccessToken) > 65536 || result.ExpiresIn <= 0 || result.ExpiresIn > 86400 || !strings.EqualFold(result.TokenType, "Bearer") {
		return "", time.Time{}, newHAError("ha_invalid_response")
	}
	return result.AccessToken, time.Now().Add(time.Duration(result.ExpiresIn) * time.Second), nil
}

// Invalidate compares the credential used by the failed request, preserving later renewals.
func (p *OAuthTokenProvider) Invalidate(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token == token {
		p.token = ""
		p.expiresAt = time.Time{}
	}
}
func (p *OAuthTokenProvider) Close() {
	p.mu.Lock()
	p.closed = true
	p.cancel()
	p.mu.Unlock()
	p.workers.Wait()
}
