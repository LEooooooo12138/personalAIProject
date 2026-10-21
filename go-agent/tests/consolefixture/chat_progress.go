package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
)

// Disposable fixture only. Real AssistantClient, classifier, direct chain,
// output filtering and persistence execute; no remote transport is allowed.
type progressTransport struct {
	base    http.RoundTripper
	fixture *fixture
	mu      sync.Mutex
	metrics string
}

var progressMarker = regexp.MustCompile(`C7_[A-Z0-9_]+`)

func (p *progressTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != "https://token-plan-cn.xiaomimimo.com/v1/chat/completions" || r.Header.Get("api-key") != "tp-c7-fixture-not-real" {
		if ip := net.ParseIP(r.URL.Hostname()); ip != nil && ip.IsLoopback() {
			return p.base.RoundTrip(r)
		}
		return nil, fmt.Errorf("fixture destination blocked")
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var body struct {
		Messages       []struct{ Role, Content string }
		ResponseFormat json.RawMessage `json:"response_format"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	marker := ""
	for _, message := range body.Messages {
		if message.Role == "user" {
			if found := progressMarker.FindString(message.Content); found != "" {
				marker = found
			}
		}
	}
	kind, delay := "generate", 650*time.Millisecond
	content := "C7 cloud answer"
	if len(body.ResponseFormat) > 0 {
		kind, delay, content = "classify", 450*time.Millisecond, `{"kind":"chat","entity_id":"","action":"","question":""}`
	}
	if strings.Contains(marker, "LONG") {
		delay = 3500 * time.Millisecond
	}
	start := time.Now()
	canceled := false
	defer func() {
		if marker == "" {
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		f, e := os.OpenFile(p.metrics, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return
		}
		defer f.Close()
		_ = json.NewEncoder(f).Encode(map[string]any{"marker": marker, "kind": kind, "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000, "canceled": canceled, "ha_writes": p.fixture.dependencies.deviceWrites.Load() + p.fixture.dependencies.automationWrites.Load(), "request_bytes": len(raw)})
	}()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		canceled = true
		return nil, r.Context().Err()
	case <-timer.C:
	}
	if marker == "C7_SAVE_FAIL" && kind == "generate" {
		mgr := p.fixture.app.SessionMgr
		mgr.Mu().RLock()
		var target string
		for _, session := range mgr.Sessions() {
			clone := core.CloneSession(session)
			for _, message := range clone.Messages {
				if strings.Contains(message.Content, marker) {
					target = filepath.Join(p.fixture.dir, "agent", "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte(clone.ID))))
				}
			}
		}
		mgr.Mu().RUnlock()
		if target == "" {
			return nil, fmt.Errorf("fixture save target absent")
		}
		if err := os.Remove(target); err != nil {
			return nil, err
		}
		if err := os.Mkdir(target, 0700); err != nil {
			return nil, err
		}
	}
	payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(payload)), Request: r}, nil
}
func (f *fixture) prepareProgress(router *chain.ChainRouter) error {
	metrics := filepath.Join(f.dir, "chat-progress-metrics.jsonl")
	if explicit := os.Getenv("CONSOLE_FIXTURE_METRICS_PATH"); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return fmt.Errorf("fixture metrics path must be absolute")
		}
		metrics = explicit
	}
	if err := os.MkdirAll(filepath.Dir(metrics), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(metrics, nil, 0600); err != nil {
		return err
	}
	absolute, err := filepath.Abs(metrics)
	if err != nil {
		return err
	}
	original := http.DefaultTransport
	http.DefaultTransport = &progressTransport{base: original, fixture: f, metrics: absolute}
	f.restoreTransport = func() { http.DefaultTransport = original }
	f.app.Infer = inference.NewAssistantClient(f.app.Infer)
	f.app.ControlChat = core.NewControlChat(f.app.Control, chain.NewHAIntentParser(f.app.Infer, f.app.Config.Inference.Models.Local))
	built, err := chain.BuildRAGChatChain(f.app.VaultR, f.app.Infer, f.app.Config.Inference.Models.Local, nil, nil, f.app.Logger)
	if err != nil {
		return err
	}
	direct, err := built.Route("direct")
	if err != nil {
		return err
	}
	router.Register("direct", direct)
	return nil
}
