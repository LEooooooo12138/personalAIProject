package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

type TokenManager struct {
	corpID     string
	corpSecret string
	token      string
	expiresAt  time.Time
	mu         sync.RWMutex
	logger     *zap.Logger
}

func NewTokenManager(corpID, corpSecret string, logger *zap.Logger) *TokenManager {
	return &TokenManager{
		corpID:     corpID,
		corpSecret: corpSecret,
		logger:     logger,
	}
}

func (m *TokenManager) GetToken(ctx context.Context) (string, error) {
	m.mu.RLock()
	if m.token != "" && time.Now().Before(m.expiresAt) {
		tok := m.token
		m.mu.RUnlock()
		return tok, nil
	}
	m.mu.RUnlock()
	return m.refresh(ctx)
}

func (m *TokenManager) refresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// double-check: another goroutine may have refreshed while we waited for the lock
	if m.token != "" && time.Now().Before(m.expiresAt) {
		return m.token, nil
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s", m.corpID, m.corpSecret)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("wecom: create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wecom: do request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Errcode     int    `json:"errcode"`
		Errmsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("wecom: decode response: %w", err)
	}

	if result.Errcode != 0 {
		return "", fmt.Errorf("wecom: api error %d: %s", result.Errcode, result.Errmsg)
	}

	m.token = result.AccessToken
	m.expiresAt = time.Now().Add(time.Duration(result.ExpiresIn-100) * time.Second)
	return m.token, nil
}

func (m *TokenManager) StartAutoRefresh(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.refresh(ctx); err != nil {
				m.logger.Error("token refresh failed", zap.Error(err))
			}
		}
	}
}
