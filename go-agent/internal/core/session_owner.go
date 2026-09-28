package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
		data, err := ss.readSessionFile(key)
		if err != nil || json.Unmarshal(data, &sf) != nil || sf.OwnerID != owner || sf.ChannelID != channelID || sf.UserID != userID {
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

func (ss *SessionStore) OwnedMessages(channelID, userID, owner string) ([]Message, bool) {
	key := sessionKey(channelID, userID)
	ss.mgr.mu.RLock()
	s := ss.mgr.sessions[key]
	if s != nil {
		clone := CloneSession(s)
		ss.mgr.mu.RUnlock()
		if clone.OwnerID != owner {
			return nil, false
		}
		return clone.Messages, true
	}
	ss.mgr.mu.RUnlock()
	data, err := ss.readSessionFile(key)
	var sf sessionFile
	if err != nil || json.Unmarshal(data, &sf) != nil || sf.OwnerID != owner || sf.ID != key {
		return nil, false
	}
	return sf.Messages, true
}

func (ss *SessionStore) ListOwnedSessions(channelID, owner string) []SessionInfo {
	infos := ss.ListSessions(channelID)
	result := make([]SessionInfo, 0, len(infos))
	for _, info := range infos {
		if _, ok := ss.OwnedMessages(info.ChannelID, info.UserID, owner); ok {
			result = append(result, info)
		}
	}
	return result
}

func (ss *SessionStore) readSessionFile(key string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(ss.sessionsDir, sessionFilename(key)))
	if err == nil {
		return data, nil
	}
	// Read pre-migration files only when every filename component is safe.
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ':' || r == '_' || r == '-') {
			return nil, err
		}
	}
	return os.ReadFile(filepath.Join(ss.sessionsDir, strings.ReplaceAll(key, ":", "_")+".json"))
}
