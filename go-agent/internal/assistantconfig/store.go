// Package assistantconfig owns the administrator's provider configuration and personality.
package assistantconfig

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Mode string

const (
	Local  Mode = "local"
	Cloud  Mode = "cloud"
	Hybrid Mode = "hybrid"
)

var (
	ErrEndpointMismatch   = errors.New("mimo endpoint and key type mismatch")
	ErrInvalid            = errors.New("invalid assistant configuration")
	ErrLocalASRURLInvalid = fmt.Errorf("%w: invalid local ASR URL", ErrInvalid)
	ErrLocalTTSURLInvalid = fmt.Errorf("%w: invalid local TTS URL", ErrInvalid)
	ErrConflict           = errors.New("assistant configuration revision conflict")
	ErrUnavailable        = errors.New("assistant configuration storage unavailable")
)

const personalityLimit = 256 * 1024
const recordPath = "secrets/mimo.dpapi"
const personalityPath = "personality/default.md"

// Settings is safe to serialize to clients. It never contains credentials.
type Settings struct {
	MiMoEndpoint  string    `json:"mimo_endpoint"`
	Revision      string    `json:"revision"`
	UpdatedAt     time.Time `json:"updated_at"`
	KeyConfigured bool      `json:"key_configured"`
	LLMMode       Mode      `json:"llm_mode"`
	ASRMode       Mode      `json:"asr_mode"`
	TTSMode       Mode      `json:"tts_mode"`
	TTSModel      string    `json:"tts_model"`
	TTSVoice      string    `json:"tts_voice"`
	LocalASRURL   string    `json:"local_asr_url"`
	LocalTTSURL   string    `json:"local_tts_url"`
}
type Update struct {
	MiMoEndpoint     *string `json:"mimo_endpoint,omitempty"`
	ExpectedRevision string  `json:"expected_revision"`
	APIKey           *string `json:"api_key,omitempty"`
	RemoveKey        bool    `json:"remove_key"`
	LLMMode          Mode    `json:"llm_mode"`
	ASRMode          Mode    `json:"asr_mode"`
	TTSMode          Mode    `json:"tts_mode"`
	TTSModel         *string `json:"tts_model,omitempty"`
	TTSVoice         *string `json:"tts_voice,omitempty"`
	LocalASRURL      string  `json:"local_asr_url"`
	LocalTTSURL      string  `json:"local_tts_url"`
}
type Personality struct {
	Exists    bool       `json:"exists"`
	SizeBytes int64      `json:"size_bytes"`
	UpdatedAt *time.Time `json:"updated_at"`
	Revision  string     `json:"revision"`
}
type record struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
	Key      string   `json:"key,omitempty"`
}
type Store struct {
	dir string
	mu  *sync.Mutex
}

var directoryLocks sync.Map

func Open(dataDir string) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, ErrInvalid
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, ErrInvalid
	}
	if err = checkPath(dir); err != nil {
		return nil, ErrUnavailable
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, ErrUnavailable
	}
	key := dir
	if caseInsensitivePaths {
		key = strings.ToLower(key)
	}
	lock, _ := directoryLocks.LoadOrStore(key, &sync.Mutex{})
	s := &Store{dir: dir, mu: lock.(*sync.Mutex)}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, name := range []string{"secrets", "personality"} {
		if err = checkPath(filepath.Join(dir, name)); err != nil {
			return nil, ErrUnavailable
		}
		if err = root.MkdirAll(name, 0700); err != nil {
			return nil, ErrUnavailable
		}
		if err = restrictPath(filepath.Join(dir, name)); err != nil {
			return nil, ErrUnavailable
		}
	}
	if _, err = s.readRecord(root); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Get() (Settings, error) { settings, _, err := s.Snapshot(); return settings, err }

// Snapshot returns the secret exclusively for transient server-side provider use.
func (s *Store) Snapshot() (Settings, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return Settings{}, "", err
	}
	defer root.Close()
	rec, err := s.readRecord(root)
	if err != nil {
		return Settings{}, "", err
	}
	return rec.Settings, rec.Key, nil
}

