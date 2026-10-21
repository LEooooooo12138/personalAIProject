package core

import (
	"encoding/json"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPinAssistantModeImmutableAndLegacyLocal(t *testing.T) {
	for _, tc := range []struct {
		name, requested, want string
		messages              []Message
		existing              string
	}{
		{name: "new cloud", requested: "cloud", want: "cloud"},
		{name: "new hybrid", requested: "hybrid", want: "hybrid"},
		{name: "invalid", requested: "unsupported", want: "local"},
		{name: "empty", want: "local"},
		{name: "legacy history", requested: "cloud", want: "local", messages: []Message{{Role: "user", Content: "private earlier history"}}},
		{name: "existing pin", requested: "local", want: "cloud", existing: "cloud"},
		{name: "corrupt persisted pin", requested: "cloud", want: "local", existing: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Messages: tc.messages, AssistantMode: tc.existing}
			if got := s.PinAssistantMode(tc.requested); got != tc.want {
				t.Fatalf("pin: %q, want %q", got, tc.want)
			}
			if got := s.PinAssistantMode("hybrid"); got != tc.want {
				t.Fatalf("pin changed: %q", got)
			}
		})
	}
	s := &Session{}
	var wg sync.WaitGroup
	results := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mode := "cloud"
			if i%2 == 0 {
				mode = "local"
			}
			results <- s.PinAssistantMode(mode)
		}(i)
	}
	wg.Wait()
	close(results)
	first := ""
	for got := range results {
		if first == "" {
			first = got
		}
		if got != first {
			t.Fatal("concurrent callers changed pin")
		}
	}
}

func TestAssistantModeSurvivesCloneAndSessionRestart(t *testing.T) {
	dir := t.TempDir()
	mgr := NewSessionManager(DefaultSessionConfig(), zap.NewNop())
	store := NewSessionStore(mgr, dir, nil, zap.NewNop())
	s, err := store.BrowserSession("console", "conversation", "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	s.PinAssistantMode("hybrid")
	mgr.AddMessage(s, Message{Role: "assistant", Content: "ordinary answer", AssistantMode: "hybrid"})
	clone := CloneSession(s)
	if clone.AssistantMode != "hybrid" || clone.Messages[0].AssistantMode != "hybrid" {
		t.Fatal("clone lost routing provenance")
	}
	if err = store.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	next := NewSessionStore(NewSessionManager(DefaultSessionConfig(), zap.NewNop()), dir, nil, zap.NewNop())
	resumed, err := next.BrowserSession("console", "conversation", "owner", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := resumed.PinAssistantMode("cloud"); got != "hybrid" {
		t.Fatalf("restart changed pin: %q", got)
	}
	messages, err := next.OwnedMessages("console", "conversation", "owner")
	if err != nil || len(messages) != 1 || messages[0].AssistantMode != "hybrid" {
		t.Fatalf("message provenance lost: %+v %v", messages, err)
	}
}

func TestLegacySessionRestoresWithLocalPolicyAndNoSpeechMarker(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"id":"console:legacy","channel_id":"console","user_id":"legacy","owner_id":"owner","messages":[{"role":"assistant","content":"private legacy answer"}]}`)
	if err := os.WriteFile(filepath.Join(dir, sessionFilename("console:legacy")), data, 0600); err != nil {
		t.Fatal(err)
	}
	store := NewSessionStore(NewSessionManager(DefaultSessionConfig(), zap.NewNop()), dir, nil, zap.NewNop())
	s, err := store.BrowserSession("console", "legacy", "owner", false)
	if err != nil {
		t.Fatal(err)
	}
	if mode := s.PinAssistantMode("cloud"); mode != "local" {
		t.Fatalf("legacy history allowed cloud: %q", mode)
	}
	encoded, err := json.Marshal(s.Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["assistant_mode"]; ok {
		t.Fatal("legacy answer acquired speech eligibility")
	}
}
