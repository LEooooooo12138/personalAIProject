package smarthome

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAnalysisRunCancellationInsideNormalization(t *testing.T) {
	es := analysisHistory(time.Now().AddDate(0, 0, -14), 14, 11, false)
	for i := 0; i < 1000; i++ {
		es = append(es, HistoryEntry{EntityID: "light.large", State: "off", Timestamp: time.Unix(int64(i), 0)})
	}
	ctx := &processingCancelContext{Context: context.Background(), after: 10}
	if _, e := deduplicateHistoryContext(ctx, es); !errors.Is(e, context.Canceled) {
		t.Fatalf("normalization ignored cancellation: %v", e)
	}
}
func TestAnalysisRunCancellationInDetectorAndSummary(t *testing.T) {
	a := NewAnalyzer(nil, nil)
	es := analysisHistory(time.Now().AddDate(0, 0, -14), 14, 11, false)
	for _, kind := range []string{"time", "anomaly", "summary"} {
		t.Run(kind, func(t *testing.T) {
			ctx := &processingCancelContext{Context: context.Background(), after: 1}
			var e error
			switch kind {
			case "time":
				_, e = a.timePatternsContext(ctx, "light.room", es, 14)
			case "anomaly":
				_, e = a.anomaliesContext(ctx, "sensor.room", es)
			case "summary":
				_, e = a.summariesContext(ctx, map[string][]HistoryEntry{"light.room": es}, 14, time.Now().AddDate(0, 0, -14), time.Now())
			}
			if !errors.Is(e, context.Canceled) {
				t.Fatalf("%s cancellation ignored: %v", kind, e)
			}
		})
	}
}
func TestAnalysisRunStoreFailureReleasesScope(t *testing.T) {
	m := analysisManager(t)
	if e := os.WriteFile(filepath.Join(m.store.basePath, "analysis"), []byte("blocked directory"), 0600); e != nil {
		t.Fatal(e)
	}
	released := 0
	if _, e := m.StartAnalysis(JobScope{Context: context.Background(), OwnerID: "member", Release: func() { released++ }}, 14); e == nil {
		t.Fatal("accepted undurable run")
	}
	if released != 1 {
		t.Fatal("scope leaked")
	}
	if !m.analysisMu.TryLock() {
		t.Fatal("analysis slot leaked")
	}
	m.analysisMu.Unlock()
}

// Cancellation becomes observable only after five checkpoints: the outcome
// pass must check inside its large local-date scan, not only at detector entry.
func TestAnalysisRunOutcomeDateScanCancellation(t *testing.T) {
	a := NewAnalyzer(nil, nil)
	var es []HistoryEntry
	for i := 0; i < 4096; i++ {
		es = append(es, HistoryEntry{EntityID: "light.a", State: "on", Timestamp: time.Unix(int64(i)*3600, 0)})
	}
	ctx := &processingCancelContext{Context: context.Background(), after: 6}
	_, err := a.patternOutcomes(ctx, []string{"light.a"}, map[string][]HistoryEntry{"light.a": es}, map[string][]HistoryEntry{"light.a": es}, nil, 14)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("outcome date scan ignored mid-processing cancellation: %v (%d checkpoints)", err, ctx.checks)
	}
}

// Catches reconciliation that walks every pattern without cancellation, even
// when the request currently has no applicable entity candidates.
func TestAnalysisRunOutcomePatternIndexCancellation(t *testing.T) {
	a := NewAnalyzer(nil, nil)
	patterns := make([]DetectedPattern, 4096)
	for i := range patterns {
		patterns[i] = DetectedPattern{Type: "time", EntityID: "light.a"}
	}
	ctx := &processingCancelContext{Context: context.Background(), after: 2}
	_, err := a.patternOutcomes(ctx, nil, nil, nil, patterns, 14)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("outcome pattern processing ignored cancellation: %v (%d checkpoints)", err, ctx.checks)
	}
}
