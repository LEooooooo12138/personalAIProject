package smarthome

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func analysisManager(t *testing.T) *Manager {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/config" {
			t.Errorf("unexpected HA request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		_, _ = w.Write([]byte(`{"time_zone":"UTC"}`))
	}))
	t.Cleanup(server.Close)
	m, e := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir(), TimeZone: "UTC"}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Stop)
	return m
}
func waitAnalysis(t *testing.T, m *Manager, id string) AnalysisRun {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		r, e := m.GetAnalysisRun(id)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status != "queued" && r.Status != "running" {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("analysis did not terminate")
	return AnalysisRun{}
}
func analysisHistory(start time.Time, days, hits int, vary bool) []HistoryEntry {
	var es []HistoryEntry
	for day := 0; day < days; day++ {
		date := start.AddDate(0, 0, day)
		at := time.Date(date.Year(), date.Month(), date.Day(), 18, 0, 0, 0, date.Location())
		es = append(es, HistoryEntry{EntityID: "light.room", State: "off", Timestamp: at.Add(-time.Minute), ObservationKind: "initial"})
		if day < hits {
			if vary {
				at = at.Add(time.Duration(day%6) * time.Hour)
			}
			es = append(es, HistoryEntry{EntityID: "light.room", State: "on", Timestamp: at, ObservationKind: "change"})
		}
	}
	return es
}

// Catches receipt completeness assumptions and changed 14-day matching threshold.
func TestAnalysisRunOutcomesAndEvidence(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start := now.AddDate(0, 0, -14)
	for _, tc := range []struct {
		name       string
		days, hits int
		vary       bool
		want       string
	}{{"seven_hits", 14, 7, false, "no_pattern"}, {"ten_hits", 14, 10, false, "no_pattern"}, {"eleven_hits", 14, 11, false, "matched"}, {"irregular", 14, 14, true, "no_pattern"}, {"truncated_seven", 7, 7, false, "insufficient_data"}, {"truncated_ten", 10, 10, false, "insufficient_data"}, {"first_hour", 1, 0, false, "insufficient_data"}} {
		t.Run(tc.name, func(t *testing.T) {
			m := analysisManager(t)
			if e := m.store.SaveHistory(analysisHistory(start, tc.days, tc.hits, tc.vary)); e != nil {
				t.Fatal(e)
			}
			r, e := m.analyzer.AnalyzeContext(context.Background(), now, 14, time.UTC)
			if e != nil {
				t.Fatal(e)
			}
			if r.Outcomes["time"].Outcome != tc.want {
				t.Fatalf("outcomes=%+v", r.Outcomes)
			}
			if (len(r.Suggestions) > 0) != (tc.want == "matched") {
				t.Fatal("14 day threshold changed")
			}
			if tc.want == "matched" {
				p := r.Patterns[0]
				if p.SampleSize != 11 || p.PeriodDays != 14 || p.HitDays == nil || *p.HitDays != 11 || p.Denominator != 14 {
					t.Fatalf("evidence lost: %+v", p)
				}
			}
		})
	}
	t.Run("empty_successful_request", func(t *testing.T) {
		m := analysisManager(t)
		w := TimeWindow{start, now}
		if e := m.store.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: []string{"light.room"}, SuccessfulEntityIDs: []string{"light.room"}, Source: "live"}, nil); e != nil {
			t.Fatal(e)
		}
		r, e := m.analyzer.AnalyzeContext(context.Background(), now, 14, time.UTC)
		if e != nil {
			t.Fatal(e)
		}
		if r.Outcome != "insufficient_data" || r.Coverage.SuccessfulUnionDuration != 14*24*time.Hour {
			t.Fatalf("empty fabricated observations: %+v", r)
		}
	})
}

