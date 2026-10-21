package main

// This file belongs only to the disposable consolefixture binary. Controls
// change bounded synthetic HA inputs; they never access Console state/stores.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type fixtureWriteSnapshot struct {
	Total   int `json:"total"`
	Config  int `json:"config"`
	Reload  int `json:"reload"`
	Enable  int `json:"enable"`
	Disable int `json:"disable"`
	Delete  int `json:"delete"`
	Device  int `json:"device"`
	Other   int `json:"other"`
}
type fixtureWriteAttempts struct {
	mu     sync.Mutex
	counts fixtureWriteSnapshot
}

func (c *fixtureWriteAttempts) record(category string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts.Total++
	switch category {
	case "config":
		c.counts.Config++
	case "reload":
		c.counts.Reload++
	case "enable":
		c.counts.Enable++
	case "disable":
		c.counts.Disable++
	case "delete":
		c.counts.Delete++
	case "device":
		c.counts.Device++
	default:
		c.counts.Other++
	}
}
func (c *fixtureWriteAttempts) recordHTTP(r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return
	}
	category := "other"
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/config/automation/config/") && r.Method == http.MethodPost:
		category = "config"
	case strings.HasPrefix(r.URL.Path, "/api/config/automation/config/") && r.Method == http.MethodDelete:
		category = "delete"
	case r.Method == http.MethodPost && r.URL.Path == "/api/services/automation/reload":
		category = "reload"
	case r.Method == http.MethodPost && r.URL.Path == "/api/services/automation/turn_on":
		category = "enable"
	case r.Method == http.MethodPost && r.URL.Path == "/api/services/automation/turn_off":
		category = "disable"
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/services/") && !strings.HasPrefix(r.URL.Path, "/api/services/automation/"):
		category = "device"
	}
	c.record(category)
}
func (c *fixtureWriteAttempts) snapshot() fixtureWriteSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts
}

type lifecycleModes struct {
	History        string `json:"history"`
	Profile        string `json:"profile"`
	Config         string `json:"config"`
	Runtime        string `json:"runtime"`
	Registry       string `json:"registry"`
	Trace          string `json:"trace"`
	State          string `json:"state"`
	Fault          string `json:"fault"`
	HistoryDelayMS int    `json:"history_delay_ms"`
	ReadDelayMS    int    `json:"read_delay_ms"`
}

func normalizeLifecycleModes(m *lifecycleModes) error {
	for _, field := range []struct {
		value    *string
		fallback string
		allowed  []string
	}{
		{&m.History, "empty", []string{"empty", "pattern", "forbidden"}},
		{&m.Profile, "supported", []string{"supported", "unknown"}},
		{&m.Config, "available", []string{"available", "forbidden", "drift"}},
		{&m.Runtime, "available", []string{"available", "missing", "drift"}},
		{&m.Registry, "available", []string{"available", "forbidden", "missing"}},
		{&m.Trace, "finished", []string{"finished", "empty", "forbidden", "unsupported", "retention_gap"}},
		{&m.State, "observed", []string{"observed", "on", "off", "unavailable"}},
		{&m.Fault, "none", []string{"none", "lose_config", "reject_config", "lose_reload", "lose_management"}},
	} {
		if *field.value == "" {
			*field.value = field.fallback
		}
		valid := false
		for _, allowed := range field.allowed {
			valid = valid || *field.value == allowed
		}
		if !valid {
			return fmt.Errorf("invalid lifecycle mode")
		}
	}
	if m.HistoryDelayMS < 0 || m.HistoryDelayMS > 5000 || m.ReadDelayMS < 0 || m.ReadDelayMS > 5000 {
		return fmt.Errorf("invalid lifecycle delay")
	}
	return nil
}

type fixtureLifecycle struct {
	mu             sync.Mutex
	enabled        bool
	modes          lifecycleModes
	deps           *fixtureDependencies
	stored, loaded map[string]json.RawMessage // guarded by deps.stateMu
	states         map[string]string          // guarded by deps.stateMu
	entity         func(string) string
	ctx            context.Context
	cancel         context.CancelFunc
	historyReads   atomic.Int32
	traceAt        time.Time
}

