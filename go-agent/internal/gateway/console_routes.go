package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
)

const consolePrefix = "/api/console/v1"

func isConsolePath(path string) bool {
	return path == consolePrefix || strings.HasPrefix(path, consolePrefix+"/")
}
func isConsoleManagementPath(path string) bool {
	return path == "/internal/console/bootstrap" || path == "/internal/console/reset-admin"
}

func (s *Server) setupConsoleRoutes(r *gin.Engine) {
	g := r.Group(consolePrefix)
	s.setupConsoleControlRoutes(g)
	s.setupConsoleKnowledgeRoutes(g)
	s.setupConsoleSmartHomeRoutes(g)
	g.GET("/auth/status", s.handleConsoleStatus)
	g.POST("/auth/login", s.handleConsoleLogin)
	g.GET("/auth/me", s.handleConsoleMe)
	g.GET("/sessions", s.handleConsoleSessions)
	g.GET("/sessions/:id/messages", s.handleConsoleMessages)
	g.GET("/chat/ws", s.handleConsoleChat)
	g.GET("/areas", s.handleConsoleAreas)
	g.GET("/areas/:areaID/devices", s.handleConsoleAreaDevices)
	g.GET("/areas/:areaID/devices/:deviceID", s.handleConsoleAreaDevice)
	g.GET("/integrations/status", s.handleConsoleIntegrations)
	g.POST("/auth/logout", s.handleConsoleLogout)
	g.POST("/auth/password", s.handleConsolePassword)
	g.GET("/admin/members", s.handleConsoleMembers)
	g.POST("/admin/members", s.handleConsoleCreateMember)
	g.PATCH("/admin/members/:id", s.handleConsoleUpdateMember)
	g.POST("/admin/members/:id/reset-password", s.handleConsoleResetMember)
	r.POST("/internal/console/bootstrap", s.handleConsoleBootstrap)
	r.POST("/internal/console/reset-admin", s.handleConsoleResetAdmin)
	r.NoRoute(func(c *gin.Context) {
		if isConsolePath(c.Request.URL.Path) {
			consoleError(c, 404, "not_found", "Not found")
			return
		}
		c.String(404, "404 page not found")
	})
}

// Every console response uses an independent server-generated request ID. Never
// reflect an arbitrary caller-supplied header into a JSON error or audit field.
func consoleRequestID(c *gin.Context) string {
	if id, ok := c.Get("console_request_id"); ok {
		return id.(string)
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	c.Set("console_request_id", id)
	c.Header("X-Request-ID", id)
	return id
}

func consoleError(c *gin.Context, status int, code, message string) {
	id := consoleRequestID(c)
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message, "request_id": id}})
}

func consoleStoreError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, console.ErrUnauthenticated):
		consoleError(c, 401, "unauthenticated", "Authentication required")
	case errors.Is(err, console.ErrForbidden):
		consoleError(c, 403, "forbidden", "Permission denied")
	case errors.Is(err, console.ErrConflict):
		consoleError(c, 409, "conflict", "State conflicts with this request")
	case errors.Is(err, console.ErrInvalid):
		consoleError(c, 422, "invalid_request", "Check the supplied fields")
	case errors.Is(err, console.ErrUninitialized):
		consoleError(c, 503, "not_initialized", "Administrator initialization required")
	case errors.Is(err, console.ErrBusy):
		consoleError(c, 429, "rate_limited", "Try again later")
	default:
		consoleError(c, 503, "unavailable", "Console is unavailable")
	}
}

func (s *Server) consoleAvailable(c *gin.Context) bool {
	consoleRequestID(c)
	c.Header("Cache-Control", "no-store")
	if !s.cfg.Console.Enabled {
		consoleError(c, 503, "not_enabled", "Console is not enabled")
		return false
	}
	if s.consoleConfigErr != nil || s.consoleStore == nil {
		consoleError(c, 503, "unavailable", "Console is unavailable")
		return false
	}
	if err := s.consoleStore.CheckAvailable(); err != nil {
		consoleStoreError(c, err)
		return false
	}
	return true
}

func decodeConsoleJSON(c *gin.Context, dst any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	if err != nil {
		consoleError(c, 422, "invalid_request", "Invalid JSON request")
		return false
	}
	data = bytes.TrimSpace(data)
	if !json.Valid(data) {
		consoleError(c, 400, "invalid_request", "Invalid JSON request")
		return false
	}
	if data[0] != '{' || !utf8.Valid(data) {
		consoleError(c, 422, "invalid_request", "Expected a JSON object")
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
