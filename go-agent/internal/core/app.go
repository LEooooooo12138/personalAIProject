package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

// App is the top-level application container that wires all runtime dependencies.
type App struct {
	Config *Config
	Logger *zap.Logger

	Infer  inference.Client
	VaultR vault.Reader
	VaultW vault.Writer

	ModelRouter *ModelRouter
	FilterChain *filter.Chain
	ChMgr       *channel.Manager

	SessionMgr   *SessionManager
	SessionStore *SessionStore
	ConsoleStore *console.Store

	ChainExecutor *chain.ChainExecutor
	ChainRouter   *chain.ChainRouter

	Sedimenter      *memory.Sedimenter
	EmbedStore      *vault.EmbeddingStore
	AgentEmbedStore *vault.EmbeddingStore

	// Smart home subsystem (Phase 4).
	SmartHome *smarthome.Manager

	Agent  *Agent
	Server interface {
		Run(ctx context.Context) error
	}

	ctx    context.Context
	cancel context.CancelFunc
}

// Bootstrap initializes the full application from a config file path.
func Bootstrap(configPath string) (*App, error) {
	logger, err := zap.NewDevelopment()
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	// Normalize existing relative configuration before validating relationships.
	personalRoot, err := filepath.Abs(cfg.Vaults.Personal)
	if err != nil {
		return nil, fmt.Errorf("vault policy: personal root: %w", err)
	}
	agentRoot, err := filepath.Abs(cfg.Vaults.Agent)
	if err != nil {
		return nil, fmt.Errorf("vault policy: agent root: %w", err)
	}
	archiveRoot, err := filepath.Abs(cfg.SmartHome.AgentVaultPath)
	if err != nil {
		return nil, fmt.Errorf("vault policy: HA archive: %w", err)
	}
	policy, err := vault.NewContentPolicy(map[string]string{"personal": personalRoot, "agent": agentRoot}, []string{archiveRoot})
	if err != nil {
		return nil, err
	}
	var consoleStore *console.Store
	if cfg.Console.Enabled {
		consoleStore, err = console.OpenStore(cfg.Console.DataDir, time.Now)
		if err != nil {
			return nil, fmt.Errorf("console store: %w", err)
		}
	}

	backend := inference.NewOllamaClient(cfg.Inference.Endpoint, cfg.Inference.Timeout)
	vaultReader := vault.NewFileReaderWithPolicy(cfg.Vaults.Personal, cfg.Vaults.Agent, policy)

	app := &App{
		Config:       cfg,
		Logger:       logger,
		Infer:        backend,
		VaultR:       vaultReader,
		VaultW:       vault.NewFileWriter(cfg.Vaults.Personal, cfg.Vaults.Agent),
		ConsoleStore: consoleStore,
	}

	// Channels
	app.ChMgr = channel.NewManager(logger)
	app.ChMgr.Register(channel.NewInternalChannel())

	if cfg.Channels.Wecom.Enabled {
		factory, ok := channel.GetFactory("wecom")
		if !ok {
			logger.Fatal("wecom channel enabled but factory not registered (import missing?)")
		}
		wcAdapter, err := factory.Create(cfg.Channels.Wecom.Config, logger)
		if err != nil {
			logger.Fatal("wecom adapter", zap.Error(err))
		}
		app.ChMgr.Register(wcAdapter)
	}

	// Router & filter
	app.ModelRouter = NewModelRouter(cfg.Inference.Models.Local, cfg.Inference.Models.Vision)
	app.FilterChain = filter.NewChain()

	// Session management
	sessionCfg := SessionConfig{
		MaxRounds:    cfg.Session.MaxRounds,
		IdleTimeout:  cfg.Session.IdleTimeout,
		ScanInterval: cfg.Session.ScanInterval,
		MaxSessions:  cfg.Session.MaxSessions,
	}
	app.SessionMgr = NewSessionManager(sessionCfg, logger)

	// Session persistence
	embedAdapter := &ollamaEmbedder{client: backend, model: cfg.Inference.Models.Embedding}
	app.SessionStore = NewSessionStore(app.SessionMgr, cfg.Vaults.Agent+"/_sessions", embedAdapter, logger)
	if err := app.SessionStore.Initialize(context.Background()); err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}

	// Memory sedimentation
	app.Sedimenter = memory.NewSedimenter(cfg.Vaults.Personal, memory.NewSummarizer(app.Infer, logger), logger)

	// Embedding store
	app.EmbedStore = vault.NewScopedEmbeddingStoreWithPolicy(app.Infer, app.VaultR, cfg.Vaults.Personal, "personal", cfg.Inference.Models.Embedding, logger, policy)
	app.AgentEmbedStore = vault.NewScopedEmbeddingStoreWithPolicy(app.Infer, app.VaultR, cfg.Vaults.Agent, "agent", cfg.Inference.Models.Embedding, logger, policy)

	// Smart home subsystem (Phase 4).
	if cfg.SmartHome.Enabled && cfg.SmartHome.BaseURL != "" {
		shCfg := smarthome.HAConfig{
			BaseURL:              cfg.SmartHome.BaseURL,
			Token:                cfg.SmartHome.Token,
			OAuthCredentialsFile: cfg.SmartHome.OAuthCredentialsFile,
			PollIntervalSec:      cfg.SmartHome.PollIntervalSec,
			AnalysisHour:         cfg.SmartHome.AnalysisHour,
			TimeZone:             cfg.SmartHome.TimeZone,
			AgentVaultPath:       cfg.SmartHome.AgentVaultPath,
		}
		shMgr, err := smarthome.NewManager(shCfg, logger)
		if err != nil {
			logger.Warn("smart home manager init failed (non-fatal)", zap.Error(err))
		} else {
			app.SmartHome = shMgr
		}
	}

	// Chain system
	embedSearchAdapter := chain.NewVaultEmbeddingStoreAdapter(
		func(ctx context.Context, vaultName, query string, k int) ([]vault.EmbeddingResult, error) {
			if vaultName == "agent" {
				return app.AgentEmbedStore.Search(ctx, query, k)
			}
			if vaultName == "personal" {
				return app.EmbedStore.Search(ctx, query, k)
			}
			return nil, fmt.Errorf("unknown vault")
		},
	)

	chainDeps := chain.ChainDeps{
		VaultReader:           app.VaultR,
		VaultWriter:           app.VaultW,
		Infer:                 app.Infer,
		Model:                 cfg.Inference.Models.Local,
		EmbedStore:            embedSearchAdapter,
		PersonalPath:          cfg.Vaults.Personal,
		AgentPath:             cfg.Vaults.Agent,
		Logger:                logger,
		TriggerEntityProvider: vaultReader,
		RRFK:                  cfg.Retrieval.RRFK,
		TopK:                  cfg.Retrieval.TopK,
		MaxChunkChars:         cfg.Retrieval.MaxChunkChars,
	}

	app.ChainRouter, err = chain.BuildAllChains(chainDeps)
	if err != nil {
		logger.Fatal("failed to build chains", zap.Error(err))
	}
	app.ChainExecutor = chain.NewChainExecutor(logger, app.ChainRouter)

	logger.Info("chain system initialized", zap.String("model", cfg.Inference.Models.Local))

	// Agent
	app.Agent = NewAgent(AgentDeps{
		Config:        cfg,
		Logger:        logger,
		SessionMgr:    app.SessionMgr,
		Sedimenter:    app.Sedimenter,
		FilterChain:   app.FilterChain,
		ModelRouter:   app.ModelRouter,
		ChannelMgr:    app.ChMgr,
		ChainExecutor: app.ChainExecutor,
		ChainRouter:   app.ChainRouter,
	})

	return app, nil
}