func newFixtureLifecycle(d *fixtureDependencies, stored, loaded map[string]json.RawMessage, entity func(string) string) *fixtureLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	l := &fixtureLifecycle{deps: d, stored: stored, loaded: loaded, states: map[string]string{}, entity: entity, ctx: ctx, cancel: cancel, traceAt: time.Now().UTC().Add(-30 * time.Second)}
	_ = normalizeLifecycleModes(&l.modes)
	return l
}
func (l *fixtureLifecycle) snapshot() (lifecycleModes, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.modes, l.enabled
}
func (l *fixtureLifecycle) consumeFault(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.enabled && l.modes.Fault == name {
		l.modes.Fault = "none"
		return true
	}
	return false
}
func (l *fixtureLifecycle) delay(ctx context.Context, ms int) bool {
	if ms == 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	case <-l.ctx.Done():
		return false
	}
}

type lifecycleControl struct {
	once                            sync.Once
	server                          *httptest.Server
	descriptorPath, url, capability string
	descriptor                      []byte
	lifecycle                       *fixtureLifecycle
}

func (d *fixtureDependencies) startLifecycleControl(root string) (*lifecycleControl, error) {
	if d.lifecycleControl != nil || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("lifecycle requires a fresh absolute runner temp root")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(filepath.Clean(root), filepath.Clean(resolved)) {
		return nil, fmt.Errorf("lifecycle temp root must not contain symlinks")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("lifecycle temp root must exist")
	}
	capability := make([]byte, 32)
	if _, err = rand.Read(capability); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &lifecycleControl{url: "http://" + listener.Addr().String(), capability: hex.EncodeToString(capability), descriptorPath: filepath.Join(root, "automation-lifecycle.json"), lifecycle: d.lifecycle}
	c.server = httptest.NewUnstartedServer(http.HandlerFunc(c.serveHTTP))
	c.server.Listener.Close()
	c.server.Listener = listener
	c.server.Config.ReadHeaderTimeout = 2 * time.Second
	c.server.Config.ReadTimeout = 3 * time.Second
	c.server.Config.WriteTimeout = 3 * time.Second
	c.server.Start()
	c.descriptor, _ = json.Marshal(map[string]any{"schema_version": 1, "url": c.url, "capability": c.capability})
	file, err := os.OpenFile(c.descriptorPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		c.server.Close()
		return nil, err
	}
	_, writeErr := file.Write(c.descriptor)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		c.Close()
		return nil, fmt.Errorf("lifecycle descriptor write failed")
	}
	d.lifecycle.mu.Lock()
	d.lifecycle.enabled = true
	d.lifecycle.mu.Unlock()
	d.lifecycleControl = c
	return c, nil
}
func (c *lifecycleControl) Close() {
	c.once.Do(func() {
		c.lifecycle.cancel()
		c.server.Close()
		info, err := os.Lstat(c.descriptorPath)
		if err == nil && info.Mode().IsRegular() {
			raw, err := os.ReadFile(c.descriptorPath)
			if err == nil && bytes.Equal(raw, c.descriptor) {
				_ = os.Remove(c.descriptorPath)
			}
		}
	})
}
func (c *lifecycleControl) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || r.Host != strings.TrimPrefix(c.url, "http://") || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.capability)) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/state":
		modes, _ := c.lifecycle.snapshot()
		_ = json.NewEncoder(w).Encode(map[string]any{"modes": modes, "writes": c.lifecycle.deps.writeAttempts.snapshot(), "history_reads": c.lifecycle.historyReads.Load()})
	case r.Method == http.MethodPut && r.URL.Path == "/mode":
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(raw) > 4096 || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
			http.Error(w, "invalid mode", 400)
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var modes lifecycleModes
		if decoder.Decode(&modes) != nil || decoder.Decode(&struct{}{}) != io.EOF || normalizeLifecycleModes(&modes) != nil {
			http.Error(w, "invalid mode", 400)
			return
		}
		c.lifecycle.mu.Lock()
		c.lifecycle.modes = modes
		c.lifecycle.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"modes": modes})
	default:
		http.NotFound(w, r)
	}
}
