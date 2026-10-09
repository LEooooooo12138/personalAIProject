package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type rejectedGatewayIntent struct{ err error }

func (p rejectedGatewayIntent) Parse(context.Context, string, chain.HACandidates) (chain.HAIntent, error) {
	return chain.HAIntent{}, p.err
}

type clarificationGatewayHA struct{ writes atomic.Int32 }

func (*clarificationGatewayHA) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	return smarthome.RegistrySnapshot{}, nil
}
func (*clarificationGatewayHA) GetStates(context.Context) ([]smarthome.EntityState, error) {
	return nil, nil
}
func (*clarificationGatewayHA) GetState(context.Context, string) (*smarthome.EntityState, error) {
	return nil, errors.New("unexpected state read")
}
func (h *clarificationGatewayHA) CallService(context.Context, string, string, map[string]interface{}) error {
	h.writes.Add(1)
	return errors.New("unexpected service call")
}

func attachRejectedControlIntent(t *testing.T, s *Server, parseErr error) *clarificationGatewayHA {
	t.Helper()
	ha := &clarificationGatewayHA{}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	t.Cleanup(catalog.Close)
	var err error
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.controlChat = core.NewControlChat(s.control, rejectedGatewayIntent{parseErr})
	return ha
}

func TestControlInvalidIntentPublishesPersistedClarification(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error {
		t.Error("invalid device intent entered ordinary chat")
		return nil
	}))
	ha := attachRejectedControlIntent(t, s, chain.ErrHAIntentInvalid)
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	conn := dialConsole(t, ts, admin)
	if err := conn.WriteJSON(clientMessage{RequestID: "rejected-request", Content: "打开书房灯"}); err != nil {
		t.Fatal(err)
	}
	var event serverMessage
	if err := conn.ReadJSON(&event); err != nil || event.Type != "session" {
		t.Fatalf("session frame: %+v %v", event, err)
	}
	sid := event.SessionID
	event = serverMessage{}
	if err := conn.ReadJSON(&event); err != nil || event.Type != "response" || event.SessionID != sid || event.RequestID != "rejected-request" || event.Proposal != nil || event.DeviceResult != nil {
		t.Fatalf("invalid intent did not finish with a correlated clarification: %+v %v", event, err)
	}
	if !strings.Contains(event.Content, "完整设备名称") || !strings.Contains(event.Content, "已配置别名") || ha.writes.Load() != 0 {
		t.Fatalf("unsafe clarification or HA write: %+v writes=%d", event, ha.writes.Load())
	}
	file := filepath.Join(s.cfg.Vaults.Agent, "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte("console:"+sid))))
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Messages []core.Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Messages) != 2 || saved.Messages[1].Role != "assistant" || saved.Messages[1].Content != event.Content || len(saved.Messages[1].Attachments) != 0 {
		t.Fatalf("clarification was not safely persisted: %+v %v", saved, err)
	}
}

func TestControlClarificationSaveFailureRemainsUncertain(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error {
		t.Error("invalid device intent entered ordinary chat")
		return nil
	}))
	ha := attachRejectedControlIntent(t, s, chain.ErrHAIntentInvalid)
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	p, err := s.consoleStore.Resolve(admin.Value)
	if err != nil {
		t.Fatal(err)
	}
	const sid = "clarification-save-failure"
	if _, err := s.sessionStore.BrowserSession("console", sid, "console:"+p.UserID, true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(s.cfg.Vaults.Agent, "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte("console:"+sid))))
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	conn := dialConsole(t, ts, admin)
	if err := conn.WriteJSON(clientMessage{SessionID: sid, RequestID: "save-failure", Content: "打开书房灯"}); err != nil {
		t.Fatal(err)
	}
	var event serverMessage
	if err := conn.ReadJSON(&event); err != nil || event.Type != "session" {
		t.Fatalf("session frame: %+v %v", event, err)
	}
	event = serverMessage{}
	if err := conn.ReadJSON(&event); err != nil || event.Type != "error" || event.Content != "" || event.Proposal != nil || event.DeviceResult != nil || !strings.Contains(event.Message, "未能保存") || ha.writes.Load() != 0 {
		t.Fatalf("failed persistence became a settled response: %+v %v writes=%d", event, err, ha.writes.Load())
	}
}

func TestControlDispatchLogsOnlyBoundedErrorClass(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		category string
	}{
		{"model_unavailable", fmt.Errorf("private credential detail: %w", chain.ErrHAIntentUnavailable), "intent_unavailable"},
		{"deadline", fmt.Errorf("private credential detail: %w", context.DeadlineExceeded), "deadline_exceeded"},
		{"unexpected", errors.New("private credential detail"), "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ts := consoleChatFixture(t, chain.NewFuncStep("unused", func(context.Context, *chain.ChainState) error { return nil }))
			ha := attachRejectedControlIntent(t, s, tc.err)
			logCore, logs := observer.New(zap.WarnLevel)
			s.logger = zap.New(logCore)
			admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
			conn := dialConsole(t, ts, admin)
			if err := conn.WriteJSON(clientMessage{RequestID: "safe-request", Content: "private query content"}); err != nil {
				t.Fatal(err)
			}
			var event serverMessage
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			event = serverMessage{}
			if err := conn.ReadJSON(&event); err != nil || event.Type != "error" || event.Code != "control_unavailable" || ha.writes.Load() != 0 {
				t.Fatalf("changed dispatch failure: %+v %v", event, err)
			}
			entries := logs.FilterMessage("console device request failed").All()
			if len(entries) != 1 {
				t.Fatalf("expected one classified dispatch log, got %d", len(entries))
			}
			fields := entries[0].ContextMap()
			if len(fields) != 1 || fields["error_class"] != tc.category {
				t.Fatalf("unsafe or missing error category: %+v", fields)
			}
			serialized, err := json.Marshal(entries)
			if err != nil || strings.Contains(string(serialized), "private") {
				t.Fatalf("log disclosed raw input/error: %s %v", serialized, err)
			}
		})
	}
}
