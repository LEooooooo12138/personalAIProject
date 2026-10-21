package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

// Explicit projections omit full reports, persisted owners, raw descriptions
// and receipt identifiers. Structured outcomes are administrator-only.
type consoleAnalysisRun struct {
	ID               string                              `json:"id"`
	Status           string                              `json:"status"`
	Source           string                              `json:"source"`
	AlgorithmVersion string                              `json:"algorithm_version"`
	RequestedWindow  smarthome.TimeWindow                `json:"requested_window"`
	HomeTimezone     string                              `json:"home_timezone"`
	CreatedAt        time.Time                           `json:"created_at"`
	StartedAt        *time.Time                          `json:"started_at,omitempty"`
	FinishedAt       *time.Time                          `json:"finished_at,omitempty"`
	Coverage         smarthome.HistoryCoverage           `json:"coverage"`
	Outcome          string                              `json:"outcome,omitempty"`
	Outcomes         map[string]smarthome.PatternOutcome `json:"outcomes,omitempty"`
	Counts           smarthome.AnalysisCounts            `json:"counts"`
	ErrorCode        string                              `json:"error_code,omitempty"`
}

func analysisRunProjection(r smarthome.AnalysisRun) consoleAnalysisRun {
	source := "manual"
	if r.OwnerID == "system" {
		source = "scheduled"
	}
	r.Coverage.Receipts = nil
	return consoleAnalysisRun{r.ID, r.Status, source, r.AlgorithmVersion, r.RequestedWindow, r.HomeTimezone, r.CreatedAt, r.StartedAt, r.FinishedAt, r.Coverage, r.Outcome, r.Outcomes, r.Counts, r.ErrorCode}
}

