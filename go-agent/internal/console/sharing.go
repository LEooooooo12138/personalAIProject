package console

import (
	"strings"
	"unicode/utf8"
)

func validIDs(ids []string) bool {
	if len(ids) > 10000 {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 256 || !utf8.ValidString(id) || strings.TrimSpace(id) != id || seen[id] {
			return false
		}
		for _, r := range id {
			if r < 32 || r == 127 {
				return false
			}
		}
		seen[id] = true
	}
	return true
}

func (s *Store) GetSharing() (Sharing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAvailableLocked(); err != nil {
		return Sharing{}, err
	}
	if !s.initialized {
		return Sharing{}, ErrUninitialized
	}
	return Sharing{Revision: s.state.Sharing.Revision, EntityIDs: append([]string{}, s.state.Sharing.EntityIDs...), SuggestionIDs: append([]string{}, s.state.Sharing.SuggestionIDs...)}, nil
}

func (s *Store) PutSharing(expectedRevision uint64, entities, suggestions []string) (Sharing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAvailableLocked(); err != nil {
		return Sharing{}, err
	}
	if !validIDs(entities) || !validIDs(suggestions) {
		return Sharing{}, ErrInvalid
	}
	if !s.initialized {
		return Sharing{}, ErrUninitialized
	}
	if s.state.Sharing.Revision != expectedRevision || expectedRevision == ^uint64(0) {
		return Sharing{}, ErrConflict
	}
	st := s.copyState()
	st.Sharing = Sharing{Revision: expectedRevision + 1, EntityIDs: append([]string{}, entities...), SuggestionIDs: append([]string{}, suggestions...)}
	if err := s.commitLocked(st); err != nil {
		return Sharing{}, err
	}
	return Sharing{Revision: st.Sharing.Revision, EntityIDs: append([]string{}, st.Sharing.EntityIDs...), SuggestionIDs: append([]string{}, st.Sharing.SuggestionIDs...)}, nil
}
