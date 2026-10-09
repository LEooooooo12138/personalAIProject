package core

import (
	"context"
	"sync"
)

type turnKey struct{ channel, user string }
type turnSlot struct {
	token chan struct{}
	refs  int // holder plus registered waiters; protected by SessionManager.turnMu
}

// AcquireTurn serializes the complete conversation transaction across transports.
// The caller must release on every exit, including failed generation or sending.
// Registration pins the conversation against idle expiry; no data mutex is held
// while waiting, generating, sending, or persisting. Release is idempotent.
func (m *SessionManager) AcquireTurn(ctx context.Context, channelID, userID string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := turnKey{channelID, userID}
	m.turnMu.Lock()
	if m.turns == nil {
		m.turns = make(map[turnKey]*turnSlot)
	}
	slot := m.turns[key]
	if slot == nil {
		slot = &turnSlot{token: make(chan struct{}, 1)}
		m.turns[key] = slot
	}
	slot.refs++
	m.turnMu.Unlock()
	drop := func(held bool) {
		m.turnMu.Lock()
		if held {
			<-slot.token
		}
		slot.refs--
		if slot.refs == 0 {
			delete(m.turns, key)
		}
		m.turnMu.Unlock()
	}
	select {
	case slot.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			drop(true)
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { drop(true) }) }, nil
	case <-ctx.Done():
		drop(false)
		return nil, ctx.Err()
	}
}
