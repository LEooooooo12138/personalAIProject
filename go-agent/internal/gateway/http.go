package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

type Server struct {
	control     *smarthome.ControlService
	controlChat *core.ControlChat
	sessionMgr  *core.SessionManager
	embedStore  *vault.EmbeddingStore
	cfg         *core.Config
	logger      *zap.Logger
	infer       inference.Client
	vaultR      vault.Reader
	vaultW      vault.Writer
	chMgr       *channel.Manager
	router      *core.ModelRouter
	filterChain *filter.Chain
	engine      *gin.Engine
	srv         *http.Server

	// Chain system (LangChain-style pipeline).
	chainExecutor *chain.ChainExecutor
	chainRouter   *chain.ChainRouter

	// Session persistence + vector store.
	sessionStore        *core.SessionStore
	sedimenter          *memory.Sedimenter
	smartHome           *smarthome.Manager
	consoleStore        *console.Store
	consoleConfigErr    error
	consoleLoginLimiter *consoleLoginLimiter
	consoleMemberMu     sync.Mutex
	areaCatalog         consoleCatalogProvider
	ollamaStatus        *ollamaStatusCache
}

func NewServer(cfg *core.Config, logger *zap.Logger, infer inference.Client, vr vault.Reader, vw vault.Writer, chMgr *channel.Manager, coreRouter *core.ModelRouter, filterChain *filter.Chain, sessionMgr *core.SessionManager, embedStore *vault.EmbeddingStore) *Server {
	if embedStore == nil {
		embedStore = vault.NewEmbeddingStore(infer, vr, cfg.Vaults.Personal, logger)
	}
	s := &Server{
		sessionMgr:  sessionMgr,
		embedStore:  vault.NewEmbeddingStore(infer, vr, cfg.Vaults.Personal, logger),
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
func NewServerWithChains(cfg *core.Config, logger *zap.Logger, infer inference.Client, vr vault.Reader, vw vault.Writer, chMgr *channel.Manager, coreRouter *core.ModelRouter, filterChain *filter.Chain, sessionMgr *core.SessionManager, chainExecutor *chain.ChainExecutor, chainRouter *chain.ChainRouter, sessionStore *core.SessionStore, embedStore *vault.EmbeddingStore, sedimenter *memory.Sedimenter) *Server {
	s := NewServer(cfg, logger, infer, vr, vw, chMgr, coreRouter, filterChain, sessionMgr, embedStore)
	s.chainExecutor = chainExecutor
	s.chainRouter = chainRouter
	s.sessionStore = sessionStore
	s.sedimenter = sedimenter
	return s
}

// NewServerFromApp creates a server from the centralized App container.
// This is the preferred constructor; older multi-param constructors remain for backward compatibility.
func NewServerFromApp(app *core.App) *Server {
	embedStore := app.EmbedStore
	s := &Server{
		sessionMgr:    app.SessionMgr,
		embedStore:    app.EmbedStore,
		cfg:           app.Config,
		logger:        app.Logger,
		infer:         app.Infer,
		vaultR:        app.VaultR,
		vaultW:        app.VaultW,
		chMgr:         app.ChMgr,
		router:        app.ModelRouter,
		filterChain:   app.FilterChain,
		chainExecutor: app.ChainExecutor,
		chainRouter:   app.ChainRouter,
		sessionStore:  app.SessionStore,
		sedimenter:    app.Sedimenter,
		smartHome:     app.SmartHome,
		consoleStore:  app.ConsoleStore,
		control:       app.Control,
		controlChat:   app.ControlChat,
	}
	_ = embedStore
	s.setupRoutes()
	return s
}

func (s *Server) Run(ctx context.Context) error {
	if s.ollamaStatus != nil {
		defer s.ollamaStatus.Close()
	}
	if err := s.cfg.ValidateConsole(); err != nil {
		return err
	}
	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	if s.cfg.Server.ListenAddress != "" {
		addr = s.cfg.Server.ListenAddress
	}
	s.srv = &http.Server{Addr: addr, Handler: s.engine, BaseContext: func(net.Listener) context.Context { return ctx }}

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	}
}

func (s *Server) setupRoutes() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies(nil)
	s.consoleConfigErr = s.cfg.ValidateConsole()
	s.consoleLoginLimiter = newConsoleLoginLimiter(time.Now)
	s.ollamaStatus = newOllamaStatusCache(time.Now)
	access := newAccessControl(s.cfg.Server.InternalKey)
	r.Use(RequestIDHeader())
	legacyAuth := access.middleware()
	consoleAuth := s.consoleMiddleware()
	r.Use(func(c *gin.Context) {
		if isConsolePath(c.Request.URL.Path) {
			consoleAuth(c)
		} else {
			legacyAuth(c)
		}
	})
	s.setupConsoleRoutes(r)
	registerConsoleStaticRoutes(r, "./static/console", s.cfg.Console.PublicOrigin)
	r.POST("/auth/browser", access.bootstrap)

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
		// Smart home endpoints (Phase 4).
		internal.GET("/smarthome/status", s.handleSmartHomeStatus)
		internal.GET("/smarthome/devices", s.handleSmartHomeDevices)
		internal.GET("/smarthome/suggestions", s.handleSmartHomeSuggestions)
		internal.POST("/smarthome/suggestions/:id/bindings", s.handleSmartHomeBindings)
		internal.POST("/smarthome/suggestions/:id/confirm", s.handleSmartHomeConfirm)
		internal.POST("/smarthome/suggestions/:id/ignore", s.handleSmartHomeIgnore)
		internal.POST("/smarthome/analyze", s.handleSmartHomeAnalyze)
		internal.POST("/wiki/ingest", s.handleWikiIngest)
	}

	// Session history API 鈥?for loading past conversations on page refresh.
	sessionGroup := r.Group("/sessions")
	{
		sessionGroup.GET("", s.handleListSessions)
		sessionGroup.GET("/:channel/:userId/messages", s.handleGetSessionMessages)
	}
	// WebChat channel 閳ユ摱ebSocket endpoint + static widget files.
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
// Returns 503 if chain system is not initialized.
func (s *Server) handleChatWithChain(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8*1024*1024))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	req, err := parseChatRequest(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error(), "type": "invalid_request_error"}})
		return
	}
	decision := s.router.Decide(body, req.Model, req.Metadata)
	if decision.Backend != "local" {
		c.JSON(422, gin.H{"error": gin.H{"message": "cloud routing is not configured", "type": "invalid_request_error"}})
		return
	}
	req.Model = decision.TargetModel
	req.Raw["model"] = req.Model

	query := extractQuery(req.Messages)
	if query == "" {
		query = "[multimodal request]"
	}

	// If chains are available, use them.
	if s.chainRouter != nil && s.chainExecutor != nil && query != "" {
		s.handleChatViaChain(c, body, req, query)
		return
	}

	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chain system not initialized"})
}

