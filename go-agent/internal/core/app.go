package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/channel/wecom"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
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

	ChainExecutor *chain.ChainExecutor
	ChainRouter   *chain.ChainRouter

	Sedimenter *memory.Sedimenter
	EmbedStore *vault.EmbeddingStore

	Agent  *Agent
	Server interface{ Run(ctx context.Context) error }

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
		logger.Fatal("failed to load config", zap.Error(err))
	}

	backend := inference.NewOllamaClient(cfg.Inference.Endpoint, cfg.Inference.Timeout)

	app := &App{
		Config:      cfg,
		Logger:      logger,
		Infer:       backend,
		VaultR:      vault.NewFileReader(cfg.Vaults.Personal, cfg.Vaults.Agent),
		VaultW:      vault.NewFileWriter(cfg.Vaults.Personal, cfg.Vaults.Agent),
	}

	// Channels
	app.ChMgr = channel.NewManager(logger)
	app.ChMgr.Register(channel.NewInternalChannel())

	if cfg.Channels.Wecom.Enabled {
		wcCfg := wecom.Config{
			Enabled:        cfg.Channels.Wecom.Enabled,
			ListenAddr:     cfg.Channels.Wecom.ListenAddr,
			CorpID:         cfg.Channels.Wecom.CorpID,
			CorpSecret:     cfg.Channels.Wecom.CorpSecret,
			AgentID:        cfg.Channels.Wecom.AgentID,
			Token:          cfg.Channels.Wecom.Token,
			EncodingAESKey: cfg.Channels.Wecom.EncodingAESKey,
			AllowedUsers:   cfg.Channels.Wecom.AllowedUsers,
			AutoApprove:    cfg.Channels.Wecom.AutoApprove,
		}
		wcAdapter, err := wecom.NewAdapter(wcCfg, logger)
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
	embedAdapter := &ollamaEmbedder{client: backend}
	app.SessionStore = NewSessionStore(app.SessionMgr, cfg.Vaults.Agent+"/_sessions", embedAdapter, logger)
	if err := app.SessionStore.Initialize(context.Background()); err != nil {
		logger.Warn("session store init failed (non-fatal)", zap.Error(err))
	}

	// Memory sedimentation
	app.Sedimenter = memory.NewSedimenter(cfg.Vaults.Personal, memory.NewSummarizer(app.Infer, logger), logger)

	// Embedding store
	app.EmbedStore = vault.NewEmbeddingStore(app.Infer, app.VaultR, cfg.Vaults.Personal, logger)

	// Trigger entities for RAG routing
	triggerEntities, err := vault.ExtractTriggerEntities(cfg.Vaults.Personal, nil)
	if err != nil {
		logger.Warn("entity extraction failed, using empty list", zap.Error(err))
	}
	logger.Info("trigger entities extracted", zap.Int("count", len(triggerEntities)))

	// Chain system
	embedSearchAdapter := chain.NewEmbeddingStoreAdapter(
		func(ctx context.Context, query string, k int) ([]chain.EmbeddingHit, error) {
			results, err := app.EmbedStore.Search(ctx, query, k)
			if err != nil {
				return nil, err
			}
			hits := make([]chain.EmbeddingHit, len(results))
			for i, r := range results {
				hits[i] = chain.EmbeddingHit{
					PagePath:     r.PagePath,
					Title:        r.Title,
					Score:        r.Score,
					ChunkContent: r.ChunkContent,
					SectionTitle: r.SectionTitle,
				}
			}
			return hits, nil
		},
	)

	chainDeps := chain.ChainDeps{
		VaultReader:     app.VaultR,
		VaultWriter:     app.VaultW,
		Infer:           app.Infer,
		Model:           cfg.Inference.Models.Local,
		EmbedStore:      embedSearchAdapter,
		PersonalPath:    cfg.Vaults.Personal,
		AgentPath:       cfg.Vaults.Agent,
		Logger:          logger,
		TriggerEntities: triggerEntities,
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

	if err := app.ChMgr.StartAll(ctx); err != nil {
		return fmt.Errorf("start channels: %w", err)
	}
	defer app.ChMgr.StopAll()
	defer app.Logger.Sync()

	go app.EmbedStore.Warmup()

	go func() {
		if err := app.Agent.Run(ctx); err != nil && err != context.Canceled {
			app.Logger.Error("agent loop error", zap.Error(err))
		}
	}()

	if app.Server != nil {
		if err := app.Server.Run(ctx); err != nil {
			app.Logger.Fatal("server stopped", zap.Error(err))
		}
	}

	app.Logger.Info("agentd stopped")
	return nil
}

// Context returns the app's lifecycle context.
func (app *App) Context() context.Context { return app.ctx }

type ollamaEmbedder struct{ client inference.Client }

func (e *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{"model": "bge-m3", "input": text}
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
