package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startControlFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := listener.Addr().String()
	listener.Close()
	origin := "http://" + addr
	f, e := newFixture(addr, origin)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("fixture did not stop")
		}
		f.Close()
	})
	for i := 0; i < 100; i++ {
		r, e := http.Get(origin + "/api/console/v1/auth/status")
		if e == nil {
			r.Body.Close()
			return f, origin
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture not ready")
	return nil, ""
}
func controlFixtureRequest(t *testing.T, origin, method, path, body string, cookie *http.Cookie, csrf string, want int) map[string]json.RawMessage {
	t.Helper()
	req, e := http.NewRequest(method, origin+"/api/console/v1"+path, bytes.NewBufferString(body))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, res.StatusCode, want, raw)
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		t.Fatalf("invalid JSON %s", raw)
	}
	return out
}
func controlFixtureLogin(t *testing.T, origin, user string) (*http.Cookie, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", origin+"/api/console/v1/auth/login", strings.NewReader(`{"username":"`+user+`","password":"`+fixturePassword+`"}`))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login %s: %d", user, res.StatusCode)
	}
	var identity struct {
		CSRF string `json:"csrf_token"`
	}
	if e = json.NewDecoder(res.Body).Decode(&identity); e != nil || len(res.Cookies()) == 0 {
		t.Fatal("login response", e)
	}
	return res.Cookies()[0], identity.CSRF
}
func controlFixtureChat(t *testing.T, origin string, cookie *http.Cookie, content string) (string, string, *smarthome.ControlProposal, *smarthome.DeviceQueryResult) {
	t.Helper()
	headers := http.Header{}
	headers.Set("Cookie", cookie.String())
	headers.Set("Origin", origin)
	conn, _, e := websocket.DefaultDialer.Dial(strings.Replace(origin, "http:", "ws:", 1)+"/api/console/v1/chat/ws", headers)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if e = conn.WriteJSON(map[string]string{"content": content, "request_id": "fixture-control"}); e != nil {
		t.Fatal(e)
	}
	var msg struct {
		Type, SessionID string
		Proposal        *smarthome.ControlProposal   `json:"proposal"`
		DeviceResult    *smarthome.DeviceQueryResult `json:"device_result"`
		Session         string                       `json:"session_id"`
	}
	for i := 0; i < 3; i++ {
		if e = conn.ReadJSON(&msg); e != nil {
			t.Fatal(e)
		}
		if msg.Type != "session" {
			return msg.Type, msg.Session, msg.Proposal, msg.DeviceResult
		}
	}
	t.Fatal("no result")
	return "", "", nil, nil
}
func TestFixtureControlChatAndHTTPConfirmation(t *testing.T) {
	f, origin := startControlFixture(t)
	cookie, csrf := controlFixtureLogin(t, origin, "owner")
	kind, sid, p, _ := controlFixtureChat(t, origin, cookie, "关闭开关 1")
	if kind != "control_proposal" || p == nil || p.Status != "pending" || p.Action != "turn_off" || f.dependencies.deviceWrites.Load() != 0 {
		t.Fatalf("proposal %s %+v", kind, p)
	}
	v := controlFixtureRequest(t, origin, "POST", "/control/proposals/"+p.ID+"/confirm", "{}", cookie, csrf, 200)
	var status string
	json.Unmarshal(v["status"], &status)
	if status != "succeeded" || f.dependencies.deviceWrites.Load() != 1 {
		t.Fatalf("confirm %s %d", status, f.dependencies.deviceWrites.Load())
	}
	controlFixtureRequest(t, origin, "POST", "/control/proposals/"+p.ID+"/confirm", "{}", cookie, csrf, 409)
	if f.dependencies.deviceWrites.Load() != 1 {
		t.Fatal("repeat executed")
	}
	history := controlFixtureRequest(t, origin, "GET", "/sessions/"+sid+"/messages", "", cookie, "", 200)
	if !bytes.Contains(history["messages"], []byte(p.ID)) || bytes.Contains(history["messages"], []byte(fixtureHAToken)) {
		t.Fatalf("history %s", history["messages"])
	}
	member, memberCSRF := controlFixtureLogin(t, origin, "alice")
	controlFixtureRequest(t, origin, "POST", "/control/proposals/"+p.ID+"/confirm", "{}", member, memberCSRF, 403)
	kind, _, _, result := controlFixtureChat(t, origin, member, "独立温度计现在多少度")
	if kind != "device_result" || result == nil || result.State != "23.5" || result.ObservedAt.IsZero() || f.dependencies.deviceWrites.Load() != 1 {
		t.Fatalf("query %s %+v", kind, result)
	}
}
