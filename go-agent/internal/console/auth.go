package console

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

const sessionLifetime = 7 * 24 * time.Hour

func csrfToken(token string) string {
	h := sha256.Sum256([]byte("console-csrf-v1:" + token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func (s *Store) Bootstrap(username, displayName, password string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized {
		return User{}, ErrConflict
	}
	name, err := normalizeUsername(username)
	if err != nil || !validDisplayName(displayName) || !validPassword(password) {
		return User{}, ErrInvalid
	}
	encoded, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	id, err := randomString(18)
	if err != nil {
		return User{}, err
	}
	u := User{ID: id, Username: name, DisplayName: displayName, Role: "admin"}
	st := diskState{Version: 1, Users: []account{{User: u, PasswordHash: encoded}}, Sessions: map[string]session{}, Sharing: Sharing{EntityIDs: []string{}, SuggestionIDs: []string{}}}
	if err := s.commitLocked(st); err != nil {
		return User{}, err
	}
	return u, nil
}

func fakePasswordHash() string {
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString([]byte("consolefake-salt")), base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
}

func (s *Store) Authenticate(username, password string) (Login, error) {
	name, nameErr := normalizeUsername(username)
	if nameErr != nil {
		name = ""
	}
	s.mu.Lock()
	if !s.initialized {
		s.mu.Unlock()
		return Login{}, ErrUninitialized
	}
	encoded := fakePasswordHash()
	var candidate account
	found := false
	for _, a := range s.state.Users {
		if a.Username == name {
			candidate = a
			found = true
			encoded = a.PasswordHash
			break
		}
	}
	s.mu.Unlock()
	matched, err := verifyPassword(encoded, password)
	if err != nil {
		return Login{}, err
	}
	if !found || candidate.Disabled || !matched {
		return Login{}, ErrUnauthenticated
	}
	token, err := randomString(32)
	if err != nil {
		return Login{}, err
	}
	sessionID, err := randomString(18)
	if err != nil {
		return Login{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.findUser(candidate.ID)
	if !ok || s.state.Users[i].Disabled || s.state.Users[i].PasswordHash != candidate.PasswordHash {
		return Login{}, ErrUnauthenticated
	}
	expires := s.now().Add(sessionLifetime).UTC()
	st := s.copyState()
	st.Sessions[tokenHash(token)] = session{ID: sessionID, UserID: candidate.ID, ExpiresAt: expires}
	if err := s.commitLocked(st); err != nil {
		return Login{}, err
	}
	return Login{Token: token, CSRFToken: csrfToken(token), ExpiresAt: expires, Principal: Principal{UserID: candidate.ID, Role: candidate.Role, SessionID: sessionID, MustChangePassword: candidate.MustChangePassword}}, nil
}

func (s *Store) resolveLocked(token string) (Principal, session, error) {
	if !s.initialized {
		return Principal{}, session{}, ErrUninitialized
	}
	if token == "" {
		return Principal{}, session{}, ErrUnauthenticated
	}
	h := tokenHash(token)
	sess, ok := s.state.Sessions[h]
	if !ok {
		return Principal{}, session{}, ErrUnauthenticated
	}
	if !s.now().Before(sess.ExpiresAt) {
		s.cancelLocked(h)
		return Principal{}, session{}, ErrUnauthenticated
	}
	i, ok := s.findUser(sess.UserID)
	if !ok || s.state.Users[i].Disabled {
		return Principal{}, session{}, ErrUnauthenticated
	}
	u := s.state.Users[i]
	return Principal{UserID: u.ID, Role: u.Role, SessionID: sess.ID, MustChangePassword: u.MustChangePassword}, sess, nil
}

func (s *Store) Resolve(token string) (Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, err := s.resolveLocked(token)
	return p, err
}

func (s *Store) CSRFToken(token string) (string, error) {
	if _, err := s.Resolve(token); err != nil {
		return "", err
	}
	return csrfToken(token), nil
}

func (s *Store) cancelLocked(hash string) {
	for _, cancel := range s.bindings[hash] {
		cancel()
	}
	delete(s.bindings, hash)
}

func (s *Store) cancelRemovedLocked() {
	for hash := range s.bindings {
		if _, ok := s.state.Sessions[hash]; !ok {
			s.cancelLocked(hash)
		}
	}
}

func (s *Store) revokeUserLocked(st *diskState, userID string) {
	for hash, sess := range st.Sessions {
		if sess.UserID == userID {
			delete(st.Sessions, hash)
		}
	}
}

func (s *Store) Logout(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, err := s.resolveLocked(token); err != nil {
		return err
	}
	st := s.copyState()
	delete(st.Sessions, tokenHash(token))
	if err := s.commitLocked(st); err != nil {
		return err
	}
	s.cancelLocked(tokenHash(token))
	return nil
}

func (s *Store) ChangePassword(token, oldPassword, newPassword string) error {
	if !validPassword(newPassword) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, err := s.resolveLocked(token)
	if err != nil {
		return err
	}
	i, _ := s.findUser(p.UserID)
	matched, err := verifyPassword(s.state.Users[i].PasswordHash, oldPassword)
	if err != nil {
		return err
	}
	if !matched {
		return ErrUnauthenticated
	}
	encoded, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	st := s.copyState()
	st.Users[i].PasswordHash = encoded
	st.Users[i].MustChangePassword = false
	s.revokeUserLocked(&st, p.UserID)
	if err := s.commitLocked(st); err != nil {
		return err
	}
	s.cancelRemovedLocked()
	return nil
}

func (s *Store) BindSession(ctx context.Context, token string) (context.Context, func(), error) {
	s.mu.Lock()
	_, sess, err := s.resolveLocked(token)
	if err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	hash := tokenHash(token)
	bound, cancel := context.WithCancel(ctx)
	s.nextBinding++
	id := s.nextBinding
	if s.bindings[hash] == nil {
		s.bindings[hash] = make(map[uint64]func())
	}
	s.bindings[hash][id] = cancel
	delay := sess.ExpiresAt.Sub(s.now())
	if delay < 0 {
		delay = 0
	}
	timer := time.AfterFunc(delay, func() { s.mu.Lock(); defer s.mu.Unlock(); s.cancelLocked(hash) })
	s.mu.Unlock()
	stop := func() {
		timer.Stop()
		s.mu.Lock()
		if entries := s.bindings[hash]; entries != nil {
			delete(entries, id)
			if len(entries) == 0 {
				delete(s.bindings, hash)
			}
		}
		s.mu.Unlock()
		cancel()
	}
	return bound, stop, nil
}

func (s *Store) RevokeAllSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return ErrUninitialized
	}
	st := s.copyState()
	st.Sessions = map[string]session{}
	if err := s.commitLocked(st); err != nil {
		return err
	}
	s.cancelRemovedLocked()
	return nil
}

func (s *Store) CreateMember(username, displayName string) (User, string, error) {
	name, err := normalizeUsername(username)
	if err != nil || !validDisplayName(displayName) {
		return User{}, "", ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return User{}, "", ErrUninitialized
	}
	for _, a := range s.state.Users {
		if a.Username == name {
			return User{}, "", ErrConflict
		}
	}
	temp, err := randomString(24)
	if err != nil {
		return User{}, "", err
	}
	hash, err := hashPassword(temp)
	if err != nil {
		return User{}, "", err
	}
	id, err := randomString(18)
	if err != nil {
		return User{}, "", err
	}
	u := User{ID: id, Username: name, DisplayName: displayName, Role: "member", MustChangePassword: true}
	st := s.copyState()
	st.Users = append(st.Users, account{User: u, PasswordHash: hash})
	if err := s.commitLocked(st); err != nil {
		return User{}, "", err
	}
	return u, temp, nil
}

func (s *Store) UpdateMember(id, displayName string, disabled bool) (User, error) {
	if !validDisplayName(displayName) {
		return User{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return User{}, ErrUninitialized
	}
	i, ok := s.findUser(id)
	if !ok {
		return User{}, ErrInvalid
	}
	if s.state.Users[i].Role != "member" {
		return User{}, ErrForbidden
	}
	st := s.copyState()
	st.Users[i].DisplayName = displayName
	st.Users[i].Disabled = disabled
	if disabled {
		s.revokeUserLocked(&st, id)
	}
	if err := s.commitLocked(st); err != nil {
		return User{}, err
	}
	s.cancelRemovedLocked()
	return st.Users[i].User, nil
}

func (s *Store) ResetMemberPassword(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return "", ErrUninitialized
	}
	i, ok := s.findUser(id)
	if !ok {
		return "", ErrInvalid
	}
	if s.state.Users[i].Role != "member" {
		return "", ErrForbidden
	}
	temp, err := randomString(24)
	if err != nil {
		return "", err
	}
	hash, err := hashPassword(temp)
	if err != nil {
		return "", err
	}
	st := s.copyState()
	st.Users[i].PasswordHash = hash
	st.Users[i].MustChangePassword = true
	s.revokeUserLocked(&st, id)
	if err := s.commitLocked(st); err != nil {
		return "", err
	}
	s.cancelRemovedLocked()
	return temp, nil
}

func (s *Store) ResetAdminPassword(password string) error {
	if !validPassword(password) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return ErrUninitialized
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	st := s.copyState()
	for i, a := range st.Users {
		if a.Role == "admin" {
			st.Users[i].PasswordHash = hash
			s.revokeUserLocked(&st, a.ID)
			break
		}
	}
	if err := s.commitLocked(st); err != nil {
		return err
	}
	s.cancelRemovedLocked()
	return nil
}

func (s *Store) ListMembers() ([]User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return nil, ErrUninitialized
	}
	users := []User{}
	for _, a := range s.state.Users {
		if a.Role == "member" {
			users = append(users, a.User)
		}
	}
	return users, nil
}
