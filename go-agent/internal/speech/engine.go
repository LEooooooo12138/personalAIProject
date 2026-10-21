// Package speech implements bounded, explicitly selected speech providers.
package speech

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const maxAudio = 8 << 20
const cloudEndpoint = "https://api.xiaomimimo.com/v1/chat/completions"

var (
	ErrVoiceReferenceRequired = errors.New("voice_reference_required")
	ErrNotFound               = errors.New("speech_not_found")
	ErrBusy                   = errors.New("speech_busy")
	ErrInvalid                = errors.New("speech_invalid")
	ErrUnavailable            = errors.New("speech_unavailable")
)

// Config is copied when an engine or job is created; modes default to local.
type Config struct {
	MiMoEndpoint      string
	ASRMode           string
	TTSMode           string
	TTSModel          string
	TTSVoice          string
	VoiceReferenceWAV []byte
	APIKey            string
	LocalASRURL       string
	LocalTTSURL       string
	FFmpegPath        string
}

type Engine struct {
	cfg      Config
	client   *http.Client
	cloudURL string
}

var defaultHTTPClient = func() *http.Client {
	// No environment proxy: local audio and cloud credentials must reach only
	// their explicit destinations. Redirects are disabled in newEngine.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: 90 * time.Second}
}()

func NewEngine(cfg Config) *Engine {
	return newEngine(cfg, defaultHTTPClient, cloudEndpoint)
}

func newEngine(cfg Config, client *http.Client, endpoint string) *Engine {
	cfg.VoiceReferenceWAV = append([]byte(nil), cfg.VoiceReferenceWAV...)
	copyClient := *client
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 90 * time.Second
	}
	return &Engine{cfg: cfg, client: &copyClient, cloudURL: endpoint}
}

func mode(s string) (string, error) {
	if s == "" {
		return "local", nil
	}
	switch s {
	case "local", "cloud", "hybrid":
		return s, nil
	default:
		return "", ErrInvalid
	}
}

// Transcribe accepts a bounded audio recording, validating duration before any
// provider request. Browser formats are decoded by an optional local FFmpeg.
func (e *Engine) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	selected, err := mode(e.cfg.ASRMode)
	if err != nil {
		return "", err
	}
	normalized, err := normalizeAudio(ctx, audio, mimeType, e.cfg.FFmpegPath)
	if err != nil {
		return "", err
	}
	if selected == "cloud" {
		return e.cloudASR(ctx, normalized)
	}
	text, err := e.localASR(ctx, normalized)
	if selected == "hybrid" && fallbackAllowed(ctx, err) {
		return e.cloudASR(ctx, normalized)
	}
	return text, err
}

func validText(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(s) <= 16000
}

func (e *Engine) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validText(text) {
		return nil, ErrInvalid
	}
	selected, err := mode(e.cfg.TTSMode)
	if err != nil {
		return nil, err
	}
	if selected == "cloud" {
		return e.cloudTTS(ctx, text)
	}
	audio, err := e.localTTS(ctx, text)
	if selected == "hybrid" && fallbackAllowed(ctx, err) {
		return e.cloudTTS(ctx, text)
	}
	return audio, err
}

type providerError struct {
	transient bool
	status    int
	code      string
}

func (e providerError) Error() string { return "speech_upstream_error" }
func (e providerError) Unwrap() error { return ErrUnavailable }

func fallbackAllowed(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if err == ErrUnavailable {
		return true
	} // local service not configured
	var p providerError
	return errors.As(err, &p) && p.transient
}

func transientNetworkError(err error) bool {
	// Certificate and protocol errors are configuration failures; automatic
	// cloud fallback would silently change policy for a broken local setup.
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	if errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) || errors.As(err, &verify) || errors.As(err, &record) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// localEndpoint allows literal loopback IPs and localhost only, without user
// credentials, query parameters or fragments. Resolve localhost to a literal
// loopback address to prevent resolver/proxy surprises.
func localEndpoint(raw string) (string, error) {
	if raw == "" {
		return "", ErrUnavailable
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrInvalid
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		port := u.Port()
		u.Host = "127.0.0.1"
		if port != "" {
			u.Host = net.JoinHostPort("127.0.0.1", port)
		}
	} else if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", ErrInvalid
	}
	return u.String(), nil
}

