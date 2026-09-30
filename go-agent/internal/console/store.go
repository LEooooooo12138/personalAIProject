package console

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const stateFile = "state.json"
const maxStateBytes = 16 << 20

type account struct {
	User
	PasswordHash string `json:"password_hash"`
}

type session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type diskState struct {
	Version  int                `json:"version"`
	Users    []account          `json:"users"`
	Sessions map[string]session `json:"sessions"`
	Sharing  Sharing            `json:"sharing"`
}

type Store struct {
	mu          sync.Mutex
	dir         string
	now         func() time.Time
	state       diskState
	initialized bool
	bindings    map[string]map[uint64]func()
	nextBinding uint64
	syncDir     func(string) error
	poisoned    bool
}

func syncStateDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func OpenStore(dir string, now func() time.Time) (*Store, error) {
	if now == nil {
		return nil, ErrInvalid
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: data directory", ErrUnavailable)
	}
	probe, err := os.CreateTemp(dir, ".console-probe-*")
	if err != nil {
		return nil, fmt.Errorf("%w: directory not writable: %v", ErrUnavailable, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		os.Remove(name)
		return nil, fmt.Errorf("%w: probe close: %v", ErrUnavailable, err)
	}
	if err := os.Remove(name); err != nil {
		return nil, fmt.Errorf("%w: probe remove: %v", ErrUnavailable, err)
	}
	s := &Store{dir: dir, now: now, bindings: make(map[string]map[uint64]func()), syncDir: syncStateDir}
	path := filepath.Join(dir, stateFile)
	stateInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: inspect state: %v", ErrUnavailable, err)
	}
	if !stateInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: state is not a regular file", ErrUnavailable)
	}
	if stateInfo.Size() > maxStateBytes {
		return nil, fmt.Errorf("%w: oversized state", ErrUnavailable)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: open state: %v", ErrUnavailable, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: read state: %v", ErrUnavailable, err)
	}
	if len(data) > maxStateBytes {
		return nil, fmt.Errorf("%w: oversized state", ErrUnavailable)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s.state); err != nil {
		return nil, fmt.Errorf("%w: decode state: %v", ErrUnavailable, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing state data", ErrUnavailable)
	}
	if err := validateState(s.state); err != nil {
		return nil, fmt.Errorf("%w: invalid state: %v", ErrUnavailable, err)
	}
	s.initialized = true
	return s, nil
}

func validateState(st diskState) error {
	if st.Version != 1 || len(st.Users) == 0 || st.Sessions == nil || st.Sharing.EntityIDs == nil || st.Sharing.SuggestionIDs == nil {
		return ErrInvalid
	}
	ids, names := map[string]bool{}, map[string]bool{}
	admins := 0
	for _, a := range st.Users {
		if a.ID == "" || ids[a.ID] || names[a.Username] || !validUsername(a.Username) || !validDisplayName(a.DisplayName) {
			return ErrInvalid
		}
		if a.Role == "admin" {
			admins++
		} else if a.Role != "member" {
			return ErrInvalid
		}
		if _, _, err := parseHash(a.PasswordHash); err != nil {
			return ErrInvalid
		}
		ids[a.ID], names[a.Username] = true, true
	}
	if admins != 1 {
		return ErrInvalid
	}
	for token, sess := range st.Sessions {
		if len(token) != 64 || !validHex(token) || sess.ID == "" || !ids[sess.UserID] || sess.ExpiresAt.IsZero() {
			return ErrInvalid
		}
	}
	if !validIDs(st.Sharing.EntityIDs) || !validIDs(st.Sharing.SuggestionIDs) {
		return ErrInvalid
	}
	return nil
}

func validHex(v string) bool { _, err := hex.DecodeString(v); return err == nil }

func validUsername(v string) bool {
	if len(v) < 3 || len(v) > 32 || v != strings.ToLower(v) {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func normalizeUsername(v string) (string, error) {
	for _, c := range v {
		if c > 127 {
			return "", ErrInvalid
		}
	}
	v = strings.ToLower(v)
	if !validUsername(v) {
		return "", ErrInvalid
	}
	return v, nil
}

func validDisplayName(v string) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= 1 && utf8.RuneCountInString(v) <= 40
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: random source: %v", ErrUnavailable, err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Store) Initialized() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.initialized }

func (s *Store) checkAvailableLocked() error {
	if s.poisoned {
		return ErrUnavailable
	}
	return nil
}

// CheckAvailable reports an uncertain post-replacement state so the caller can fail closed.
func (s *Store) CheckAvailable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkAvailableLocked()
}

func (s *Store) copyState() diskState {
	st := s.state
	st.Users = append([]account(nil), s.state.Users...)
	st.Sessions = make(map[string]session, len(s.state.Sessions))
	now := s.now()
	for k, v := range s.state.Sessions {
		if now.Before(v.ExpiresAt) {
			st.Sessions[k] = v
		}
	}
	st.Sharing.EntityIDs = append([]string{}, s.state.Sharing.EntityIDs...)
	st.Sharing.SuggestionIDs = append([]string{}, s.state.Sharing.SuggestionIDs...)
	return st
}

// commitLocked publishes the new state only after the replacement has been flushed.
func (s *Store) commitLocked(st diskState) error {
	if err := s.checkAvailableLocked(); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("%w: marshal state: %v", ErrUnavailable, err)
	}
	if len(data) > maxStateBytes {
		return fmt.Errorf("%w: state exceeds %d bytes", ErrUnavailable, maxStateBytes)
	}
	f, err := os.CreateTemp(s.dir, ".console-state-*")
	if err != nil {
		return fmt.Errorf("%w: create state: %v", ErrUnavailable, err)
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return fmt.Errorf("%w: state mode: %v", ErrUnavailable, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("%w: state write: %v", ErrUnavailable, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("%w: state sync: %v", ErrUnavailable, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%w: state close: %v", ErrUnavailable, err)
	}
	if err := os.Rename(name, filepath.Join(s.dir, stateFile)); err != nil {
		return fmt.Errorf("%w: replace state: %v", ErrUnavailable, err)
	}
	if err := s.syncDir(s.dir); err != nil {
		s.poisoned = true
		for hash := range s.bindings {
			s.cancelLocked(hash)
		}
		return fmt.Errorf("%w: sync state dir after replacement: %v", ErrUnavailable, err)
	}
	s.state = st
	s.initialized = true
	s.cancelRemovedLocked()
	return nil
}

func (s *Store) findUser(id string) (int, bool) {
	for i, a := range s.state.Users {
		if a.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (s *Store) GetUser(id string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAvailableLocked(); err != nil {
		return User{}, err
	}
	if !s.initialized {
		return User{}, ErrUninitialized
	}
	if i, ok := s.findUser(id); ok {
		return s.state.Users[i].User, nil
	}
	return User{}, ErrInvalid
}
