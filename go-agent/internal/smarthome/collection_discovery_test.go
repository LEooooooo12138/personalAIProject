package smarthome

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCollectionDiscoveryFiltersOwnersAndSeparatesRequestFacts(t *testing.T) {
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"time_zone":"UTC"}`) }))
	defer ha.Close()
	m, e := NewManager(HAConfig{BaseURL: ha.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Stop()
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	window := TimeWindow{Start: end.Add(-time.Hour), End: end}
	saveCoverageBatch(t, m.store, window, []string{"light.empty"}, []string{"light.empty"}, nil)
	m.analysisRuns["own"] = AnalysisRun{ID: "own", OwnerID: "owner", CreatedAt: now, Coverage: HistoryCoverage{Entities: map[string]EntityHistoryCoverage{"light.empty": {Retention: "unknown"}}}}
	m.analysisRuns["system"] = AnalysisRun{ID: "system", OwnerID: "system", CreatedAt: now.Add(-time.Second)}
	m.analysisRuns["private"] = AnalysisRun{ID: "private", OwnerID: "other", CreatedAt: now.Add(time.Second)}
	m.backfillRuns["own"] = BackfillRun{ID: "own", OwnerID: "owner", CreatedAt: now}
	m.backfillRuns["private"] = BackfillRun{ID: "private", OwnerID: "other", CreatedAt: now.Add(time.Second)}
	got := m.CollectionDiscovery(context.Background(), "owner")
	if len(got.AnalysisRuns) != 2 || got.AnalysisRuns[0].ID != "own" || len(got.BackfillRuns) != 1 || got.BackfillRuns[0].ID != "own" {
		t.Fatalf("foreign projection: %+v", got)
	}
	if got.NextAnalysisAt != nil || got.NextAnalysisReason != "scheduler_inactive" {
		t.Fatal("fabricated schedule")
	}
	if got.Coverage == nil || got.Coverage.SuccessfulUnionDuration != time.Hour || got.Coverage.Entities["light.empty"].ValidStateCount != 0 || got.Coverage.Entities["light.empty"].Retention != "unknown" {
		t.Fatalf("request became observations: %+v", got)
	}
	got.AnalysisRuns[0].Coverage.Entities["light.empty"] = EntityHistoryCoverage{Retention: "invented"}
	if m.analysisRuns["own"].Coverage.Entities["light.empty"].Retention != "unknown" {
		t.Fatal("projection aliases stored job")
	}
}
