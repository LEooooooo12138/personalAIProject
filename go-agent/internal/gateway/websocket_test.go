package gateway

import (
	"testing"
)

func TestNewSessionID_Unique(t *testing.T) {
	id1 := newSessionID()
	id2 := newSessionID()
	if id1 == id2 {
		t.Error("two generated session IDs should be different")
	}
}

func TestNewSessionID_Length(t *testing.T) {
	id := newSessionID()
	if len(id) < 16 {
		t.Errorf("session ID length = %d, want >= 16", len(id))
	}
}

func TestNewSessionID_Format(t *testing.T) {
	id := newSessionID()
	// Should be hex-encoded (all characters in 0-9a-f).
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("unexpected character in session ID: %c", c)
		}
	}
}