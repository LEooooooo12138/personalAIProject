package core

import (
	"errors"
	"maps"
	"time"
)

func cloneMessage(msg Message) Message {
	msg.Attachments = append([]MessageAttachment(nil), msg.Attachments...)
	if msg.DeviceResult != nil {
		result := *msg.DeviceResult
		if result.DeviceClass != nil {
			v := *result.DeviceClass
			result.DeviceClass = &v
		}
		if result.Unit != nil {
			v := *result.Unit
			result.Unit = &v
		}
		msg.DeviceResult = &result
	}
	return msg
}

// OwnedEvent only returns durable events. Callers serialize writes with AcquireTurn.
func (ss *SessionStore) OwnedEvent(channel, user, owner, event string) (int, *Message, error) {
	if owner == "" {
		return -1, nil, ErrSessionNotFound
	}
	sf, err := ss.loadSessionFile(sessionKey(channel, user))
	if errors.Is(err, ErrSessionNotFound) {
		return -1, nil, nil
	}
	if err != nil {
		return -1, nil, err
	}
	if sf.OwnerID != owner || sf.ID != sessionKey(channel, user) {
		return -1, nil, ErrSessionNotFound
	}
	for i, msg := range sf.Messages {
		if msg.EventID == event {
			copy := cloneMessage(msg)
			return i, &copy, nil
		}
	}
	return -1, nil, nil
}

// SaveOwnedEvent appends one durable assistant event without reopening an ended
// conversation. The caller must own its AcquireTurn lease. No unsaved message
// or index is published to readers when the atomic disk write fails.
func (ss *SessionStore) SaveOwnedEvent(channel, user, owner string, msg Message) (int, *Message, error) {
	if owner == "" || msg.EventID == "" || msg.Role != "assistant" {
		return -1, nil, ErrSessionNotFound
	}
	index, existing, err := ss.OwnedEvent(channel, user, owner, msg.EventID)
	if err != nil || existing != nil {
		return index, existing, err
	}
	key := sessionKey(channel, user)
	// Keep the identity stable through clone, atomic persistence, and live
	// publication. CompleteSession and BrowserSession both replace/remove this
	// map entry under mgr.mu; releasing it early could let a resumed session
	// load the old file and later overwrite this acknowledged event.
	ss.mgr.mu.RLock()
	defer ss.mgr.mu.RUnlock()
	live := ss.mgr.sessions[key]
	var snapshot *Session
	if live != nil {
		snapshot = CloneSession(live)
	} else {
		sf, err := ss.loadSessionFile(key)
		if err != nil {
			return -1, nil, err
		}
		snapshot = &Session{RequestEntries: maps.Clone(sf.RequestEntries), ID: sf.ID, ChannelID: sf.ChannelID, UserID: sf.UserID, OwnerID: sf.OwnerID, AssistantMode: sf.AssistantMode, State: SessionClosed, StartedAt: sf.StartedAt, LastActiveAt: sf.LastActiveAt, RoundCount: sf.RoundCount, Messages: sf.Messages}
	}
	if snapshot.OwnerID != owner || snapshot.ID != key {
		return -1, nil, ErrSessionNotFound
	}
	index = len(snapshot.Messages)
	snapshot.Messages = append(snapshot.Messages, cloneMessage(msg))
	snapshot.LastActiveAt = time.Now()
	if err := ss.SaveSession(snapshot); err != nil {
		return -1, nil, err
	}
	if live != nil {
		live.mu.Lock()
		live.Messages = snapshot.Messages
		live.LastActiveAt = snapshot.LastActiveAt
		live.mu.Unlock()
	}
	saved := cloneMessage(msg)
	return index, &saved, nil
}

// OwnedLegacyProposalMessage recovers pre-event history without rewriting its
// original text or adding another assistant turn during protocol migration.
func (ss *SessionStore) OwnedLegacyProposalMessage(channel, user, owner, proposal string) (*Message, error) {
	if owner == "" {
		return nil, ErrSessionNotFound
	}
	sf, err := ss.loadSessionFile(sessionKey(channel, user))
	if errors.Is(err, ErrSessionNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sf.OwnerID != owner || sf.ID != sessionKey(channel, user) {
		return nil, ErrSessionNotFound
	}
	for _, msg := range sf.Messages {
		if msg.Role != "assistant" || msg.EventID != "" {
			continue
		}
		for _, attachment := range msg.Attachments {
			if attachment.Kind == "control_proposal" && attachment.ProposalID == proposal {
				saved := cloneMessage(msg)
				return &saved, nil
			}
		}
	}
	return nil, nil
}
