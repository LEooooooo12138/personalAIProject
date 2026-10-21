package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

var ErrRequestEntryConflict = errors.New("request entry conflict")
var ErrRequestEntryLimit = errors.New("request entry limit")
var ErrRequestEntryInvalid = errors.New("invalid request entry")

// RequestEntryBinding persists only capability and an exact-content digest.
// It is a binding, not a promise to deduplicate ordinary model generation.
type RequestEntryBinding struct {
	Entry         string `json:"entry"`
	ContentSHA256 string `json:"content_sha256"`
}

// BindRequestEntry must be persisted under the caller's turn lease before work.
// Bindings live as long as the conversation; reaching the cap never evicts IDs.
func (s *Session) BindRequestEntry(requestID, content, entry string) error {
	if entry == "" {
		entry = "auto"
	}
	if entry != "auto" && entry != "chat" {
		return ErrRequestEntryInvalid
	}
	if requestID == "" {
		if entry == "auto" {
			return nil
		}
		return ErrRequestEntryInvalid
	}
	sum := sha256.Sum256([]byte(content))
	binding := RequestEntryBinding{Entry: entry, ContentSHA256: hex.EncodeToString(sum[:])}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.RequestEntries[requestID]; ok {
		if old != binding {
			return ErrRequestEntryConflict
		}
		return nil
	}
	// Pre-binding durable request events have auto capability. Only the existing
	// adjacent user/event ordering can establish their content; ambiguity fails closed.
	found := false
	for i, msg := range s.Messages {
		if msg.EventID != "request:"+requestID {
			continue
		}
		if found || entry != "auto" || msg.Role != "assistant" || i == 0 || s.Messages[i-1].Role != "user" || s.Messages[i-1].Content != content {
			return ErrRequestEntryConflict
		}
		found = true
	}
	if len(s.RequestEntries) >= 1024 {
		return ErrRequestEntryLimit
	}
	if s.RequestEntries == nil {
		s.RequestEntries = make(map[string]RequestEntryBinding)
	}
	s.RequestEntries[requestID] = binding
	return nil
}
