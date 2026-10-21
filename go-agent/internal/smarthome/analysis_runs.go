package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const AnalysisAlgorithmVersion = "deterministic-v1"

var ErrAnalysisNotFound = errors.New("analysis_not_found")

type AnalysisCounts struct {
	Entries     int            `json:"entries"`
	Patterns    int            `json:"patterns"`
	Suggestions int            `json:"suggestions"`
	Excluded    map[string]int `json:"excluded"`
}
type PatternCandidate struct {
	EntityID             string `json:"entity_id"`
	RelatedID            string `json:"related_id,omitempty"`
	ObservationDays      int    `json:"observation_days"`
	RequiredDays         int    `json:"required_days"`
	SourceTransitions    int    `json:"source_transitions"`
	SourceTransitionDays int    `json:"source_transition_days"`
	NumericSamples       int    `json:"numeric_samples"`
	Outcome              string `json:"outcome"`
	Reason               string `json:"reason"`
}
type PatternOutcome struct {
	Applicable bool               `json:"applicable"`
	Outcome    string             `json:"outcome"`
	Reason     string             `json:"reason"`
	Candidates []PatternCandidate `json:"candidates"`
}
type AnalysisRun struct {
	SchemaVersion    int                       `json:"schema_version"`
	ID               string                    `json:"id"`
	OwnerID          string                    `json:"owner_id"`
	Status           string                    `json:"status"`
	AlgorithmVersion string                    `json:"algorithm_version"`
	RequestedWindow  TimeWindow                `json:"requested_window"`
	HomeTimezone     string                    `json:"home_timezone"`
	CreatedAt        time.Time                 `json:"created_at"`
	StartedAt        *time.Time                `json:"started_at,omitempty"`
	FinishedAt       *time.Time                `json:"finished_at,omitempty"`
	Coverage         HistoryCoverage           `json:"coverage"`
	Outcome          string                    `json:"outcome,omitempty"`
	Outcomes         map[string]PatternOutcome `json:"outcomes,omitempty"`
	Counts           AnalysisCounts            `json:"counts"`
	ErrorCode        string                    `json:"error_code,omitempty"`
	Report           *DeviceReport             `json:"report,omitempty"`
}

func copyAnalysisRun(r AnalysisRun) AnalysisRun {
	b, _ := json.Marshal(r)
	var result AnalysisRun
	_ = json.Unmarshal(b, &result)
	return result
}

// Scope is consumed once even on rejection. The single analysis slot is reserved
// before timezone resolution and durable queuing, shared by daily/manual work.
func (m *Manager) StartAnalysis(scope JobScope, days int) (AnalysisRun, error) {
	ctx, finish, err := m.acquireJob(scope)
	if err != nil {
		return AnalysisRun{}, err
	}
	accepted := false
	defer func() {
		if !accepted {
			finish()
		}
	}()
	if days < 1 || days > 366 {
		return AnalysisRun{}, errors.New("invalid_analysis_days")
	}
	if !m.analysisMu.TryLock() {
		return AnalysisRun{}, ErrAnalysisBusy
	}
	defer func() {
		if !accepted {
			m.analysisMu.Unlock()
		}
	}()
	loc, err := m.homeLocation(ctx)
	if err != nil {
		return AnalysisRun{}, fmt.Errorf("analysis_timezone_unavailable: %w", err)
	}
	now := time.Now().UTC()
	local := now.In(loc)
	end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	id, err := randomAnalysisID()
	if err != nil {
		return AnalysisRun{}, errors.New("analysis_id_unavailable")
	}
	run := AnalysisRun{SchemaVersion: 1, ID: id, OwnerID: scope.OwnerID, Status: "queued", AlgorithmVersion: AnalysisAlgorithmVersion, RequestedWindow: TimeWindow{Start: end.AddDate(0, 0, -days).UTC(), End: end.UTC()}, HomeTimezone: loc.String(), CreatedAt: now}
	if err := ctx.Err(); err != nil {
		return AnalysisRun{}, err
	}
	if err := m.store.saveAnalysisRun(run); err != nil {
		return AnalysisRun{}, errors.New("analysis_store_failed")
	}
	jobCtx, cancel := context.WithCancel(ctx)
	m.analysisRunsMu.Lock()
	m.analysisRuns[id] = copyAnalysisRun(run)
	m.analysisActive = id
	m.analysisCancel = cancel
	m.analysisRunsMu.Unlock()
	accepted = true
	go func() { defer finish(); defer cancel(); m.executeAnalysis(jobCtx, run, days, loc) }()
	return copyAnalysisRun(run), nil
}
func (m *Manager) GetAnalysisRun(id string) (AnalysisRun, error) {
	m.analysisRunsMu.Lock()
	defer m.analysisRunsMu.Unlock()
	r, ok := m.analysisRuns[id]
	if !ok {
		return AnalysisRun{}, ErrAnalysisNotFound
	}
	return copyAnalysisRun(r), nil
}
func (m *Manager) CancelAnalysis(id string) error {
	m.analysisRunsMu.Lock()
	defer m.analysisRunsMu.Unlock()
	if _, ok := m.analysisRuns[id]; !ok {
		return ErrAnalysisNotFound
	}
	if m.analysisActive == id {
		m.analysisCancel()
	}
	return nil
}
func (m *Manager) executeAnalysis(ctx context.Context, run AnalysisRun, days int, loc *time.Location) {
	started := time.Now().UTC()
	run.StartedAt = &started
	run.Status = "running"
	err := m.store.saveAnalysisRun(run)
	if err != nil {
		err = errAnalysisStore
	}
	if err == nil {
		m.analysisRunsMu.Lock()
		m.analysisRuns[run.ID] = copyAnalysisRun(run)
		m.analysisRunsMu.Unlock()
		err = m.analyzeAndPublish(ctx, &run, days, loc)
	}
	run.Status = "completed"
	if err != nil {
		run.Status = "failed"
		run.ErrorCode = "analysis_failed"
		if errors.Is(err, errAnalysisStore) {
			run.ErrorCode = "analysis_store_failed"
		}
		if ctx.Err() != nil {
			run.Status = "cancelled"
			run.ErrorCode = "cancelled"
		}
	}
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	m.analysisRunsMu.Lock()
	if err := m.store.saveAnalysisRun(run); err != nil {
		run.Status = "failed"
		run.ErrorCode = "analysis_store_failed"
	}
	m.analysisRuns[run.ID] = copyAnalysisRun(run)
	m.analysisActive = ""
	m.analysisCancel = nil
	m.analysisMu.Unlock()
	m.analysisRunsMu.Unlock()
}

