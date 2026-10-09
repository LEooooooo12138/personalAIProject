package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func registryServer(t *testing.T, response func(map[string]any) any) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]any{"type": "auth_required", "ha_version": "2026.7.2"})
		var auth map[string]any
		if conn.ReadJSON(&auth) != nil {
			return
		}
		if auth["type"] != "auth" || auth["access_token"] != "fixture-secret" {
			t.Error("wrong auth")
			return
		}
		conn.WriteJSON(map[string]any{"type": "auth_ok"})
		previous := 0
		for {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			id := int(req["id"].(float64))
			if id <= previous {
				t.Error("IDs not increasing")
			}
			previous = id
			conn.WriteJSON(response(req))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestRegistryCommandsAndNullableFields(t *testing.T) {
	types := []string{}
	s := registryServer(t, func(req map[string]any) any {
		typ := req["type"].(string)
		types = append(types, typ)
		if len(req) != 2 {
			t.Error("list contains extra fields")
		}
		var result any
		switch typ {
		case "config/area_registry/list":
			result = []any{map[string]any{"area_id": "b1", "name": "地下室B1", "aliases": []string{"地下室"}, "created_at": 123456.7}}
		case "config/device_registry/list":
			result = []any{map[string]any{"id": "d", "area_id": nil, "name": nil, "name_by_user": "控制器", "created_at": 12345}}
		case "config/entity_registry/list":
			result = []any{map[string]any{"entity_id": "switch.one", "device_id": "d", "area_id": nil, "name": nil, "disabled_by": nil, "hidden_by": "user", "entity_category": nil, "created_at": 12345}}
		case "config/label_registry/list":
			result = []any{map[string]any{"label_id": "l", "name": "地下室"}}
		default:
			t.Error("unexpected command")
		}
		return map[string]any{"id": req["id"], "type": "result", "success": true, "result": result}
	})
	c := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second)
	got, err := c.GetRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 4 || got.Areas[0].ID != "b1" || got.Devices[0].AreaID != nil || got.Entities[0].HiddenBy == nil || got.Labels[0].ID != "l" {
		t.Fatalf("wrong snapshot: %+v", got)
	}
}

func TestRegistryRejectsUncorrelatedAndUnsafeErrors(t *testing.T) {
	for _, mode := range []string{"id", "type", "failure", "null"} {
		t.Run(mode, func(t *testing.T) {
			s := registryServer(t, func(req map[string]any) any {
				r := map[string]any{"id": req["id"], "type": "result", "success": true, "result": []any{}}
				switch mode {
				case "id":
					r["id"] = 99
				case "type":
					r["type"] = "event"
				case "failure":
					r["success"] = false
					r["error"] = map[string]any{"message": "fixture-secret"}
				case "null":
					r["result"] = nil
				}
				return r
			})
			_, err := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second).GetRegistry(context.Background())
			if err == nil || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("unsafe or absent error %v", err)
			}
		})
	}
}

func TestRegistryAreaWritesUseMinimalPayloadAndNull(t *testing.T) {
	s := registryServer(t, func(req map[string]any) any {
		typ := req["type"].(string)
		var result any
		switch typ {
		case "config/area_registry/create":
			if len(req) != 3 || req["name"] != "其他" {
				t.Error("bad create payload")
			}
			result = map[string]any{"area_id": "other", "name": "其他"}
		case "config/device_registry/update":
			if len(req) != 4 || req["device_id"] != "d" || req["area_id"] != nil {
				t.Error("bad device payload")
			}
			result = map[string]any{"id": "d", "area_id": nil}
		case "config/entity_registry/update":
			if len(req) != 4 || req["entity_id"] != "switch.one" || req["area_id"] != "other" {
				t.Error("bad entity payload")
			}
			result = map[string]any{"entity_entry": map[string]any{"entity_id": "switch.one", "area_id": "other"}}
		default:
			t.Error("unexpected command")
		}
		return map[string]any{"id": req["id"], "type": "result", "success": true, "result": result}
	})
	c := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second)
	a, e := c.CreateArea(context.Background(), "其他")
	if e != nil || a.ID != "other" {
		t.Fatal(a, e)
	}
	if e = c.SetDeviceArea(context.Background(), "d", nil); e != nil {
		t.Fatal(e)
	}
	id := "other"
	if e = c.SetEntityArea(context.Background(), "switch.one", &id); e != nil {
		t.Fatal(e)
	}
}

func TestRegistryCancelClosesBlockedRead(t *testing.T) {
	entered := make(chan struct{})
	closed := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		close(entered)
		_, _, _ = conn.ReadMessage()
		close(closed)
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second).GetRegistry(ctx)
		done <- e
	}()
	<-entered
	cancel()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Fatalf("got %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection still open")
	}
}

func TestWSAuthRejectsUnexpectedGreetingWithoutToken(t *testing.T) {
	got := make(chan []byte, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]any{"type": "event", "message": "fixture-secret"})
		_, b, _ := conn.ReadMessage()
		got <- b
	}))
	defer s.Close()
	_, e := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second).GetRegistry(context.Background())
	if e == nil || strings.Contains(e.Error(), "fixture-secret") {
		t.Fatal(e)
	}
	var request map[string]any
	json.Unmarshal(<-got, &request)
	if request["access_token"] != nil {
		t.Fatal("sent token before auth_required")
	}
}

func TestRegistryDialFailureIsUnavailableRatherThanCancelled(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	endpoint := s.URL
	s.Close()
	_, err := NewHomeAssistantClient(endpoint, "fixture-secret", time.Second).GetRegistry(context.Background())
	if err == nil || HAErrorCode(err) != "ha_unavailable" {
		t.Fatalf("network failure reported as %v", err)
	}
}
func TestRegistryAuthFailureAndCallerDeadline(t *testing.T) {
	for _, typ := range []string{"auth_invalid", "unexpected"} {
		t.Run(typ, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if e != nil {
					return
				}
				defer conn.Close()
				conn.WriteJSON(map[string]any{"type": "auth_required"})
				var auth any
				conn.ReadJSON(&auth)
				conn.WriteJSON(map[string]any{"type": typ, "message": "fixture-secret"})
				var next any
				if conn.ReadJSON(&next) == nil {
					t.Error("command sent after failed auth")
				}
			}))
			defer s.Close()
			_, e := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second).GetRegistry(context.Background())
			want := "ha_invalid_response"
			if typ == "auth_invalid" {
				want = "ha_auth_required"
			}
			if e == nil || HAErrorCode(e) != want {
				t.Fatal(e)
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second).GetRegistry(ctx)
	if e == nil || time.Since(start) > time.Second {
		t.Fatal("caller deadline ignored", e)
	}
}

func TestRegistryRejectsMissingRequiredIDs(t *testing.T) {
	s := registryServer(t, func(req map[string]any) any {
		return map[string]any{"id": req["id"], "type": "result", "success": true, "result": []any{map[string]any{"name": "area without area_id"}}}
	})
	_, e := NewHomeAssistantClient(s.URL, "fixture-secret", time.Second).GetRegistry(context.Background())
	if !errors.Is(e, ErrRegistryUnavailable) {
		t.Fatalf("accepted missing IDs: %v", e)
	}
}
