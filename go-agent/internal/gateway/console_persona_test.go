package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/persona"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlTerminalMessagePersistsAndRecovers(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	principal, _ := s.consoleStore.Resolve(admin.Value)
	session, _ := s.sessionStore.BrowserSession("console", "terminal", "console:"+principal.UserID, true)
	s.sessionMgr.EndSession(session)
	ha := &controlGatewayHA{state: "off"}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	defer catalog.Close()
	var err error
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{{EntityID: "light.test", Name: "台灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on"}}}}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", `{"session_id":"terminal","request_id":"r","entity_id":"light.test","action":"turn_on"}`, admin, csrf, testConsoleOrigin, ""), 200)
	route := consolePrefix + "/control/proposals/" + out["proposal"].(map[string]any)["id"].(string)
	result := consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	envelope, ok := result["assistant_message"].(map[string]any)
	if !ok {
		t.Fatalf("terminal result missing persisted assistant message: %v", result)
	}
	if envelope["index"] != float64(0) {
		t.Fatalf("incorrect index: %v", envelope)
	}
	message := envelope["message"].(map[string]any)
	if message["event_id"] == "" || message["assistant_mode"] != "local" {
		t.Fatalf("unsafe metadata: %v", message)
	}
	restored := consoleJSON(t, consoleRequest(s, "GET", route, "", admin, "", "", ""), 200)
	if restored["assistant_message"].(map[string]any)["message"].(map[string]any)["content"] != message["content"] {
		t.Fatal("GET changed saved prose")
	}
	messages, err := s.sessionStore.OwnedMessages("console", "terminal", "console:"+principal.UserID)
	if err != nil || len(messages) != 1 || ha.writes != 1 {
		t.Fatalf("history/writes: %d %d %v", len(messages), ha.writes, err)
	}
}

type personaGatewayClient struct {
	inference.Client
	chat func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (c personaGatewayClient) Chat(ctx context.Context, b json.RawMessage) (json.RawMessage, error) {
	return c.chat(ctx, b)
}

func terminalFixture(t *testing.T) (*Server, *http.Cookie, string, string, *controlGatewayHA) {
	t.Helper()
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	p, _ := s.consoleStore.Resolve(admin.Value)
	_, err := s.sessionStore.BrowserSession("console", "terminal", "console:"+p.UserID, true)
	if err != nil {
		t.Fatal(err)
	}
	ha := &controlGatewayHA{state: "off"}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	t.Cleanup(catalog.Close)
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{{EntityID: "light.test", Name: "台灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on"}}}}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", `{"session_id":"terminal","request_id":"r","entity_id":"light.test","action":"turn_on"}`, admin, csrf, testConsoleOrigin, ""), 200)
	return s, admin, csrf, consolePrefix + "/control/proposals/" + out["proposal"].(map[string]any)["id"].(string), ha
}

func TestTerminalMessageSaveFailureRecoversWithoutDeviceReplay(t *testing.T) {
	s, admin, csrf, route, ha := terminalFixture(t)
	file := filepath.Join(s.cfg.Vaults.Agent, "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte("console:terminal"))))
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	result := consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	if result["status"] != "succeeded" || result["assistant_message"] != nil || result["assistant_message_unavailable"] != true || ha.writes != 1 {
		t.Fatalf("save failure changed control outcome: %v writes=%d", result, ha.writes)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result = consoleJSON(t, consoleRequest(s, "GET", route, "", admin, "", "", ""), 200)
		if result["assistant_message"].(map[string]any)["index"] != float64(0) || ha.writes != 1 {
			t.Fatalf("recovery replayed control or duplicated history: %v writes=%d", result, ha.writes)
		}
	}
}

func TestTerminalMessageWaitsForChatTurnAndUsesExactIndex(t *testing.T) {
	s, admin, csrf, route, ha := terminalFixture(t)
	release, err := s.sessionMgr.AcquireTurn(context.Background(), "console", "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, "") }()
	select {
	case <-done:
		t.Fatal("operation bypassed chat turn")
	case <-time.After(40 * time.Millisecond):
	}
	p, _ := s.consoleStore.Resolve(admin.Value)
	session, _ := s.sessionStore.BrowserSession("console", "terminal", "console:"+p.UserID, false)
	s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "concurrent question"})
	s.sessionMgr.AddMessage(session, core.Message{Role: "assistant", Content: "concurrent reply"})
	if err = s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case response := <-done:
		result := consoleJSON(t, response, 200)
		if result["assistant_message"].(map[string]any)["index"] != float64(2) || ha.writes != 1 {
			t.Fatalf("interleaved index/write: %v writes=%d", result, ha.writes)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("turn never resumed")
	}
}

