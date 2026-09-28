package core

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	return zap.NewNop()
}

func testSessionConfig() SessionConfig {
	return SessionConfig{
		MaxRounds:    5,
		IdleTimeout:  30 * time.Minute,
		ScanInterval: 1 * time.Hour, // don't scan during tests
		MaxSessions:  50,
	}
}

func TestGetOrCreate_New(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, err := mgr.GetOrCreate("internal", "user1")

	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if s == nil {
		t.Fatal("session is nil")
	}
	if s.State != SessionActive {
		t.Errorf("State = %s, want active", s.State)
	}
	if s.ChannelID != "internal" {
		t.Errorf("ChannelID = %s, want internal", s.ChannelID)
	}
	if s.UserID != "user1" {
		t.Errorf("UserID = %s, want user1", s.UserID)
	}
	if s.RoundCount != 0 {
		t.Errorf("RoundCount = %d, want 0", s.RoundCount)
	}
}

func TestGetOrCreate_Existing(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s1, _ := mgr.GetOrCreate("internal", "user1")
	s2, _ := mgr.GetOrCreate("internal", "user1")

	if s1 != s2 {
		t.Error("GetOrCreate should return the same session pointer")
	}
}

func TestGetOrCreate_DifferentChannels(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s1, _ := mgr.GetOrCreate("internal", "user1")
	s2, _ := mgr.GetOrCreate("wecom", "user1")

	if s1 == s2 {
		t.Error("different channels should have different sessions")
	}
	if s1.ID == s2.ID {
		t.Error("different channels should have different session IDs")
	}
}

func TestGetOrCreate_EndedCreatesNew(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s1, _ := mgr.GetOrCreate("internal", "user1")
	mgr.EndSession(s1)
	// After end, a new GetOrCreate should create a new session (old one is Ending).
	s2, err := mgr.GetOrCreate("internal", "user1")
	if err != nil {
		t.Fatalf("GetOrCreate after end: %v", err)
	}
	if s1 == s2 {
		t.Error("should create new session after previous one ended")
	}
}

func TestAddMessage_User(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")

	ok := mgr.AddMessage(s, Message{Role: "user", Content: "hello", Timestamp: time.Now()})
	if !ok {
		t.Error("AddMessage should return true")
	}
	if s.RoundCount != 1 {
		t.Errorf("RoundCount = %d, want 1", s.RoundCount)
	}
}

func TestAddMessage_Assistant(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")

	mgr.AddMessage(s, Message{Role: "assistant", Content: "hi", Timestamp: time.Now()})
	if s.RoundCount != 0 {
		t.Errorf("assistant message should not increase RoundCount, got %d", s.RoundCount)
	}
}

func TestAddMessage_RoundLimit(t *testing.T) {
	cfg := testSessionConfig()
	cfg.MaxRounds = 3
	mgr := NewSessionManager(cfg, testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")

	// 3 user messages should be allowed.
	for i := 0; i < 3; i++ {
		ok := mgr.AddMessage(s, Message{Role: "user", Content: "msg", Timestamp: time.Now()})
		if !ok {
			t.Errorf("round %d: expected true", i+1)
		}
	}
	if mgr.AddMessage(s, Message{Role: "user", Content: "excess"}) {
		t.Error("round 4 must start a new conversation")
	}
}

func TestAddMessage_NotActive(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")
	mgr.EndSession(s)

	ok := mgr.AddMessage(s, Message{Role: "user", Content: "hello", Timestamp: time.Now()})
	if ok {
		t.Error("AddMessage should return false for non-active session")
	}
}

func TestEndSession_StateTransition(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")

	mgr.EndSession(s)

	s.mu.RLock()
	state := s.State
	s.mu.RUnlock()
	if state != SessionEnding {
		t.Errorf("State = %s, want ending", state)
	}
}

func TestEndSession_ChannelNotification(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")

	mgr.EndSession(s)

	select {
	case received := <-mgr.EndChan():
		if received != s {
			t.Error("EndChan received wrong session")
		}
	default:
		t.Error("EndChan should receive the ended session (non-blocking)")
	}
}

func TestCompleteSession(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")

	mgr.EndSession(s)
	// Drain EndChan.
	<-mgr.EndChan()

	mgr.CompleteSession(s)

	s.mu.RLock()
	state := s.State
	s.mu.RUnlock()
	if state != SessionClosed {
		t.Errorf("State = %s, want closed", state)
	}
}

func TestCloneSession_DeepCopy(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())
	s, _ := mgr.GetOrCreate("internal", "user1")
	mgr.AddMessage(s, Message{Role: "user", Content: "original", Timestamp: time.Now()})

	clone := CloneSession(s)

	// Modify original messages.
	s.mu.Lock()
	s.Messages[0].Content = "modified"
	s.mu.Unlock()

	// Clone should not be affected.
	if clone.Messages[0].Content != "original" {
		t.Errorf("clone content = %q, want original (deep copy failed)", clone.Messages[0].Content)
	}
}

func TestActiveCount(t *testing.T) {
	mgr := NewSessionManager(testSessionConfig(), testLogger())

	if mgr.ActiveCount() != 0 {
		t.Errorf("initial ActiveCount = %d, want 0", mgr.ActiveCount())
	}

	mgr.GetOrCreate("internal", "user1")
	mgr.GetOrCreate("internal", "user2")
	mgr.GetOrCreate("wecom", "user1")

	if mgr.ActiveCount() != 3 {
		t.Errorf("ActiveCount = %d, want 3", mgr.ActiveCount())
	}
}

func TestSessionLimit(t *testing.T) {
	cfg := testSessionConfig()
	cfg.MaxSessions = 2
	mgr := NewSessionManager(cfg, testLogger())

	mgr.GetOrCreate("internal", "user1")
	mgr.GetOrCreate("internal", "user2")

	_, err := mgr.GetOrCreate("internal", "user3")
	if err == nil {
		t.Error("expected error when exceeding session limit")
	}
}
