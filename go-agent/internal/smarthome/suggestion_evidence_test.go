package smarthome

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Catches counting baseline as a new action and using elapsed days across DST.
func TestAnalysisRunLocalDaysDSTAndBaseline(t *testing.T) {
	loc, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 3, 16, 12, 0, 0, 0, loc)
	end := time.Date(2026, 3, 16, 0, 0, 0, 0, loc)
	start := end.AddDate(0, 0, -14)
	m := analysisManager(t)
	es := analysisHistory(start, 14, 11, false)
	es = append(es, HistoryEntry{EntityID: "light.baseline", State: "on", ObservationKind: "change", Timestamp: start.Add(-time.Hour)}, HistoryEntry{EntityID: "light.baseline", State: "off", ObservationKind: "change", Timestamp: start.Add(time.Hour)})
	if e := m.store.SaveHistory(es); e != nil {
		t.Fatal(e)
	}
	r, e := m.analyzer.AnalyzeContext(context.Background(), now, 14, loc)
	if e != nil {
		t.Fatal(e)
	}
	if r.PeriodEnd.Sub(r.PeriodStart) != 335*time.Hour {
		t.Fatalf("DST interval %s", r.PeriodEnd.Sub(r.PeriodStart))
	}
	for _, d := range r.Devices {
		if d.EntityID == "light.baseline" && (d.OnCount != 0 || d.OffCount != 1 || d.TotalOnTime != 1) {
			t.Fatalf("baseline invented action: %+v", d)
		}
	}
	if len(r.Suggestions) != 1 || r.Suggestions[0].Intent.TimeZone != "America/New_York" {
		t.Fatalf("timezone/suggestions: %+v", r.Suggestions)
	}
}

// Catches making numeric-only data insufficient due to inapplicable detectors.
func TestAnalysisRunNumericApplicability(t *testing.T) {
	m := analysisManager(t)
	end := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	var es []HistoryEntry
	for i := 0; i < 10; i++ {
		es = append(es, HistoryEntry{EntityID: "sensor.temperature", State: "20", Timestamp: end.Add(-time.Duration(i+1) * time.Hour)})
	}
	if e := m.store.SaveHistory(es); e != nil {
		t.Fatal(e)
	}
	r, e := m.analyzer.AnalyzeContext(context.Background(), end, 14, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	if r.Outcome != "no_pattern" || r.Outcomes["time"].Applicable || r.Outcomes["correlation"].Applicable || r.Outcomes["anomaly"].Candidates[0].NumericSamples != 10 {
		t.Fatalf("applicability %+v", r.Outcomes)
	}
}

// Catches use of unrelated target days or daily denominator for correlations.
func TestAnalysisRunCorrelationPrerequisites(t *testing.T) {
	end := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -14)
	for _, disjoint := range []bool{false, true} {
		t.Run(fmt.Sprint(disjoint), func(t *testing.T) {
			m := analysisManager(t)
			source := analysisHistory(start, 7, 7, false)
			var es []HistoryEntry
			es = append(es, source...)
			for _, v := range source {
				v.EntityID = "switch.target"
				v.Timestamp = v.Timestamp.Add(2 * time.Minute)
				if disjoint {
					v.Timestamp = v.Timestamp.AddDate(0, 0, 7)
				}
				es = append(es, v)
			}
			if e := m.store.SaveHistory(es); e != nil {
				t.Fatal(e)
			}
			r, e := m.analyzer.AnalyzeContext(context.Background(), end, 14, time.UTC)
			if e != nil {
				t.Fatal(e)
			}
			out := r.Outcomes["correlation"]
			if disjoint {
				if out.Outcome != "insufficient_data" || out.Candidates[0].ObservationDays != 0 {
					t.Fatalf("unrelated dates: %+v", out)
				}
			} else {
				if out.Outcome != "matched" || r.Outcome != "matched" {
					t.Fatalf("correlation blocked by time insufficiency: %+v", r.Outcomes)
				}
				for _, p := range r.Patterns {
					if p.Type == "correlation" && (p.Denominator != 7 || p.DenominatorType != "source_transitions" || p.HitDays == nil || *p.HitDays != 7) {
						t.Fatalf("denominator changed: %+v", p)
					}
				}
			}
		})
	}
}

