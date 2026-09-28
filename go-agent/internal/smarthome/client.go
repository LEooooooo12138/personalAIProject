package smarthome

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HomeAssistantClient communicates with Home Assistant via its REST API.
type HomeAssistantClient struct {
	BaseURL    string
	Token      string
	httpClient *http.Client
}

// NewHomeAssistantClient creates a new HA REST client.
func NewHomeAssistantClient(baseURL, token string, timeout time.Duration) *HomeAssistantClient {
	return &HomeAssistantClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *HomeAssistantClient) do(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	url := c.BaseURL + path
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("smarthome: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("smarthome: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("smarthome: request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("smarthome: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("smarthome: %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// GetStates queries all entity states from Home Assistant.
func (c *HomeAssistantClient) GetStates(ctx context.Context) ([]EntityState, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/states", nil)
	if err != nil {
		return nil, err
	}
	var states []EntityState
	if err := json.Unmarshal(body, &states); err != nil {
		return nil, fmt.Errorf("smarthome: unmarshal states: %w", err)
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
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("smarthome: unmarshal state: %w", err)
	}
	return &state, nil
}

// GetHistory queries historical state changes.
// entityID can be "" to get all entities.
// start and end define the time range; pass zero values for "now".
func (c *HomeAssistantClient) GetHistory(ctx context.Context, entityID string, start, end time.Time) ([]HistoryEntry, error) {
	path := "/api/history/period"
	if !start.IsZero() {
		path += "/" + start.UTC().Format(time.RFC3339)
	}
	if entityID != "" {
		path += "?filter_entity_id=" + entityID
	}
	if !end.IsZero() {
		sep := "&"
		if !strings.Contains(path, "?") {
			sep = "?"
		}
		path += sep + "end_time=" + end.UTC().Format(time.RFC3339)
	}

	body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	// HA returns a slice of slices: [][]HistoryEntry
	var chunks [][]HistoryEntry
	if err := json.Unmarshal(body, &chunks); err != nil {
		return nil, fmt.Errorf("smarthome: unmarshal history: %w", err)
	}

	var entries []HistoryEntry
	for _, chunk := range chunks {
		entries = append(entries, chunk...)
	}
	return entries, nil
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
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("smarthome: unmarshal config: %w", err)
	}
	return config, nil
}
