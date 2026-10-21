package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"time"
)

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
	discovery := s.smartHome.CollectionDiscovery(ctx, requestConsolePrincipal(c.Request).UserID)
	if discovery.Coverage != nil {
		discovery.Coverage.Receipts = nil
	}
	analyses := []consoleAnalysisRun{}
	for _, run := range discovery.AnalysisRuns {
		analyses = append(analyses, analysisRunProjection(run))
	}
	backfills := []consoleBackfillRun{}
	for _, run := range discovery.BackfillRuns {
		backfills = append(backfills, backfillRunProjection(run))
	}
	var latest *consoleAnalysisRun
	if len(analyses) > 0 {
		latest = &analyses[0]
	}
	s.consoleSmartHomeJSON(c, ctx, struct {
		smarthome.CollectionStatus
		TargetDays         int                        `json:"target_days"`
		RequestedWindow    *smarthome.TimeWindow      `json:"requested_window"`
		Coverage           *smarthome.HistoryCoverage `json:"coverage"`
		CoverageError      string                     `json:"coverage_error,omitempty"`
		Retention          string                     `json:"retention"`
		AnalysisRuns       []consoleAnalysisRun       `json:"analysis_runs"`
		BackfillRuns       []consoleBackfillRun       `json:"backfill_runs"`
		LatestAnalysis     *consoleAnalysisRun        `json:"latest_analysis"`
		NextAnalysisAt     *time.Time                 `json:"next_analysis_at"`
		NextAnalysisReason string                     `json:"next_analysis_reason"`
	}{CollectionStatus: status, TargetDays: 14, RequestedWindow: discovery.RequestedWindow, Coverage: discovery.Coverage, CoverageError: discovery.CoverageError, Retention: "unknown", AnalysisRuns: analyses, BackfillRuns: backfills, LatestAnalysis: latest, NextAnalysisAt: discovery.NextAnalysisAt, NextAnalysisReason: discovery.NextAnalysisReason})
}
