package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type auditChannel struct {
	response string
	input    chan channel.Message
}

func (c *auditChannel) ID() string                      { return "audit" }
func (c *auditChannel) Type() channel.Type              { return channel.External }
func (c *auditChannel) Start(context.Context) error     { return nil }
func (c *auditChannel) Stop() error                     { return nil }
func (c *auditChannel) Receive() <-chan channel.Message { return c.input }
func (c *auditChannel) Send(_ channel.Message, r channel.Response) error {
	c.response = r.Content
	return nil
}

// Catches omitted chain injection and external requests selecting personal data.
func TestAgentExternalMessageUsesAgentVault(t *testing.T) {
	log := zap.NewNop()
	ch := &auditChannel{}
	cm := channel.NewManager(log)
	cm.Register(ch)
	router := chain.NewChainRouter()
	router.Register("chat", chain.NewChain("chat", "", chain.NewFuncStep("answer", func(_ context.Context, s *chain.ChainState) error { s.FinalAnswer = s.Vault; return nil })))
	a := NewAgent(AgentDeps{Config: &Config{Inference: InferenceConfig{Timeout: time.Second}}, Logger: log, SessionMgr: NewSessionManager(DefaultSessionConfig(), log), FilterChain: filter.NewChain(), ChannelMgr: cm, ChainRouter: router, ChainExecutor: chain.NewChainExecutor(log, router)})
	a.handleMessage(channel.Message{ChannelID: "audit", UserID: "alice", Content: "hello", Timestamp: time.Now()})
	if ch.response != "agent" {
		t.Fatalf("external reply=%q, want agent-scoped chain response", ch.response)
	}
}

type auditRunner struct{ run func(context.Context) error }

func (r auditRunner) Run(ctx context.Context) error { return r.run(ctx) }
func TestAppStartsSmartHomeCollection(t *testing.T) {
	called := make(chan struct{}, 1)
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case called <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer ha.Close()
	log := zap.NewNop()
	dir := t.TempDir()
	sh, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "synthetic", PollIntervalSec: 3600, AgentVaultPath: dir}, log)
	if err != nil {
		t.Fatal(err)
	}
	cm := channel.NewManager(log)
	mgr := NewSessionManager(DefaultSessionConfig(), log)
	cfg := &Config{}
	reader := vault.NewFileReader(dir, dir)
	app := &App{Logger: log, Config: cfg, ChMgr: cm, SmartHome: sh, EmbedStore: vault.NewEmbeddingStore(nil, reader, dir, log), Agent: NewAgent(AgentDeps{Config: cfg, Logger: log, SessionMgr: mgr, ChannelMgr: cm})}
	seen := false
	app.Server = auditRunner{func(context.Context) error {
		select {
		case <-called:
			seen = true
		case <-time.After(time.Second):
		}
		return nil
	}}
	if err := app.Run(); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("App.Run never started HA collection")
	}
}

func TestCompletedSessionReleasesCapacity(t *testing.T) {
	cfg := DefaultSessionConfig()
	cfg.MaxSessions = 1
	m := NewSessionManager(cfg, zap.NewNop())
	s, _ := m.GetOrCreate("webchat", "alice")
	m.EndSession(s)
	m.CompleteSession(s)
	if _, err := m.GetOrCreate("webchat", "bob"); err != nil {
		t.Fatalf("closed session kept capacity: %v", err)
	}
}

func TestSessionFileCannotEscapeDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions")
	m := NewSessionManager(DefaultSessionConfig(), zap.NewNop())
	store := NewSessionStore(m, dir, nil, zap.NewNop())
	s, _ := m.GetOrCreate("webchat", "x/../../escaped")
	_ = store.SaveSession(s)
	if _, err := os.Stat(filepath.Join(root, "escaped.json")); !os.IsNotExist(err) {
		t.Fatalf("session write escaped root: %v", err)
	}
}

func TestArchivedConversationsDoNotConsumeActiveCapacity(t *testing.T) {
	cfg := DefaultSessionConfig()
	cfg.MaxSessions = 1
	dir := t.TempDir()
	m := NewSessionManager(cfg, zap.NewNop())
	store := NewSessionStore(m, dir, &mockEmbedder{vec: []float32{1}}, zap.NewNop())
	for _, id := range []string{"one", "two"} {
		s, err := store.BrowserSession("webchat", id, "owner", true)
		if err != nil {
			t.Fatal(err)
		}
		m.AddMessage(s, Message{Role: "user", Content: "hello"})
		if err := store.SaveSession(s); err != nil {
			t.Fatal(err)
		}
		m.EndSession(s)
		m.CompleteSession(s)
	}
	reloaded := NewSessionStore(NewSessionManager(cfg, zap.NewNop()), dir, &mockEmbedder{vec: []float32{1}}, zap.NewNop())
	if err := reloaded.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reloaded.mgr.ActiveCount() != 0 {
		t.Fatal("archived conversations restored as active at startup")
	}
	s, err := reloaded.BrowserSession("webchat", "two", "owner", false)
	if err != nil || len(s.Messages) != 1 {
		t.Fatalf("owned history could not resume: %v", err)
	}
}

func TestSensitiveCloudHintStaysLocal(t *testing.T) {
	d := NewModelRouter("local-test", "vision-test").Decide(nil, "cloud", map[string]string{"sensitive": "true"})
	if d.Backend != "local" || d.TargetModel != "local-test" {
		t.Fatalf("sensitive routed to %+v", d)
	}
}

