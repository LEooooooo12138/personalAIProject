package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestCloudTTSVoiceSelectionClonePayloadAndNoDowngrade(t *testing.T) {
	var calls atomic.Int32
	sample := wav(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		audio := p["audio"].(map[string]any)
		if len(p) != 4 || p["stream"] != false {
			t.Error("unexpected clone payload fields")
		}
		messages := p["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["role"] != "assistant" || messages[0].(map[string]any)["content"] != "你好" {
			t.Error("unexpected content/history")
		}
		switch p["model"] {
		case "mimo-v2.5-tts":
			if audio["voice"] != "Mia" {
				t.Error("selected voice lost")
			}
		case "mimo-v2.5-tts-voiceclone":
			if audio["voice"] != "data:audio/wav;base64,"+base64.StdEncoding.EncodeToString(sample) {
				t.Error("clone audio missing or mutated")
			}
		default:
			t.Error("wrong model")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]string{"data": base64.StdEncoding.EncodeToString(wav(1))}}}}})
	}))
	defer srv.Close()
	for _, model := range []string{"mimo-v2.5-tts", "mimo-v2.5-tts-voiceclone"} {
		cfg := Config{TTSMode: "cloud", TTSModel: model, TTSVoice: "Mia", VoiceReferenceWAV: append([]byte(nil), sample...), APIKey: "not-real"}
		e := newEngine(cfg, srv.Client(), srv.URL)
		cfg.VoiceReferenceWAV[44] = 99
		if _, err := e.Synthesize(context.Background(), "你好"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected request count")
	}
	missing := newEngine(Config{TTSMode: "cloud", TTSModel: "mimo-v2.5-tts-voiceclone", APIKey: "not-real"}, srv.Client(), srv.URL)
	if _, err := missing.Synthesize(context.Background(), "你好"); !errors.Is(err, ErrVoiceReferenceRequired) || errorCode(err) != "voice_reference_required" {
		t.Fatal("missing sample did not fail clearly", err)
	}
	bad := newEngine(Config{TTSMode: "cloud", TTSModel: "mimo-v2.5-tts-voiceclone", VoiceReferenceWAV: []byte("invalid"), APIKey: "not-real"}, srv.Client(), srv.URL)
	if _, err := bad.Synthesize(context.Background(), "你好"); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid reference accepted", err)
	}
	if calls.Load() != 2 {
		t.Fatal("clone silently downgraded")
	}
}
func TestLocalTTSNeverReceivesCloneSample(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if len(p) != 4 || p["model"] != "tts-1" || p["voice"] != "alloy" || r.Header.Get("api-key") != "" {
			t.Error("local protocol or confidentiality changed")
		}
		_, _ = w.Write(wav(1))
	}))
	defer srv.Close()
	e := NewEngine(Config{TTSMode: "local", TTSModel: "mimo-v2.5-tts-voiceclone", LocalTTSURL: srv.URL, APIKey: "not-real"})
	if _, err := e.Synthesize(context.Background(), "hello"); err != nil {
		t.Fatal("local path unnecessarily required clone sample", err)
	}
}
func TestVoiceReferenceNormalizationBoundaries(t *testing.T) {
	original := wav(1)
	got, err := NormalizeVoiceReference(context.Background(), original, "audio/wav", "missing-ffmpeg")
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal(err)
	}
	got[44] = 2
	if original[44] != 0 {
		t.Fatal("normalization retained input alias")
	}
	for _, input := range []struct {
		b    []byte
		mime string
	}{{wav(61), "audio/wav"}, {make([]byte, 6*1024*1024+1), "audio/wav"}, {[]byte("https://example.test/audio.wav"), "audio/mpeg"}, {[]byte{0x1a, 0x45, 0xdf, 0xa3}, "audio/webm"}, {wav(1), "text/plain"}} {
		if _, err := NormalizeVoiceReference(context.Background(), input.b, input.mime, "missing-ffmpeg"); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid voice sample accepted", err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPEECH_FFMPEG_HELPER", "valid")
	t.Setenv("SPEECH_FFMPEG_FORMAT", "mp3")
	got, err = NormalizeVoiceReference(context.Background(), []byte("ID3fixture"), "audio/mpeg", executable)
	if err != nil || len(got) != 32044 {
		t.Fatal("MP3 pipe normalization", err)
	}
	t.Setenv("SPEECH_FFMPEG_HELPER", "long")
	if _, err = NormalizeVoiceReference(context.Background(), []byte("ID3fixture"), "audio/mp3", executable); !errors.Is(err, ErrInvalid) {
		t.Fatal("long MP3 accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = NormalizeVoiceReference(ctx, wav(1), "audio/wav", ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled normalization accepted", err)
	}
}
func TestSpeechJobSnapshotsCloneSampleBeforeStartingWorker(t *testing.T) {
	j := NewJobs()
	entered, release := make(chan struct{}), make(chan struct{})
	seen := make(chan byte, 1)
	j.factory = func(cfg Config) speechRunner {
		close(entered)
		<-release
		seen <- cfg.VoiceReferenceWAV[44]
		return immediateSpeech{}
	}
	sample := wav(1)
	job, err := j.Start("owner", Config{TTSMode: "cloud", TTSModel: "mimo-v2.5-tts-voiceclone", VoiceReferenceWAV: sample}, Request{Kind: "synthesize", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	sample[44] = 77
	close(release)
	if got := <-seen; got != 0 {
		t.Error("job read mutated reference", got)
	}
	if got := result(t, j, "owner", job.ID); got.Status != "succeeded" {
		t.Fatal(got)
	}
}

func TestHybridCloneKeepsLocalSuccessAndFailsMissingReferenceOnFallback(t *testing.T) {
	for _, localOK := range []bool{true, false} {
		t.Run(fmt.Sprint(localOK), func(t *testing.T) {
			var cloudCalls atomic.Int32
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cloudCalls.Add(1); w.WriteHeader(503) }))
			defer cloud.Close()
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if localOK {
					_, _ = w.Write(wav(1))
				} else {
					w.WriteHeader(503)
				}
			}))
			defer local.Close()
			e := newEngine(Config{TTSMode: "hybrid", TTSModel: "mimo-v2.5-tts-voiceclone", LocalTTSURL: local.URL, APIKey: "not-real"}, cloud.Client(), cloud.URL)
			_, err := e.Synthesize(context.Background(), "hello")
			if localOK && err != nil {
				t.Fatal("local success required cloud reference", err)
			}
			if !localOK && !errors.Is(err, ErrVoiceReferenceRequired) {
				t.Fatal("missing fallback sample downgraded", err)
			}
			if cloudCalls.Load() != 0 {
				t.Fatal("missing sample reached cloud")
			}
		})
	}
}
func TestCloudCloneProviderFailureDoesNotRetryOrdinaryVoice(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	e := newEngine(Config{TTSMode: "cloud", TTSModel: "mimo-v2.5-tts-voiceclone", VoiceReferenceWAV: wav(1), APIKey: "not-real"}, srv.Client(), srv.URL)
	if _, err := e.Synthesize(context.Background(), "hello"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("clone failure retried or changed voice")
	}
	j := NewJobs()
	job, err := j.Start("owner", Config{TTSMode: "cloud", TTSModel: "mimo-v2.5-tts-voiceclone"}, Request{Kind: "synthesize", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	finished := result(t, j, "owner", job.ID)
	if finished.Status != "failed" || finished.ErrorCode != "voice_reference_required" {
		t.Fatal("job lost required reference error", finished)
	}
}
