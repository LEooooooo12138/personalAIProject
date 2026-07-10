package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\internal\gateway\http.go`
    data, _ := os.ReadFile(path)
    text := string(data)

    // 1. Add route registration in setupRoutes
    oldRoute := `internal.GET("/sessions/search", s.handleSessionSearch)`
    newRoute := `internal.GET("/sessions/search", s.handleSessionSearch)
		internal.POST("/wiki/ingest", s.handleWikiIngest)`

    if strings.Contains(text, oldRoute) {
        text = strings.Replace(text, oldRoute, newRoute, 1)
        fmt.Println("[1/2] Added wiki/ingest route")
    } else {
        fmt.Println("ERROR: route anchor not found")
    }

    // 2. Add the handler function before the chatRequest type
    handlerAnchor := "type chatRequest struct {"
    handlerCode := `
// handleWikiIngest runs the wiki-ingest chain on raw content.
func (s *Server) handleWikiIngest(c *gin.Context) {
	if s.chainRouter == nil || s.chainExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chain system not initialized"})
		return
	}

	var req struct {
		Content     string ` + "`" + `json:"content"` + "`" + `
		SourceTitle string ` + "`" + `json:"source_title"` + "`" + `
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
		responseText = "ingest completed (no output text)"
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
`

    if strings.Contains(text, handlerAnchor) {
        text = strings.Replace(text, handlerAnchor, handlerCode + "\n" + handlerAnchor, 1)
        fmt.Println("[2/2] Added handleWikiIngest handler")
    } else {
        fmt.Println("ERROR: handler anchor not found")
    }

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("Done")
}