type consoleBackfillRun struct {
	ID               string                 `json:"id"`
	Status           string                 `json:"status"`
	RequestedWindow  smarthome.TimeWindow   `json:"requested_window"`
	Cursor           time.Time              `json:"cursor"`
	CompletedBatches int                    `json:"completed_batches"`
	Gaps             []smarthome.TimeWindow `json:"gaps"`
	Retention        string                 `json:"retention"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	ErrorCode        string                 `json:"error_code,omitempty"`
	LastFailureCode  string                 `json:"last_failure_code,omitempty"`
}

func backfillRunProjection(r smarthome.BackfillRun) consoleBackfillRun {
	return consoleBackfillRun{r.ID, r.Status, r.RequestedWindow, r.Cursor, r.CompletedBatches, r.Gaps, r.Retention, r.CreatedAt, r.UpdatedAt, r.ErrorCode, r.LastFailureCode}
}
func (s *Server) consoleJobScope(c *gin.Context) (smarthome.JobScope, bool) {
	if !s.consoleSmartHomeSessionValid(c) {
		return smarthome.JobScope{}, false
	}
	// Detached from HTTP disconnect/deadline, rebound to expiry/logout/revocation.
	ctx, release, err := s.consoleStore.BindSession(context.WithoutCancel(c.Request.Context()), consoleToken(c.Request))
	if err != nil {
		consoleStoreError(c, err)
		return smarthome.JobScope{}, false
	}
	return smarthome.JobScope{Context: ctx, Release: release, OwnerID: requestConsolePrincipal(c.Request).UserID}, true
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
	scope, ok := s.consoleJobScope(c)
	if !ok {
		return
	}
	run, err := s.smartHome.StartAnalysis(scope, body.Days)
	if err != nil {
		s.consoleJobError(c, err)
		return
	}
	s.consoleSmartHomeJSONStatus(c, ctx, http.StatusAccepted, gin.H{"run_id": run.ID, "status": run.Status})
}
func (s *Server) handleConsoleBackfill(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	var body struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	if !decodeConsoleJSON(c, &body) {
		return
	}
	scope, ok := s.consoleJobScope(c)
	if !ok {
		return
	}
	run, err := s.smartHome.StartBackfill(scope, body.Start, body.End)
	if err != nil {
		s.consoleJobError(c, err)
		return
	}
	s.consoleSmartHomeJSONStatus(c, ctx, http.StatusAccepted, gin.H{"run_id": run.ID, "status": run.Status})
}
func (s *Server) handleConsoleAnalysisRun(c *gin.Context)    { s.consoleRun(c, false, false) }
func (s *Server) handleConsoleCancelAnalysis(c *gin.Context) { s.consoleRun(c, false, true) }
func (s *Server) handleConsoleBackfillRun(c *gin.Context)    { s.consoleRun(c, true, false) }
func (s *Server) handleConsoleCancelBackfill(c *gin.Context) { s.consoleRun(c, true, true) }
func (s *Server) consoleRun(c *gin.Context, backfill, cancel bool) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	id := c.Param("id")
	if !safeSuggestionID(id) {
		s.consoleRunNotFound(c)
		return
	}
	if cancel {
		var body struct{}
		if !decodeConsoleJSON(c, &body) {
			return
		}
	}
	owner := requestConsolePrincipal(c.Request).UserID
	var view any
	var status string
	if backfill {
		r, err := s.smartHome.GetBackfillRun(id)
		if err != nil || r.OwnerID != owner {
			s.consoleRunNotFound(c)
			return
		}
		if cancel {
			if err = s.consoleStore.WithSession(ctx, consoleToken(c.Request), func(context.Context) error { return s.smartHome.CancelBackfill(id) }); err != nil {
				s.consoleJobError(c, err)
				return
			}
		}
		view = backfillRunProjection(r)
		status = r.Status
	} else {
		r, err := s.smartHome.GetAnalysisRun(id)
		if err != nil || (r.OwnerID != owner && r.OwnerID != "system") {
			s.consoleRunNotFound(c)
			return
		}
		if cancel {
			if err = s.consoleStore.WithSession(ctx, consoleToken(c.Request), func(context.Context) error { return s.smartHome.CancelAnalysis(id) }); err != nil {
				s.consoleJobError(c, err)
				return
			}
		}
		view = analysisRunProjection(r)
		status = r.Status
	}
	if cancel {
		s.consoleSmartHomeJSONStatus(c, ctx, 202, gin.H{"run_id": id, "status": status, "cancel_requested": true})
		return
	}
	s.consoleSmartHomeJSON(c, ctx, view)
}
func (s *Server) consoleRunNotFound(c *gin.Context) {
	s.consoleSmartHomeFailure(c, 404, "not_found", "Run not found")
}
func (s *Server) consoleJobError(c *gin.Context, err error) {
	if !s.consoleSmartHomeSessionValid(c) {
		return
	}
	switch {
	case errors.Is(err, smarthome.ErrAnalysisNotFound), errors.Is(err, smarthome.ErrBackfillNotFound):
		s.consoleRunNotFound(c)
	case errors.Is(err, smarthome.ErrAnalysisBusy), errors.Is(err, smarthome.ErrBackfillBusy), errors.Is(err, smarthome.ErrBackfillConflict):
		consoleError(c, 409, "busy", "Task cannot be started now")
	case strings.HasPrefix(err.Error(), "invalid_backfill_window"):
		consoleError(c, 422, "invalid_request", "Choose a bounded history window")
	default:
		s.consoleSuggestionError(c, err)
	}
}

// Publication is serialized against revocation just like other Console reads.
func (s *Server) consoleSmartHomeJSONStatus(c *gin.Context, ctx context.Context, status int, data any) {
	if !s.consoleSmartHomeSessionValid(c) {
		return
	}
	err := s.consoleStore.WithSession(ctx, consoleToken(c.Request), func(bound context.Context) error {
		if err := bound.Err(); err != nil {
			return err
		}
		c.JSON(status, data)
		return nil
	})
	if err != nil {
		consoleStoreError(c, err)
	}
}
