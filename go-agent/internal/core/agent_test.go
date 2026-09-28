package core

import (
	"testing"
	"time"
)

func TestSessionToConversation_Basic(t *testing.T) {
	s := &Session{
		ID:           "internal:user1",
		ChannelID:    "internal",
		UserID:       "user1",
		State:        SessionActive,
		Messages: []Message{
			{Role: "user", Content: "hello", Timestamp: time.Now()},
			{Role: "assistant", Content: "hi there", Timestamp: time.Now()},
			{Role: "user", Content: "question", Timestamp: time.Now()},
			{Role: "assistant", Content: "answer", Timestamp: time.Now()},
		},
		StartedAt:    time.Now().Add(-5 * time.Minute),
		LastActiveAt: time.Now(),
	}

	conv := sessionToConversation(s)

	if len(conv.Messages) != 4 {
		t.Errorf("Messages length = %d, want 4", len(conv.Messages))
	}
	if conv.ChannelID != "internal" {
		t.Errorf("ChannelID = %s, want internal", conv.ChannelID)
	}
	if conv.UserID != "user1" {
		t.Errorf("UserID = %s, want user1", conv.UserID)
	}
	// Verify role order.
	expectedRoles := []string{"user", "assistant", "user", "assistant"}
	for i, role := range expectedRoles {
		if conv.Messages[i].Role != role {
			t.Errorf("Messages[%d].Role = %s, want %s", i, conv.Messages[i].Role, role)
		}
	}
}

func TestSessionToConversation_Empty(t *testing.T) {
	s := &Session{
		ID:        "internal:user1",
		ChannelID: "internal",
		UserID:    "user1",
		Messages:  []Message{},
	}

	conv := sessionToConversation(s)
	if len(conv.Messages) != 0 {
		t.Errorf("Messages length = %d, want 0", len(conv.Messages))
	}
}

func TestNewAgent_Initialization(t *testing.T) {
	// Verify Agent can be created without panicking (minimal deps).
	a := NewAgent(AgentDeps{})
	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	// All fields should be zero-valued / nil.
	if a.cfg != nil {
		t.Error("cfg should be nil with empty deps")
	}
	if a.logger != nil {
		t.Error("logger should be nil with empty deps")
	}
}