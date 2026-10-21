package smarthome

import (
	"context"
	"sort"
	"time"
)

// CollectionDiscovery is read-only. The caller supplies a trusted administrator
// owner; human job records are filtered before they leave the Manager.
type CollectionDiscovery struct {
	TargetDays         int
	RequestedWindow    *TimeWindow
	Coverage           *HistoryCoverage
	CoverageError      string
	AnalysisRuns       []AnalysisRun
	BackfillRuns       []BackfillRun
	NextAnalysisAt     *time.Time
	NextAnalysisReason string
}

func (m *Manager) CollectionDiscovery(ctx context.Context, owner string) CollectionDiscovery {
	out := CollectionDiscovery{TargetDays: 14, AnalysisRuns: []AnalysisRun{}, BackfillRuns: []BackfillRun{}, NextAnalysisReason: "schedule_not_observed"}
	m.analysisRunsMu.Lock()
	for _, run := range m.analysisRuns {
		if owner != "" && (run.OwnerID == owner || run.OwnerID == "system") {
			r := run
			r.Report = nil
			out.AnalysisRuns = append(out.AnalysisRuns, copyAnalysisRun(r))
		}
	}
	m.analysisRunsMu.Unlock()
	m.backfillMu.Lock()
	for _, run := range m.backfillRuns {
		if owner != "" && run.OwnerID == owner {
			out.BackfillRuns = append(out.BackfillRuns, copyBackfillRun(run))
		}
	}
	m.backfillMu.Unlock()
	sort.Slice(out.AnalysisRuns, func(i, j int) bool { return out.AnalysisRuns[i].CreatedAt.After(out.AnalysisRuns[j].CreatedAt) })
	sort.Slice(out.BackfillRuns, func(i, j int) bool { return out.BackfillRuns[i].CreatedAt.After(out.BackfillRuns[j].CreatedAt) })
	if len(out.AnalysisRuns) > 10 {
		out.AnalysisRuns = out.AnalysisRuns[:10]
	}
	if len(out.BackfillRuns) > 10 {
		out.BackfillRuns = out.BackfillRuns[:10]
	}
	// The scheduler does not persist/expose its current timer. A configured hour
	// alone does not prove a next tick, so report an explicit unknown.
	m.lifecycleMu.Lock()
	if !m.started || m.stopped {
		out.NextAnalysisReason = "scheduler_inactive"
	}
	m.lifecycleMu.Unlock()
	loc, err := m.homeLocation(ctx)
	if err != nil {
		out.CoverageError = "coverage_timezone_unavailable"
		return out
	}
	now := time.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	window := TimeWindow{Start: end.AddDate(0, 0, -14).UTC(), End: end.UTC()}
	out.RequestedWindow = &window
	ids, corrupt, err := m.store.analysisEntityIDs(ctx, window)
	if err != nil {
		out.CoverageError = "coverage_unavailable"
		return out
	}
	coverage := HistoryCoverage{Entities: map[string]EntityHistoryCoverage{}, CorruptFiles: corrupt}
	if len(ids) > 0 {
		read, e := m.store.ReadHistory(ctx, HistoryQuery{EntityIDs: ids, Window: window})
		if e != nil {
			out.CoverageError = "coverage_unavailable"
			return out
		}
		coverage = read.Coverage
	}
	out.Coverage = &coverage
	return out
}
