package speech

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHybridDoesNotFallbackForTLSConfigurationOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		var cloudCalls atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "cloud.invalid" {
				cloudCalls.Add(1)
				return nil, errors.New("unexpected cloud")
			}
			if cancelled {
				cancel()
				return nil, context.Canceled
			}
			return nil, x509.UnknownAuthorityError{}
		})}
		e := newEngine(Config{ASRMode: "hybrid", LocalASRURL: "https://127.0.0.1/transcribe", APIKey: "secret"}, client, "https://cloud.invalid")
		_, err := e.Transcribe(ctx, wav(1), "audio/wav")
		cancel()
		if err == nil || cloudCalls.Load() != 0 {
			t.Fatal("nontransient error reached cloud", cloudCalls.Load(), err)
		}
	}
}

func TestModesAreIndependentAndTTSHybridFallsBack(t *testing.T) {
	var cloudCalls atomic.Int32
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(wav(1))}}}}})
	}))
	defer cloud.Close()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer local.Close()
	e := newEngine(Config{ASRMode: "local", TTSMode: "hybrid", LocalTTSURL: local.URL, APIKey: "secret"}, cloud.Client(), cloud.URL)
	if _, err := e.Transcribe(context.Background(), wav(1), "audio/wav"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if cloudCalls.Load() != 0 {
		t.Fatal("ASR inherited TTS mode")
	}
	if audio, err := e.Synthesize(context.Background(), "hello"); err != nil || len(audio) != 32044 {
		t.Fatal(len(audio), err)
	}
	if cloudCalls.Load() != 1 {
		t.Fatal("TTS failed to fall back")
	}
}

func wav(seconds int) []byte {
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

func TestCloudContracts(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != "POST" || r.Header.Get("api-key") != "secret" {
			t.Error("wrong method or auth")
		}
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		messages := p["messages"].([]any)
		if len(messages) != 1 {
			t.Error("unexpected history")
		}
		msg := messages[0].(map[string]any)
		switch p["model"] {
		case "mimo-v2.5-asr":
			if msg["role"] != "user" || p["stream"] != false || p["asr_options"].(map[string]any)["language"] != "auto" {
				t.Error("wrong ASR protocol")
			}
			a := msg["content"].([]any)[0].(map[string]any)
			if a["type"] != "input_audio" || !strings.HasPrefix(a["input_audio"].(map[string]any)["data"].(string), "data:audio/wav;base64,") {
				t.Error("wrong audio")
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"你好"}}]}`))
		case "mimo-v2.5-tts":
			if msg["role"] != "assistant" || msg["content"] != "你好" {
				t.Error("wrong TTS message")
			}
			audio := p["audio"].(map[string]any)
			if audio["format"] != "wav" || audio["voice"] != "冰糖" {
				t.Error("wrong voice")
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(wav(1))}}}}})
		default:
			t.Error("wrong model")
		}
	}))
	defer srv.Close()
	e := newEngine(Config{ASRMode: "cloud", TTSMode: "cloud", APIKey: "secret"}, srv.Client(), srv.URL)
	text, err := e.Transcribe(context.Background(), wav(1), "audio/wav")
	if err != nil || text != "你好" {
		t.Fatalf("%q %v", text, err)
	}
	audio, err := e.Synthesize(context.Background(), "你好")
	if err != nil || len(audio) != 32044 {
		t.Fatalf("%d %v", len(audio), err)
	}
	if count.Load() != 2 {
		t.Fatal("unexpected retry")
	}
}

func TestLocalContracts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "" {
			t.Error("cloud secret sent locally")
		}
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			f, h, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			if h.Filename != "recording.wav" {
				t.Error(h.Filename)
			}
			w.Write([]byte(`{"text":"local"}`))
		case "/v1/audio/speech":
			var p map[string]any
			json.NewDecoder(r.Body).Decode(&p)
			if p["input"] != "hello" || p["response_format"] != "wav" {
				t.Error(p)
			}
			w.Write(wav(1))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer srv.Close()
	e := NewEngine(Config{LocalASRURL: srv.URL + "/v1/audio/transcriptions", LocalTTSURL: srv.URL + "/v1/audio/speech", APIKey: "secret"})
	text, err := e.Transcribe(context.Background(), wav(1), "audio/wav")
	if err != nil || text != "local" {
		t.Fatalf("%q %v", text, err)
	}
	if _, err = e.Synthesize(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestHybridFallbackClassification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantCloud bool
	}{{"transient", 503, "secret", true}, {"rate limit", 429, "secret", false}, {"invalid", 400, "secret", false}, {"unauthorized", 401, "secret", false}, {"malformed", 200, "not json", false}} {
		t.Run(tc.name, func(t *testing.T) {
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer local.Close()
			var calls atomic.Int32
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Write([]byte(`{"choices":[{"message":{"content":"cloud"}}]}`))
			}))
			defer cloud.Close()
			e := newEngine(Config{ASRMode: "hybrid", LocalASRURL: local.URL, APIKey: "secret"}, cloud.Client(), cloud.URL)
			_, err := e.Transcribe(context.Background(), wav(1), "audio/wav")
			if (calls.Load() == 1) != tc.wantCloud {
				t.Fatalf("calls %d error %v", calls.Load(), err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	e := newEngine(Config{ASRMode: "cloud", TTSMode: "cloud", APIKey: "secret"}, srv.Client(), srv.URL)
	badCompressed := wav(1)
	binary.LittleEndian.PutUint16(badCompressed[20:], 3)
	badFraming := wav(1)
	binary.LittleEndian.PutUint32(badFraming[40:], 999999)
	for _, b := range [][]byte{nil, []byte("bad"), badCompressed, badFraming, wav(61), make([]byte, 8*1024*1024+1)} {
		if _, err := e.Transcribe(context.Background(), b, "audio/wav"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("wanted invalid, got %v", err)
		}
	}
	if _, err := e.Synthesize(context.Background(), " "); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Transcribe(ctx, wav(1), "audio/wav"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("validation reached network")
	}
	for _, u := range []string{"https://example.com/v1/audio/speech", "http://localhost.evil.test/", "http://127.0.0.1@evil.test/", "http://127.0.0.1/?x=y"} {
		if _, err := NewEngine(Config{LocalTTSURL: u}).Synthesize(context.Background(), "hello"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("url %s: %v", u, err)
		}
	}
}

func TestCloudFailuresAreSafeAndNotRetried(t *testing.T) {
	for _, body := range []string{`{"choices":[]}`, `{"choices":[{"message":{"audio":{"data":"bad"}}}]}`, `{"choices":[{"message":{"audio":{"data":"` + base64.StdEncoding.EncodeToString(wav(121)) + `"}}}]}`} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte(body)) }))
		e := newEngine(Config{TTSMode: "cloud", APIKey: "secret"}, srv.Client(), srv.URL)
		if _, err := e.Synthesize(context.Background(), "hello"); err == nil {
			t.Error("bad response accepted")
		}
		srv.Close()
		if calls.Load() != 1 {
			t.Error("retried")
		}
	}
}

func TestUnconfiguredHybridUsesCloudAndRedirectDenied(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"cloud"}}]}`))
	}))
	defer srv.Close()
	e := newEngine(Config{ASRMode: "hybrid", APIKey: "secret"}, srv.Client(), srv.URL)
	if s, err := e.Transcribe(context.Background(), wav(1), "audio/wav"); s != "cloud" || err != nil {
		t.Fatal(s, err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, srv.URL, 302) }))
	defer redirect.Close()
	e = NewEngine(Config{LocalASRURL: redirect.URL})
	if _, err := e.Transcribe(context.Background(), wav(1), "audio/wav"); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("redirect followed")
	}
}

func TestSpeechProviderStatusKeepsSafeDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{401, "speech_auth_failed"}, {403, "speech_forbidden"}, {402, "speech_quota_exceeded"}, {429, "speech_rate_limited"}, {503, "speech_unavailable"}, {400, "speech_invalid_request"}, {404, "speech_model_unavailable"}} {
		t.Run(tc.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte("private provider credential"))
			}))
			defer server.Close()
			e := newEngine(Config{TTSMode: "cloud", APIKey: "key"}, server.Client(), server.URL)
			_, err := e.Synthesize(context.Background(), "hello")
			if errorCode(err) != tc.want || strings.Contains(err.Error(), "private") {
				t.Fatalf("lost status: %s %v", errorCode(err), err)
			}
		})
	}
}

func TestSpeechMissingKeyAndBadResponseDiagnoses(t *testing.T) {
	for _, tc := range []struct{ key, body, want string }{{"", `{}`, "key_unavailable"}, {"key", `private not JSON`, "speech_invalid_response"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.body)) }))
		e := newEngine(Config{TTSMode: "cloud", APIKey: tc.key}, server.Client(), server.URL)
		_, err := e.Synthesize(context.Background(), "hello")
		server.Close()
		if errorCode(err) != tc.want {
			t.Errorf("got %s want %s", errorCode(err), tc.want)
		}
	}
}
func TestSpeechOfficialHTTPErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{421, "speech_content_filtered"}, {408, "speech_timeout"}, {504, "speech_timeout"}} {
		if got := errorCode(providerError{status: tc.status}); got != tc.want {
			t.Errorf("status %d: %s want %s", tc.status, got, tc.want)
		}
	}
}