func TestUnresolvedManagementKeyCannotBecomeLiteralCredential(t *testing.T) {
	name := "AUDIT_MISSING_MANAGEMENT_KEY"
	old, had := os.LookupEnv(name)
	os.Unsetenv(name)
	t.Cleanup(func() {
		if had {
			os.Setenv(name, old)
		} else {
			os.Unsetenv(name)
		}
	})
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  internal_key: ${AUDIT_MISSING_MANAGEMENT_KEY}\ninference:\n  endpoint: http://127.0.0.1:1\nvaults:\n  personal: personal\n  agent: agent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err == nil && cfg.Server.InternalKey != "" {
		t.Fatal("unresolved environment placeholder became a usable management credential")
	}
}

func TestRoundLimitPreservesTheLastCompleteExchange(t *testing.T) {
	cfg := DefaultSessionConfig()
	cfg.MaxRounds = 1
	m := NewSessionManager(cfg, zap.NewNop())
	s, _ := m.GetOrCreate("webchat", "alice")
	if !m.AddMessage(s, Message{Role: "user", Content: "last question"}) {
		t.Fatal("the final allowed question was rejected before it could receive a reply")
	}
	if !m.AddMessage(s, Message{Role: "assistant", Content: "last reply"}) {
		t.Fatal("the final reply was rejected")
	}
	if m.AddMessage(s, Message{Role: "user", Content: "next conversation"}) || len(CloneSession(s).Messages) != 2 {
		t.Fatal("rejected next question polluted the completed conversation")
	}
}

func TestEndingSessionDoesNotBlockItsReplacement(t *testing.T) {
	cfg := DefaultSessionConfig()
	cfg.MaxSessions = 1
	m := NewSessionManager(cfg, zap.NewNop())
	old, _ := m.GetOrCreate("webchat", "alice")
	m.EndSession(old)
	replacement, err := m.GetOrCreate("webchat", "alice")
	if err != nil {
		t.Fatal(err)
	}
	m.CompleteSession(old)
	if current, err := m.GetOrCreate("webchat", "alice"); err != nil || current != replacement {
		t.Fatal("finishing the previous conversation removed its replacement")
	}
}

func TestAgentReportsChainFailureWithoutLeakingDetails(t *testing.T) {
	log := zap.NewNop()
	ch := &auditChannel{}
	cm := channel.NewManager(log)
	cm.Register(ch)
	router := chain.NewChainRouter()
	router.Register("chat", chain.NewChain("chat", "", chain.NewFuncStep("fail", func(context.Context, *chain.ChainState) error { return errors.New("synthetic-private-detail") })))
	a := NewAgent(AgentDeps{Config: &Config{Inference: InferenceConfig{Timeout: time.Second}}, Logger: log, SessionMgr: NewSessionManager(DefaultSessionConfig(), log), FilterChain: filter.NewChain(), ChannelMgr: cm, ChainRouter: router, ChainExecutor: chain.NewChainExecutor(log, router)})
	a.handleMessage(channel.Message{ChannelID: "audit", UserID: "alice", Content: "hello"})
	if ch.response == "" || strings.Contains(ch.response, "synthetic-private-detail") {
		t.Fatalf("missing or unsafe error response: %q", ch.response)
	}
}

func TestAgentCancellationReachesInflightChain(t *testing.T) {
	log := zap.NewNop()
	ch := &auditChannel{input: make(chan channel.Message, 1)}
	cm := channel.NewManager(log)
	cm.Register(ch)
	started, stopped, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	router := chain.NewChainRouter()
	router.Register("chat", chain.NewChain("chat", "", chain.NewFuncStep("wait", func(ctx context.Context, _ *chain.ChainState) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})))
	a := NewAgent(AgentDeps{Config: &Config{Inference: InferenceConfig{Timeout: 2 * time.Second}}, Logger: log, SessionMgr: NewSessionManager(DefaultSessionConfig(), log), FilterChain: filter.NewChain(), ChannelMgr: cm, ChainRouter: router, ChainExecutor: chain.NewChainExecutor(log, router)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { a.Run(ctx); close(done) }()
	ch.input <- channel.Message{ChannelID: "audit", UserID: "alice", Content: "hello"}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("chain did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Agent did not cancel its inflight chain")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Agent returned before the chain stopped")
	}
}

type auditFatalHook struct{}

func (auditFatalHook) OnWrite(*zapcore.CheckedEntry, []zapcore.Field) {
	panic("fatal bypassed cleanup")
}

func TestAppReturnsServerFailureAndCancelsWorkers(t *testing.T) {
	log := zap.NewNop().WithOptions(zap.WithFatalHook(auditFatalHook{}))
	cm := channel.NewManager(log)
	cfg := &Config{}
	app := &App{Config: cfg, Logger: log, ChMgr: cm, Agent: NewAgent(AgentDeps{Config: cfg, Logger: log, ChannelMgr: cm, SessionMgr: NewSessionManager(DefaultSessionConfig(), log)})}
	want := errors.New("synthetic bind failure")
	app.Server = auditRunner{func(context.Context) error { return want }}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Run terminated instead of returning a recoverable server error: %v", r)
		}
	}()
	if err := app.Run(); !errors.Is(err, want) {
		t.Fatalf("server error = %v", err)
	}
	if app.Context().Err() == nil {
		t.Fatal("workers' lifecycle context remained live after server failure")
	}
}

func TestShortImageRequestUsesVisionModel(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://a"}}]}]}`)
	d := NewModelRouter("text", "vision").Decide(body, "", nil)
	if d.TargetModel != "vision" {
		t.Fatalf("short multimodal request selected %q", d.TargetModel)
	}
}
