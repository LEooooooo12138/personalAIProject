package assistantconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSettingsPersistenceAndSerializedRevision(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.Get()
	if err != nil || initial.LLMMode != Local || initial.KeyConfigured {
		t.Fatalf("defaults: %+v %v", initial, err)
	}
	next, err := s.Update(Update{LLMMode: Cloud, ASRMode: Hybrid, TTSMode: Local, LocalASRURL: "http://127.0.0.1:9001/v1"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := other.Get()
	if err != nil || got != next || got.Revision == "" {
		t.Fatalf("persistence: %+v %v", got, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, st := range []*Store{s, other} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			_, e := st.Update(Update{ExpectedRevision: next.Revision, LLMMode: Hybrid, ASRMode: Local, TTSMode: Cloud})
			results <- e
		}(st)
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
		t.Fatalf("writers: %d successes %d conflicts", success, conflict)
	}
}

func TestInvalidSettingsPreserveRecord(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Update(Update{LLMMode: Local, ASRMode: Local, TTSMode: Local})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(s.dir, recordPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"http://localhost:9000", "https://127.0.0.1", "http://example.com", "http://127.0.0.1@evil.test", "http://127.0.0.1?a=b", "http://127.0.0.1#x", "http://127.0.0.1:65536", "http://127.0.0.1/" + strings.Repeat("x", 65536)} {
		for _, field := range []string{"asr", "tts"} {
			in := Update{ExpectedRevision: saved.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Local}
			want := ErrLocalASRURLInvalid
			if field == "asr" {
				in.LocalASRURL = address
			} else {
				in.LocalTTSURL = address
				want = ErrLocalTTSURLInvalid
			}
			_, err = s.Update(in)
			if !errors.Is(err, ErrInvalid) || !errors.Is(err, want) {
				t.Errorf("%s invalid URL (%d bytes): got %v, want %v wrapping ErrInvalid", field, len(address), err, want)
			}
		}
	}
	for _, key := range []string{"", " \t", "a\r\nb", "a\x00b", strings.Repeat("x", 8193)} {
		_, err = s.Update(Update{ExpectedRevision: saved.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Local, APIKey: &key})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid key result: %v", err)
		}
	}
	got, err := s.Get()
	if err != nil || got != saved {
		t.Fatalf("changed record: %+v %v", got, err)
	}
	after, err := os.ReadFile(filepath.Join(s.dir, recordPath))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("invalid update rewrote saved bytes: %v", err)
	}
}

func TestKeyRoundtripRetentionRemovalCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "test-secret-not-real-api-key"
	saved, err := s.Update(Update{LLMMode: Cloud, ASRMode: Local, TTSMode: Hybrid, APIKey: &key})
	if runtime.GOOS != "windows" {
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unsupported storage: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secrets", "mimo.dpapi")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatal("plaintext secret on disk")
	}
	safe, _ := json.Marshal(saved)
	if strings.Contains(string(safe), key) || strings.Contains(string(safe), "api_key") {
		t.Fatal("secret in public settings")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := reopened.Snapshot()
	if err != nil || got != key {
		t.Fatalf("secret roundtrip: %v", err)
	}
	saved, err = s.Update(Update{ExpectedRevision: saved.Revision, LLMMode: Hybrid, ASRMode: Local, TTSMode: Local})
	if err != nil {
		t.Fatal(err)
	}
	_, got, err = s.Snapshot()
	if err != nil || got != key {
		t.Fatal("nil key did not retain")
	}
	saved, err = s.Update(Update{ExpectedRevision: saved.Revision, LLMMode: Local, ASRMode: Local, TTSMode: Local, RemoveKey: true})
	if err != nil || saved.KeyConfigured {
		t.Fatalf("remove: %v", err)
	}
	_, got, err = s.Snapshot()
	if err != nil || got != "" {
		t.Fatal("key not removed")
	}
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("corruption accepted: %v", err)
	}
}

func TestPersonalityBoundariesAndRevision(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Personality()
	if err != nil || before.Exists {
		t.Fatalf("initial: %+v %v", before, err)
	}
	saved, err := s.SavePersonality("custom.md", "# 温柔助手\n你好", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "personality", "default.md"))
	if err != nil || string(raw) != "# 温柔助手\n你好" {
		t.Fatal("fixed path write failed")
	}
	if saved.SizeBytes != int64(len(raw)) || saved.Revision == "" || saved.UpdatedAt == nil {
		t.Fatalf("metadata: %+v", saved)
	}
	_, err = s.SavePersonality("custom.md", "stale", "")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	for _, tc := range []struct{ name, content string }{{"wrong.txt", "x"}, {"../escape.md", "x"}, {"x.md", " \n"}, {"x.md", "x\x00y"}, {"x.md", string([]byte{0xff})}, {"x.md", strings.Repeat("x", 256*1024+1)}} {
		_, err = s.SavePersonality(tc.name, tc.content, saved.Revision)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid personality accepted: %q %v", tc.name, err)
		}
	}
	got, err := s.Personality()
	if err != nil || got.Revision != saved.Revision {
		t.Fatal("invalid upload changed personality")
	}
	_, err = s.SavePersonality("x.md", strings.Repeat("x", 256*1024), saved.Revision)
	if err != nil {
		t.Fatalf("exact max: %v", err)
	}
}

func TestRefusesSymlinkRootsAndPersonalityFiles(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("accepted linked root")
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "original.md")
	if err = os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, filepath.Join(dir, "personality", "default.md")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePersonality("new.md", "new", ""); err == nil {
		t.Fatal("accepted linked personality")
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "original" {
		t.Fatal("changed external file")
	}
}
