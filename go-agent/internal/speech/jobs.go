package speech

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"strings"
	"sync"
	"time"
)

type Request struct {
	Kind        string `json:"kind"`
	AudioBase64 string `json:"audio_base64,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Text        string `json:"text,omitempty"`
}

type Job struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Text        string `json:"text,omitempty"`
	AudioBase64 string `json:"audio_base64,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
}

type speechRunner interface {
	Transcribe(context.Context, []byte, string) (string, error)
	Synthesize(context.Context, string) ([]byte, error)
}

type jobRecord struct {
	job      Job
	owner    string
	cancel   context.CancelFunc
	active   bool
	expires  time.Time
	finished time.Time
}

// Jobs owns at most four workers and sixteen result records. A cancelled
// worker retains its slot until it exits, including while FFmpeg is stopping.
type Jobs struct {
	mu           sync.Mutex
	records      map[string]*jobRecord
	activeOwners map[string]string
	active       int
	factory      func(Config) speechRunner
}

func NewJobs() *Jobs {
	return &Jobs{records: make(map[string]*jobRecord), activeOwners: make(map[string]string), factory: func(cfg Config) speechRunner { return NewEngine(cfg) }}
}

func (j *Jobs) pruneLocked(now time.Time) {
	for id, r := range j.records {
		if !r.active && !r.expires.After(now) {
			delete(j.records, id)
		}
	}
}

func (j *Jobs) Start(owner string, cfg Config, req Request) (Job, error) {
	if strings.TrimSpace(owner) == "" || len(owner) > 1024 {
		return Job{}, ErrInvalid
	}
	var audio []byte
	switch req.Kind {
	case "transcribe":
		if req.Text != "" || req.AudioBase64 == "" || len(req.AudioBase64) > base64.StdEncoding.EncodedLen(maxAudio) || len(req.MIMEType) > 128 {
			return Job{}, ErrInvalid
		}
		var err error
		audio, err = base64.StdEncoding.DecodeString(req.AudioBase64)
		if err != nil || len(audio) == 0 || len(audio) > maxAudio {
			return Job{}, ErrInvalid
		}
	case "synthesize":
		if !validText(req.Text) || req.AudioBase64 != "" {
			return Job{}, ErrInvalid
		}
	default:
		return Job{}, ErrInvalid
	}
	if _, err := mode(cfg.ASRMode); err != nil {
		return Job{}, err
	}
	if _, err := mode(cfg.TTSMode); err != nil {
		return Job{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Job{}, ErrUnavailable
	}
	id := hex.EncodeToString(random[:])
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked(time.Now())
	if j.active >= 4 || j.activeOwners[owner] != "" {
		return Job{}, ErrBusy
	}
	if len(j.records) >= 16 {
		var oldestID string
		var oldest time.Time
		for candidate, r := range j.records {
			if !r.active && (oldestID == "" || r.finished.Before(oldest)) {
				oldestID = candidate
				oldest = r.finished
			}
		}
		if oldestID == "" {
			return Job{}, ErrBusy
		}
		delete(j.records, oldestID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	record := &jobRecord{job: Job{ID: id, Status: "queued"}, owner: owner, cancel: cancel, active: true}
	j.records[id] = record
	j.activeOwners[owner] = id
	j.active++
	// Copy the complete immutable config and request before launching work. A
	// later settings/key save cannot change a job already accepted.
	cfg.VoiceReferenceWAV = append([]byte(nil), cfg.VoiceReferenceWAV...)
	go j.run(ctx, record, cfg, req, audio)
	return record.job, nil
}

func (j *Jobs) run(ctx context.Context, r *jobRecord, cfg Config, req Request, audio []byte) {
	j.mu.Lock()
	cancelled := r.job.Status == "cancelled"
	if !cancelled {
		r.job.Status = "running"
	}
	j.mu.Unlock()
	var text string
	var output []byte
	var err error
	if !cancelled {
		engine := j.factory(cfg)
		if req.Kind == "transcribe" {
			text, err = engine.Transcribe(ctx, audio, req.MIMEType)
		} else {
			output, err = engine.Synthesize(ctx, req.Text)
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	defer r.cancel()
	r.active = false
	j.active--
	delete(j.activeOwners, r.owner)
	r.finished = time.Now()
	r.expires = r.finished.Add(120 * time.Second)
	if r.job.Status == "cancelled" {
		return
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		r.job.Status = "failed"
		r.job.ErrorCode = errorCode(err)
		if errors.Is(err, context.Canceled) {
			r.job.Status = "cancelled"
		}
		return
	}
	r.job.Status = "succeeded"
	r.job.Text = text
	if req.Kind == "synthesize" {
		r.job.AudioBase64 = base64.StdEncoding.EncodeToString(output)
		r.job.MIMEType = "audio/wav"
	}
}

func errorCode(err error) string {
	if errors.Is(err, ErrVoiceReferenceRequired) {
		return "voice_reference_required"
	}
	if errors.Is(err, assistantconfig.ErrEndpointMismatch) {
		return "mimo_endpoint_mismatch"
	}
	var provider providerError
	if errors.As(err, &provider) {
		if provider.code != "" {
			return provider.code
		}
		switch provider.status {
		case 421:
			return "speech_content_filtered"
		case 408, 504:
			return "speech_timeout"
		case 401:
			return "speech_auth_failed"
		case 403:
			return "speech_forbidden"
		case 402:
			return "speech_quota_exceeded"
		case 429:
			return "speech_rate_limited"
		case 404:
			return "speech_model_unavailable"
		case 400, 405, 413, 415, 422:
			return "speech_invalid_request"
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "speech_cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "speech_timeout"
	case errors.Is(err, ErrInvalid):
		return "speech_invalid"
	case errors.Is(err, ErrUnavailable):
		return "speech_unavailable"
	default:
		return "speech_failed"
	}
}

func (j *Jobs) Get(owner, id string) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked(time.Now())
	r, ok := j.records[id]
	if !ok || r.owner != owner {
		return Job{}, ErrNotFound
	}
	return r.job, nil
}

func cancelRecord(r *jobRecord) {
	if r.active {
		r.job = Job{ID: r.job.ID, Status: "cancelled", ErrorCode: "speech_cancelled"}
		r.cancel()
	}
}

func (j *Jobs) Cancel(owner, id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked(time.Now())
	r, ok := j.records[id]
	if !ok || r.owner != owner {
		return ErrNotFound
	}
	cancelRecord(r)
	return nil
}

// CancelAll is used when credentials are replaced or the server shuts down.
// It cancels active jobs without releasing capacity ahead of worker teardown.
func (j *Jobs) CancelAll() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, r := range j.records {
		cancelRecord(r)
	}
}
