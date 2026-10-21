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
	"testing"
	"time"

	"go.uber.org/zap"
)

func coverageStore(t *testing.T) *DeviceStore {
	t.Helper()
	s, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func saveCoverageBatch(t *testing.T, s *DeviceStore, w TimeWindow, requested, successful []string, entries []HistoryEntry) {
	t.Helper()
	if err := s.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: requested, SuccessfulEntityIDs: successful, Source: "backfill", ErrorCode: func() string {
		if len(successful) < len(requested) {
			return "ha_unavailable"
		}
		return ""
	}()}, entries); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitHistoryWindow(w.Start, w.End); err != nil {
		t.Fatal(err)
	}
}

// Catches treating zero events or legacy records as recorder completeness and silent corruption.
func TestHistoryCoverageDistinguishesEmptyUnknownAndCorrupt(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	w := TimeWindow{Start: start, End: start.Add(24 * time.Hour)}
	saveCoverageBatch(t, s, w, []string{"light.empty", "light.failed"}, []string{"light.empty"}, nil)
	if err := s.SaveHistory([]HistoryEntry{{EntityID: "light.legacy", State: "on", Timestamp: start}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.basePath, "snapshots", "corrupt.json"), []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.empty", "light.failed", "light.legacy"}, Window: w})
	if err != nil {
		t.Fatal(err)
	}
	empty := got.Coverage.Entities["light.empty"]
	failed := got.Coverage.Entities["light.failed"]
	legacy := got.Coverage.Entities["light.legacy"]
	if empty.SuccessfulUnionDuration != 24*time.Hour || empty.ValidStateCount != 0 || empty.Retention != "unknown" || len(empty.Gaps) != 0 {
		t.Fatalf("empty success overclaimed: %#v", empty)
	}
	if failed.RequestedUnionDuration != 24*time.Hour || failed.SuccessfulUnionDuration != 0 || len(failed.Gaps) != 1 {
		t.Fatalf("failure lost: %#v", failed)
	}
	if legacy.Retention != "unknown" || legacy.SuccessfulUnionDuration != 0 || got.Coverage.CorruptFiles != 1 {
		t.Fatalf("quality facts lost: %#v", got.Coverage)
	}
	cp, err := s.LoadHistoryCheckpoint()
	if err != nil || !cp.IsZero() {
		t.Fatalf("batch moved live checkpoint: %v %v", cp, err)
	}
}

// Catches double-counting overlap, replacing same-window entities, and inclusive end leakage.
func TestHistoryCoverageUnionAndHalfOpenEnd(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	w := TimeWindow{Start: start, End: end}
	saveCoverageBatch(t, s, w, []string{"light.a"}, []string{"light.a"}, []HistoryEntry{
		{EntityID: "light.a", State: "off", Timestamp: start.Add(-time.Minute), ObservationKind: "initial"},
		{EntityID: "light.a", State: "on", Timestamp: start, ObservationKind: "change"},
		{EntityID: "light.a", State: "on", Timestamp: start.Add(time.Hour), ObservationKind: "change"},
	})
	saveCoverageBatch(t, s, w, []string{"light.b"}, []string{"light.b"}, []HistoryEntry{{EntityID: "light.b", State: "off", Timestamp: start, ObservationKind: "initial"}})
	saveCoverageBatch(t, s, TimeWindow{Start: start.Add(12 * time.Hour), End: end.Add(time.Hour)}, []string{"light.a"}, []string{"light.a"}, []HistoryEntry{{EntityID: "light.a", State: "off", Timestamp: end, ObservationKind: "change"}})
	// A failed retry for a must not replace a's earlier entries or successful receipt.
	saveCoverageBatch(t, s, w, []string{"light.a"}, nil, nil)
	reopened, err := NewDeviceStore(s.basePath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.a", "light.b"}, Window: w})
	if err != nil {
		t.Fatal(err)
	}
	if got.Coverage.RequestedUnionDuration != 24*time.Hour || got.Coverage.SuccessfulUnionDuration != 24*time.Hour {
		t.Fatalf("overlap counted twice: %#v", got.Coverage)
	}
	if len(got.Entries) != 3 || len(got.BaselineEntries) != 1 {
		t.Fatalf("same-window batch lost: %#v", got)
	}
	for _, e := range got.Entries {
		if !e.Timestamp.Before(end) || e.Timestamp.Before(start) {
			t.Fatalf("entry outside half-open window: %#v", e)
		}
	}
	a := got.Coverage.Entities["light.a"]
	if a.ValidStateCount != 2 || a.ChangeCount != 1 || a.SuccessfulUnionDuration != 24*time.Hour {
		t.Fatalf("state observations confused with genuine changes: %#v", a)
	}
	old, err := reopened.GetHistoryRange("", time.Time{}, end)
	if err != nil || len(old) != 5 {
		t.Fatalf("inclusive legacy reader lost envelope/baseline/end: %#v %v", old, err)
	}
}

// Catches invalid or out-of-scope entities being accepted by the history trust boundary.
func TestHistoryCoverageRejectsInvalidScope(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	w := TimeWindow{Start: start, End: start.Add(time.Hour)}
	for _, ids := range [][]string{nil, {""}, {"../light.a"}, {"light.a", ""}} {
		if _, err := s.ReadHistory(context.Background(), HistoryQuery{EntityIDs: ids, Window: w}); err == nil {
			t.Fatalf("query accepted %q", ids)
		}
		if err := s.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: ids, Source: "backfill"}, nil); err == nil {
			t.Fatalf("receipt accepted %q", ids)
		}
	}
	for _, tc := range []struct {
		success []string
		entries []HistoryEntry
	}{
		{[]string{"light.b"}, nil},
		{[]string{"light.a"}, []HistoryEntry{{EntityID: "light.b", Timestamp: start}}},
		{nil, []HistoryEntry{{EntityID: "light.a", Timestamp: start}}},
	} {
		if err := s.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: []string{"light.a"}, SuccessfulEntityIDs: tc.success, Source: "backfill"}, tc.entries); err == nil {
			t.Fatal("out-of-scope entry or success accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadHistory(ctx, HistoryQuery{EntityIDs: []string{"light.a"}, Window: w}); err != context.Canceled {
		t.Fatalf("cancel ignored: %v", err)
	}
}

