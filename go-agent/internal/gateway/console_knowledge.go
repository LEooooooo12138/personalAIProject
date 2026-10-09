package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

func (s *Server) setupConsoleKnowledgeRoutes(g *gin.RouterGroup) {
	g.GET("/knowledge/search", s.handleConsoleKnowledgeSearch)
	g.GET("/knowledge/page", s.handleConsoleKnowledgePage)
	g.GET("/admin/vault/status", s.handleConsoleVaultStatus)
	g.GET("/admin/vault/search", s.handleConsoleVaultSearch)
	g.GET("/admin/vault/page", s.handleConsoleVaultPage)
	g.POST("/admin/knowledge/query", s.handleConsoleKnowledgeQuery)
	g.POST("/admin/wiki/ingest", s.handleConsoleWikiIngest)
}

type consoleKnowledgeHit struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}
type consoleKnowledgePage struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Body  string `json:"body"`
}
type consoleKnowledgeSource struct {
	Path  string  `json:"path"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

func (s *Server) consoleKnowledgeAccess(c *gin.Context, private bool) bool {
	p := requestConsolePrincipal(c.Request)
	if p.UserID == "" {
		consoleError(c, 401, "unauthenticated", "Authentication required")
		return false
	}
	if p.MustChangePassword || (private && p.Role != "admin") {
		consoleError(c, 403, "forbidden", "Permission denied")
		return false
	}
	if s.vaultR == nil {
		consoleError(c, 503, "unavailable", "Knowledge is unavailable")
		return false
	}
	return true
}
func (s *Server) consoleKnowledgeContext(c *gin.Context, model bool) (context.Context, context.CancelFunc) {
	timeout := s.cfg.Server.SearchTimeout
	if model {
		timeout = s.cfg.Server.ChainTimeout
	}
	if timeout <= 0 {
		if model {
			timeout = 300 * time.Second
		} else {
			timeout = 30 * time.Second
		}
	}
	bound, stop, err := s.consoleStore.BindSession(c.Request.Context(), consoleToken(c.Request))
	if err != nil {
		consoleStoreError(c, err)
		return nil, func() {}
	}
	ctx, cancel := context.WithTimeout(bound, timeout)
	c.Request = c.Request.WithContext(ctx)
	return ctx, func() { cancel(); stop() }
}

// Return only canonical wiki paths; system/session files never become page APIs.
func consoleWikiPath(value string) (string, bool) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\\:\x00") || strings.HasPrefix(value, "/") || path.Clean(value) != value || !strings.HasSuffix(strings.ToLower(value), ".md") {
		return "", false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return "", false
		}
	}
	switch strings.ToLower(path.Base(value)) {
	case "index.md", "log.md", "hot.md", "agents.md":
		return "", false
	}
	return value, true
}
func consoleKnowledgeQueryParam(c *gin.Context, key string, max int) (string, bool) {
	values := c.Request.URL.Query()
	for name, items := range values {
		if name != key || len(items) != 1 {
			return "", false
		}
	}
	value := strings.TrimSpace(values.Get(key))
	return value, value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}
func (s *Server) consoleKnowledgeUnavailable(c *gin.Context) {
	// Resolve first so an upstream canceled by logout maps to Console 401,
	// while a valid caller's timeout remains a safe service error.
	if _, err := s.consoleStore.Resolve(consoleToken(c.Request)); err != nil {
		consoleStoreError(c, err)
		return
	}
	consoleError(c, 503, "unavailable", "Knowledge is unavailable")
}

func (s *Server) handleConsoleKnowledgeSearch(c *gin.Context) { s.consoleKnowledgeSearch(c, "agent") }
func (s *Server) handleConsoleVaultSearch(c *gin.Context)     { s.consoleKnowledgeSearch(c, "personal") }
func (s *Server) consoleKnowledgeSearch(c *gin.Context, vaultName string) {
	if !s.consoleKnowledgeAccess(c, vaultName == "personal") {
		return
	}
	q, ok := consoleKnowledgeQueryParam(c, "q", 200)
	if !ok {
		consoleError(c, 422, "invalid_request", "Provide a search query of at most 200 characters")
		return
	}
	ctx, cancel := s.consoleKnowledgeContext(c, false)
	if ctx == nil {
		return
	}
	defer cancel()
	candidates, err := s.vaultR.Search(ctx, vaultName, q)
	if err != nil {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	hits := make([]consoleKnowledgeHit, 0)
	for _, candidate := range candidates {
		rel, ok := consoleWikiPath(filepath.ToSlash(candidate.Path))
		if !ok {
			continue
		}
		page, err := s.vaultR.ReadPage(ctx, vaultName, rel)
		if err != nil {
			if errors.Is(err, vault.ErrInvalidFrontmatter) || errors.Is(err, os.ErrNotExist) {
				continue
			}
			s.consoleKnowledgeUnavailable(c)
			return
		}
		if page == nil || (vaultName == "agent" && vault.IsInternalPage(page)) {
			continue
		}
		// Use current content rather than any stale title/snippet from the index.
		snippet := []rune(page.Body)
		if len(snippet) > 120 {
			snippet = snippet[:120]
		}
		hits = append(hits, consoleKnowledgeHit{rel, page.Title, string(snippet)})
		if len(hits) == 50 {
			break
		}
	}
	s.consoleReadJSON(c, gin.H{"results": hits, "count": len(hits)})
}
func (s *Server) handleConsoleKnowledgePage(c *gin.Context) { s.consoleKnowledgeReadPage(c, "agent") }
func (s *Server) handleConsoleVaultPage(c *gin.Context)     { s.consoleKnowledgeReadPage(c, "personal") }
func (s *Server) consoleKnowledgeReadPage(c *gin.Context, vaultName string) {
	if !s.consoleKnowledgeAccess(c, vaultName == "personal") {
		return
	}
	requested, ok := consoleKnowledgeQueryParam(c, "path", 4096)
	rel, valid := consoleWikiPath(requested)
	if !ok || !valid {
		consoleError(c, 404, "not_found", "Not found")
		return
	}
	ctx, cancel := s.consoleKnowledgeContext(c, false)
	if ctx == nil {
		return
	}
	defer cancel()
	page, err := s.vaultR.ReadPage(ctx, vaultName, rel)
	if ctx.Err() != nil || errors.Is(err, os.ErrPermission) {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	if err != nil || page == nil || (vaultName == "agent" && vault.IsInternalPage(page)) {
		consoleError(c, 404, "not_found", "Not found")
		return
	}
	s.consoleReadJSON(c, consoleKnowledgePage{rel, page.Title, page.Body})
}
func (s *Server) handleConsoleVaultStatus(c *gin.Context) {
	if !s.consoleKnowledgeAccess(c, true) {
		return
	}
	ctx, cancel := s.consoleKnowledgeContext(c, false)
	if ctx == nil {
		return
	}
	defer cancel()
	status, err := s.vaultR.Status(ctx)
	if err != nil || status == nil {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	s.consoleReadJSON(c, gin.H{"vault": "personal", "page_count": status.Personal.PageCount, "total_bytes": status.Personal.TotalBytes})
}

// The ordinary Console JSON limit is 4 KiB; knowledge input has explicit larger
// character limits. The byte budget also permits legal JSON unicode escapes.
func decodeConsoleKnowledgeJSON(c *gin.Context, dst any, maxBytes int64) bool {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes))
	data = bytes.TrimSpace(data)
	if err != nil || len(data) == 0 || data[0] != '{' || !utf8.Valid(data) || !json.Valid(data) {
		consoleError(c, 422, "invalid_request", "Invalid JSON request")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		consoleError(c, 422, "invalid_request", "Invalid JSON request")
		return false
	}
	return true
}
func validKnowledgeText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}
func (s *Server) handleConsoleKnowledgeQuery(c *gin.Context) {
	if !s.consoleKnowledgeAccess(c, true) {
		return
	}
	var input struct {
		Query string `json:"query"`
	}
	if !decodeConsoleKnowledgeJSON(c, &input, 6*4000+8192) {
		return
	}
	if !validKnowledgeText(input.Query, 4000) {
		consoleError(c, 422, "invalid_request", "Provide a query of at most 4000 characters")
		return
	}
	if s.chainExecutor == nil {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	ctx, cancel := s.consoleKnowledgeContext(c, true)
	if ctx == nil {
		return
	}
	defer cancel()
	state := chain.NewChainState(strings.TrimSpace(input.Query), "personal", map[string]string{"channel": "console", "operation": "knowledge-query"})
	result, err := s.chainExecutor.Run(ctx, "rag-answer", state)
	if err != nil || result == nil || result.Error != nil || strings.TrimSpace(result.FinalAnswer) == "" {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	sources := make([]consoleKnowledgeSource, 0, len(state.Sources))
	for _, source := range state.Sources {
		rel, ok := consoleWikiPath(filepath.ToSlash(source.Path))
		if !ok {
			s.consoleKnowledgeUnavailable(c)
			return
		}
		page, err := s.vaultR.ReadPage(ctx, "personal", rel)
		if err != nil || page == nil {
			s.consoleKnowledgeUnavailable(c)
			return
		}
		sources = append(sources, consoleKnowledgeSource{rel, page.Title, source.Score})
	}
	s.consoleReadJSON(c, gin.H{"answer": result.FinalAnswer, "sources": sources})
}
func (s *Server) handleConsoleWikiIngest(c *gin.Context) {
	if !s.consoleKnowledgeAccess(c, true) {
		return
	}
	var input struct {
		Content     string `json:"content"`
		SourceTitle string `json:"source_title"`
	}
	if !decodeConsoleKnowledgeJSON(c, &input, 6*100000+8192) {
		return
	}
	if !validKnowledgeText(input.Content, 100000) || !utf8.ValidString(input.SourceTitle) || utf8.RuneCountInString(input.SourceTitle) > 200 {
		consoleError(c, 422, "invalid_request", "Check the content and source title limits")
		return
	}
	if s.chainExecutor == nil {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	ctx, cancel := s.consoleKnowledgeContext(c, true)
	if ctx == nil {
		return
	}
	defer cancel()
	state := chain.NewChainState("wiki-ingest", "personal", map[string]string{"channel": "console", "operation": "wiki-ingest", "source_title": input.SourceTitle})
	state.Data["raw_content"] = input.Content
	result, err := s.chainExecutor.Run(ctx, "wiki-ingest", state)
	if err != nil || result == nil || result.Error != nil {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	written, _ := state.GetString("written_path")
	rel, ok := consoleWikiPath(filepath.ToSlash(written))
	if !ok {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	page, err := s.vaultR.ReadPage(ctx, "personal", rel)
	if err != nil || page == nil {
		s.consoleKnowledgeUnavailable(c)
		return
	}
	s.consoleReadJSON(c, gin.H{"vault": "personal", "path": rel, "title": page.Title})
}
