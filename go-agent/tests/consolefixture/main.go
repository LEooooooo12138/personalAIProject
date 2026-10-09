// Command consolefixture runs the real console API against disposable local data.
// Run from go-agent after building the frontend into static/console.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/gateway"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

const fixturePassword = "fixture-password-123"

var errFixtureOffline = errors.New("offline fixture has no model")

type offlineInference struct{ tagsClient inference.Client }

func (offlineInference) Chat(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errFixtureOffline
}
func (offlineInference) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errFixtureOffline
}
func (client offlineInference) ListModels(ctx context.Context) ([]inference.ModelInfo, error) {
	if client.tagsClient == nil {
		return nil, errFixtureOffline
	}
	return client.tagsClient.ListModels(ctx)
}

type offlineSessionEmbedder struct{}

func (offlineSessionEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []float32{1, 0, 0}, nil
}

type fixture struct {
	dir          string
	app          *core.App
	server       *gateway.Server
	tempPassword string
	dependencies *fixtureDependencies
}

func fixtureAddress(host string, port int) (string, string, error) {
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() || ip.Is4In6() || ip.Zone() != "" || port < 1 || port > 65535 {
		return "", "", fmt.Errorf("fixture host must be a loopback IP and port must be 1-65535")
	}
	listen := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	return listen, "http://" + listen, nil
}

func newFixture(listen, origin string) (_ *fixture, err error) {
	host, portText, splitErr := net.SplitHostPort(listen)
	port, parseErr := strconv.Atoi(portText)
	wantListen, wantOrigin, addressErr := fixtureAddress(host, port)
	if splitErr != nil || parseErr != nil || addressErr != nil || listen != wantListen || origin != wantOrigin {
		return nil, fmt.Errorf("fixture requires a canonical loopback listener and matching origin")
	}
	dir, err := os.MkdirTemp("", "console-fixture-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	for _, name := range []string{"accounts", "agent", "personal"} {
		if err = os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			return nil, err
		}
	}
	keyBytes := make([]byte, 32)
	if _, err = rand.Read(keyBytes); err != nil {
		return nil, err
	}
	// SmartHome is constructed for on-demand catalog reads, never started;
	// collector/scheduler are not started. Explicit confirmation targets only the mock.
	dependencies := newFixtureDependencies()
	defer func() {
		if err != nil {
			dependencies.Close()
		}
	}()
	config := fmt.Sprintf("server:\n  listen_address: %q\n  internal_key: %q\ninference:\n  endpoint: %q\n  models:\n    local: gemma4:12b\n    embedding: bge-m3:latest\nvaults:\n  personal: %q\n  agent: %q\nsmarthome:\n  enabled: true\n  base_url: %q\n  token: %q\nconsole:\n  enabled: true\n  data_dir: %q\n  public_origin: %q\n  allow_insecure_http: true\n", listen, hex.EncodeToString(keyBytes), dependencies.tags.URL, filepath.Join(dir, "personal"), filepath.Join(dir, "agent"), dependencies.ha.URL, fixtureHAToken, filepath.Join(dir, "accounts"), origin)
	configPath := filepath.Join(dir, "fixture.yaml")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		return nil, err
	}
	// Bootstrap uses Viper environment overrides. Clear them only during this
	// call so ambient production settings cannot redirect fixture dependencies.
	savedEnv := os.Environ()
	os.Clearenv()
	app, bootstrapErr := core.Bootstrap(configPath)
	os.Clearenv()
	for _, entry := range savedEnv {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				_ = os.Setenv(entry[:i], entry[i+1:])
				break
			}
		}
	}
	if bootstrapErr != nil {
		return nil, bootstrapErr
	}
	f := &fixture{dir: dir, app: app, dependencies: dependencies}
	defer func() {
		if err != nil {
			if app.SmartHome != nil {
				app.SmartHome.Stop()
			}
			_ = app.Logger.Sync()
		}
	}()
	// Bootstrap constructs Ollama-backed objects but does not contact them.
	// Replace every server-reachable inference path before creating the gateway.
	app.Infer = offlineInference{tagsClient: inference.NewOllamaClient(dependencies.tags.URL, 5*time.Second)}
	app.SessionStore = core.NewSessionStore(app.SessionMgr, filepath.Join(dir, "agent", "_sessions"), offlineSessionEmbedder{}, app.Logger)
	if err = app.SessionStore.Initialize(context.Background()); err != nil {
		return nil, err
	}
	app.EmbedStore = vault.NewScopedEmbeddingStore(app.Infer, app.VaultR, app.Config.Vaults.Personal, "personal", app.Config.Inference.Models.Embedding, app.Logger)
	app.AgentEmbedStore = vault.NewScopedEmbeddingStore(app.Infer, app.VaultR, app.Config.Vaults.Agent, "agent", app.Config.Inference.Models.Embedding, app.Logger)
	app.Sedimenter = memory.NewSedimenter(app.Config.Vaults.Personal, memory.NewSummarizer(app.Infer, app.Logger), app.Logger)
	app.Agent = nil // The fixture starts only the gateway; discard Bootstrap's production agent graph.
	if _, err = app.ConsoleStore.Bootstrap("owner", "家长", fixturePassword); err != nil {
		return nil, err
	}
	for _, name := range []string{"alice", "bobby", "familyreader"} {
		var temporary string
		if _, temporary, err = app.ConsoleStore.CreateMember(name, name); err != nil {
			return nil, err
		}
		var loginToken string
		login, authErr := app.ConsoleStore.Authenticate(name, temporary)
		if authErr != nil {
			return nil, authErr
		}
		loginToken = login.Token
		if err = app.ConsoleStore.ChangePassword(loginToken, temporary, fixturePassword); err != nil {
			return nil, err
		}
	}
	if _, f.tempPassword, err = app.ConsoleStore.CreateMember("tempuser", "待改密成员"); err != nil {
		return nil, err
	}
	router := chain.NewChainRouter()
	router.Register("chat", chain.NewChain("chat", "offline fixture", chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
		state.FinalAnswer = "## 离线测试回复\n\n这是一条 **确定性** Markdown 回复。\n\n- 不连接真实模型\n- 仅使用本地模拟 HA 数据，不连接真实 Home Assistant"
		return nil
	})))
	if err = prepareFixtureFeatures(app, router); err != nil {
		return nil, err
	}
	app.ChainRouter = router
	app.ChainExecutor = chain.NewChainExecutor(app.Logger, router)
	f.server = gateway.NewServerFromApp(app)
	return f, nil
}

func (f *fixture) Close() error {
	f.app.SessionMgr.Stop()
	if f.app.SmartHome != nil {
		f.app.SmartHome.Stop()
	}
	f.dependencies.Close()
	_ = f.app.Logger.Sync()
	return os.RemoveAll(f.dir)
}

func run() error {
	host := flag.String("host", "127.0.0.1", "loopback IP only (127.0.0.1 or ::1)")
	port := flag.Int("port", 18081, "loopback port")
	flag.Parse()
	listen, origin, err := fixtureAddress(*host, *port)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Join("static", "console", "index.html")); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("build the frontend from go-agent before starting the fixture")
	}
	f, err := newFixture(listen, origin)
	if err != nil {
		return err
	}
	defer f.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	f.app.SessionMgr.Start(ctx)
	fmt.Printf("Console fixture: %s/app/\n", origin)
	fmt.Printf("Ready users: owner, alice, bobby, familyreader / %s\n", fixturePassword)
	fmt.Printf("First-password-change user: tempuser / %s\n", f.tempPassword)
	err = f.server.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "consolefixture:", err)
		os.Exit(1)
	}
}