// Catches partial live batches being discarded or incorrectly advancing the live cursor.
func TestHistoryCoverageCollectorPersistsPartialReceipts(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			var states []EntityState
			for i := 0; i < 51; i++ {
				states = append(states, EntityState{EntityID: fmt.Sprintf("light.test_%02d", i)})
			}
			json.NewEncoder(w).Encode(states)
			return
		}
		if strings.Contains(r.URL.Query().Get("filter_entity_id"), "light.test_50") {
			http.Error(w, "failure", 503)
			return
		}
		fmt.Fprint(w, "[]")
	}))
	defer server.Close()
	s := coverageStore(t)
	if err := s.SaveHistoryWindow(start.Add(-time.Hour), start, nil); err != nil {
		t.Fatal(err)
	}
	c := NewCollector(NewHomeAssistantClient(server.URL, "", time.Second), s, 3600, zap.NewNop())
	if err := c.collect(context.Background()); err == nil {
		t.Fatal("partial failure swallowed")
	}
	cp, _ := s.LoadHistoryCheckpoint()
	if !cp.Equal(start) {
		t.Fatal("partial window advanced checkpoint")
	}
	got, err := s.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.test_00", "light.test_50"}, Window: TimeWindow{Start: start, End: start.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Coverage.Entities["light.test_00"].SuccessfulUnionDuration <= 0 || got.Coverage.Entities["light.test_50"].RequestedUnionDuration <= 0 || got.Coverage.Entities["light.test_50"].SuccessfulUnionDuration != 0 {
		t.Fatalf("partial receipts lost: %#v", got.Coverage)
	}
	files, err := os.ReadDir(filepath.Join(s.basePath, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, f := range files {
		data, _ := os.ReadFile(filepath.Join(s.basePath, "snapshots", f.Name()))
		var env struct {
			SchemaVersion int              `json:"schema_version"`
			Receipts      []HistoryReceipt `json:"receipts"`
		}
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		for _, r := range env.Receipts {
			if env.SchemaVersion != 1 || r.SchemaVersion != 1 || r.ID == "" || ids[r.ID] || r.CompletedAt.IsZero() || r.Source != "live" {
				t.Fatalf("receipt metadata invalid: %#v", r)
			}
			ids[r.ID] = true
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected two independent request receipts: %#v", ids)
	}
}

// Catches partial publication and losing already-completed entities during retries.
func TestHistoryCoveragePublicationPreservesCompleteData(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	w := TimeWindow{Start: start, End: end}
	if err := s.SaveHistoryWindow(start, end, []HistoryEntry{{EntityID: "light.complete", State: "off", Timestamp: start, ObservationKind: "initial"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: []string{"light.partial"}, SuccessfulEntityIDs: []string{"light.partial"}, Source: "backfill"}, []HistoryEntry{{EntityID: "light.partial", State: "on", Timestamp: start, ObservationKind: "change"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHistoryBatch(HistoryReceipt{Window: w, RequestedEntityIDs: []string{"light.failed"}, ErrorCode: "ha_unavailable", Source: "backfill"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetHistoryRange("", start, end)
	if err != nil || len(got) != 1 || got[0].EntityID != "light.complete" {
		t.Fatalf("partial publication or complete data loss: %#v %v", got, err)
	}
	read, err := s.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.complete", "light.partial", "light.failed"}, Window: w})
	if err != nil || len(read.Entries) != 1 || read.Coverage.Entities["light.partial"].SuccessfulUnionDuration != time.Hour {
		t.Fatalf("partial facts confused with published events: %#v %v", read, err)
	}
	if err := s.CommitHistoryWindow(start, end); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetHistoryRange("", start, end)
	if err != nil || len(got) != 2 {
		t.Fatalf("explicit publication failed: %#v %v", got, err)
	}
	cp, _ := s.LoadHistoryCheckpoint()
	if !cp.Equal(end) {
		t.Fatal("backfill changed live checkpoint")
	}
}

// Catches counting another entity's requests toward the selected scope.
func TestHistoryCoverageUnionsRespectQueryScope(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	saveCoverageBatch(t, s, TimeWindow{Start: start, End: start.Add(time.Hour)}, []string{"light.a"}, nil, nil)
	saveCoverageBatch(t, s, TimeWindow{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)}, []string{"light.b"}, []string{"light.b"}, nil)
	got, err := s.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{"light.a"}, Window: TimeWindow{Start: start, End: start.Add(2 * time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Coverage.RequestedUnionDuration != time.Hour || got.Coverage.SuccessfulUnionDuration != 0 || len(got.Coverage.Receipts) != 1 {
		t.Fatalf("other entity leaked into coverage: %#v", got)
	}
}

// Catches a failed merge overwriting corrupt evidence instead of requiring repair.
func TestHistoryCoverageCorruptWindowIsNotOverwritten(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	path := filepath.Join(s.basePath, "snapshots", fmt.Sprintf("window-%d-%d.json", start.UnixNano(), end.UnixNano()))
	original := []byte(`{"schema_version":99}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHistoryWindow(start, end, nil); err == nil {
		t.Fatal("corrupt existing window overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("corrupt evidence modified")
	}
	cp, _ := s.LoadHistoryCheckpoint()
	if !cp.IsZero() {
		t.Fatal("corrupt write advanced checkpoint")
	}
}

// Catches offset-bearing caller timestamps escaping the new UTC storage contract.
func TestHistoryCoveragePersistsUTCInstants(t *testing.T) {
	s := coverageStore(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.FixedZone("household", 8*3600))
	end := start.Add(time.Hour)
	if err := s.SaveHistoryWindow(start, end, []HistoryEntry{{EntityID: "light.a", State: "on", Timestamp: start}}); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(s.basePath, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.basePath, "snapshots", files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Entries []struct {
			Timestamp string `json:"last_changed"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Entries) != 1 || saved.Entries[0].Timestamp != "2026-09-30T16:00:00Z" {
		t.Fatalf("stored non-UTC instant: %s", data)
	}
}