func TestTerminalPersonaDoesNotHoldAuthenticationLock(t *testing.T) {
	s, admin, csrf, route, ha := terminalFixture(t)
	config, err := assistantconfig.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.assistantConfig = config
	if _, err = config.SavePersonality("custom.md", "# patient style", ""); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	finished := make(chan struct{})
	s.presenter = persona.NewPresenter(personaGatewayClient{chat: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}}, "local")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, "") }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("persona did not run")
	}
	logout := make(chan error, 1)
	go func() { logout <- s.consoleStore.Logout(admin.Value) }()
	select {
	case err = <-logout:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("persona held auth lock")
	}
	select {
	case response := <-done:
		if response.Code != 401 {
			t.Fatalf("revoked request published: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not cancel presentation")
	}
	<-finished
	p := core.CloneSession(s.sessionMgr.Sessions()["console:terminal"])
	if len(p.Messages) != 0 || ha.writes != 1 {
		t.Fatalf("revoked event saved or execution repeated: %d %d", len(p.Messages), ha.writes)
	}
}

func TestDevicePersonalitySwitchPreservesReplayAndMetadata(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { return errors.New("unexpected ordinary chat") }))
	attachRejectedControlIntent(t, s, chain.ErrHAIntentInvalid)
	config, err := assistantconfig.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.assistantConfig = config
	first, err := config.SavePersonality("custom.md", "# first gentle document", "")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.presenter = persona.NewPresenter(personaGatewayClient{chat: func(_ context.Context, b json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		if strings.Contains(string(b), "完整设备名称") || strings.Contains(string(b), "打开台灯") {
			t.Error("fact or query entered persona model")
		}
		tone := "gentle"
		if strings.Contains(string(b), "second formal document") {
			tone = "formal"
		}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": fmt.Sprintf(`{"tone":%q,"rhythm":"plain"}`, tone)}}}})
		return response, nil
	}}, "configured-local")
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	conn := dialConsole(t, ts, admin)
	defer conn.Close()
	if err = conn.WriteJSON(clientMessage{RequestID: "first", Content: "打开台灯"}); err != nil {
		t.Fatal(err)
	}
	var session, reply serverMessage
	readChatMessage(conn, &session)
	if err = readChatMessage(conn, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.EventID == "" || reply.PersonalityRevision != first.Revision || reply.PresentationVersion == "" {
		t.Fatalf("missing metadata: %+v", reply)
	}
	second, err := config.SavePersonality("new.md", "# second formal document", first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	conn.WriteJSON(clientMessage{RequestID: "first", Content: "打开台灯"})
	var replay serverMessage
	readChatMessage(conn, &replay)
	if replay.Content != reply.Content || replay.PersonalityRevision != first.Revision || calls.Load() != 1 {
		t.Fatalf("replay regenerated personality: %+v calls=%d", replay, calls.Load())
	}
	conn.WriteJSON(clientMessage{RequestID: "second", Content: "打开台灯"})
	var next serverMessage
	readChatMessage(conn, &next)
	if next.PersonalityRevision != second.Revision || next.Content == reply.Content || calls.Load() != 2 {
		t.Fatalf("new personality not applied: %+v calls=%d", next, calls.Load())
	}
	principal, _ := s.consoleStore.Resolve(admin.Value)
	messages, err := s.sessionStore.OwnedMessages("console", session.SessionID, "console:"+principal.UserID)
	if err != nil || len(messages) != 4 || messages[1].Content != reply.Content || messages[3].Content != next.Content || messages[1].AssistantMode != "local" {
		t.Fatalf("persisted WS mismatch: %+v %v", messages, err)
	}
}

func TestCancelledControlHasDurableAssistantResult(t *testing.T) {
	s, admin, csrf, route, ha := terminalFixture(t)
	result := consoleJSON(t, consoleRequest(s, "POST", route+"/cancel", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	message := result["assistant_message"].(map[string]any)["message"].(map[string]any)
	if result["status"] != "cancelled" || !strings.Contains(message["content"].(string), "未通过此提议发送") || ha.writes != 0 {
		t.Fatalf("cancellation facts: %v writes=%d", result, ha.writes)
	}
	restored := consoleJSON(t, consoleRequest(s, "GET", route, "", admin, "", "", ""), 200)
	if restored["assistant_message"].(map[string]any)["message"].(map[string]any)["event_id"] != message["event_id"] {
		t.Fatal("cancelled event identity changed")
	}
}
func TestLegacyControlReplayKeepsOriginalHistory(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { return errors.New("unexpected ordinary chat") }))
	ha := &controlGatewayHA{state: "off"}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	defer catalog.Close()
	var err error
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{{EntityID: "light.test", Name: "台灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on"}}}}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.controlChat = core.NewControlChat(s.control, fixedControlIntent{})
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	principal, _ := s.consoleStore.Resolve(admin.Value)
	session, _ := s.sessionStore.BrowserSession("console", "legacy", "console:"+principal.UserID, true)
	ctx := smarthome.WithControlAuthorization(context.Background(), func(context.Context) error { return nil })
	result, err := s.controlChat.Handle(ctx, smarthome.ControlActor{UserID: principal.UserID, SessionID: "legacy"}, "old-request", "打开台灯")
	if err != nil {
		t.Fatal(err)
	}
	s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "打开台灯"})
	s.sessionMgr.AddMessage(session, core.Message{Role: "assistant", Content: "original legacy prose", Attachments: []core.MessageAttachment{{Kind: "control_proposal", ProposalID: result.Proposal.ID}}})
	if err = s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	conn := dialConsole(t, ts, admin)
	defer conn.Close()
	conn.WriteJSON(clientMessage{SessionID: "legacy", RequestID: "old-request", Content: "打开台灯"})
	var frame serverMessage
	readChatMessage(conn, &frame)
	for i := 0; i < 2; i++ {
		frame = serverMessage{}
		if err = readChatMessage(conn, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != "control_proposal" || frame.Content != "original legacy prose" {
			t.Fatalf("legacy replay changed history: %+v", frame)
		}
		if i == 0 {
			conn.WriteJSON(clientMessage{RequestID: "old-request", Content: "打开台灯"})
		}
	}
	messages, _ := s.sessionStore.OwnedMessages("console", "legacy", "console:"+principal.UserID)
	if len(messages) != 2 || ha.writes != 0 {
		t.Fatal("legacy replay appended history or wrote device")
	}
}
func TestPersonalityReadFailureFallsBackToImmutableFact(t *testing.T) {
	dir := t.TempDir()
	config, err := assistantconfig.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = config.SavePersonality("custom.md", "valid", ""); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "personality", "default.md"), []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	presenter := persona.NewPresenter(personaGatewayClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		t.Error("invalid personality reached model")
		return nil, errors.New("unexpected")
	}}, "local")
	msg := presentDevice(context.Background(), presenter, config, "unknown", "fact must remain unchanged", "event")
	if msg.Content != "fact must remain unchanged" || msg.AssistantMode != "local" || msg.PersonalityRevision != "" {
		t.Fatalf("invalid personality damaged factual reply: %+v", msg)
	}
}

