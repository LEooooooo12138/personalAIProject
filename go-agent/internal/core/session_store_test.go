package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockEmbedder implements Embedder for testing.
type mockEmbedder struct {
	vec []float32
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return m.vec, nil
}

func newTestSessionStore(t *testing.T) (*SessionStore, string) {
	tmpDir := t.TempDir()
	cfg := SessionConfig{
		MaxRounds:    20,
		IdleTimeout:  30 * time.Minute,
		ScanInterval: 1 * time.Hour,
		MaxSessions:  50,
	}
	mgr := NewSessionManager(cfg, testLogger())
	mockEmb := &mockEmbedder{vec: make([]float32, 10)}
	store := NewSessionStore(mgr, tmpDir, mockEmb, testLogger())
	return store, tmpDir
}

func TestSaveAndLoadSession(t *testing.T) {
	store, _ := newTestSessionStore(t)

	// Create and populate a session.
	s, err := store.mgr.GetOrCreate("internal", "user1")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	store.mgr.AddMessage(s, Message{Role: "user", Content: "hello", Timestamp: time.Now()})
	store.mgr.AddMessage(s, Message{Role: "assistant", Content: "hi there", Timestamp: time.Now()})

	// Save.
	if err := store.SaveSession(s); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Verify file was written.
	files, err := os.ReadDir(store.sessionsDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("saved session files: %v, %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(store.sessionsDir, files[0].Name()))
	if err != nil {
		t.Fatalf("read saved session: %v", err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("parse session file: %v", err)
	}
	if len(sf.Messages) != 2 {
		t.Errorf("saved messages = %d, want 2", len(sf.Messages))
	}
}

func TestListSessions(t *testing.T) {
	store, _ := newTestSessionStore(t)

	// Save two sessions.
	for i, uid := range []string{"user1", "user2"} {
		s, _ := store.mgr.GetOrCreate("internal", uid)
		store.mgr.AddMessage(s, Message{Role: "user", Content: "msg", Timestamp: time.Now()})
		store.SaveSession(s)

		// To avoid duplicate key issues, end the session.
		store.mgr.EndSession(s)
		<-store.mgr.EndChan()
		store.mgr.CompleteSession(s)

		// Reset sessions map to simulate real scenario where old sessions are cleared.
		if i == 0 {
			store.mgr.mu.Lock()
			store.mgr.sessions = make(map[string]*Session)
			store.mgr.mu.Unlock()
		}
	}

	infos := store.ListSessions("")
	if len(infos) < 2 {
		t.Errorf("ListSessions = %d, want >= 2", len(infos))
	}
}

func TestListSessions_ChannelFilter(t *testing.T) {
	store, _ := newTestSessionStore(t)

	// Save sessions on different channels.
	for _, ch := range []string{"internal", "wecom"} {
		s, _ := store.mgr.GetOrCreate(ch, "user1")
		store.mgr.AddMessage(s, Message{Role: "user", Content: "msg", Timestamp: time.Now()})
		store.SaveSession(s)
		store.mgr.EndSession(s)
		<-store.mgr.EndChan()
		store.mgr.CompleteSession(s)
		store.mgr.mu.Lock()
		store.mgr.sessions = make(map[string]*Session)
		store.mgr.mu.Unlock()
	}

	infos := store.ListSessions("wecom")
	for _, info := range infos {
		if info.ChannelID != "wecom" {
			t.Errorf("filtered session should be wecom, got %s", info.ChannelID)
		}
	}
}

func TestGetMessages_Memory(t *testing.T) {
	store, _ := newTestSessionStore(t)

	s, _ := store.mgr.GetOrCreate("internal", "user1")
	store.mgr.AddMessage(s, Message{Role: "user", Content: "test message", Timestamp: time.Now()})

	msgs := store.GetMessages("internal", "user1")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in memory, got %d", len(msgs))
	}
	if msgs[0].Content != "test message" {
		t.Errorf("content = %q, want test message", msgs[0].Content)
	}
}

func TestGetMessages_DiskFallback(t *testing.T) {
	store, _ := newTestSessionStore(t)

	s, _ := store.mgr.GetOrCreate("internal", "user3")
	store.mgr.AddMessage(s, Message{Role: "user", Content: "disk content", Timestamp: time.Now()})
	store.SaveSession(s)

	// End session (remove from active).
	store.mgr.EndSession(s)
	<-store.mgr.EndChan()
	store.mgr.CompleteSession(s)
	store.mgr.mu.Lock()
	store.mgr.sessions = make(map[string]*Session)
	store.mgr.mu.Unlock()

	msgs := store.GetMessages("internal", "user3")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message from disk, got %d", len(msgs))
	}
	if msgs[0].Content != "disk content" {
		t.Errorf("content = %q, want disk content", msgs[0].Content)
	}
}