// handleChatViaChain runs the chat chain.
func (s *Server) handleChatViaChain(c *gin.Context, body []byte, req chatRequest, query string) {
	// Build chain state.
	metadata := req.Metadata
	metadata["channel"] = "rest-api"
	state := chain.NewChainState(query, "personal", metadata)
	state.Data["chat_request"] = req.Raw

	// Execute the chat chain.
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.Server.ChainTimeout)
	defer cancel()

	chainName := "chat"
	if req.Metadata["skill"] == "wiki-query" {
		chainName = "rag-answer"
	}
	result, err := s.chainExecutor.Run(ctx, chainName, state)
	if err == nil && result != nil {
		err = result.Error
	}
	if err != nil {
		s.logger.Error("chain chat failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "chat generation failed", "type": "upstream_error"}})
		return
	}

	responseText := result.FinalAnswer
	if responseText == "" {
		c.JSON(502, gin.H{"error": gin.H{"message": "empty model response", "type": "upstream_error"}})
		return
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
	if raw, ok := state.Data["chat_response"].(json.RawMessage); ok {
		var upstream map[string]interface{}
		if json.Unmarshal(raw, &upstream) == nil {
			resp = upstream
		}
	}
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

	if req.Stream {
		writeChatSSE(c, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

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
	type entry struct {
		ID string `json:"id"`
	}
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.Server.SearchTimeout)
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.Server.ChainTimeout)
	defer cancel()

	result, err := s.chainExecutor.Run(ctx, "wiki-ingest", state)
	if err == nil && result != nil {
		err = result.Error
	}
	if err != nil {
		s.logger.Error("wiki ingest chain failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ingest chain failed"})
		return
	}

	responseText := result.FinalAnswer
	if responseText == "" {
		responseText = "\u62b1\u6b49\uff0c\u6211\u6682\u65f6\u65e0\u6cd5\u56de\u7b54\u8fd9\u4e2a\u95ee\u9898\u3002"
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
	p := requestPrincipal(c.Request)
	if !p.Admin {
		infos = s.sessionStore.ListOwnedSessions(channel, p.Owner)
	}
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
	var msgs []core.Message
	p := requestPrincipal(c.Request)
	if p.Admin {
		msgs = s.sessionStore.GetMessages(channel, userId)
	} else {
		var err error
		msgs, err = s.sessionStore.OwnedMessages(channel, userId, p.Owner)
		if errors.Is(err, core.ErrSessionNotFound) {
			c.JSON(404, gin.H{"error": "session not found"})
			return
		}
		if err != nil {
			s.logger.Warn("session history unavailable", zap.Error(err))
			c.JSON(500, gin.H{"error": "session unavailable", "code": "session_unavailable"})
			return
		}
	}
	if msgs == nil {
		msgs = []core.Message{}
	}
	c.JSON(200, gin.H{"messages": msgs, "channel": channel, "user_id": userId})
}

type chatRequest struct {
	Model    string                 `json:"model"`
	Messages []message              `json:"messages"`
	Metadata map[string]string      `json:"-"`
	Stream   bool                   `json:"stream"`
	Raw      map[string]interface{} `json:"-"`
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

// --- Smart Home handlers (Phase 4) ---

func (s *Server) handleSmartHomeStatus(c *gin.Context) {
	if s.smartHome == nil {
		c.JSON(200, gin.H{"enabled": false, "message": "smart home not configured"})
		return
	}
	states, err := s.smartHome.GetStore().LatestSnapshot()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"enabled": true, "device_count": len(states), "states": states})
}

