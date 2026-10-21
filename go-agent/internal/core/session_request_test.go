package core

import (
	"errors"
	"fmt"
	"testing"
)

func bindEntry(t *testing.T, s *Session, id, content, entry string) error {
	t.Helper()
	return s.BindRequestEntry(id, content, entry)
}
func TestChatEntryRetryConflictSurvivesReload(t *testing.T) {
	store, dir := newTestSessionStore(t)
	s, err := store.BrowserSession("console", "entry", "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindEntry(t, s, "id", "hello", "chat"); err != nil {
		t.Fatal(err)
	}
	clone := CloneSession(s)
	if err = bindEntry(t, clone, "copy", "copy", "chat"); err != nil {
		t.Fatal(err)
	}
	if err = bindEntry(t, s, "copy", "different", "auto"); err != nil {
		t.Fatalf("clone aliases binding: %v", err)
	}
	if err = store.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	cold := NewSessionStore(NewSessionManager(DefaultSessionConfig(), testLogger()), dir, nil, testLogger())
	if _, _, err = cold.SaveOwnedEvent("console", "entry", "owner", Message{EventID: "offline", Role: "assistant", Content: "offline event"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := cold.BrowserSession("console", "entry", "owner", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = bindEntry(t, loaded, "id", "hello", "chat"); err != nil {
		t.Fatal(err)
	}
	if err = bindEntry(t, loaded, "id", "hello", "auto"); !errors.Is(err, ErrRequestEntryConflict) {
		t.Fatal("entry change accepted after reload/offline save")
	}
	if err = bindEntry(t, loaded, "id", "changed", "chat"); err == nil {
		t.Fatal("content change accepted after reload")
	}
}
func TestChatEntryBindingLimitNeverEvicts(t *testing.T) {
	s := &Session{}
	for i := 0; i < 1024; i++ {
		if err := bindEntry(t, s, fmt.Sprint(i), "hello", "chat"); err != nil {
			t.Fatal(err)
		}
	}
	if err := bindEntry(t, s, "overflow", "hello", "chat"); !errors.Is(err, ErrRequestEntryLimit) {
		t.Fatal("limit ignored")
	}
	if err := bindEntry(t, s, "0", "hello", "chat"); err != nil {
		t.Fatal(err)
	}
	if err := bindEntry(t, s, "0", "hello", "auto"); err == nil {
		t.Fatal("old binding evicted")
	}
}
func TestChatEntryLegacyEventDefaultsAuto(t *testing.T) {
	for _, messages := range [][]Message{{{Role: "user", Content: "old"}, {Role: "assistant", EventID: "request:id"}}, {{Role: "assistant", EventID: "request:id"}}} {
		s := &Session{Messages: messages}
		if err := bindEntry(t, s, "id", "old", "chat"); err == nil {
			t.Fatal("legacy auto event accepted chat")
		}
		if len(messages) == 2 {
			if err := bindEntry(t, s, "id", "old", ""); err != nil {
				t.Fatal(err)
			}
		} else if err := bindEntry(t, s, "id", "old", "auto"); err == nil {
			t.Fatal("ambiguous legacy content accepted")
		}
	}
}

func TestChatEntryInvalidAndLegacyMissingID(t *testing.T) {
	s := &Session{}
	if err := s.BindRequestEntry("", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if len(s.RequestEntries) != 0 {
		t.Fatal("missing legacy ID consumed binding")
	}
	if err := s.BindRequestEntry("", "hello", "chat"); !errors.Is(err, ErrRequestEntryInvalid) {
		t.Fatal("explicit chat accepted missing ID")
	}
	if err := s.BindRequestEntry("id", "hello", "other"); !errors.Is(err, ErrRequestEntryInvalid) {
		t.Fatal("unknown entry accepted")
	}
}

// Catches accepting a legacy binding despite ambiguous identity or content.
func TestChatEntryLegacyAssociationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []Message
	}{
		{"duplicate_matching_events", []Message{{Role: "user", Content: "old"}, {Role: "assistant", EventID: "request:id"}, {Role: "user", Content: "old"}, {Role: "assistant", EventID: "request:id"}}},
		{"non_user_predecessor", []Message{{Role: "assistant", Content: "old"}, {Role: "assistant", EventID: "request:id"}}},
		{"adjacent_user_content_mismatch", []Message{{Role: "user", Content: "different"}, {Role: "assistant", EventID: "request:id"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Messages: tc.messages}
			if err := s.BindRequestEntry("id", "old", "auto"); !errors.Is(err, ErrRequestEntryConflict) {
				t.Fatalf("unsafe legacy association accepted: %v", err)
			}
			if len(CloneSession(s).RequestEntries) != 0 {
				t.Fatal("rejected legacy request published a binding")
			}
		})
	}
}
