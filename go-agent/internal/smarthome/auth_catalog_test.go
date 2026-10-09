package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryOAuthRenewsBeforeAnyCommand(t *testing.T) {
	var connections, commands atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		_ = conn.WriteJSON(map[string]any{"type": "auth_required"})
		var auth map[string]any
		if conn.ReadJSON(&auth) != nil {
			return
		}
		if auth["access_token"] != "renewed" {
			_ = conn.WriteJSON(map[string]any{"type": "auth_invalid", "message": "secret"})
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "auth_ok"})
		for {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			commands.Add(1)
			_ = conn.WriteJSON(map[string]any{"id": req["id"], "type": "result", "success": true, "result": []any{}})
		}
	}))
	defer srv.Close()
	c := NewHomeAssistantClient(srv.URL, "", time.Second)
	p := &testTokenProvider{token: "expired"}
	c.tokenProvider = p
	if _, e := c.GetRegistry(context.Background()); e != nil {
		t.Fatal(e)
	}
	if connections.Load() != 2 || commands.Load() != 4 || p.invalidations != 1 {
		t.Fatalf("connections=%d commands=%d refresh=%d", connections.Load(), commands.Load(), p.invalidations)
	}
}
func TestRegistryAuthErrorReachesCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"type": "auth_required"})
		var auth any
		_ = conn.ReadJSON(&auth)
		_ = conn.WriteJSON(map[string]any{"type": "auth_invalid"})
	}))
	defer srv.Close()
	catalog := NewCatalogService(context.Background(), NewHomeAssistantClient(srv.URL, "bad", time.Second))
	defer catalog.Close()
	snapshot, e := catalog.Get(context.Background())
	if e == nil || snapshot.Meta.ErrorCode == nil || *snapshot.Meta.ErrorCode != "ha_auth_required" || HAErrorCode(e) != "ha_auth_required" {
		t.Fatalf("lost auth classification: %+v %v", snapshot.Meta, e)
	}
}
func TestCatalogDeviceClassIsValidatedAndCloned(t *testing.T) {
	for _, class := range []any{"door", "tamper", "future_unsafe", 55, nil} {
		reg := catalogFixture()
		reg.Entities[0].EntityID = "binary_sensor.door"
		snapshot, e := buildCatalog(reg, []EntityState{{EntityID: "binary_sensor.door", State: "on", Attributes: map[string]any{"device_class": class, "secret": "hidden"}}})
		if e != nil {
			t.Fatal(e)
		}
		d, e := snapshot.Device("a_Yg", "d_Y29udHJvbGxlcg")
		if e != nil {
			t.Fatal(e)
		}
		entity := d.Device.Entities[0]
		if class == "door" || class == "tamper" {
			if entity.DeviceClass == nil || *entity.DeviceClass != class {
				t.Fatalf("lost class %+v", entity)
			}
			*entity.DeviceClass = "changed"
			again, _ := snapshot.Device("a_Yg", "d_Y29udHJvbGxlcg")
			if *again.Device.Entities[0].DeviceClass == "changed" {
				t.Fatal("snapshot mutated")
			}
		} else if entity.DeviceClass != nil {
			t.Fatal("unvalidated class")
		}
		raw, _ := json.Marshal(d)
		if string(raw) == "" {
			t.Fatal("empty DTO")
		}
	}
}

func TestHAInvalidJSONAndNullPayloadAreNotHealthy(t *testing.T) {
	for _, body := range []string{"null", "{invalid"} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer srv.Close()
			c := NewHomeAssistantClient(srv.URL, "fixture", time.Second)
			_, e := c.GetStates(context.Background())
			if HAErrorCode(e) != "ha_invalid_response" {
				t.Fatalf("states accepted %q: %v", body, e)
			}
			_, e = c.GetConfig(context.Background())
			if HAErrorCode(e) != "ha_invalid_response" {
				t.Fatalf("config accepted %q: %v", body, e)
			}
			_, e = c.GetState(context.Background(), "light.room")
			if HAErrorCode(e) != "ha_invalid_response" {
				t.Fatalf("state accepted %q: %v", body, e)
			}
			_, e = c.GetHistory(context.Background(), "light.room", time.Now().Add(-time.Hour), time.Now())
			if HAErrorCode(e) != "ha_invalid_response" {
				t.Fatalf("history accepted %q: %v", body, e)
			}
		})
	}
}
func TestRegistryMalformedJSONIsInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		conn.WriteMessage(websocket.TextMessage, []byte("{invalid"))
	}))
	defer srv.Close()
	_, e := NewHomeAssistantClient(srv.URL, "fixture", time.Second).GetRegistry(context.Background())
	if HAErrorCode(e) != "ha_invalid_response" {
		t.Fatalf("wrong malformed classification: %v", e)
	}
}
func TestRegistryInvalidResultPreservesSentinelAndClassification(t *testing.T) {
	srv := registryServer(t, func(req map[string]any) any {
		return map[string]any{"id": req["id"], "type": "result", "success": true, "result": nil}
	})
	_, e := NewHomeAssistantClient(srv.URL, "fixture-secret", time.Second).GetRegistry(context.Background())
	if !errors.Is(e, ErrRegistryUnavailable) || HAErrorCode(e) != "ha_invalid_response" {
		t.Fatalf("invalid result: %v", e)
	}
}

func TestHistoryUnexpectedEntityIsSafeInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[[{"entity_id":"sensor.private_unrequested","state":"on","last_changed":"2026-10-09T01:00:00Z"}]]`))
	}))
	defer srv.Close()
	_, err := NewHomeAssistantClient(srv.URL, "fixture", time.Second).GetHistory(context.Background(), "light.room", time.Now().Add(-time.Hour), time.Now())
	if HAErrorCode(err) != "ha_invalid_response" || err.Error() != "home assistant: ha_invalid_response" {
		t.Fatalf("unexpected history payload must be classified without upstream data: %v", err)
	}
}