type uncertainGatewayHA struct{ controlGatewayHA }

func (h *uncertainGatewayHA) CallService(context.Context, string, string, map[string]interface{}) error {
	h.writes++
	return errors.New("connection ended after send")
}
func TestUnknownReconcileSavesObservationWithoutClaimingSuccessOrRewriting(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	principal, _ := s.consoleStore.Resolve(admin.Value)
	_, err := s.sessionStore.BrowserSession("console", "unknown", "console:"+principal.UserID, true)
	if err != nil {
		t.Fatal(err)
	}
	ha := &uncertainGatewayHA{controlGatewayHA: controlGatewayHA{state: "off"}}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	defer catalog.Close()
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{{EntityID: "light.test", Name: "台灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on"}}}}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outcome := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", `{"session_id":"unknown","request_id":"r","entity_id":"light.test","action":"turn_on"}`, admin, csrf, testConsoleOrigin, ""), 200)
	route := consolePrefix + "/control/proposals/" + outcome["proposal"].(map[string]any)["id"].(string)
	unknown := consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	old := unknown["assistant_message"].(map[string]any)["message"].(map[string]any)
	if unknown["status"] != "unknown" || !strings.Contains(old["content"].(string), "不能确定指令未执行") {
		t.Fatalf("uncertainty changed to factual claim: %v", unknown)
	}
	ha.state = "on"
	current := consoleJSON(t, consoleRequest(s, "POST", route+"/reconcile", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	message := current["assistant_message"].(map[string]any)
	if current["status"] != "unknown" || message["index"] != float64(1) || ha.writes != 1 {
		t.Fatalf("reconcile changed status/index or repeated write: %v writes=%d", current, ha.writes)
	}
	fresh := message["message"].(map[string]any)
	if fresh["event_id"] == old["event_id"] || fresh["device_result"].(map[string]any)["state"] != "on" {
		t.Fatalf("new observation was lost: %v", fresh)
	}
	restored := consoleJSON(t, consoleRequest(s, "GET", route, "", admin, "", "", ""), 200)
	if restored["assistant_message"].(map[string]any)["message"].(map[string]any)["event_id"] != fresh["event_id"] {
		t.Fatal("GET did not recover latest observation")
	}
}

func TestDevicePresentationHonorsGenerationDeadline(t *testing.T) {
	s, _ := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { return nil }))
	attachRejectedControlIntent(t, s, chain.ErrHAIntentInvalid)
	config, err := assistantconfig.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = config.SavePersonality("custom.md", "gentle", ""); err != nil {
		t.Fatal(err)
	}
	presenter := persona.NewPresenter(personaGatewayClient{chat: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return json.RawMessage(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"tone\":\"gentle\",\"rhythm\":\"plain\"}"}}]}`), nil
		}
	}}, "local")
	session, _ := s.sessionStore.BrowserSession("console", "deadline-persona", "owner", true)
	s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "device query"})
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		conn, err := u.Upgrade(writer, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		w := &wsConn{conn: conn, ctx: r.Context(), owner: "owner", sessionID: "deadline-persona", requestID: "deadline", logger: s.logger, sessionMgr: s.sessionMgr, sessionStore: s.sessionStore, options: webChatOptions{ChannelID: "console", AssistantConfig: config, Presenter: presenter, ControlChat: s.controlChat}}
		generation, cancel := context.WithTimeout(r.Context(), 20*time.Millisecond)
		defer cancel()
		w.handleControlChat(generation, "device query", session)
	}))
	defer ts.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frame serverMessage
	if err = readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "error" || frame.Code != "chat_publish_failed" {
		t.Fatalf("expired generation published successful device prose: %+v", frame)
	}
	messages, err := s.sessionStore.OwnedMessages("console", "deadline-persona", "owner")
	if err != nil || len(messages) != 1 {
		t.Fatalf("expired generation persisted a reply: %+v %v", messages, err)
	}
}
