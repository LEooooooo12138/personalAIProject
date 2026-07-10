package main

import (
	"encoding/json"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/channel/wecom"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/gateway"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

func main() {
	configPath := flag.String("config", "config/agent.yaml", "path to agent.yaml")
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// 1. Load config
	cfg, err := core.LoadConfig(*configPath)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	logger.Info("config loaded",
		zap.Int("port", cfg.Server.Port),
		zap.String("inference_endpoint", cfg.Inference.Endpoint),
		zap.String("vault_personal", cfg.Vaults.Personal),
		zap.String("vault_agent", cfg.Vaults.Agent),
	)

	// 2. Init dependencies
	ollamaClient := inference.NewOllamaClient(cfg.Inference.Endpoint, cfg.Inference.Timeout)
	vaultReader := vault.NewFileReader(cfg.Vaults.Personal, cfg.Vaults.Agent)
	vaultWriter := vault.NewFileWriter(cfg.Vaults.Personal, cfg.Vaults.Agent)

	// 3. Channel manager
	chMgr := channel.NewManager(logger)

	internalCh := channel.NewInternalChannel()
	chMgr.Register(internalCh)

	// WeCom adapter (Phase 2)
	if cfg.Channels.Wecom.Enabled {
		wecomCfg := wecom.Config{
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
		wecomAdapter, err := wecom.NewAdapter(wecomCfg, logger)
		if err != nil {
			logger.Fatal("failed to create wecom adapter", zap.Error(err))
		}
		chMgr.Register(wecomAdapter)
		logger.Info("wecom adapter registered")
	} else {
		logger.Info("wecom adapter disabled in config")
	}

	// 4. Model router
	coreRouter := core.NewRouter(cfg.Inference.Models.Local, cfg.Inference.Models.Vision)

	// 5. Output filter chain
	filterChain := filter.NewChain()

	// 6. Session manager
	sessionCfg := core.SessionConfig{
		MaxRounds:    cfg.Session.MaxRounds,
		IdleTimeout:  cfg.Session.IdleTimeout,
		ScanInterval: cfg.Session.ScanInterval,
		MaxSessions:  cfg.Session.MaxSessions,
	}
	sessionMgr := core.NewSessionManager(sessionCfg, logger)

	// 6.5 Session persistence + vector store
	embedAdapter2 := &ollamaEmbedder{client: ollamaClient}

	sessionStore := core.NewSessionStore(sessionMgr, cfg.Vaults.Agent+"/_sessions", embedAdapter2, logger)
	ctxBg := context.Background()
	if err := sessionStore.Initialize(ctxBg); err != nil {
		logger.Warn("session store init failed (non-fatal)", zap.Error(err))
	}
	// 7. Memory sedimentation engine
	llmSummarizer := gateway.NewLLMSummarizer(ollamaClient, logger)
	sedimenter := memory.NewSedimenter(cfg.Vaults.Personal, llmSummarizer, logger)

	// 8. Agent main loop
	agent := core.NewAgent(core.AgentDeps{
		Config:      cfg,
		Logger:      logger,
		SessionMgr:  sessionMgr,
		Sedimenter:  sedimenter,
		FilterChain: filterChain,
		Router:      coreRouter,
		ChannelMgr:  chMgr,
		Infer:       ollamaClient,
	})

	// 9. Embedding store (for chain system)
	embedStore := gateway.NewEmbeddingStore(ollamaClient, vaultReader, cfg.Vaults.Personal, logger)

	// Warm up embedding index in background so first request is fast.
	go embedStore.Warmup()

	// 9.5. Extract trigger entities from vault for RAG routing.
	// These are entity names (person, project) that, when mentioned in a query,
	// trigger a knowledge base search.
	triggerEntities, err := vault.ExtractTriggerEntities(cfg.Vaults.Personal, nil)
	if err != nil {
		logger.Warn("entity extraction failed, using empty list", zap.Error(err))
		triggerEntities = nil
	}
	logger.Info("trigger entities extracted",
		zap.Int("count", len(triggerEntities)),
		zap.Strings("entities", triggerEntities),
	)

	// 10. Build chain system (LangChain-style pipeline)
	// Create adapter that bridges gateway.EmbeddingStore -> chain.EmbeddingSearcher
	embedAdapter := chain.NewEmbeddingStoreAdapter(
		func(ctx context.Context, query string, k int) ([]chain.EmbeddingHit, error) {
			results, err := embedStore.Search(ctx, query, k)
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
		VaultReader:     vaultReader,
		VaultWriter:     vaultWriter,
		Infer:           ollamaClient,
		Model:           cfg.Inference.Models.Local,
		EmbedStore:      embedAdapter,
		PersonalPath:    cfg.Vaults.Personal,
		AgentPath:       cfg.Vaults.Agent,
		Logger:          logger,
		TriggerEntities: triggerEntities,
	}

	chainRouter, err := chain.BuildAllChains(chainDeps)
	if err != nil {
		logger.Fatal("failed to build chains", zap.Error(err))
	}

	chainExecutor := chain.NewChainExecutor(logger, chainRouter)

	logger.Info("chain system initialized",
		zap.String("model", cfg.Inference.Models.Local),
	)

	// 11. Start all channels
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := chMgr.StartAll(ctx); err != nil {
		logger.Fatal("failed to start channels", zap.Error(err))
	}
	defer chMgr.StopAll()

	// 12. Start agent main loop (background)
	go func() {
		if err := agent.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("agent loop error", zap.Error(err))
		}
	}()

	// 13. Start HTTP server with chain support
	srv := gateway.NewServerWithChains(
		cfg, logger, ollamaClient,
		vaultReader, vaultWriter,
		chMgr, coreRouter, filterChain,
		sessionMgr,
		chainExecutor, chainRouter,
		sessionStore,
		embedStore,
		sedimenter,
	)
	if err := srv.Run(ctx); err != nil {
		logger.Fatal("server stopped with error", zap.Error(err))
	}
	logger.Info("agentd stopped")
}


// ollamaEmbedder adapts inference.Client to core.Embedder for session vector storage.
type ollamaEmbedder struct {
	client inference.Client
}

func (e *ollamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{
		"model": "bge-m3",
		"input": text,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}
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
