package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")

// BrowserSession resumes only conversations owned by the authenticated browser.
// create is used exclusively with a fresh server-generated conversation ID.
func (ss *SessionStore) BrowserSession(channelID, userID, owner string, create bool) (*Session, error) {
	if owner == "" {
		return nil, ErrSessionNotFound
	}
	key := sessionKey(channelID, userID)
	ss.mgr.mu.Lock()
	defer ss.mgr.mu.Unlock()
	if s := ss.mgr.sessions[key]; s != nil {
		s.mu.RLock()
		allowed := s.OwnerID == owner && s.State == SessionActive
		s.mu.RUnlock()
		if allowed {
			return s, nil
		}
		if !create {
			return nil, ErrSessionNotFound
		}
	}
	var sf sessionFile
	if !create {
		var err error
		sf, err = ss.loadSessionFile(key)
		if err != nil {
			return nil, err
		}
		if sf.OwnerID != owner || sf.ChannelID != channelID || sf.UserID != userID {
			return nil, ErrSessionNotFound
		}
	}
	if ss.mgr.activeCountLocked() >= ss.mgr.cfg.MaxSessions {
		return nil, errors.New("session limit reached")
	}
	now := time.Now()
	s := &Session{ID: key, ChannelID: channelID, UserID: userID, OwnerID: owner, State: SessionActive, StartedAt: now, CreatedAt: now, LastActiveAt: now}
	if !create {
		s.Messages = sf.Messages
		s.RoundCount = sf.RoundCount
		s.StartedAt = sf.StartedAt
	}
	ss.mgr.sessions[key] = s
	return s, nil
}

// OwnedMessages distinguishes absent/foreign sessions from unavailable storage.
func (ss *SessionStore) OwnedMessages(channelID, userID, owner string) ([]Message, error) {
	if owner == "" {
		return nil, ErrSessionNotFound
	}
	key := sessionKey(channelID, userID)
	ss.mgr.mu.RLock()
	s := ss.mgr.sessions[key]
	if s != nil {
		clone := CloneSession(s)
		ss.mgr.mu.RUnlock()
		if clone.OwnerID != owner {
			return nil, ErrSessionNotFound
		}
		return clone.Messages, nil
	}
	ss.mgr.mu.RUnlock()
	sf, err := ss.loadSessionFile(key)
	if err != nil {
		return nil, err
	}
	if sf.OwnerID != owner || sf.ID != key {
		return nil, ErrSessionNotFound
	}
	return sf.Messages, nil
}

func (ss *SessionStore) loadSessionFile(key string) (sessionFile, error) {
	var sf sessionFile
	data, err := ss.readSessionFile(key)
	if errors.Is(err, os.ErrNotExist) {
		return sf, ErrSessionNotFound
	}
	if err != nil {
		return sf, fmt.Errorf("read session: %w", err)
	}
	if err := json.Unmarshal(data, &sf); err != nil {
		return sf, fmt.Errorf("decode session: %w", err)
	}
	return sf, nil
}

func (ss *SessionStore) ListOwnedSessions(channelID, owner string) []SessionInfo {
	infos := ss.ListSessions(channelID)
	result := make([]SessionInfo, 0, len(infos))
	for _, info := range infos {
		if _, err := ss.OwnedMessages(info.ChannelID, info.UserID, owner); err == nil {
			result = append(result, info)
		}
	}
	return result
}

// ListOwnedSessionsStrict reports unreadable/corrupt history instead of claiming
// an empty list. Read each atomic file once so owner and summary are one snapshot.
func (ss *SessionStore) ListOwnedSessionsStrict(channelID, owner string) ([]SessionInfo, error) {
	result := []SessionInfo{}
	if owner == "" {
		return result, nil
	}
	entries, err := os.ReadDir(ss.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Name() == "embeddings.json" || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ss.sessionsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read session: %w", err)
		}
		var sf sessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			return nil, fmt.Errorf("decode session: %w", err)
		}
		if sf.ID == "" || sf.ChannelID == "" || sf.UserID == "" || sf.ID != sessionKey(sf.ChannelID, sf.UserID) {
			return nil, errors.New("invalid session identity")
		}
		if sf.ChannelID != channelID || sf.OwnerID != owner || seen[sf.ID] {
			continue
		}
		// If a migrated hashed file exists, it is authoritative over legacy history.
		if entry.Name() != sessionFilename(sf.ID) {
			if _, err := os.Stat(filepath.Join(ss.sessionsDir, sessionFilename(sf.ID))); err == nil {
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		seen[sf.ID] = true
		preview := ""
		for _, msg := range sf.Messages {
			if msg.Role == "user" && msg.Content != "" {
				preview = msg.Content
				break
			}
		}
		if len([]rune(preview)) > 50 {
			preview = string([]rune(preview)[:50]) + "..."
		}
		result = append(result, SessionInfo{ID: sf.ID, ChannelID: sf.ChannelID, UserID: sf.UserID, StartedAt: sf.StartedAt, LastActiveAt: sf.LastActiveAt, RoundCount: sf.RoundCount, MessageCount: len(sf.Messages), Preview: preview})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastActiveAt.After(result[j].LastActiveAt) })
	return result, nil
}

func (ss *SessionStore) readSessionFile(key string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(ss.sessionsDir, sessionFilename(key)))
	if err == nil {
		return data, nil
	}
	// A legacy miss must never replace a primary I/O error with not-found.
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// Read pre-migration files only when every filename component is safe.
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ':' || r == '_' || r == '-') {
			return nil, err
		}
	}
	return os.ReadFile(filepath.Join(ss.sessionsDir, strings.ReplaceAll(key, ":", "_")+".json"))
}
