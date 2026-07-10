package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/zap"
)

type APIClient struct {
	auth     *TokenManager
	logger   *zap.Logger
	client   *http.Client
	senderID string
}

func NewAPIClient(auth *TokenManager, senderID string, logger *zap.Logger) *APIClient {
	return &APIClient{
		auth:     auth,
		logger:   logger,
		client:   &http.Client{},
		senderID: senderID,
	}
}

func (c *APIClient) SendText(externalUserID, text string) error {
	token, err := c.auth.GetToken(context.Background())
	if err != nil {
		return fmt.Errorf("wecom: get token: %w", err)
	}

	payload := map[string]interface{}{
		"chat_type":       "single",
		"external_userid": []string{externalUserID},
		"sender":          c.senderID,
		"text":            map[string]string{"content": text},
		"msgtype":         "text",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("wecom: marshal payload: %w", err)
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/externalcontact/message/send?access_token=%s", token)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("wecom: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("wecom: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("wecom: read response: %w", err)
	}

	var result struct {
		Errcode int    `json:"errcode"`
		Errmsg  string `json:"errmsg"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("wecom: decode response: %w (body: %s)", err, string(respBody))
	}

	if result.Errcode != 0 {
		return fmt.Errorf("wecom: api error %d: %s", result.Errcode, result.Errmsg)
	}

	return nil
}