func (s *Server) handleSmartHomeDevices(c *gin.Context) {
	if s.smartHome == nil {
		c.JSON(200, gin.H{"devices": []interface{}{}})
		return
	}
	states, err := s.smartHome.GetStore().LatestSnapshot()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	type deviceInfo struct {
		EntityID string `json:"entity_id"`
		State    string `json:"state"`
		Name     string `json:"name"`
	}
	var devices []deviceInfo
	for _, st := range states {
		name := st.EntityID
		if attrName, ok := st.Attributes["friendly_name"]; ok {
			if s, ok := attrName.(string); ok {
				name = s
			}
		}
		devices = append(devices, deviceInfo{
			EntityID: st.EntityID,
			State:    st.State,
			Name:     name,
		})
	}
	c.JSON(200, gin.H{"devices": devices, "count": len(devices)})
}

func (s *Server) handleSmartHomeSuggestions(c *gin.Context) {
	if s.smartHome == nil {
		c.JSON(200, gin.H{"suggestions": []interface{}{}})
		return
	}
	suggestions, err := s.smartHome.GetStore().GetSuggestions()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if suggestions == nil {
		suggestions = []smarthome.RuleSuggestion{}
	}
	// Only return pending suggestions by default
	filter := c.DefaultQuery("status", "pending")
	if filter != "" {
		var filtered []smarthome.RuleSuggestion
		for _, s := range suggestions {
			if s.Status == filter {
				filtered = append(filtered, s)
			}
		}
		suggestions = filtered
	}
	c.JSON(200, gin.H{"suggestions": suggestions, "count": len(suggestions)})
}

func (s *Server) handleSmartHomeConfirm(c *gin.Context) {
	id := c.Param("id")
	if s.smartHome == nil {
		c.JSON(503, gin.H{"error": "smart home not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	suggestion, err := s.smartHome.ConfirmSuggestion(ctx, id)
	if err != nil {
		writeSmartHomeError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "rule created and activated", "id": id, "suggestion": suggestion})
}

func (s *Server) handleSmartHomeIgnore(c *gin.Context) {
	id := c.Param("id")
	if s.smartHome == nil {
		c.JSON(503, gin.H{"error": "smart home not configured"})
		return
	}
	if _, err := s.smartHome.IgnoreSuggestion(id); err != nil {
		writeSmartHomeError(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "suggestion ignored", "id": id})
}

func (s *Server) handleSmartHomeAnalyze(c *gin.Context) {
	if s.smartHome == nil {
		c.JSON(503, gin.H{"error": "smart home not configured"})
		return
	}

	days := 14
	var req struct {
		Days int `json:"days"`
	}
	if err := c.ShouldBindJSON(&req); err == nil && req.Days > 0 {
		days = req.Days
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	report, err := s.smartHome.TriggerAnalysis(ctx, days)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"report":      report,
		"patterns":    len(report.Patterns),
		"suggestions": len(report.Suggestions),
		"devices":     len(report.Devices),
	})
}

func writeSmartHomeError(c *gin.Context, err error) {
	status := 500
	message := "smart home operation failed"
	switch {
	case errors.Is(err, smarthome.ErrSuggestionNotFound):
		status = 404
		message = "suggestion not found"
	case errors.Is(err, smarthome.ErrSuggestionConflict):
		status = 409
		message = "suggestion state conflicts with this operation"
	case errors.Is(err, smarthome.ErrUnsupportedRule):
		status = 422
		message = "rule requires explicit supported trigger, condition and action"
	case errors.Is(err, smarthome.ErrHARequest):
		status = 502
		message = "Home Assistant request failed"
	}
	c.JSON(status, gin.H{"error": message})
}