// Catches treating almost seven elapsed days as enough observation opportunities.
func TestAnalysisRunSixPointNineDays(t *testing.T) {
	m := analysisManager(t)
	end := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -14)
	es := analysisHistory(start, 7, 0, false)
	es = append(es, HistoryEntry{EntityID: "light.room", State: "off", Timestamp: start.Add(165*time.Hour + 36*time.Minute)})
	if e := m.store.SaveHistory(es); e != nil {
		t.Fatal(e)
	}
	r, e := m.analyzer.AnalyzeContext(context.Background(), end, 14, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	if r.Outcomes["time"].Outcome != "insufficient_data" {
		t.Fatalf("6.9 days sufficient: %+v", r.Outcomes)
	}
}
func TestAnalysisLifecycleAcceptedCancellation(t *testing.T) {
	for _, mode := range []string{"explicit", "authorization", "stop"} {
		t.Run(mode, func(t *testing.T) {
			m := analysisManager(t)
			var es []HistoryEntry
			start := time.Now().AddDate(0, 0, -14)
			for i := 0; i < 30000; i++ {
				es = append(es, HistoryEntry{EntityID: "light.room", State: "off", Timestamp: start.Add(time.Duration(i) * time.Minute), ObservationKind: "initial"})
			}
			if e := m.store.SaveHistory(es); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			released := make(chan struct{})
			r, e := m.StartAnalysis(JobScope{Context: ctx, OwnerID: "member", Release: func() { close(released) }}, 14)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "explicit":
				if e := m.CancelAnalysis(r.ID); e != nil {
					t.Fatal(e)
				}
			case "authorization":
				cancel()
			case "stop":
				m.Stop()
			}
			done := waitAnalysis(t, m, r.ID)
			if done.Status != "cancelled" || done.ErrorCode != "cancelled" {
				t.Fatalf("cancellation lost: %+v", done)
			}
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Fatal("scope not released")
			}
			if e := m.CancelAnalysis("missing"); !errors.Is(e, ErrAnalysisNotFound) {
				t.Fatal(e)
			}
		})
	}
}

