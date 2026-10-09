package gateway

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"io"
	"net/http"
	"time"
)

func (s *Server) handleSmartHomeBindings(c *gin.Context) {
	if !requestPrincipal(c.Request).Admin {
		c.JSON(http.StatusForbidden, gin.H{"error": "administrator required"})
		return
	}
	if s.smartHome == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "smart home not configured"})
		return
	}
	var binding *smarthome.SuggestionBindings
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil || binding == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid suggestion bindings"})
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected one JSON object"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	suggestion, err := s.smartHome.BindSuggestion(ctx, c.Param("id"), *binding)
	if err != nil {
		writeSmartHomeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"suggestion": suggestion, "id": suggestion.ID})
}
