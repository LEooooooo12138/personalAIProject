package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func backfillManager(t *testing.T, handler http.HandlerFunc, count int) *Manager {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	var states []EntityState
	for i := 0; i < count; i++ {
		states = append(states, EntityState{EntityID: fmt.Sprintf("light.test_%03d", i), State: "off"})
	}
	if count > 0 {
		states = append(states, states[0])
	}
	if err := m.store.SaveSnapshot(states); err != nil {
		t.Fatal(err)
	}
	return m
}
func backfillScope(releases *atomic.Int32) JobScope {
	return JobScope{Context: context.Background(), OwnerID: "owner", Release: func() { releases.Add(1) }}
}
func awaitBackfill(t *testing.T, m *Manager, id string) BackfillRun {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		run, err := m.GetBackfillRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "queued" && run.Status != "running" {
			return run
		}
		select {
		case <-deadline:
			t.Fatal("backfill did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestBackfillDoesNotMoveLiveCheckpoint(t *testing.T) {
	var requests, writes, largest atomic.Int32
	var seenMu sync.Mutex
	seen := map[string]bool{}
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/history/period/") {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		ids := strings.Split(r.URL.Query().Get("filter_entity_id"), ",")
		for {
			old := largest.Load()
			if int32(len(ids)) <= old || largest.CompareAndSwap(old, int32(len(ids))) {
				break
			}
		}
		if len(r.URL.String()) > 6000 {
			t.Error("URL limit exceeded")
		}
		seenMu.Lock()
		for _, id := range ids {
			if seen[id] {
				t.Errorf("duplicate request for %s", id)
			}
			seen[id] = true
		}
		seenMu.Unlock()
		requests.Add(1)
		fmt.Fprint(w, "[]")
	}, 101)
	end := time.Now().UTC().Add(-time.Hour)
	start := end.Add(-time.Hour)
	liveBefore := end.Add(30 * time.Minute)
	if err := m.store.SaveHistoryWindow(liveBefore.Add(-time.Hour), liveBefore, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.store.savePendingHistoryWindow(liveBefore, liveBefore.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	pendingBefore, _ := os.ReadFile(filepath.Join(m.store.basePath, "history-pending.json"))
	var releases atomic.Int32
	run, err := m.StartBackfill(backfillScope(&releases), start, end)
	if err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	liveAfter, _ := m.store.LoadHistoryCheckpoint()
	if !liveBefore.Equal(liveAfter) {
		t.Fatal("backfill moved live checkpoint")
	}
	pendingAfter, _ := os.ReadFile(filepath.Join(m.store.basePath, "history-pending.json"))
	if string(pendingBefore) != string(pendingAfter) {
		t.Fatal("backfill mutated live pending")
	}
	if largest.Load() > 50 || writes.Load() != 0 {
		t.Fatal("backfill exceeded read boundary")
	}
	if run.Status != "completed" || run.CompletedBatches != 3 || requests.Load() != 3 || len(seen) != 101 {
		t.Fatalf("run=%+v requests=%d seen=%d", run, requests.Load(), len(seen))
	}
	m.Stop()
	if releases.Load() != 1 {
		t.Fatalf("release=%d", releases.Load())
	}
}
func TestBackfillUnknownRetentionStaysUnknown(t *testing.T) {
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") }, 1)
	var releases atomic.Int32
	end := time.Now().UTC().Add(-10 * 24 * time.Hour)
	run, err := m.StartBackfill(backfillScope(&releases), end.Add(-time.Hour), end)
	if err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Retention != "unknown" {
		t.Fatal("empty history overclaimed retention")
	}
	result, err := m.store.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.test_000"}, Window: run.RequestedWindow})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 || result.Coverage.Entities["light.test_000"].Retention != "unknown" || len(result.Coverage.Receipts) != 1 {
		t.Fatalf("unexpected coverage %+v", result)
	}
}
func TestBackfillYieldsAndResumes(t *testing.T) {
	first, allowFirst, liveEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var history atomic.Int32
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			close(liveEntered)
			json.NewEncoder(w).Encode([]EntityState{{EntityID: "light.test_000", State: "off"}})
			return
		}
		if history.Add(1) == 1 {
			close(first)
			<-allowFirst
		} else {
			select {
			case <-liveEntered:
			default:
				t.Error("backfill second window overtook queued live collection")
			}
		}
		fmt.Fprint(w, "[]")
	}, 1)
	end := time.Now().UTC().Add(-time.Hour)
	// Live snapshot can finish without another history request.
	if err := m.store.SaveHistoryWindow(time.Now().Add(time.Hour), time.Now().Add(2*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	var releases atomic.Int32
	run, err := m.StartBackfill(backfillScope(&releases), end.Add(-2*time.Hour), end)
	if err != nil {
		t.Fatal(err)
	}
	<-first
	liveDone := make(chan error, 1)
	go func() { liveDone <- m.collector.collect(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		m.collector.scheduleMu.Lock()
		waiting := m.collector.liveWaiters
		m.collector.scheduleMu.Unlock()
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live collection did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	close(allowFirst)
	select {
	case <-liveEntered:
	case <-time.After(time.Second):
		t.Fatal("backfill did not yield to live")
	}
	if err := <-liveDone; err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "completed" || history.Load() != 2 {
		t.Fatalf("run=%+v requests=%d", run, history.Load())
	}
}
func TestBackfillRestartRequiresExplicitResumeAndDeduplicatesBatches(t *testing.T) {
	end := time.Now().UTC().Add(-time.Hour)
	start := end.Add(-time.Hour)
	var second atomic.Bool
	var calls atomic.Int32
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 && !second.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		var chunks [][]HistoryEntry
		for _, id := range strings.Split(r.URL.Query().Get("filter_entity_id"), ",") {
			chunks = append(chunks, []HistoryEntry{{EntityID: id, State: "on", Timestamp: start.Add(time.Minute)}})
		}
		json.NewEncoder(w).Encode(chunks)
	}, 51)
	var releases atomic.Int32
	run, err := m.StartBackfill(backfillScope(&releases), start, end)
	if err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "failed" || run.CompletedBatches != 1 || !run.Cursor.Equal(start) {
		t.Fatalf("partial progress=%+v", run)
	}
	result, err := m.store.ReadHistory(context.Background(), HistoryQuery{EntityIDs: run.EntityIDs, Window: run.RequestedWindow})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 {
		t.Fatal("interrupted batch leaked unpublished entries")
	}
	m.Stop()
	// Simulate process loss after the durable batch receipt, before run progress.
	run.Status = "running"
	run.CompletedBatches = 0
	if err := m.store.saveBackfillRun(run); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SaveSnapshot([]EntityState{{EntityID: "light.replacement", State: "off"}}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatal("restart automatically resumed history")
	}
	interrupted, err := reopened.GetBackfillRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != "cancelled" || interrupted.ErrorCode != "interrupted" {
		t.Fatalf("crashed run incorrectly active: %+v", interrupted)
	}
	other := backfillScope(&releases)
	other.OwnerID = "other"
	if _, err := reopened.StartBackfill(other, start, end); err != ErrBackfillConflict {
		t.Fatalf("foreign owner resumed run: %v", err)
	}
	second.Store(true)
	resumed, err := reopened.StartBackfill(backfillScope(&releases), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != run.ID {
		t.Fatal("resume changed stable run ID")
	}
	resumed = awaitBackfill(t, reopened, resumed.ID)
	if resumed.Status != "completed" || resumed.CompletedBatches != 2 || calls.Load() != 3 || resumed.LastFailureCode == "" {
		t.Fatalf("resume=%+v calls=%d", resumed, calls.Load())
	}
	result, err = reopened.store.ReadHistory(context.Background(), HistoryQuery{EntityIDs: run.EntityIDs, Window: run.RequestedWindow})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 51 || len(resumed.EntityIDs) != 51 {
		t.Fatalf("resume did not publish original entity snapshot: %d/%d", len(result.Entries), len(resumed.EntityIDs))
	}
	for _, receipt := range result.Coverage.Receipts {
		if receipt.AcquisitionScope != resumed.ID {
			t.Fatal("resume did not retain its durable acquisition scope")
		}
	}
}

func TestBackfillCancellationBusyAndScopeLifecycle(t *testing.T) {
	entered := make(chan struct{})
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }, 1)
	var releases atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := backfillScope(&releases)
	scope.Context = ctx
	end := time.Now().UTC().Add(-time.Hour)
	run, err := m.StartBackfill(scope, end.Add(-time.Hour), end)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = m.StartBackfill(backfillScope(&releases), end.Add(-time.Hour), end); err == nil {
		t.Fatal("concurrent backfill accepted")
	}
	cancel()
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "cancelled" || run.ErrorCode != "cancelled" {
		t.Fatalf("run=%+v", run)
	}
	m.Stop()
	if _, err = m.StartBackfill(backfillScope(&releases), end.Add(-time.Hour), end); err == nil {
		t.Fatal("stopped manager accepted work")
	}
	if releases.Load() != 3 {
		t.Fatalf("release=%d", releases.Load())
	}
}
func TestBackfillRangeValidationAndDST(t *testing.T) {
	var calls atomic.Int32
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, "[]") }, 1)
	var releases atomic.Int32
	now := time.Now().UTC()
	for _, w := range []TimeWindow{{now, now.Add(time.Hour)}, {now.Add(-14*24*time.Hour - time.Nanosecond), now}, {now, now}} {
		if _, err := m.StartBackfill(backfillScope(&releases), w.Start, w.End); err == nil {
			t.Fatal("invalid range accepted")
		}
	}
	if calls.Load() != 0 || releases.Load() != 3 {
		t.Fatalf("invalid work called HA or leaked scope: %d/%d", calls.Load(), releases.Load())
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 8, 1, 30, 0, 0, loc)
	end := time.Date(2026, 3, 8, 4, 30, 0, 0, loc)
	run, err := m.StartBackfill(backfillScope(&releases), start, end)
	if err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "completed" || calls.Load() != 2 || run.RequestedWindow.Start.Location() != time.UTC {
		t.Fatalf("DST instant boundaries incorrect: %+v calls=%d", run, calls.Load())
	}
}
func TestBackfillScopeStopWaitsForRelease(t *testing.T) {
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") }, 1)
	entered, unwind := make(chan struct{}), make(chan struct{})
	var releases atomic.Int32
	ctx, finish, err := m.acquireJob(backfillScope(&releases))
	if err != nil {
		t.Fatal(err)
	}
	go func() { <-ctx.Done(); close(entered); <-unwind; finish(); finish() }()
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	<-entered
	select {
	case <-stopped:
		t.Fatal("Stop did not wait for registered work")
	default:
	}
	close(unwind)
	<-stopped
	if releases.Load() != 1 {
		t.Fatalf("release=%d", releases.Load())
	}
}

