package smarthome

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"testing"
	"time"
)

type contextIgnoring interface {
	IgnoreSuggestionContext(context.Context, string) (*RuleSuggestion, error)
}

func TestIgnoreSuggestionChecksCancellationAfterWaitingForAction(t *testing.T) {
	m, err := NewManager(HAConfig{AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	if err = m.store.SaveSuggestions([]RuleSuggestion{{ID: "pending", Status: "pending"}}); err != nil {
		t.Fatal(err)
	}
	ignoring, ok := any(m).(contextIgnoring)
	if !ok {
		t.Fatal("ignore has no cancellation-aware entry point")
	}
	m.actionMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := ignoring.IgnoreSuggestionContext(ctx, "pending"); done <- err }()
	cancel()
	m.actionMu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ignore did not finish")
	}
	item, err := m.findSuggestion("pending")
	if err != nil || item.Status != "pending" {
		t.Fatal("cancelled ignore persisted")
	}
	if _, err = m.IgnoreSuggestion("pending"); err != nil {
		t.Fatal("old interface broke:", err)
	}
}
