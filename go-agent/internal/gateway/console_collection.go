package gateway

import "github.com/gin-gonic/gin"

func (s *Server) handleConsoleCollection(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	status, err := s.smartHome.CollectionStatus()
	if err != nil {
		s.consoleSuggestionError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, status)
}
func (s *Server) handleConsoleAnalyze(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	var body struct {
		Days int `json:"days"`
	}
	if !decodeConsoleJSON(c, &body) {
		return
	}
	if body.Days != 14 {
		consoleError(c, 422, "invalid_request", "Analyze the last fourteen days")
		return
	}
	report, err := s.smartHome.TriggerAnalysis(ctx, body.Days)
	if err != nil {
		s.consoleSuggestionError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, gin.H{"period_start": report.PeriodStart, "period_end": report.PeriodEnd, "suggestion_count": len(report.Suggestions)})
}
