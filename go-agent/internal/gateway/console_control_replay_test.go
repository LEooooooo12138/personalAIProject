package gateway

import (
	"context"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
)

type fixedControlIntent struct{}

func (fixedControlIntent) Parse(context.Context, string, chain.HACandidates) (chain.HAIntent, error) {
	return chain.HAIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"}, nil
}
func TestControlReconnectReplayBeforeSessionRollover(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { t.Error("unexpected ordinary chat"); return nil }))
	s.sessionMgr = core.NewSessionManager(core.SessionConfig{MaxRounds: 1, MaxSessions: 100}, s.logger)
	s.sessionStore = core.NewSessionStore(s.sessionMgr, t.TempDir(), nil, s.logger)
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
	conn := dialConsole(t, ts, admin)
	conn.WriteJSON(clientMessage{RequestID: "same-request", Content: "打开台灯"})
	var event serverMessage
	conn.ReadJSON(&event)
	sid := event.SessionID
	if err = conn.ReadJSON(&event); err != nil || event.Proposal == nil {
		t.Fatalf("first proposal %v %v", event, err)
	}
	id := event.Proposal.ID
	conn.Close()
	retry := dialConsole(t, ts, admin)
	retry.WriteJSON(clientMessage{SessionID: sid, RequestID: "same-request", Content: "打开台灯"})
	retry.ReadJSON(&event)
	if err = retry.ReadJSON(&event); err != nil || event.Type != "control_proposal" || event.Proposal == nil || event.Proposal.ID != id || event.SessionID != sid {
		t.Fatalf("replay moved session or proposal: %+v err %v", event, err)
	}
	p, _ := s.consoleStore.Resolve(admin.Value)
	messages, err := s.sessionStore.OwnedMessages("console", sid, "console:"+p.UserID)
	if err != nil || len(messages) != 2 || ha.writes != 0 {
		t.Fatalf("replay duplicated history or wrote: %d %d %v", len(messages), ha.writes, err)
	}
}
func TestControlReplayRejectsChangedContent(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { return nil }))
	ha := &controlGatewayHA{state: "off"}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	defer catalog.Close()
	var err error
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{{EntityID: "light.test", Name: "台灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on", "turn_off"}}}}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.controlChat = core.NewControlChat(s.control, fixedControlIntent{})
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	conn := dialConsole(t, ts, admin)
	conn.WriteJSON(clientMessage{RequestID: "same-request", Content: "打开台灯"})
	var event serverMessage
	conn.ReadJSON(&event)
	sid := event.SessionID
	conn.ReadJSON(&event)
	conn.Close()
	retry := dialConsole(t, ts, admin)
	retry.WriteJSON(clientMessage{SessionID: sid, RequestID: "same-request", Content: "关闭台灯"})
	event = serverMessage{}
	retry.ReadJSON(&event)
	event = serverMessage{}
	if err = retry.ReadJSON(&event); err != nil || event.Type != "error" || event.Code != "control_conflict" {
		t.Fatalf("changed input replay accepted: %+v err %v", event, err)
	}
	if ha.writes != 0 {
		t.Fatal("replay wrote")
	}
}
