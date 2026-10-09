package gateway

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/core"
	"time"
)

type consoleSessionInfo struct {
	ID           string    `json:"id"`
	StartedAt    time.Time `json:"started_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	RoundCount   int       `json:"round_count"`
	MessageCount int       `json:"message_count"`
	Preview      string    `json:"preview"`
}

func (s *Server) handleConsoleSessions(c *gin.Context) {
	if s.sessionStore == nil {
		consoleError(c, 503, "unavailable", "History is unavailable")
		return
	}
	infos, err := s.sessionStore.ListOwnedSessionsStrict("console", "console:"+requestConsolePrincipal(c.Request).UserID)
	if err != nil {
		consoleError(c, 500, "session_unavailable", "History could not be read")
		return
	}
	result := make([]consoleSessionInfo, 0, len(infos))
	for _, info := range infos {
		result = append(result, consoleSessionInfo{info.UserID, info.StartedAt, info.LastActiveAt, info.RoundCount, info.MessageCount, info.Preview})
	}
	c.JSON(200, gin.H{"sessions": result})
}
func (s *Server) handleConsoleMessages(c *gin.Context) {
	if s.sessionStore == nil {
		consoleError(c, 503, "unavailable", "History is unavailable")
		return
	}
	messages, err := s.sessionStore.OwnedMessages("console", c.Param("id"), "console:"+requestConsolePrincipal(c.Request).UserID)
	if errors.Is(err, core.ErrSessionNotFound) {
		consoleError(c, 404, "not_found", "Not found")
		return
	}
	if err != nil {
		consoleError(c, 500, "session_unavailable", "History could not be read")
		return
	}
	if messages == nil {
		messages = []core.Message{}
	}
	c.JSON(200, gin.H{"id": c.Param("id"), "messages": messages})
}
