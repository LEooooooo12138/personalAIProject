package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/memory"
)

func TestConsoleExcludedFromGlobalIndexAndSedimentation(t *testing.T) {
	store, dir := newTestSessionStore(t)
	store.infer = &mockEmbedder{vec: []float32{1, 0}}
	s, err := store.BrowserSession("console", "private-sid", "console:alice", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		store.mgr.AddMessage(s, Message{Role: "user", Content: "private family config question?"})
		store.mgr.AddMessage(s, Message{Role: "assistant", Content: "private family answer"})
	}
	if err := store.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	if err := store.IndexMessage(context.Background(), s, 0, CloneSession(s).Messages[0]); err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchSessions(context.Background(), "private", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Error("console entered live index")
	}
	for _, cached := range []bool{false, true} {
		os.Remove(filepath.Join(dir, "embeddings.json"))
		if cached {
			data, _ := json.Marshal(sessionEmbeddingCache{Version: sessionEmbeddingVersion, Messages: []sessionMessage{{SessionID: "console:private-sid", Content: "private cached", Embedding: []float32{1, 0}}}})
			if err := os.WriteFile(filepath.Join(dir, "embeddings.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		cold := NewSessionStore(NewSessionManager(DefaultSessionConfig(), testLogger()), dir, store.infer, testLogger())
		if err := cold.Initialize(context.Background()); err != nil {
			t.Fatal(err)
		}
		hits, err := cold.SearchSessions(context.Background(), "private", 10)
		if err != nil || len(hits) != 0 {
			t.Errorf("cached=%v privacy leak: %v %v", cached, hits, err)
		}
	}
	personal := t.TempDir()
	cfg := &Config{}
	cfg.Vaults.Personal = personal
	a := NewAgent(AgentDeps{Config: cfg, Logger: testLogger(), SessionMgr: store.mgr, Sedimenter: memory.NewSedimenter(personal, consoleSummary{}, testLogger())})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.consumeSessionEnds(ctx) }()
	store.mgr.EndSession(s)
	deadline := time.Now().Add(time.Second)
	for CloneSession(s).State != SessionClosed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if CloneSession(s).State != SessionClosed || store.mgr.ActiveCount() != 0 {
		t.Error("console end did not release session")
	}
	cancel()
	<-done
	files, _ := os.ReadDir(filepath.Join(personal, "_memory"))
	if len(files) != 0 {
		t.Fatalf("private memory written: %v", files)
	}
}

type consoleSummary struct{}

func (consoleSummary) Summarize(context.Context, string) (*memory.StructuredSummary, error) {
	return &memory.StructuredSummary{Title: "private family configuration", Decisions: "private family answer", Confidence: 0.95}, nil
}

func TestSessionInitializeRejectsUnusableDirectory(t *testing.T) {
	parent := t.TempDir()
	blocked := filepath.Join(parent, "file")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewSessionStore(NewSessionManager(DefaultSessionConfig(), testLogger()), filepath.Join(blocked, "sessions"), nil, testLogger())
	if err := store.Initialize(context.Background()); err == nil {
		t.Fatal("unusable persistence accepted")
	}
}
