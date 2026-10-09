package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"strings"
	"testing"
)

type downControlCatalog struct{}

func (downControlCatalog) Get(context.Context) (smarthome.CatalogSnapshot, error) {
	return smarthome.CatalogSnapshot{}, errors.New("offline")
}

type rejectedControlIntent struct{ err error }

func (p rejectedControlIntent) Parse(context.Context, string, chain.HACandidates) (chain.HAIntent, error) {
	return chain.HAIntent{}, p.err
}

func TestControlChatClarifiesInvalidIntentWithoutProposing(t *testing.T) {
	for _, parseErr := range []error{chain.ErrHAIntentInvalid, fmt.Errorf("private model detail: %w", chain.ErrHAIntentInvalid)} {
		svc, err := smarthome.NewControlService(smarthome.ControlConfig{}, downControlCatalog{}, unusedControlHA{}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		actor := smarthome.ControlActor{UserID: "a", SessionID: "s"}
		result, err := NewControlChat(svc, rejectedControlIntent{parseErr}).Handle(context.Background(), actor, "request", "打开书房灯")
		if err != nil || result == nil || result.Type != "response" || result.Proposal != nil || result.DeviceResult != nil {
			t.Fatalf("invalid intent was not a safe clarification: result=%+v err=%v", result, err)
		}
		if !strings.Contains(result.Content, "完整设备名称") || !strings.Contains(result.Content, "已配置别名") || strings.Contains(result.Content, "private model detail") {
			t.Fatalf("clarification does not explain safe target selection: %q", result.Content)
		}
		if outcome, err := svc.Replay(context.Background(), actor, "request", ""); err != nil || outcome != nil {
			t.Fatalf("invalid intent persisted a control result: %+v %v", outcome, err)
		}
	}
}

func TestControlChatKeepsOtherParserErrors(t *testing.T) {
	for _, parseErr := range []error{chain.ErrHAIntentUnavailable, context.Canceled, context.DeadlineExceeded, errors.New("private model error")} {
		svc, err := smarthome.NewControlService(smarthome.ControlConfig{}, downControlCatalog{}, unusedControlHA{}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		result, err := NewControlChat(svc, rejectedControlIntent{parseErr}).Handle(context.Background(), smarthome.ControlActor{UserID: "a", SessionID: "s"}, "request", "打开书房灯")
		if result != nil || !errors.Is(err, parseErr) {
			t.Fatalf("changed non-validation error: result=%+v err=%v", result, err)
		}
	}
}

type cancelledControlIntent struct{ cancel context.CancelFunc }

func (p cancelledControlIntent) Parse(context.Context, string, chain.HACandidates) (chain.HAIntent, error) {
	p.cancel()
	return chain.HAIntent{}, chain.ErrHAIntentInvalid
}

func TestControlChatDoesNotClarifyAfterCancellation(t *testing.T) {
	svc, err := smarthome.NewControlService(smarthome.ControlConfig{}, downControlCatalog{}, unusedControlHA{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := NewControlChat(svc, cancelledControlIntent{cancel}).Handle(ctx, smarthome.ControlActor{UserID: "a", SessionID: "s"}, "request", "打开书房灯")
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request produced clarification: result=%+v err=%v", result, err)
	}
}

type clarificationCandidateRegistry struct{}

func (clarificationCandidateRegistry) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	area := "study"
	registry := smarthome.RegistrySnapshot{Areas: []smarthome.RegistryArea{{ID: area, Name: "书房"}}}
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("控制器开关%d", i)
		registry.Entities = append(registry.Entities, smarthome.RegistryEntity{EntityID: fmt.Sprintf("switch.channel_%d", i), Name: &name, AreaID: &area})
	}
	return registry, nil
}
func (clarificationCandidateRegistry) GetStates(context.Context) ([]smarthome.EntityState, error) {
	return nil, nil
}

func TestControlChatClarificationListsAtMostThreeAuthorizedNames(t *testing.T) {
	catalog := smarthome.NewCatalogService(context.Background(), clarificationCandidateRegistry{})
	defer catalog.Close()
	var targets []smarthome.ControlTarget
	for i := 1; i <= 4; i++ {
		targets = append(targets, smarthome.ControlTarget{EntityID: fmt.Sprintf("switch.channel_%d", i), Name: fmt.Sprintf("书房授权开关%d（授权通道测试）", i), AreaName: "登记区域", AllowedActions: []string{"turn_on", "turn_off"}, SwitchTestAuthorized: true})
	}
	svc, err := smarthome.NewControlService(smarthome.ControlConfig{Targets: targets}, catalog, unusedControlHA{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewControlChat(svc, rejectedControlIntent{chain.ErrHAIntentInvalid}).Handle(context.Background(), smarthome.ControlActor{UserID: "a", SessionID: "s"}, "request", "打开书房灯")
	if err != nil || result == nil || result.Type != "response" || result.Proposal != nil {
		t.Fatalf("missing clarification: result=%+v err=%v", result, err)
	}
	for _, name := range []string{"书房授权开关1（授权通道测试）", "书房授权开关2（授权通道测试）", "书房授权开关3（授权通道测试）"} {
		if !strings.Contains(result.Content, name) {
			t.Errorf("clarification omits authorized display name %q: %s", name, result.Content)
		}
	}
	if strings.Contains(result.Content, "书房授权开关4") || strings.Contains(result.Content, "switch.channel_") || strings.Contains(result.Content, "控制器开关") {
		t.Fatalf("clarification exceeds bounded public names: %s", result.Content)
	}
}

type unusedControlHA struct{}

func (unusedControlHA) GetState(context.Context, string) (*smarthome.EntityState, error) {
	panic("unexpected HA read")
}
func (unusedControlHA) CallService(context.Context, string, string, map[string]interface{}) error {
	panic("unexpected HA write")
}

type chatIntentStub struct{ called bool }

func (p *chatIntentStub) Parse(_ context.Context, _ string, c chain.HACandidates) (chain.HAIntent, error) {
	p.called = true
	if len(c.Query)+len(c.Control) != 0 {
		panic("unexpected targets")
	}
	return chain.HAIntent{Kind: "chat"}, nil
}
func TestControlChatAllowsOrdinaryChatDuringHAOutage(t *testing.T) {
	svc, err := smarthome.NewControlService(smarthome.ControlConfig{}, downControlCatalog{}, unusedControlHA{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := &chatIntentStub{}
	result, err := NewControlChat(svc, parser).Handle(context.Background(), smarthome.ControlActor{UserID: "a", SessionID: "s"}, "request", "你好")
	if err != nil || result != nil || !parser.called {
		t.Fatalf("ordinary chat blocked result=%v err=%v called=%v", result, err, parser.called)
	}
}
