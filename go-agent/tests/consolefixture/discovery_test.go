package main

import (
	"context"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
	"time"
)

func TestFixtureDiscoveryEmptyHistoryRemainsInsufficient(t *testing.T) {
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()
	scope := func() smarthome.JobScope {
		return smarthome.JobScope{Context: context.Background(), Release: func() {}, OwnerID: "fixture-owner"}
	}
	backfill, err := f.app.SmartHome.StartBackfill(scope(), end.Add(-time.Hour), end)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, e := f.app.SmartHome.GetBackfillRun(backfill.ID)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status == "completed" {
			break
		}
		if r.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("backfill %+v", r)
		}
		time.Sleep(time.Millisecond)
	}
	run, err := f.app.SmartHome.StartAnalysis(scope(), 14)
	if err != nil {
		t.Fatal(err)
	}
	for {
		r, e := f.app.SmartHome.GetAnalysisRun(run.ID)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status == "completed" {
			if r.Outcome != "insufficient_data" || r.Counts.Entries != 0 || r.Coverage.SuccessfulUnionDuration != time.Hour {
				t.Fatalf("empty history became evidence %+v", r)
			}
			break
		}
		if r.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("analysis %+v", r)
		}
		time.Sleep(time.Millisecond)
	}
	if f.dependencies.deviceWrites.Load() != 0 || f.dependencies.automationWrites.Load() != 0 {
		t.Fatal("discovery wrote HA")
	}
}
