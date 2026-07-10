package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

type Server struct {
	sessionMgr    *core.SessionManager
	embedStore    *EmbeddingStore
	cfg           *core.Config
	logger        *zap.Logger
	infer         inference.Client
	vaultR        vault.Reader
	vaultW        vault.Writer
	chMgr         *channel.Manager
	router        *core.Router
	filterChain   *filter.Chain
	engine        *gin.Engine
	srv           *http.Server

	// Chain system (LangChain-style pipeline).
	chainExecutor *chain.ChainExecutor
	chainRouter   *chain.ChainRouter

	// Session persistence + vector store.
	sessionStore *core.SessionStore
	sedimenter  *memory.Sedimenter
}

func NewServer(cfg *core.Config, logger *zap.Logger, infer inference.Client, vr vault.Reader, vw vault.Writer, chMgr *channel.Manager, coreRouter *core.Router, filterChain *filter.Chain, sessionMgr *core.SessionManager, embedStore *EmbeddingStore) *Server {
	if embedStore == nil {
        embedStore = NewEmbeddingStore(infer, vr, cfg.Vaults.Personal, logger)
    }
    s := &Server{
		sessionMgr:  sessionMgr,
		embedStore: NewEmbeddingStore(infer, vr, cfg.Vaults.Personal, logger),
		cfg:         cfg,
		logger:      logger,
		infer:       infer,
		vaultR:      vr,
		vaultW:      vw,
		chMgr:       chMgr,
		router:      coreRouter,
		filterChain: filterChain,
	}
	s.setupRoutes()
	return s
}

// NewServerWithChains creates a server with chain support.
func NewServerWithChains(cfg *core.Config, logger *zap.Logger, infer inference.Client, vr vault.Reader, vw vault.Writer, chMgr *channel.Manager, coreRouter *core.Router, filterChain *filter.Chain, sessionMgr *core.SessionManager, chainExecutor *chain.ChainExecutor, chainRouter *chain.ChainRouter, sessionStore *core.SessionStore, embedStore *EmbeddingStore, sedimenter *memory.Sedimenter) *Server {
	s := NewServer(cfg, logger, infer, vr, vw, chMgr, coreRouter, filterChain, sessionMgr, embedStore)
	s.chainExecutor = chainExecutor
	s.chainRouter = chainRouter
	s.sessionStore = sessionStore
	s.sedimenter = sedimenter
	return s
}

func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	s.srv = &http.Server{Addr: addr, Handler: s.engine}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server starting", zap.String("addr", addr))
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.logger.Info("http server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) setupRoutes() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))
	r.Use(AuthMiddleware(s.cfg.Server.InternalKey))

	r.GET("/health", s.handleHealth)

	// Chain management endpoint (for debugging and introspection).
	r.GET("/chains", s.handleListChains)

	v1 := r.Group("/v1")
	{
		v1.POST("/chat/completions", s.handleChatWithChain) // uses chain when available
		v1.POST("/embeddings", s.handleEmbed)
		v1.GET("/models", s.handleModels)
	}

	internal := r.Group("/internal")
	{
		internal.GET("/vault/status", s.handleVaultStatus)
		internal.GET("/vault/search", s.handleVaultSearch)
		internal.POST("/filter/test", s.handleFilterTest)
		internal.GET("/sessions/search", s.handleSessionSearch)
		internal.POST("/wiki/ingest", s.handleWikiIngest)
	}

	// Session history API — for loading past conversations on page refresh.
	sessionGroup := r.Group("/sessions")
	{
		sessionGroup.GET("", s.handleListSessions)
		sessionGroup.GET("/:channel/:userId/messages", s.handleGetSessionMessages)
	}
	// WebChat channel 鈥擶ebSocket endpoint + static widget files.
	r.GET("/channels/webchat/ws", func(c *gin.Context) {
		handleWebSocket(s.logger, s.infer, s.vaultR, s.router, s.filterChain, s.sessionMgr, s.embedStore, s.chainExecutor, s.chainRouter, s.sessionStore, s.sedimenter)(c.Writer, c.Request)
	})
	r.Static("/chat", "./static")

	s.engine = r
}

// handleListChains returns all registered chain names.
func (s *Server) handleListChains(c *gin.Context) {
	if s.chainRouter == nil {
		c.JSON(http.StatusOK, gin.H{"chains": []string{}, "message": "chain system not initialized"})
		return
	}
	// chainRouter.routes is private; return a simple message for now.
	c.JSON(http.StatusOK, gin.H{
		"chains":  []string{"chat", "simple-answer", "rag-answer", "summarize", "synthesize", "cross-link", "wiki-ingest"},
		"enabled": true,
	})
}

