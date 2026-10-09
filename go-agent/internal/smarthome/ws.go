package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SubscribeEvents connects to the HA WebSocket API and forwards events to the handler.
// This method blocks until ctx is cancelled or the connection is lost.
func (c *HomeAssistantClient) SubscribeEvents(ctx context.Context, handler EventHandler) error {
	conn, cleanup, err := c.openAuthenticatedWS(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	// Step 2: subscribe to state_changed events
	subscribeMsg := map[string]interface{}{
		"id":         2,
		"type":       "subscribe_events",
		"event_type": "state_changed",
	}
	if err := conn.WriteJSON(subscribeMsg); err != nil {
		return fmt.Errorf("smarthome: ws subscribe: %w", err)
	}

	// Read auth OK
	if _, _, err := conn.ReadMessage(); err != nil {
		return fmt.Errorf("smarthome: ws read subscribe ack: %w", err)
	}

	// Step 3: event loop
	done := ctx.Done()
	var mu sync.Mutex

	for {
		select {
		case <-done:
			return ctx.Err()
		default:
		}

		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// If context cancelled, suppress the error
			select {
			case <-done:
				return ctx.Err()
			default:
			}
			return fmt.Errorf("smarthome: ws read event: %w", err)
		}

		var event HAEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			continue // skip unparseable messages
		}

		// Send pong for ping messages
		if event.Type == "pong" || (event.Success != nil && *event.Success) {
			continue
		}

		mu.Lock()
		handler(event)
		mu.Unlock()
	}
}

func (c *HomeAssistantClient) wsAuth(ctx context.Context, conn *websocket.Conn) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	// Read the initial auth_required message
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read auth_required: %w", err)
	}

	var authMsg HAEvent
	if err := json.Unmarshal(msg, &authMsg); err != nil {
		return newHAError("ha_invalid_response")
	}

	if authMsg.Type != "auth_required" {
		return newHAError("ha_invalid_response")
	}
	// Send auth
	authReq := map[string]interface{}{
		"type":         "auth",
		"access_token": token,
	}
	if err := conn.WriteJSON(authReq); err != nil {
		return fmt.Errorf("write auth: %w", err)
	}

	// Read auth response
	conn.SetReadDeadline(deadline)
	_, authResp, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	var authResult HAEvent
	if err := json.Unmarshal(authResp, &authResult); err != nil {
		return newHAError("ha_invalid_response")
	}

	if authResult.Type == "auth_ok" {
		return nil
	}
	if authResult.Type == "auth_invalid" {
		if c.tokenProvider != nil {
			c.tokenProvider.Invalidate(token)
		}
		return newHAError("ha_auth_required")
	}
	return newHAError("ha_invalid_response")
}

// Authentication may retry before any application command has been sent.
func (c *HomeAssistantClient) openAuthenticatedWS(ctx context.Context) (*websocket.Conn, func(), error) {
	for attempt := 0; attempt < 2; attempt++ {
		conn, resp, e := websocket.DefaultDialer.DialContext(ctx, strings.Replace(c.BaseURL, "http", "ws", 1)+"/api/websocket", http.Header{})
		if e != nil {
			if resp != nil {
				resp.Body.Close()
				return nil, func() {}, haStatusError(resp.StatusCode)
			}
			return nil, func() {}, safeHAError(e)
		}
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		cleanup := func() { stop(); conn.Close() }
		deadline := time.Now().Add(10 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		conn.SetWriteDeadline(deadline)
		e = c.wsAuth(ctx, conn)
		if e == nil {
			return conn, cleanup, nil
		}
		cleanup()
		if ctx.Err() != nil {
			return nil, func() {}, ctx.Err()
		}
		if attempt == 0 && c.tokenProvider != nil && HAErrorCode(e) == "ha_auth_required" {
			continue
		}
		return nil, func() {}, safeHAError(e)
	}
	return nil, func() {}, newHAError("ha_auth_required")
}