func (e *Engine) request(ctx context.Context, endpoint, contentType string, body []byte, cloud bool, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cloud && endpoint == cloudEndpoint {
		base, err := assistantconfig.MiMoBaseURL(e.cfg.MiMoEndpoint, e.cfg.APIKey)
		if err != nil {
			return nil, err
		}
		endpoint = base + "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Content-Type", contentType)
	if cloud {
		if strings.TrimSpace(e.cfg.APIKey) == "" {
			return nil, providerError{code: "key_unavailable"}
		}
		req.Header.Set("api-key", e.cfg.APIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, providerError{transient: true, code: "speech_timeout"}
		}
		return nil, providerError{transient: transientNetworkError(err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, providerError{transient: resp.StatusCode >= 500, status: resp.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, providerError{transient: true}
	}
	if int64(len(b)) > limit {
		return nil, ErrInvalid
	}
	return b, nil
}

func (e *Engine) jsonRequest(ctx context.Context, endpoint string, payload any, cloud bool, limit int64) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalid
	}
	return e.request(ctx, endpoint, "application/json", b, cloud, limit)
}

func (e *Engine) localASR(ctx context.Context, audio []byte) (string, error) {
	endpoint, err := localEndpoint(e.cfg.LocalASRURL)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "recording.wav")
	if err != nil {
		return "", ErrInvalid
	}
	if _, err = part.Write(audio); err != nil {
		return "", ErrInvalid
	}
	// Stable adapter defaults. A service may map these aliases to its installed
	// model; the application never installs or downloads a runtime implicitly.
	if err = writer.WriteField("model", "whisper-1"); err != nil {
		return "", ErrInvalid
	}
	if err = writer.Close(); err != nil {
		return "", ErrInvalid
	}
	b, err := e.request(ctx, endpoint, writer.FormDataContentType(), body.Bytes(), false, 1<<20)
	if err != nil {
		return "", err
	}
	var result struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(b, &result) != nil || !validText(result.Text) {
		return "", ErrInvalid
	}
	return result.Text, nil
}

func (e *Engine) localTTS(ctx context.Context, text string) ([]byte, error) {
	endpoint, err := localEndpoint(e.cfg.LocalTTSURL)
	if err != nil {
		return nil, err
	}
	b, err := e.jsonRequest(ctx, endpoint, map[string]any{"model": "tts-1", "input": text, "voice": "alloy", "response_format": "wav"}, false, maxAudio)
	if err != nil {
		return nil, err
	}
	if err = validateWAV(b, 120); err != nil {
		return nil, err
	}
	return b, nil
}

type cloudResult struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Audio   struct {
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"message"`
	} `json:"choices"`
}

func (e *Engine) cloudASR(ctx context.Context, audio []byte) (string, error) {
	encoded := base64.StdEncoding.EncodeToString(audio)
	if len(encoded) >= 10000000 {
		return "", ErrInvalid
	}
	payload := map[string]any{"model": "mimo-v2.5-asr", "stream": false, "asr_options": map[string]string{"language": "auto"}, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": "data:audio/wav;base64," + encoded}}}}}}
	b, err := e.jsonRequest(ctx, e.cloudURL, payload, true, 1<<20)
	if err != nil {
		return "", err
	}
	var result cloudResult
	if json.Unmarshal(b, &result) != nil || len(result.Choices) == 0 || !validText(result.Choices[0].Message.Content) {
		return "", providerError{code: "speech_invalid_response"}
	}
	return result.Choices[0].Message.Content, nil
}

func (e *Engine) cloudTTS(ctx context.Context, text string) ([]byte, error) {
	model, voice := e.cfg.TTSModel, e.cfg.TTSVoice
	if model == "" {
		model = assistantconfig.DefaultTTSModel
	}
	if voice == "" {
		voice = assistantconfig.DefaultTTSVoice
	}
	if !assistantconfig.ValidTTSModel(model) || !assistantconfig.ValidTTSVoice(voice) {
		return nil, ErrInvalid
	}
	if model == assistantconfig.VoiceCloneTTSModel {
		if len(e.cfg.VoiceReferenceWAV) == 0 {
			return nil, ErrVoiceReferenceRequired
		}
		if len(e.cfg.VoiceReferenceWAV) > 6<<20 || validateWAV(e.cfg.VoiceReferenceWAV, 60) != nil {
			return nil, ErrInvalid
		}
		voice = "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(e.cfg.VoiceReferenceWAV)
	}
	payload := map[string]any{"model": model, "stream": false, "audio": map[string]string{"format": "wav", "voice": voice}, "messages": []any{map[string]string{"role": "assistant", "content": text}}}
	b, err := e.jsonRequest(ctx, e.cloudURL, payload, true, int64(base64.StdEncoding.EncodedLen(maxAudio)+1<<16))
	if err != nil {
		return nil, err
	}
	var result cloudResult
	if json.Unmarshal(b, &result) != nil || len(result.Choices) == 0 {
		return nil, providerError{code: "speech_invalid_response"}
	}
	encoded := result.Choices[0].Message.Audio.Data
	if len(encoded) > base64.StdEncoding.EncodedLen(maxAudio) {
		return nil, providerError{code: "speech_invalid_response"}
	}
	audio, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, providerError{code: "speech_invalid_response"}
	}
	if err = validateWAV(audio, 120); err != nil {
		return nil, providerError{code: "speech_invalid_response"}
	}
	return audio, nil
}