// handleChatWithChain handles /v1/chat/completions using the chain system.
// Falls back to legacy path if chains are not initialized.
func (s *Server) handleChatWithChain(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request json"})
		return
	}

	query := extractQuery(req.Messages)

	// If chains are available, use them.
	if s.chainRouter != nil && s.chainExecutor != nil && query != "" {
		s.handleChatViaChain(c, body, req, query)
		return
	}

	// Legacy path (fallback).
	s.handleChatLegacyHTTP(c, body, req, query)
}

// handleChatViaChain runs the chat chain.
func (s *Server) handleChatViaChain(c *gin.Context, body []byte, req chatRequest, query string) {
	// Build chain state.
	metadata := map[string]string{"channel": "rest-api"}
	state := chain.NewChainState(query, "personal", metadata)

	// Execute the chat chain.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	result, err := s.chainExecutor.Run(ctx, "chat", state)
	if err != nil {
		s.logger.Error("chain chat failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("chain execution failed: %v", err)})
		return
	}

	responseText := result.FinalAnswer
	if responseText == "" {
		responseText = "抱歉，我暂时无法回答这个问题。"
	}

	// Build OpenAI-compatible response.
	resp := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": responseText,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}

	// Include source citations if available.
	if state.HasSources() {
		var srcs []map[string]interface{}
		for _, src := range state.Sources {
			srcs = append(srcs, map[string]interface{}{
				"title": src.Title,
				"path":  src.Path,
				"score": src.Score,
			})
		}
		resp["sources"] = srcs
	}

	// Log chain trace.
	for _, t := range result.Trace {
		s.logger.Debug("chain step trace",
			zap.String("step", t.StepName),
			zap.Duration("duration", t.Duration),
			zap.String("status", t.Status),
		)
	}

	c.JSON(http.StatusOK, resp)
}

// handleChatLegacyHTTP is the original handler, kept as fallback.
func (s *Server) handleChatLegacyHTTP(c *gin.Context, body []byte, req chatRequest, query string) {
	bodyForRoute, err := json.Marshal(req)
	if err != nil {
		s.logger.Warn("marshal request for routing failed", zap.Error(err))
	}
	decision := s.router.Decide(bodyForRoute, req.Model, nil)
	s.logger.Debug("routing", zap.String("model", decision.TargetModel))

	if query != "" {
		results, err := s.vaultR.Search(c.Request.Context(), "personal", query)
		if err != nil {
			s.logger.Warn("vault search failed", zap.Error(err))
		} else if len(results) > 0 {
			var ctxStr strings.Builder
			ctxStr.WriteString("[vault] ")
			for i, r := range results {
				if i >= 2 {
					break
				}
				if i > 0 {
					ctxStr.WriteString("; ")
				}
				ctxStr.WriteString(r.Title)
			}
			if len(req.Messages) > 0 {
				last := &req.Messages[len(req.Messages)-1]
				last.Content = json.RawMessage(`"` + ctxStr.String() + ` | Question: ` + string(last.Content) + `"`)
			}
			if enriched, err := json.Marshal(req); err == nil {
				body = enriched
			}
		}
	}

	req.Model = decision.TargetModel
	if finalBody, err := json.Marshal(req); err == nil {
		body = finalBody
	}

	resp, err := s.infer.Chat(c.Request.Context(), body)
	if err != nil {
		s.logger.Error("chat failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("inference failed: %v", err)})
		return
	}

	c.Data(http.StatusOK, "application/json", resp)
}

// --- Existing handlers (unchanged) ---

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) handleEmbed(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}
	resp, err := s.infer.Embed(c.Request.Context(), body)
	if err != nil {
		s.logger.Error("embed failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("embedding failed: %v", err)})
		return
	}
	c.Data(http.StatusOK, "application/json", resp)
}

func (s *Server) handleModels(c *gin.Context) {
	models, err := s.infer.ListModels(c.Request.Context())
	if err != nil {
		s.logger.Error("list models failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("list models failed: %v", err)})
		return
	}
	type entry struct{ ID string `json:"id"` }
	data := make([]entry, len(models))
	for i, m := range models {
		data[i] = entry{ID: m.ID}
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (s *Server) handleVaultStatus(c *gin.Context) {
	status, err := s.vaultR.Status(c.Request.Context())
	if err != nil {
		s.logger.Error("vault status failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, status)
}

func (s *Server) handleVaultSearch(c *gin.Context) {
	keyword := c.Query("q")
	vaultName := c.DefaultQuery("vault", "personal")
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing ?q parameter"})
		return
	}
	results, err := s.vaultR.Search(c.Request.Context(), vaultName, keyword)
	if err != nil {
		s.logger.Error("vault search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results, "count": len(results)})
}

func (s *Server) handleFilterTest(c *gin.Context) {
	var req struct {
		Text     string            `json:"text"`
		Metadata map[string]string `json:"metadata,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	filtered, records := s.filterChain.Apply(req.Text, req.Metadata)
	var audit []gin.H
	for _, r := range records {
		audit = append(audit, gin.H{"filter": r.Filter, "reason": r.Reason})
	}
	c.JSON(http.StatusOK, gin.H{
		"original": req.Text,
		"filtered": filtered,
		"audit":    audit,
	})
}


// handleSessionSearch performs semantic search over past conversation history.
func (s *Server) handleSessionSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing ?q parameter"})
		return
	}
	if s.sessionStore == nil {
		c.JSON(http.StatusOK, gin.H{"hits": []interface{}{}, "message": "session store not initialized"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	hits, err := s.sessionStore.SearchSessions(ctx, query, 5)
	if err != nil {
		s.logger.Error("session search failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"hits": hits, "count": len(hits)})
}

// handleWikiIngest runs the wiki-ingest chain on raw content.
func (s *Server) handleWikiIngest(c *gin.Context) {
	if s.chainRouter == nil || s.chainExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chain system not initialized"})
		return
	}

	var req struct {
		Content     string `json:"content"`
		SourceTitle string `json:"source_title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing 'content' field in request body"})
		return
	}

	metadata := map[string]string{"channel": "rest-api", "operation": "wiki-ingest"}
	if req.SourceTitle != "" {
		metadata["source_title"] = req.SourceTitle
	}

	state := chain.NewChainState(req.Content, "personal", metadata)
	state.Data["raw_content"] = req.Content

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	result, err := s.chainExecutor.Run(ctx, "wiki-ingest", state)
	if err != nil {
		s.logger.Error("wiki ingest chain failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("ingest chain failed: %v", err)})
		return
	}

	responseText := result.FinalAnswer
	if responseText == "" {
		responseText = "抱歉，我暂时无法回答这个问题。"
	}

	// Log chain trace for debugging.
	for _, t := range result.Trace {
		s.logger.Debug("ingest step trace",
			zap.String("step", t.StepName),
			zap.Duration("duration", t.Duration),
			zap.String("status", t.Status),
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": responseText,
		"steps":   len(result.Trace),
	})
}


// handleListSessions returns all past session summaries.
func (s *Server) handleListSessions(c *gin.Context) {
	channel := c.DefaultQuery("channel", "webchat")
	if s.sessionStore == nil {
		c.JSON(200, []interface{}{})
		return
	}
	infos := s.sessionStore.ListSessions(channel)
	if infos == nil {
		infos = []core.SessionInfo{}
	}
	c.JSON(200, infos)
}

// handleGetSessionMessages returns the full message history for a session.
func (s *Server) handleGetSessionMessages(c *gin.Context) {
	channel := c.Param("channel")
	userId := c.Param("userId")
	if channel == "" || userId == "" {
		c.JSON(400, gin.H{"error": "missing channel or userId"})
		return
	}
	if s.sessionStore == nil {
		c.JSON(200, gin.H{"messages": []interface{}{}})
		return
	}
	msgs := s.sessionStore.GetMessages(channel, userId)
	if msgs == nil {
		msgs = []core.Message{}
	}
	c.JSON(200, gin.H{"messages": msgs, "channel": channel, "user_id": userId})
}
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func extractQuery(msgs []message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			var s string
			if json.Unmarshal(msgs[i].Content, &s) == nil {
				return s
			}
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(msgs[i].Content, &parts) == nil {
				var texts []string
				for _, p := range parts {
					if p.Type == "text" && p.Text != "" {
						texts = append(texts, p.Text)
					}
				}
				return strings.Join(texts, " ")
			}
			return ""
		}
	}
	return ""
}

