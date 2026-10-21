package assistantconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func referenceWAV(seconds int) []byte {
	b := make([]byte, 44+seconds*32000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}
func TestTTSSettingsDefaultsCompatibilityAndValidation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"version":1,"settings":{"revision":"legacy","updated_at":"2026-10-01T00:00:00Z","key_configured":false,"llm_mode":"local","asr_mode":"local","tts_mode":"local","local_asr_url":"","local_tts_url":""}}`)
	sealed, err := sealRecord(old, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, recordPath), sealed, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get()
	if err != nil || got.TTSModel != "mimo-v2.5-tts" || got.TTSVoice != "冰糖" {
		t.Fatalf("legacy defaults: %+v %v", got, err)
	}
	model, voice := "mimo-v2.5-tts-voiceclone", "Mia"
	got, err = s.Update(Update{ExpectedRevision: got.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Cloud, TTSModel: &model, TTSVoice: &voice})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Update(Update{ExpectedRevision: got.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Local})
	if err != nil || got.TTSModel != model || got.TTSVoice != voice {
		t.Fatal("old client reset tts configuration", err)
	}
	for _, bad := range []string{"", "unknown", "data:audio/wav;base64,private"} {
		for _, isModel := range []bool{true, false} {
			in := Update{ExpectedRevision: got.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Local}
			if isModel {
				in.TTSModel = &bad
			} else {
				in.TTSVoice = &bad
			}
			if _, err = s.Update(in); !errors.Is(err, ErrInvalid) {
				t.Errorf("accepted invalid model/voice: %v", err)
			}
		}
	}
	for _, allowed := range []string{"mimo_default", "冰糖", "茉莉", "苏打", "白桦", "Mia", "Chloe", "Milo", "Dean"} {
		got, err = s.Update(Update{ExpectedRevision: got.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Cloud, TTSVoice: &allowed})
		if err != nil {
			t.Fatal(allowed, err)
		}
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Get()
	if err != nil || again != got {
		t.Fatal("tts config did not persist", err)
	}
}
func TestVoiceReferenceEncryptedRevisionTombstoneAndCopies(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.VoiceReference()
	if err != nil || empty.Exists || empty.Revision != "" {
		t.Fatal(empty, err)
	}
	if b, err := s.VoiceReferenceContent(); err != nil || len(b) != 0 {
		t.Fatal("missing optional reference", err)
	}
	audio := referenceWAV(1)
	saved, err := s.SaveVoiceReference(audio, "")
	if runtime.GOOS != "windows" {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal("non-Windows stored sensitive audio", err)
		}
		if _, err = os.Stat(filepath.Join(dir, "secrets/tts-reference.dpapi")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("non-Windows wrote reference file")
		}
		return
	}
	if err != nil || !saved.Exists || saved.SizeBytes != int64(len(audio)) || saved.UpdatedAt == nil || saved.Revision == "" {
		t.Fatal(saved, err)
	}
	disk, err := os.ReadFile(filepath.Join(dir, "secrets/tts-reference.dpapi"))
	if err != nil || bytes.Contains(disk, []byte("RIFF")) || bytes.Contains(disk, []byte(base64.StdEncoding.EncodeToString(audio))) {
		t.Fatal("unencrypted reference", err)
	}
	public, _ := json.Marshal(saved)
	if bytes.Contains(public, []byte("audio")) {
		t.Fatal("public metadata exposes audio")
	}
	audio[44] = 99
	got, err := s.VoiceReferenceContent()
	if err != nil || got[44] != 0 {
		t.Fatal("input alias retained", err)
	}
	got[44] = 77
	got, err = s.VoiceReferenceContent()
	if err != nil || got[44] != 0 {
		t.Fatal("output alias retained", err)
	}
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Store{s, other} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			_, e := store.SaveVoiceReference(referenceWAV(1), saved.Revision)
			results <- e
		}(store)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent replacement", success, conflict)
	}
	saved, err = s.VoiceReference()
	if err != nil {
		t.Fatal(err)
	}
	removed, err := s.RemoveVoiceReference(saved.Revision)
	if err != nil || removed.Exists || removed.SizeBytes != 0 || removed.Revision == "" || removed.Revision == saved.Revision {
		t.Fatal("missing tombstone", removed, err)
	}
	if _, err = s.SaveVoiceReference(referenceWAV(1), ""); !errors.Is(err, ErrConflict) {
		t.Fatal("deletion permitted stale initial upload", err)
	}
	if _, err = s.RemoveVoiceReference(saved.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete", err)
	}
	if b, err := s.VoiceReferenceContent(); err != nil || len(b) != 0 {
		t.Fatal("deleted content remains", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := reopened.VoiceReference()
	if err != nil || meta.Revision != removed.Revision {
		t.Fatal("tombstone lost", err)
	}
	if _, err = s.SaveVoiceReference(referenceWAV(1), removed.Revision); err != nil {
		t.Fatal(err)
	}
}
func TestVoiceReferenceRejectsInvalidCorruptAndLinkedFiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, audio := range [][]byte{nil, []byte("not wav"), referenceWAV(61), make([]byte, 6*1024*1024+1)} {
		if _, err = s.SaveVoiceReference(audio, ""); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid sample accepted", err)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	saved, err := s.SaveVoiceReference(referenceWAV(1), "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "secrets/tts-reference.dpapi")
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VoiceReference(); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corruption concealed", err)
	}
	if _, err = s.VoiceReferenceContent(); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupt content accepted", err)
	}
	if _, err = s.SaveVoiceReference(referenceWAV(1), saved.Revision); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupt reference overwritten without revision", err)
	}
	t.Run("symlink", func(t *testing.T) {
		store, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "sample")
		original := []byte("outside")
		if err = os.WriteFile(outside, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(outside, filepath.Join(store.dir, "secrets/tts-reference.dpapi")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err = store.SaveVoiceReference(referenceWAV(1), ""); !errors.Is(err, ErrUnavailable) {
			t.Fatal("linked reference accepted", err)
		}
		got, _ := os.ReadFile(outside)
		if !bytes.Equal(got, original) {
			t.Fatal("outside file changed")
		}
	})
}

func TestVoiceReferenceRecordRejectsInvalidVersionAndInvalidEncryptedWAV(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI-only protected reference")
	}
	for _, raw := range []string{
		`{"version":2,"revision":"r","updated_at":"2026-10-01T00:00:00Z"}`,
		`{"version":1,"revision":"","updated_at":"2026-10-01T00:00:00Z"}`,
		`{"version":1,"revision":"r","updated_at":"2026-10-01T00:00:00Z","audio":"aW52YWxpZA=="}`,
	} {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := sealRecord([]byte(raw), true)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.dir, voiceReferencePath), sealed, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.VoiceReferenceContent(); !errors.Is(err, ErrUnavailable) {
			t.Fatal("invalid authenticated envelope accepted", err)
		}
	}
}