// Catches missing durable report, same-day overwrite and automatic recovery.
func TestAnalysisRunPersistenceAndRecovery(t *testing.T) {
	m := analysisManager(t)
	scope := JobScope{Context: context.Background(), OwnerID: "member"}
	r, e := m.StartAnalysis(scope, 14)
	if e != nil {
		t.Fatal(e)
	}
	done := waitAnalysis(t, m, r.ID)
	if done.Status != "completed" || done.OwnerID != "member" || done.Report == nil {
		t.Fatalf("run=%+v", done)
	}
	if _, e := os.Stat(filepath.Join(m.store.basePath, "analysis", r.ID+".json")); e != nil {
		t.Fatal(e)
	}
	again, e := m.StartAnalysis(scope, 14)
	if e != nil {
		t.Fatal(e)
	}
	waitAnalysis(t, m, again.ID)
	if again.ID == r.ID {
		t.Fatal("same run")
	}
	for _, status := range []string{"queued", "running"} {
		done.Status = status
		if e := m.store.saveAnalysisRun(done); e != nil {
			t.Fatal(e)
		}
		runs, e := m.store.loadAnalysisRuns()
		if e != nil {
			t.Fatal(e)
		}
		got := runs[r.ID]
		if got.Status != "failed" || got.ErrorCode != "interrupted" {
			t.Fatal("stale running job")
		}
	}
}
func TestAnalysisLifecycleCancellation(t *testing.T) {
	m := analysisManager(t)
	var releases atomic.Int32
	scope := func(c context.Context) JobScope {
		return JobScope{Context: c, OwnerID: "member", Release: func() { releases.Add(1) }}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.StartAnalysis(scope(ctx), 14); !errors.Is(e, context.Canceled) {
		t.Fatalf("revoked accepted: %v", e)
	}
	m.analysisMu.Lock()
	if _, e := m.StartAnalysis(scope(context.Background()), 14); !errors.Is(e, ErrAnalysisBusy) {
		t.Fatalf("parallel accepted: %v", e)
	}
	if e := m.runDailyAnalysis(context.Background()); !errors.Is(e, ErrAnalysisBusy) {
		t.Fatalf("daily bypassed lock: %v", e)
	}
	m.analysisMu.Unlock()
	m.Stop()
	if _, e := m.StartAnalysis(scope(context.Background()), 14); e == nil {
		t.Fatal("stopped accepted")
	}
	if releases.Load() != 3 {
		t.Fatalf("release count %d", releases.Load())
	}
}

// Deterministic cancellation after entering file processing (no timing race).
type processingCancelContext struct {
	context.Context
	checks, after int
}

func (c *processingCancelContext) Err() error {
	c.checks++
	if c.checks >= c.after {
		return context.Canceled
	}
	return nil
}
func TestAnalysisRunReadHistoryPostReadCancellation(t *testing.T) {
	m := analysisManager(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if e := m.store.SaveHistory(analysisHistory(start, 14, 11, false)); e != nil {
		t.Fatal(e)
	}
	ctx := &processingCancelContext{Context: context.Background(), after: 3}
	_, e := m.store.ReadHistory(ctx, HistoryQuery{EntityIDs: []string{"light.room"}, Window: TimeWindow{start, start.AddDate(0, 0, 14)}})
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("post-read cancellation ignored: %v", e)
	}
}
func TestAnalysisRunAssociationCancellation(t *testing.T) {
	a := NewAnalyzer(nil, zap.NewNop())
	ctx := &processingCancelContext{Context: context.Background(), after: 5}
	es := map[string][]HistoryEntry{"light.a": analysisHistory(time.Now().AddDate(0, 0, -14), 14, 11, false), "light.b": analysisHistory(time.Now().AddDate(0, 0, -14), 14, 11, false)}
	if _, e := a.correlationsContext(ctx, es, 14); !errors.Is(e, context.Canceled) {
		t.Fatalf("association cancellation ignored: %v", e)
	}
}
func TestRepeatedSuggestionAppendsEvidence(t *testing.T) {
	m := analysisManager(t)
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if e := m.store.SaveHistory(analysisHistory(end.AddDate(0, 0, -14), 14, 11, false)); e != nil {
		t.Fatal(e)
	}
	scope := JobScope{Context: context.Background(), OwnerID: "member"}
	r, e := m.StartAnalysis(scope, 14)
	if e != nil {
		t.Fatal(e)
	}
	done := waitAnalysis(t, m, r.ID)
	if done.Status != "completed" || len(done.Report.Suggestions) != 1 {
		t.Fatalf("run=%+v", done)
	}
	first := done.Report.Suggestions[0]
	original := *first.Intent
	if e := m.store.UpdateSuggestionStatus(first.ID, "applying"); e != nil {
		t.Fatal(e)
	}
	if e := m.store.UpdateSuggestionStatus(first.ID, "confirmed"); e != nil {
		t.Fatal(e)
	}
	r, e = m.StartAnalysis(scope, 14)
	if e != nil {
		t.Fatal(e)
	}
	done = waitAnalysis(t, m, r.ID)
	got := done.Report.Suggestions[0]
	if got.Status != "confirmed" || !reflect.DeepEqual(original, *got.Intent) {
		t.Fatal("confirmed intent changed")
	}
	if len(got.Evidence) != 2 || got.Evidence[0].RunID == got.Evidence[1].RunID {
		t.Fatalf("evidence not appended: %+v", got.Evidence)
	}
	if e := m.store.AppendSuggestionEvidence(first.ID, got.Evidence[1]); e != nil {
		t.Fatal(e)
	}
	stored, e := m.store.GetSuggestions()
	if e != nil {
		t.Fatal(e)
	}
	if len(stored[0].Evidence) != 2 {
		t.Fatal("same run duplicated")
	}
	conflict := got.Evidence[1]
	conflict.SampleSize++
	if e := m.store.AppendSuggestionEvidence(first.ID, conflict); !errors.Is(e, ErrSuggestionConflict) {
		t.Fatalf("same run changed: %v", e)
	}
}
