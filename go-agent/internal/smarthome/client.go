package smarthome

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// HomeAssistantClient communicates with Home Assistant via its REST API.
type HomeAssistantClient struct {
	BaseURL       string
	Token         string
	httpClient    *http.Client
	tokenProvider TokenProvider
}

// NewHomeAssistantClient creates a new HA REST client.
func NewHomeAssistantClient(baseURL, token string, timeout time.Duration) *HomeAssistantClient {
	return &HomeAssistantClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		httpClient: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *HomeAssistantClient) accessToken(ctx context.Context) (string, error) {
	if c.tokenProvider != nil {
		return c.tokenProvider.Token(ctx)
	}
	return c.Token, nil
}
func (c *HomeAssistantClient) Close() {
	if c.tokenProvider != nil {
		c.tokenProvider.Close()
	}
}
func (c *HomeAssistantClient) do(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, newHAError("ha_invalid_response")
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, e := c.accessToken(ctx)
		if e != nil {
			return nil, e
		}
		req, e := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
		if e != nil {
			return nil, newHAError("ha_invalid_response")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, e := c.httpClient.Do(req)
		if e != nil {
			return nil, safeHAError(e)
		}
		if resp.StatusCode == 401 && c.tokenProvider != nil {
			c.tokenProvider.Invalidate(token)
		}
		if resp.StatusCode == 401 && c.tokenProvider != nil && method == http.MethodGet && attempt == 0 {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, haStatusError(resp.StatusCode)
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if e != nil {
			return nil, safeHAError(e)
		}
		return raw, nil
	}
	return nil, newHAError("ha_auth_required")
}

// GetStates queries all entity states from Home Assistant.
func (c *HomeAssistantClient) GetStates(ctx context.Context) ([]EntityState, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/states", nil)
	if err != nil {
		return nil, err
	}
	var states []EntityState
	if err := json.Unmarshal(body, &states); err != nil || states == nil {
		return nil, newHAError("ha_invalid_response")
	}
	for _, state := range states {
		if state.EntityID == "" {
			return nil, newHAError("ha_invalid_response")
		}
	}
	return states, nil
}

// GetState queries a single entity state.
func (c *HomeAssistantClient) GetState(ctx context.Context, entityID string) (*EntityState, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/states/"+entityID, nil)
	if err != nil {
		return nil, err
	}
	var state EntityState
	if err := json.Unmarshal(body, &state); err != nil || state.EntityID != entityID {
		return nil, newHAError("ha_invalid_response")
	}
	return &state, nil
}

// GetHistory retains the single-entity API; unfiltered requests are forbidden.
func (c *HomeAssistantClient) GetHistory(ctx context.Context, entityID string, start, end time.Time) ([]HistoryEntry, error) {
	return c.GetHistoryForEntities(ctx, []string{entityID}, start, end)
}
func historyPath(ids []string, start, end time.Time) string {
	q := url.Values{}
	q.Set("filter_entity_id", strings.Join(ids, ","))
	q.Set("end_time", end.UTC().Format(time.RFC3339Nano))
	return "/api/history/period/" + url.PathEscape(start.UTC().Format(time.RFC3339Nano)) + "?" + q.Encode()
}
func uniqueEntityIDs(ids []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}
func (c *HomeAssistantClient) GetHistoryForEntities(ctx context.Context, entityIDs []string, start, end time.Time) ([]HistoryEntry, error) {
	ids := uniqueEntityIDs(entityIDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("history requires explicit entity IDs")
	}
	if start.IsZero() || !end.After(start) {
		return nil, fmt.Errorf("history requires ordered start and end")
	}
	body, err := c.do(ctx, http.MethodGet, historyPath(ids, start, end), nil)
	if err != nil {
		return nil, err
	}
	var chunks [][]HistoryEntry
	if err = json.Unmarshal(body, &chunks); err != nil || chunks == nil {
		return nil, newHAError("ha_invalid_response")
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	seen := map[string]bool{}
	var result []HistoryEntry
	for _, chunk := range chunks {
		if chunk == nil {
			return nil, newHAError("ha_invalid_response")
		}
		for _, entry := range chunk {
			if !allowed[entry.EntityID] {
				return nil, newHAError("ha_invalid_response")
			}
			first := !seen[entry.EntityID]
			seen[entry.EntityID] = true
			entry.ObservationKind = "change"
			if first && entry.Timestamp.Equal(start) {
				entry.ObservationKind = "initial"
			}
			if !entry.Timestamp.Before(start) && entry.Timestamp.Before(end) {
				result = append(result, entry)
			}
		}
	}
	return deduplicateHistory(result), nil
}

// CallService calls a Home Assistant service (domain.service).
// e.g. CallService(ctx, "light", "turn_on", {"entity_id": "light.living_room"})
func (c *HomeAssistantClient) CallService(ctx context.Context, domain, service string, data map[string]interface{}) error {
	path := fmt.Sprintf("/api/services/%s/%s", domain, service)
	_, err := c.do(ctx, http.MethodPost, path, data)
	return err
}

// CreateAutomation creates a new automation rule in Home Assistant.
func (c *HomeAssistantClient) CreateAutomation(ctx context.Context, automation AutomationConfig) error {
	if err := validateAutomation(automation); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodPost, "/api/config/automation/config/"+automation.ID, automation)
	return err
}

// ReloadAutomations triggers HA to reload all automations.
func (c *HomeAssistantClient) ReloadAutomations(ctx context.Context) error {
	return c.CallService(ctx, "automation", "reload", nil)
}

// GetConfig returns the current HA configuration (used for validation).
func (c *HomeAssistantClient) GetConfig(ctx context.Context) (map[string]interface{}, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/config", nil)
	if err != nil {
		return nil, err
	}
	var config map[string]interface{}
	if err := json.Unmarshal(body, &config); err != nil || config == nil {
		return nil, newHAError("ha_invalid_response")
	}
	return config, nil
}
