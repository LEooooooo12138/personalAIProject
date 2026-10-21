package speech

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
)

type blockedSpeech struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockedSpeech) Transcribe(ctx context.Context, a []byte, m string) (string, error) {
	close(b.started)
	<-b.release
	return "late", nil
}
func (b *blockedSpeech) Synthesize(ctx context.Context, s string) ([]byte, error) {
	close(b.started)
	<-b.release
	return wav(1), nil
}

type immediateSpeech struct{}

func (immediateSpeech) Transcribe(context.Context, []byte, string) (string, error) {
	return "recognized", nil
}
func (immediateSpeech) Synthesize(context.Context, string) ([]byte, error) { return wav(1), nil }
func result(t *testing.T, j *Jobs, owner, id string) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, err := j.Get(owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == "succeeded" || v.Status == "failed" || v.Status == "cancelled" {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not complete")
	return Job{}
}

func TestJobsResultsOwnerIsolationAndValidation(t *testing.T) {
	j := NewJobs()
	j.factory = func(Config) speechRunner { return immediateSpeech{} }
	for _, req := range []Request{{Kind: "unknown"}, {Kind: "synthesize", Text: " "}, {Kind: "transcribe", AudioBase64: "bad", MIMEType: "audio/wav"}} {
		if _, err := j.Start("alice", Config{}, req); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	v, err := j.Start("alice", Config{}, Request{Kind: "transcribe", AudioBase64: base64.StdEncoding.EncodeToString(wav(1)), MIMEType: "audio/wav"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.Get("bob", v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross owner read", err)
	}
	if err = j.Cancel("bob", v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross owner cancel", err)
	}
	v = result(t, j, "alice", v.ID)
	if v.Status != "succeeded" || v.Text != "recognized" || v.AudioBase64 != "" {
		t.Fatal(v)
	}
	v.Text = "changed"
	again, _ := j.Get("alice", v.ID)
	if again.Text != "recognized" {
		t.Fatal("mutable snapshot")
	}
	v, err = j.Start("alice", Config{}, Request{Kind: "synthesize", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	v = result(t, j, "alice", v.ID)
	if v.Status != "succeeded" || v.MIMEType != "audio/wav" || v.AudioBase64 != base64.StdEncoding.EncodeToString(wav(1)) {
		t.Fatal("wrong synthesis result")
	}
}

func TestCancelledJobsKeepCapacityUntilWorkersReturn(t *testing.T) {
	j := NewJobs()
	var blocks []*blockedSpeech
	var mu sync.Mutex
	j.factory = func(Config) speechRunner {
		b := &blockedSpeech{make(chan struct{}), make(chan struct{})}
		mu.Lock()
		blocks = append(blocks, b)
		mu.Unlock()
		return b
	}
	var ids []string
	for _, owner := range []string{"a", "b", "c", "d"} {
		v, err := j.Start(owner, Config{}, Request{Kind: "synthesize", Text: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(blocks)
		mu.Unlock()
		if n == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workers not started")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	bs := append([]*blockedSpeech(nil), blocks...)
	mu.Unlock()
	for _, b := range bs {
		<-b.started
		defer close(b.release)
	}
	if _, err := j.Start("e", Config{}, Request{Kind: "synthesize", Text: "hello"}); !errors.Is(err, ErrBusy) {
		t.Fatal("global cap", err)
	}
	if err := j.Cancel("a", ids[0]); err != nil {
		t.Fatal(err)
	}
	v, _ := j.Get("a", ids[0])
	if v.Status != "cancelled" {
		t.Fatal(v)
	}
	if _, err := j.Start("a", Config{}, Request{Kind: "synthesize", Text: "hello"}); !errors.Is(err, ErrBusy) {
		t.Fatal("cancel freed running owner", err)
	}
	if _, err := j.Start("e", Config{}, Request{Kind: "synthesize", Text: "hello"}); !errors.Is(err, ErrBusy) {
		t.Fatal("cancel freed running global slot", err)
	}
	j.CancelAll()
	for i, owner := range []string{"a", "b", "c", "d"} {
		v, _ := j.Get(owner, ids[i])
		if v.Status != "cancelled" {
			t.Fatal(v)
		}
	}
}

func TestJobsBoundResultsAndExpire(t *testing.T) {
	j := NewJobs()
	j.factory = func(Config) speechRunner { return immediateSpeech{} }
	var ids []string
	for i := 0; i < 20; i++ {
		v, err := j.Start("owner", Config{}, Request{Kind: "synthesize", Text: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		result(t, j, "owner", v.ID)
		ids = append(ids, v.ID)
	}
	if len(j.records) > 16 {
		t.Fatal("unbounded records")
	}
	if _, err := j.Get("owner", ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("old result not evicted")
	}
	j.mu.Lock()
	for _, r := range j.records {
		r.expires = time.Now().Add(-time.Second)
	}
	j.mu.Unlock()
	if _, err := j.Get("owner", ids[len(ids)-1]); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired result visible")
	}
}

func TestCancelledJobDoesNotPublishLateResult(t *testing.T) {
	j := NewJobs()
	b := &blockedSpeech{make(chan struct{}), make(chan struct{})}
	j.factory = func(Config) speechRunner { return b }
	v, err := j.Start("owner", Config{}, Request{Kind: "synthesize", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	<-b.started
	if err := j.Cancel("owner", v.ID); err != nil {
		t.Fatal(err)
	}
	close(b.release)
	deadline := time.Now().Add(time.Second)
	for {
		j.mu.Lock()
		active := j.active
		j.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not release capacity")
		}
		time.Sleep(time.Millisecond)
	}
	v, err = j.Get("owner", v.ID)
	if err != nil || v.Status != "cancelled" || v.Text != "" || v.AudioBase64 != "" {
		t.Fatal("late cancelled result exposed", v, err)
	}
}
