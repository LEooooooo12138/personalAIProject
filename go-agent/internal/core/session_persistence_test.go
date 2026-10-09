package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionAtomicSave(t *testing.T) {
	store, dir := newTestSessionStore(t)
	s, err := store.BrowserSession("console", "saved", "console:alice", true)
	if err != nil {
		t.Fatal(err)
	}
	store.mgr.AddMessage(s, Message{Role: "user", Content: "old history"})
	if err := store.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	// An existing reader's inode must retain the entire old document after replace.
	path := filepath.Join(dir, sessionFilename("console:saved"))
	snapshot := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.Link(path, snapshot); err != nil {
		t.Fatal(err)
	}
	store.mgr.AddMessage(s, Message{Role: "assistant", Content: "new answer"})
	if err := store.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var old sessionFile
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	if len(old.Messages) != 1 || old.Messages[0].Content != "old history" {
		t.Fatalf("save overwrote old inode instead of atomic replacement: %v", old.Messages)
	}
	cold := NewSessionStore(NewSessionManager(DefaultSessionConfig(), testLogger()), dir, nil, testLogger())
	messages, err := cold.OwnedMessages("console", "saved", "console:alice")
	if err != nil || len(messages) != 2 {
		t.Fatalf("cold history: %v %v", messages, err)
	}
}

func TestSessionAtomicSaveFailuresPreserveHistory(t *testing.T) {
	for _, failure := range []string{"write", "short-write", "replace"} {
		t.Run(failure, func(t *testing.T) {
			store, dir := newTestSessionStore(t)
			s, err := store.BrowserSession("console", "saved", "console:alice", true)
			if err != nil {
				t.Fatal(err)
			}
			store.mgr.AddMessage(s, Message{Role: "user", Content: "committed history"})
			if err := store.SaveSession(s); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "write":
				store.writeSession = func(f *os.File, data []byte) (int, error) {
					n, _ := f.Write(data[:len(data)/2])
					return n, errors.New("injected write failure")
				}
			case "short-write":
				store.writeSession = func(f *os.File, data []byte) (int, error) { return f.Write(data[:len(data)/2]) }
			case "replace":
				store.replaceSession = func(string, string) error { return errors.New("injected replacement failure") }
			}
			store.mgr.AddMessage(s, Message{Role: "assistant", Content: "uncommitted answer"})
			if err := store.SaveSession(s); err == nil {
				t.Fatal("injected save failure ignored")
			}
			cold := NewSessionStore(NewSessionManager(DefaultSessionConfig(), testLogger()), dir, nil, testLogger())
			messages, err := cold.OwnedMessages("console", "saved", "console:alice")
			if err != nil || len(messages) != 1 || messages[0].Content != "committed history" {
				t.Fatalf("old history damaged: %v %v", messages, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary file leak: %v %v", entries, err)
			}
		})
	}
}

func TestSessionBootstrapRejectsUnusableDirectory(t *testing.T) {
	path := writeConsoleConfig(t, "key", "https://family.example", t.TempDir(), "", false)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.Vaults.Agent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Vaults.Agent, "_sessions"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(path); err == nil {
		t.Fatal("critical session storage failure only logged as warning")
	}
}

func TestSessionLegacyHistoryAndOfflineEmbeddings(t *testing.T) {
	store, dir := newTestSessionStore(t)
	data := []byte(`{"id":"webchat:old","channel_id":"webchat","user_id":"old","owner_id":"browser","messages":[{"role":"user","content":"legacy saved history"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "webchat_old.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store.infer = unavailableSessionEmbedder{}
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	messages, err := store.OwnedMessages("webchat", "old", "browser")
	if err != nil || len(messages) != 1 {
		t.Fatalf("legacy cold read: %v %v", messages, err)
	}
}

type unavailableSessionEmbedder struct{}

func (unavailableSessionEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errors.New("local model offline")
}
