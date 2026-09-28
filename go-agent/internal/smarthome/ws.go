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
	wsURL := strings.Replace(c.BaseURL, "http", "ws", 1) + "/api/websocket"

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return fmt.Errorf("smarthome: ws dial: %w", err)
	}
	defer conn.Close()

	// Step 1: wait for auth_required message
	if err := c.wsAuth(ctx, conn); err != nil {
		return fmt.Errorf("smarthome: ws auth: %w", err)
	}

	// Step 2: subscribe to state_changed events
	subscribeMsg := map[string]interface{}{
		"id":   2,
		"type": "subscribe_events",
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
	// Read the initial auth_required message
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read auth_required: %w", err)
	}

	var authMsg HAEvent
	if err := json.Unmarshal(msg, &authMsg); err != nil {
		return fmt.Errorf("parse auth_required: %w", err)
	}

	// Send auth
	authReq := map[string]interface{}{
		"type":        "auth",
		"access_token": c.Token,
	}
	if err := conn.WriteJSON(authReq); err != nil {
		return fmt.Errorf("write auth: %w", err)
	}

	// Read auth response
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, authResp, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	var authResult HAEvent
	if err := json.Unmarshal(authResp, &authResult); err != nil {
		return fmt.Errorf("parse auth result: %w", err)
	}

	if authResult.Type == "auth_ok" {
		return nil
	}
	if authResult.Type == "auth_invalid" {
		return fmt.Errorf("smarthome: invalid token")
	}
	return fmt.Errorf("smarthome: unexpected auth response: %s", string(authResp))
}