func validMode(m Mode) bool { return m == Local || m == Cloud || m == Hybrid }
func validKey(k string) bool {
	return strings.TrimSpace(k) != "" && len(k) <= 8192 && !strings.ContainsAny(k, "\r\n\x00")
}
func validURL(address string) bool {
	if address == "" {
		return true
	}
	if len(address) > 2048 {
		return false
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(address, "#") {
		return false
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

func (s *Store) Update(in Update) (Settings, error) {
	if !validMode(in.LLMMode) || !validMode(in.ASRMode) || !validMode(in.TTSMode) || in.RemoveKey && in.APIKey != nil || in.APIKey != nil && !validKey(*in.APIKey) {
		return Settings{}, ErrInvalid
	}
	if !validURL(in.LocalASRURL) {
		return Settings{}, ErrLocalASRURLInvalid
	}
	if !validURL(in.LocalTTSURL) {
		return Settings{}, ErrLocalTTSURLInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return Settings{}, err
	}
	defer root.Close()
	old, err := s.readRecord(root)
	if err != nil {
		return Settings{}, err
	}
	if old.Settings.Revision != in.ExpectedRevision {
		return Settings{}, ErrConflict
	}
	key := old.Key
	if in.RemoveKey {
		key = ""
	}
	if in.APIKey != nil {
		key = *in.APIKey
	}
	endpoint := old.Settings.MiMoEndpoint
	if in.MiMoEndpoint != nil {
		endpoint = *in.MiMoEndpoint
	}
	if endpoint == "" {
		endpoint = "auto"
	}
	if _, err := MiMoBaseURL(endpoint, key); err != nil {
		return Settings{}, err
	}
	ttsModel, ttsVoice := old.Settings.TTSModel, old.Settings.TTSVoice
	if in.TTSModel != nil {
		ttsModel = *in.TTSModel
	}
	if in.TTSVoice != nil {
		ttsVoice = *in.TTSVoice
	}
	if !ValidTTSModel(ttsModel) || !ValidTTSVoice(ttsVoice) {
		return Settings{}, ErrInvalid
	}
	revision, err := randomName()
	if err != nil {
		return Settings{}, ErrUnavailable
	}
	settings := Settings{TTSModel: ttsModel, TTSVoice: ttsVoice, MiMoEndpoint: endpoint, Revision: revision, UpdatedAt: time.Now().UTC(), KeyConfigured: key != "", LLMMode: in.LLMMode, ASRMode: in.ASRMode, TTSMode: in.TTSMode, LocalASRURL: in.LocalASRURL, LocalTTSURL: in.LocalTTSURL}
	plain, err := json.Marshal(record{Version: 1, Settings: settings, Key: key})
	if err != nil {
		return Settings{}, ErrUnavailable
	}
	defer clear(plain)
	sealed, err := sealRecord(plain, key != "")
	if err != nil {
		return Settings{}, ErrUnavailable
	}
	if err = s.atomicWrite(root, recordPath, sealed); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (s *Store) readRecord(root *os.Root) (record, error) {
	data, _, err := s.read(root, recordPath, 64*1024)
	if errors.Is(err, os.ErrNotExist) {
		return record{Version: 1, Settings: Settings{TTSModel: DefaultTTSModel, TTSVoice: DefaultTTSVoice, MiMoEndpoint: "auto", LLMMode: Local, ASRMode: Local, TTSMode: Local}}, nil
	}
	if err != nil {
		return record{}, ErrUnavailable
	}
	plain, err := unsealRecord(data)
	if err != nil {
		return record{}, ErrUnavailable
	}
	defer clear(plain)
	var rec record
	if json.Unmarshal(plain, &rec) != nil || rec.Version != 1 || !validMode(rec.Settings.LLMMode) || !validMode(rec.Settings.ASRMode) || !validMode(rec.Settings.TTSMode) || !validURL(rec.Settings.LocalASRURL) || !validURL(rec.Settings.LocalTTSURL) || rec.Key != "" && !validKey(rec.Key) || rec.Settings.KeyConfigured != (rec.Key != "") || rec.Settings.Revision == "" {
		return record{}, ErrUnavailable
	}
	if rec.Settings.TTSModel == "" {
		rec.Settings.TTSModel = DefaultTTSModel
	}
	if rec.Settings.TTSVoice == "" {
		rec.Settings.TTSVoice = DefaultTTSVoice
	}
	if !ValidTTSModel(rec.Settings.TTSModel) || !ValidTTSVoice(rec.Settings.TTSVoice) {
		return record{}, ErrUnavailable
	}
	if rec.Settings.MiMoEndpoint == "" {
		rec.Settings.MiMoEndpoint = "auto"
	}
	if _, err := MiMoBaseURL(rec.Settings.MiMoEndpoint, rec.Key); err != nil {
		return record{}, ErrUnavailable
	}
	return rec, nil
}

// PersonalityContent is a bounded, server-only snapshot for the next answer.
// No upload is optional; a present but unreadable/invalid file is an error.
func (s *Store) PersonalityContent() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return "", err
	}
	defer root.Close()
	data, _, err := s.read(root, personalityPath, personalityLimit)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !validContent(string(data)) {
		return "", ErrUnavailable
	}
	return string(data), nil
}

func (s *Store) Personality() (Personality, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return Personality{}, err
	}
	defer root.Close()
	return s.personality(root)
}
func (s *Store) personality(root *os.Root) (Personality, error) {
	data, info, err := s.read(root, personalityPath, personalityLimit)
	if errors.Is(err, os.ErrNotExist) {
		return Personality{}, nil
	}
	if err != nil {
		return Personality{}, ErrUnavailable
	}
	if !validContent(string(data)) {
		return Personality{}, ErrUnavailable
	}
	sum := sha256.Sum256(data)
	updated := info.ModTime().UTC()
	return Personality{Exists: true, SizeBytes: int64(len(data)), UpdatedAt: &updated, Revision: hex.EncodeToString(sum[:])}, nil
}
func validContent(content string) bool {
	return len(content) <= personalityLimit && utf8.ValidString(content) && !strings.ContainsRune(content, 0) && strings.TrimSpace(content) != ""
}
func (s *Store) SavePersonality(filename, content, expectedRevision string) (Personality, error) {
	if strings.ContainsAny(filename, "/\\\x00:") || !strings.EqualFold(filepath.Ext(filename), ".md") || !validContent(content) {
		return Personality{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return Personality{}, err
	}
	defer root.Close()
	old, err := s.personality(root)
	if err != nil {
		return Personality{}, err
	}
	if old.Revision != expectedRevision {
		return Personality{}, ErrConflict
	}
	if err = s.atomicWrite(root, personalityPath, []byte(content)); err != nil {
		return Personality{}, err
	}
	return s.personality(root)
}

func (s *Store) root() (*os.Root, error) {
	if checkPath(s.dir) != nil {
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, ErrUnavailable
	}
	return root, nil
}

// Reject links and Windows reparse points in every existing component before
// opening. Root then confines the actual I/O even if a component is replaced.
func checkPath(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return ErrUnavailable
			}
			if err = rejectReparse(current); err != nil {
				return err
			}
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
func (s *Store) read(root *os.Root, name string, limit int64) ([]byte, os.FileInfo, error) {
	if err := checkPath(filepath.Join(s.dir, name)); err != nil {
		return nil, nil, err
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, nil, ErrUnavailable
	}
	return data, info, nil
}
func randomName() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func (s *Store) atomicWrite(root *os.Root, name string, data []byte) error {
	if err := checkPath(filepath.Join(s.dir, name)); err != nil {
		return ErrUnavailable
	}
	nonce, err := randomName()
	if err != nil {
		return ErrUnavailable
	}
	tmp := filepath.Join(filepath.Dir(name), ".pending-"+nonce)
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrUnavailable
	}
	defer root.Remove(tmp)
	if err = restrictPath(filepath.Join(s.dir, tmp)); err != nil {
		f.Close()
		return ErrUnavailable
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrUnavailable
	}
	if err = checkPath(filepath.Join(s.dir, name)); err != nil {
		return ErrUnavailable
	}
	if err = root.Rename(tmp, name); err != nil {
		return fmt.Errorf("%w: atomic replacement failed", ErrUnavailable)
	}
	return nil
}
