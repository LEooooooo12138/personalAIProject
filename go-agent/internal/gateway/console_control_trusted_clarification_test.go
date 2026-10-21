package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

// Only HA I/O and the model completion are replaced. Intent validation,
// candidate authorization, chat dispatch, presentation and persistence are real.
type trustedClarificationHA struct {
	clarificationGatewayHA
	empty bool
}

func (h *trustedClarificationHA) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	if h.empty {
		return smarthome.RegistrySnapshot{}, nil
	}
	name, study, living := "台灯", "study", "living"
	hidden := "未授权设备"
	return smarthome.RegistrySnapshot{
		Areas: []smarthome.RegistryArea{{ID: study, Name: "书房"}, {ID: living, Name: "客厅"}},
		Entities: []smarthome.RegistryEntity{
			{EntityID: "light.study", Name: &name, AreaID: &study},
			{EntityID: "light.living", Name: &name, AreaID: &living},
			{EntityID: "light.hidden", Name: &hidden, AreaID: &living},
		},
	}, nil
}

func TestControlClarificationUsesServerFactsInWebSocketAndHistory(t *testing.T) {
	for _, tc := range []struct {
		name, query, question string
		empty                 bool
		wantNames             []string
	}{
		{"invented_success", "把客厅台灯处理一下", "已经为你打开了书房灯，还需要什么？", false, []string{"客厅台灯"}},
		{"ambiguous_device", "打开台灯", "你想打开哪一盏台灯？", false, []string{"书房台灯", "客厅台灯"}},
		{"missing_device", "打开未知设备", "你要控制哪个设备？", false, nil},
		{"unsupported_action", "把客厅台灯调暗", "请提供亮度值，我可以调光。", false, []string{"客厅台灯"}},
		{"empty_catalog", "打开台灯", "已经打开了台灯。", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ts := consoleChatFixture(t, chain.NewFuncStep("ordinary-not-expected", func(context.Context, *chain.ChainState) error {
				t.Error("device clarification entered ordinary chat")
				return fmt.Errorf("unexpected ordinary chat")
			}))
			ha := &trustedClarificationHA{empty: tc.empty}
			catalog := smarthome.NewCatalogService(context.Background(), ha)
			t.Cleanup(catalog.Close)
			config := smarthome.ControlConfig{}
			if !tc.empty {
				config.Targets = []smarthome.ControlTarget{
					{EntityID: "light.study", Name: "书房台灯", AreaName: "书房", Aliases: []string{"台灯"}, LoadLocationVerified: true, AllowedActions: []string{"turn_on", "turn_off"}},
					{EntityID: "light.living", Name: "客厅台灯", AreaName: "客厅", Aliases: []string{"台灯"}, LoadLocationVerified: true, AllowedActions: []string{"turn_on", "turn_off"}},
				}
			}
			var err error
			s.control, err = smarthome.NewControlService(config, catalog, ha, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			// Returning raw legal JSON proves this is the clarify branch, not
			// malformed-model fallback. Question must never become a fact.
			parser := chain.NewHAIntentParser(personaGatewayClient{chat: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				intent, err := json.Marshal(map[string]string{"kind": "clarify", "entity_id": "", "action": "", "question": tc.question})
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": string(intent)}}}})
			}}, "fixture-intent-model")
			if intent, err := parser.Parse(context.Background(), tc.query, chain.HACandidates{}); err != nil || intent.Kind != "clarify" || intent.Question != tc.question {
				t.Fatalf("legal clarification JSON was rejected before chat dispatch: %+v %v", intent, err)
			}
			s.controlChat = core.NewControlChat(s.control, parser)
			admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			conn := dialConsole(t, ts, admin)
			if err := conn.WriteJSON(clientMessage{RequestID: "trusted-clarify", Content: tc.query}); err != nil {
				t.Fatal(err)
			}
			var event serverMessage
			if err := readChatMessage(conn, &event); err != nil || event.Type != "session" {
				t.Fatalf("session frame: %+v %v", event, err)
			}
			sid := event.SessionID
			event = serverMessage{}
			if err := readChatMessage(conn, &event); err != nil || event.Type != "response" || event.SessionID != sid || event.RequestID != "trusted-clarify" || event.Proposal != nil || event.DeviceResult != nil {
				t.Fatalf("correlated clarification frame: %+v %v", event, err)
			}
			if ha.writes.Load() != 0 {
				t.Fatalf("clarification performed %d HA writes", ha.writes.Load())
			}

			file := filepath.Join(s.cfg.Vaults.Agent, "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte("console:"+sid))))
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Messages []core.Message `json:"messages"`
			}
			if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Messages) != 2 {
				t.Fatalf("persisted clarification: %+v %v", saved, err)
			}
			message := saved.Messages[1]
			if message.Role != "assistant" || message.Content != event.Content || message.DeviceResult != nil || len(message.Attachments) != 0 {
				t.Fatalf("history does not match safe response: %+v", message)
			}
			for _, content := range []string{event.Content, message.Content} {
				if strings.Contains(content, tc.question) {
					t.Errorf("model clarification became a published or persisted fact: %q", content)
				}
				for _, required := range []string{"完整设备名称", "已配置别名", "查询状态、打开还是关闭", "尚未发送设备控制指令"} {
					if !strings.Contains(content, required) {
						t.Errorf("clarification omits safe next step %q: %q", required, content)
					}
				}
				for _, name := range tc.wantNames {
					if !strings.Contains(content, name) {
						t.Errorf("clarification omits authorized candidate %q: %q", name, content)
					}
				}
				if strings.Contains(content, "未授权设备") || strings.Contains(content, "light.") || (len(tc.wantNames) == 0 && strings.Contains(content, "可用设备名称")) {
					t.Errorf("clarification disclosed an unauthorized or unavailable candidate: %q", content)
				}
			}
		})
	}
}