var errAnalysisStore = errors.New("analysis_store_failed")

func (m *Manager) analyzeAndPublish(ctx context.Context, run *AnalysisRun, days int, loc *time.Location) error {
	report, err := m.analyzer.AnalyzeContext(ctx, run.RequestedWindow.End, days, loc)
	if err != nil {
		return err
	}
	run.Report = report
	run.Coverage = report.Coverage
	run.Outcome = report.Outcome
	run.Outcomes = report.Outcomes
	run.Counts = report.Counts
	if err := ctx.Err(); err != nil {
		return err
	}
	// Persist the independently addressed report before publishing its evidence.
	if err := m.store.saveAnalysisRun(*run); err != nil {
		return errAnalysisStore
	}
	if err := m.store.SaveSuggestions(report.Suggestions); err != nil {
		return errAnalysisStore
	}
	for _, p := range report.Patterns {
		if p.Type != "time" && p.Type != "correlation" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		ss := m.analyzer.generateSuggestionsInLocation([]DetectedPattern{p}, loc)
		e := SuggestionEvidence{RunID: run.ID, Window: run.RequestedWindow, AlgorithmVersion: run.AlgorithmVersion, SampleSize: p.SampleSize, PeriodDays: p.PeriodDays, HitDays: p.HitDays, EligibleDays: p.EligibleDays, Denominator: p.Denominator, DenominatorType: p.DenominatorType, ExcludedCounts: p.ExcludedCounts, EventRefs: p.EventRefs}
		if err := m.store.AppendSuggestionEvidence(ss[0].ID, e); err != nil {
			return errAnalysisStore
		}
	}
	stored, err := m.store.GetSuggestions()
	if err != nil {
		return errAnalysisStore
	}
	byID := map[string]RuleSuggestion{}
	for _, s := range stored {
		byID[s.ID] = s
	}
	for i, s := range report.Suggestions {
		report.Suggestions[i] = byID[s.ID]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Compatibility daily report is additional to the run's durable own report.
	if err := m.store.SaveReport(*report); err != nil {
		return errAnalysisStore
	}
	return ctx.Err()
}
func (s *DeviceStore) saveAnalysisRun(run AnalysisRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.basePath, "analysis")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if validateHistoryScope(run.ID) != nil || run.ID == "" {
		return fmt.Errorf("invalid analysis ID")
	}
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, run.ID+".json"), b)
}
func (s *DeviceStore) loadAnalysisRuns() (map[string]AnalysisRun, error) {
	runs := map[string]AnalysisRun{}
	dir := filepath.Join(s.basePath, "analysis")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return runs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		var r AnalysisRun
		if json.Unmarshal(b, &r) != nil || r.SchemaVersion != 1 || r.ID == "" || validateHistoryScope(r.ID) != nil || f.Name() != r.ID+".json" || r.OwnerID == "" || r.AlgorithmVersion == "" || r.CreatedAt.IsZero() || validateHistoryWindow(r.RequestedWindow) != nil {
			return nil, errors.New("invalid analysis record")
		}
		if _, err := time.LoadLocation(r.HomeTimezone); err != nil {
			return nil, errors.New("invalid analysis timezone")
		}
		switch r.Status {
		case "queued", "running":
			r.Status = "failed"
			r.ErrorCode = "interrupted"
			at := time.Now().UTC()
			r.FinishedAt = &at
			if err := s.saveAnalysisRun(r); err != nil {
				return nil, err
			}
		case "completed", "failed", "cancelled":
		default:
			return nil, errors.New("invalid analysis status")
		}
		runs[r.ID] = r
	}
	return runs, nil
}