func TestBackfillExplicitCancelWhileWaitingAndDefaultWindow(t *testing.T) {
	var calls atomic.Int32
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, "[]") }, 1)
	release, err := m.collector.acquireCollection(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var releases atomic.Int32
	before := time.Now().UTC()
	run, err := m.StartBackfill(backfillScope(&releases), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	if run.RequestedWindow.End.Before(before) || run.RequestedWindow.End.After(after) || run.RequestedWindow.End.Sub(run.RequestedWindow.Start) != 336*time.Hour {
		t.Fatalf("incorrect default window: %+v", run.RequestedWindow)
	}
	if err := m.CancelBackfill(run.ID); err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "cancelled" || calls.Load() != 0 {
		t.Fatalf("cancel did not unblock waiting job: %+v calls=%d", run, calls.Load())
	}
	if err := m.CancelBackfill("missing"); err != ErrBackfillNotFound {
		t.Fatalf("missing cancel=%v", err)
	}
}
func TestBackfillURLLimitAndOverlap(t *testing.T) {
	end := time.Now().UTC().Add(-time.Hour)
	start := end.Add(-time.Hour)
	var calls atomic.Int32
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if len("http://"+r.Host+r.URL.String()) > 6000 {
			t.Error("history exceeded URL limit")
		}
		queryStart, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(r.URL.Path, "/api/history/period/"))
		if err != nil {
			t.Error(err)
		}
		if !queryStart.Equal(start.Add(-time.Minute)) {
			t.Errorf("missing one-minute overlap: %v", queryStart)
		}
		fmt.Fprint(w, "[]")
	}, 1)
	var states []EntityState
	for i := 0; i < 12; i++ {
		states = append(states, EntityState{EntityID: fmt.Sprintf("light.long_%02d_", i) + strings.Repeat("a", 700), State: "off"})
	}
	if err := m.store.SaveSnapshot(states); err != nil {
		t.Fatal(err)
	}
	var releases atomic.Int32
	run, err := m.StartBackfill(backfillScope(&releases), start, end)
	if err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "completed" || calls.Load() < 2 {
		t.Fatalf("URL limit did not split a small entity batch: %+v/%d", run, calls.Load())
	}
}
func TestBackfillInvalidSnapshotAndScopeRelease(t *testing.T) {
	var calls, releases atomic.Int32
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }, 0)
	end := time.Now().UTC().Add(-time.Hour)
	for _, states := range [][]EntityState{nil, {{EntityID: "bad/id"}}} {
		if err := m.store.SaveSnapshot(states); err != nil {
			t.Fatal(err)
		}
		if _, err := m.StartBackfill(backfillScope(&releases), end.Add(-time.Hour), end); err == nil || err.Error() != "backfill_entities_unavailable" {
			t.Fatalf("invalid snapshot accepted: %v", err)
		}
	}
	scope := backfillScope(&releases)
	scope.Context = nil
	if _, err := m.StartBackfill(scope, end.Add(-time.Hour), end); err == nil {
		t.Fatal("nil scope context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scope = backfillScope(&releases)
	scope.Context = ctx
	if _, err := m.StartBackfill(scope, end.Add(-time.Hour), end); err == nil {
		t.Fatal("revoked scope accepted")
	}
	if releases.Load() != 4 || calls.Load() != 0 {
		t.Fatalf("scope releases=%d requests=%d", releases.Load(), calls.Load())
	}
}

func TestBackfillTerminalRunCanBeAcknowledgedImmediately(t *testing.T) {
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") }, 1)
	var releases atomic.Int32
	for i := 0; i < 40; i++ {
		end := time.Now().UTC().Add(-time.Duration(i+1) * time.Hour)
		run, err := m.StartBackfill(backfillScope(&releases), end.Add(-time.Hour), end)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			current, err := m.GetBackfillRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status == "completed" {
				acknowledged, err := m.StartBackfill(backfillScope(&releases), run.RequestedWindow.Start, run.RequestedWindow.End)
				if err != nil || acknowledged.ID != run.ID {
					t.Fatalf("terminal run still busy: %v", err)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("run did not finish")
			}
		}
	}
}

func TestBackfillPublicationIsolatesLiveScope(t *testing.T) {
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) {
		at, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end_time"))
		if err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode([][]HistoryEntry{{{EntityID: "light.test_000", State: "on", Timestamp: at.Add(-time.Minute)}}})
	}, 1)
	end := time.Now().UTC().Add(-time.Hour)
	start := end.Add(-time.Hour)
	window := TimeWindow{Start: start, End: end}
	if err := m.store.SaveHistoryBatch(HistoryReceipt{Window: window, Source: "live", RequestedEntityIDs: []string{"light.live"}, SuccessfulEntityIDs: []string{"light.live"}}, []HistoryEntry{{EntityID: "light.live", State: "on", Timestamp: start.Add(time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SaveHistoryBatch(HistoryReceipt{Window: window, Source: "live", RequestedEntityIDs: []string{"light.failed"}, ErrorCode: "ha_unavailable"}, nil); err != nil {
		t.Fatal(err)
	}
	var releases atomic.Int32
	run, err := m.StartBackfill(backfillScope(&releases), start, end)
	if err != nil {
		t.Fatal(err)
	}
	run = awaitBackfill(t, m, run.ID)
	if run.Status != "completed" {
		t.Fatalf("backfill=%+v", run)
	}
	result, err := m.store.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.live", "light.test_000"}, Window: window})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].EntityID != "light.test_000" {
		t.Fatalf("backfill published incomplete live scope: %+v", result.Entries)
	}
}
func TestBackfillPublicationIsolatesFailedAcquisitionScopes(t *testing.T) {
	for _, nextSource := range []string{"live", "another_backfill"} {
		t.Run(nextSource, func(t *testing.T) {
			end := time.Now().UTC().Add(-time.Hour)
			start := end.Add(-time.Hour)
			window := TimeWindow{Start: start, End: end}
			var calls atomic.Int32
			m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 2 {
					http.Error(w, "unavailable", 503)
					return
				}
				var chunks [][]HistoryEntry
				for _, id := range strings.Split(r.URL.Query().Get("filter_entity_id"), ",") {
					chunks = append(chunks, []HistoryEntry{{EntityID: id, State: "on", Timestamp: start.Add(time.Minute)}})
				}
				json.NewEncoder(w).Encode(chunks)
			}, 51)
			var releases atomic.Int32
			// A different target end lets another run share this first hourly window.
			failed, err := m.StartBackfill(backfillScope(&releases), start, end.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			failed = awaitBackfill(t, m, failed.ID)
			if failed.Status != "failed" {
				t.Fatalf("expected failed acquisition: %+v", failed)
			}
			if nextSource == "live" {
				if err := m.store.SaveHistoryBatch(HistoryReceipt{Window: window, Source: "live", RequestedEntityIDs: []string{"light.other"}, SuccessfulEntityIDs: []string{"light.other"}}, []HistoryEntry{{EntityID: "light.other", State: "on", Timestamp: start.Add(time.Minute)}}); err != nil {
					t.Fatal(err)
				}
				if err := m.store.SaveHistoryWindow(start, end, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := m.store.SaveSnapshot([]EntityState{{EntityID: "light.other", State: "off"}}); err != nil {
					t.Fatal(err)
				}
				scope := backfillScope(&releases)
				scope.OwnerID = "another_owner"
				next, err := m.StartBackfill(scope, start, end)
				if err != nil {
					t.Fatal(err)
				}
				next = awaitBackfill(t, m, next.ID)
				if next.Status != "completed" {
					t.Fatalf("next backfill=%+v", next)
				}
			}
			result, err := m.store.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.test_000", "light.other"}, Window: window})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Entries) != 1 || result.Entries[0].EntityID != "light.other" {
				t.Fatalf("%s published failed acquisition: %+v", nextSource, result.Entries)
			}
			legacy, err := m.store.GetHistoryRange("", start, end)
			if err != nil {
				t.Fatal(err)
			}
			if len(legacy) != 1 || legacy[0].EntityID != "light.other" {
				t.Fatalf("legacy reader exposed failed acquisition: %+v", legacy)
			}
		})
	}
}

func TestBackfillAcquisitionScopeValidation(t *testing.T) {
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HA request") }, 1)
	end := time.Now().UTC().Add(-time.Hour)
	window := TimeWindow{Start: end.Add(-time.Hour), End: end}
	for _, scope := range []string{"../outside", strings.Repeat("F", 32), strings.Repeat("a", 31)} {
		receipt := HistoryReceipt{Window: window, Source: "backfill", AcquisitionScope: scope, RequestedEntityIDs: []string{"light.a"}, SuccessfulEntityIDs: []string{"light.a"}}
		if err := m.store.SaveHistoryBatch(receipt, nil); err == nil {
			t.Fatalf("accepted invalid receipt scope %q", scope)
		}
		if err := m.store.CommitScopedHistoryWindow(scope, window.Start, window.End); err == nil {
			t.Fatalf("accepted invalid commit scope %q", scope)
		}
	}
	if err := m.store.CommitScopedHistoryWindow("", window.Start, window.End); err == nil {
		t.Fatal("scoped commit could publish legacy/live entries")
	}
	receipt := HistoryReceipt{Window: window, Source: "live", AcquisitionScope: strings.Repeat("a", 32), RequestedEntityIDs: []string{"light.a"}, SuccessfulEntityIDs: []string{"light.a"}}
	if err := m.store.SaveHistoryBatch(receipt, nil); err == nil {
		t.Fatal("live request acquired a backfill namespace")
	}
}

func TestBackfillRejectsLegacyArrayInsideScopedEnvelope(t *testing.T) {
	m := backfillManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HA request") }, 1)
	end := time.Now().UTC().Add(-time.Hour)
	window := TimeWindow{Start: end.Add(-time.Hour), End: end}
	scope := strings.Repeat("a", 32)
	if err := os.WriteFile(m.store.scopedHistoryWindowPath(window, scope), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.store.CommitScopedHistoryWindow(scope, window.Start, window.End); err == nil {
		t.Fatal("unscoped legacy data was accepted in a scoped acquisition file")
	}
	if _, err := os.Stat(m.store.historyWindowPath(window)); !os.IsNotExist(err) {
		t.Fatal("scoped corruption created or overwrote the live window")
	}
}
