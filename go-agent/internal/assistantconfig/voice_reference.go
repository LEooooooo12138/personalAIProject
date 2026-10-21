package assistantconfig

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/yuanleyao/ai-agent/internal/assistantconfig/voiceaudio"
)

const voiceReferencePath = "secrets/tts-reference.dpapi"
const voiceReferenceLimit = 6 << 20

// VoiceReference contains only safe metadata. Audio stays server-side.
type VoiceReference struct {
	Exists    bool       `json:"exists"`
	SizeBytes int64      `json:"size_bytes"`
	UpdatedAt *time.Time `json:"updated_at"`
	Revision  string     `json:"revision"`
}
type voiceReferenceRecord struct {
	Version   int       `json:"version"`
	Revision  string    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Audio     []byte    `json:"audio,omitempty"`
}

func (r voiceReferenceRecord) metadata() VoiceReference {
	var updated *time.Time
	if !r.UpdatedAt.IsZero() {
		value := r.UpdatedAt
		updated = &value
	}
	return VoiceReference{Exists: len(r.Audio) > 0, SizeBytes: int64(len(r.Audio)), UpdatedAt: updated, Revision: r.Revision}
}
func (s *Store) readVoiceReference(root *os.Root) (voiceReferenceRecord, error) {
	data, _, err := s.read(root, voiceReferencePath, voiceReferenceLimit*4/3+65536)
	if errors.Is(err, os.ErrNotExist) {
		return voiceReferenceRecord{}, nil
	}
	if err != nil || !supportsVoiceReference {
		return voiceReferenceRecord{}, ErrUnavailable
	}
	plain, err := unsealRecord(data)
	if err != nil {
		return voiceReferenceRecord{}, ErrUnavailable
	}
	defer clear(plain)
	var rec voiceReferenceRecord
	if json.Unmarshal(plain, &rec) != nil || rec.Version != 1 || rec.Revision == "" || rec.UpdatedAt.IsZero() {
		clear(rec.Audio)
		return voiceReferenceRecord{}, ErrUnavailable
	}
	if len(rec.Audio) > 0 && voiceaudio.ValidateWAV(rec.Audio, 60, voiceReferenceLimit) != nil {
		clear(rec.Audio)
		return voiceReferenceRecord{}, ErrUnavailable
	}
	return rec, nil
}
func (s *Store) VoiceReference() (VoiceReference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return VoiceReference{}, err
	}
	defer root.Close()
	rec, err := s.readVoiceReference(root)
	if err != nil {
		return VoiceReference{}, err
	}
	defer clear(rec.Audio)
	return rec.metadata(), nil
}

// VoiceReferenceContent returns a fresh server-only snapshot; absent/deleted is nil.
func (s *Store) VoiceReferenceContent() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rec, err := s.readVoiceReference(root)
	if err != nil {
		return nil, err
	}
	return rec.Audio, nil
}
func (s *Store) SaveVoiceReference(audio []byte, expectedRevision string) (VoiceReference, error) {
	snapshot := append([]byte(nil), audio...)
	defer clear(snapshot)
	if voiceaudio.ValidateWAV(snapshot, 60, voiceReferenceLimit) != nil {
		return VoiceReference{}, ErrInvalid
	}
	return s.writeVoiceReference(snapshot, expectedRevision)
}
func (s *Store) RemoveVoiceReference(expectedRevision string) (VoiceReference, error) {
	// An encrypted empty record retains revision and prevents a stale first
	// upload from becoming valid again after deletion.
	return s.writeVoiceReference(nil, expectedRevision)
}
func (s *Store) writeVoiceReference(audio []byte, expectedRevision string) (VoiceReference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root()
	if err != nil {
		return VoiceReference{}, err
	}
	defer root.Close()
	old, err := s.readVoiceReference(root)
	if err != nil {
		return VoiceReference{}, err
	}
	defer clear(old.Audio)
	if old.Revision != expectedRevision {
		return VoiceReference{}, ErrConflict
	}
	if !supportsVoiceReference {
		return VoiceReference{}, ErrUnavailable
	}
	revision, err := randomName()
	if err != nil {
		return VoiceReference{}, ErrUnavailable
	}
	rec := voiceReferenceRecord{Version: 1, Revision: revision, UpdatedAt: time.Now().UTC(), Audio: audio}
	plain, err := json.Marshal(rec)
	if err != nil {
		return VoiceReference{}, ErrUnavailable
	}
	defer clear(plain)
	// true forbids plaintext fallback on platforms without protected storage.
	sealed, err := sealRecord(plain, true)
	if err != nil {
		return VoiceReference{}, ErrUnavailable
	}
	if err = s.atomicWrite(root, voiceReferencePath, sealed); err != nil {
		return VoiceReference{}, err
	}
	return rec.metadata(), nil
}