// Run starts all services and blocks until a shutdown signal is received.
func (app *App) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	app.ctx = ctx
	app.cancel = cancel

	defer app.ChMgr.StopAll()
	if err := app.ChMgr.StartAll(ctx); err != nil {
		return fmt.Errorf("start channels: %w", err)
	}
	defer app.Logger.Sync()
	if app.SmartHome != nil {
		app.SmartHome.Start(ctx)
		defer app.SmartHome.Stop()
	}

	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()
	for _, store := range []*vault.EmbeddingStore{app.EmbedStore, app.AgentEmbedStore} {
		if store == nil {
			continue
		}
		workers.Add(1)
		go func(store *vault.EmbeddingStore) {
			defer workers.Done()
			store.WarmupContext(ctx)
		}(store)
	}

	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := app.Agent.Run(ctx); err != nil && err != context.Canceled {
			app.Logger.Error("agent loop error", zap.Error(err))
		}
	}()

	if app.Server != nil {
		if err := app.Server.Run(ctx); err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
	}

	app.Logger.Info("agentd stopped")
	return nil
}

// Context returns the app's lifecycle context.
func (app *App) Context() context.Context { return app.ctx }

type ollamaEmbedder struct {
	client inference.Client
	model  string
}

func (e *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{"model": e.model, "input": text}
	body, _ := json.Marshal(req)
	resp, err := e.client.Embed(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("embed parse: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return result.Data[0].Embedding, nil
}