// Catches omitting corruption evidence when there are no discoverable entity IDs.
func TestAnalysisRunCorruptEmptyHistory(t *testing.T) {
	m := analysisManager(t)
	if e := os.WriteFile(filepath.Join(m.store.basePath, "snapshots", "broken.json"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := m.analyzer.AnalyzeContext(context.Background(), time.Now(), 14, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	if r.Outcome != "insufficient_data" || r.Coverage.CorruptFiles != 1 {
		t.Fatalf("corruption lost: %+v", r)
	}
}

// Catches leaking unrelated observations into member-visible evidence, and
// changing the first report after later evidence is appended.
func TestRepeatedSuggestionEvidenceScopeAndImmutableReport(t *testing.T) {
	m := analysisManager(t)
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	es := analysisHistory(end.AddDate(0, 0, -14), 14, 11, false)
	es = append(es, HistoryEntry{EntityID: "sensor.private", State: "unavailable", Timestamp: end.Add(-time.Hour)})
	if e := m.store.SaveHistory(es); e != nil {
		t.Fatal(e)
	}
	scope := JobScope{Context: context.Background(), OwnerID: "admin"}
	first, e := m.StartAnalysis(scope, 14)
	if e != nil {
		t.Fatal(e)
	}
	r := waitAnalysis(t, m, first.ID)
	if r.Status != "completed" {
		t.Fatalf("run: %+v", r)
	}
	evidence := r.Report.Suggestions[0].Evidence[0]
	if r.Counts.Excluded["invalid_or_ambiguous_state"] != 1 || evidence.ExcludedCounts["invalid_or_ambiguous_state"] != 0 {
		t.Fatal("unrelated counts leaked")
	}
	for _, ref := range evidence.EventRefs {
		if ref.EntityID != "light.room" {
			t.Fatal("unrelated event leaked")
		}
	}
	second, e := m.StartAnalysis(scope, 14)
	if e != nil {
		t.Fatal(e)
	}
	waitAnalysis(t, m, second.ID)
	r, e = m.GetAnalysisRun(first.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Report.Suggestions[0].Evidence) != 1 {
		t.Fatal("past report changed")
	}
	r.Report.Suggestions[0].Evidence[0].SampleSize = 999
	r, e = m.GetAnalysisRun(first.ID)
	if e != nil {
		t.Fatal(e)
	}
	if r.Report.Suggestions[0].Evidence[0].SampleSize != 11 {
		t.Fatal("Get exposed mutable internal report")
	}
}

// Old-only entities and half-open boundary observations/receipts must not alter
// the applicability or overall result of a current numeric-only analysis.
func TestAnalysisRunWindowScopedEligibility(t *testing.T) {
	end := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -14)
	for _, kind := range []string{"old_observation", "end_observation", "old_receipt", "future_receipt"} {
		t.Run(kind, func(t *testing.T) {
			m := analysisManager(t)
			var es []HistoryEntry
			for i := 0; i < 10; i++ {
				es = append(es, HistoryEntry{EntityID: "sensor.current", State: "20", Timestamp: start.Add(time.Duration(i) * time.Hour)})
			}
			switch kind {
			case "old_observation":
				es = append(es, HistoryEntry{EntityID: "light.retired", State: "off", ObservationKind: "initial", Timestamp: start.Add(-time.Nanosecond)})
			case "end_observation":
				es = append(es, HistoryEntry{EntityID: "light.future", State: "off", ObservationKind: "initial", Timestamp: end})
			case "old_receipt", "future_receipt":
				w := TimeWindow{Start: start.Add(-time.Hour), End: start}
				if kind == "future_receipt" {
					w = TimeWindow{Start: end, End: end.Add(time.Hour)}
				}
				if err := m.store.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: []string{"light.other"}, SuccessfulEntityIDs: []string{"light.other"}, Source: "live"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.store.SaveHistory(es); err != nil {
				t.Fatal(err)
			}
			r, err := m.analyzer.AnalyzeContext(context.Background(), end, 14, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			if r.Outcome != "no_pattern" || r.Outcomes["time"].Applicable || r.Outcomes["correlation"].Applicable {
				t.Fatalf("out-of-window entity changed applicability: %+v", r.Outcomes)
			}
			if len(r.Devices) != 1 || r.Devices[0].EntityID != "sensor.current" || len(r.Coverage.Entities) != 1 {
				t.Fatalf("out-of-window entity included: devices=%+v coverage=%+v", r.Devices, r.Coverage.Entities)
			}
		})
	}
}
func TestAnalysisRunOverlappingReceiptRetainsEmptyCandidate(t *testing.T) {
	m := analysisManager(t)
	end := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -14)
	w := TimeWindow{Start: start.Add(-time.Hour), End: start.Add(time.Hour)}
	if err := m.store.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: []string{"light.current"}, SuccessfulEntityIDs: []string{"light.current"}, Source: "live"}, nil); err != nil {
		t.Fatal(err)
	}
	r, err := m.analyzer.AnalyzeContext(context.Background(), end, 14, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	out := r.Outcomes["time"]
	if !out.Applicable || len(out.Candidates) != 1 || out.Candidates[0].EntityID != "light.current" || out.Outcome != "insufficient_data" {
		t.Fatalf("current empty receipt lost candidate: %+v", out)
	}
	if r.Coverage.SuccessfulUnionDuration != time.Hour {
		t.Fatalf("receipt clipping changed: %+v", r.Coverage)
	}
}
func TestAnalysisRunEligibleEntityKeepsPredecessor(t *testing.T) {
	m := analysisManager(t)
	end := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, 0, -14)
	if err := m.store.SaveHistory([]HistoryEntry{{EntityID: "light.current", State: "off", ObservationKind: "initial", Timestamp: start.Add(-time.Hour)}, {EntityID: "light.current", State: "on", ObservationKind: "change", Timestamp: start}, {EntityID: "light.retired", State: "on", ObservationKind: "change", Timestamp: start.Add(-time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	r, err := m.analyzer.AnalyzeContext(context.Background(), end, 14, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Devices) != 1 || r.Devices[0].EntityID != "light.current" || r.Devices[0].OnCount != 1 || r.Counts.Entries != 1 {
		t.Fatalf("eligible baseline lost or counted: %+v", r)
	}
}
