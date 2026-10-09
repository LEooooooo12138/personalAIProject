package core

import (
	"context"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
)

type downControlCatalog struct{}

func (downControlCatalog) Get(context.Context) (smarthome.CatalogSnapshot, error) {
	return smarthome.CatalogSnapshot{}, errors.New("offline")
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
