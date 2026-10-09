package core

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"runtime"
	"testing"
	"time"
)

type turnAcquirer interface {
	AcquireTurn(context.Context, string, string) (func(), error)
}

func acquireTestTurn(t *testing.T, m *SessionManager, ctx context.Context, user string) (func(), error) {
	t.Helper()
	api, ok := interface{}(m).(turnAcquirer)
	if !ok {
		t.Fatal("SessionManager must provide cancellable conversation turns")
	}
	return api.AcquireTurn(ctx, "webchat", user)
}
func TestTurnCancellationReleasesWaiters(t *testing.T) {
	m := NewSessionManager(DefaultSessionConfig(), zap.NewNop())
	release, err := acquireTestTurn(t, m, context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		unlock, err := acquireTestTurn(t, m, ctx, "alice")
		if unlock != nil {
			unlock()
		}
		result <- err
	}()
	<-started
	waitTurnRefs(t, m, turnKey{"webchat", "alice"}, 2)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked waiter ignored cancellation")
	}
	// A blocked identity never serializes unrelated conversations.
	other, err := acquireTestTurn(t, m, context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	again, err := acquireTestTurn(t, m, context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	again()
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	if len(m.turns) != 0 {
		t.Fatalf("released/canceled turns remain registered: %d", len(m.turns))
	}
}
func TestIdleScanSkipsInflightTurn(t *testing.T) {
	cfg := DefaultSessionConfig()
	cfg.IdleTimeout = time.Second
	m := NewSessionManager(cfg, zap.NewNop())
	s, _ := m.GetOrCreate("webchat", "alice")
	s.LastActiveAt = time.Now().Add(-time.Hour)
	release, err := acquireTestTurn(t, m, context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	m.scanExpired()
	if CloneSession(s).State != SessionActive {
		t.Fatal("scanner ended a generating conversation")
	}
	select {
	case <-m.EndChan():
		t.Fatal("inflight conversation sent for sedimentation")
	default:
	}
	release()
	m.scanExpired()
	if CloneSession(s).State != SessionEnding {
		t.Fatal("released idle session did not expire")
	}
	select {
	case <-m.EndChan():
	default:
		t.Fatal("expired session not archived")
	}
}

// Wait for registration rather than assuming the blocked goroutine was scheduled.
func waitTurnRefs(t *testing.T, m *SessionManager, key turnKey, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.turnMu.Lock()
		refs := 0
		if slot := m.turns[key]; slot != nil {
			refs = slot.refs
		}
		m.turnMu.Unlock()
		if refs == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("turn did not register %d participants", want)
}
