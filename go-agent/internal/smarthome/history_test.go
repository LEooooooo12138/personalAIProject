package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectorBatchesAllEntities(t *testing.T) {
	var batches int
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			var states []EntityState
			for i := 0; i < 166; i++ {
				states = append(states, EntityState{EntityID: fmt.Sprintf("light.test_%d", i), State: "on"})
			}
			json.NewEncoder(w).Encode(states)
			return
		}
		ids := r.URL.Query().Get("filter_entity_id")
		if ids == "" {
			http.Error(w, "filter required", 400)
			return
		}
		batches++
		batch := strings.Split(ids, ",")
		if len(batch) > 50 {
			t.Errorf("oversized batch %d", len(batch))
		}
		for _, id := range batch {
			if seen[id] {
				t.Errorf("duplicate %s", id)
			}
			seen[id] = true
		}
		fmt.Fprint(w, "[]")
	}))
	defer server.Close()
	store, _ := NewDeviceStore(t.TempDir())
	c := NewCollector(NewHomeAssistantClient(server.URL, "", time.Second), store, 3600, zap.NewNop())
	if err := c.collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if batches != 4 || len(seen) != 166 {
		t.Fatalf("batches=%d entities=%d", batches, len(seen))
	}
}
func TestHistoryEmptyFilterNeverRequestsHA(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, "[]") }))
	defer s.Close()
	c := NewHomeAssistantClient(s.URL, "", time.Second)
	_, err := c.GetHistory(context.Background(), "", time.Now().Add(-time.Hour), time.Now())
	if err == nil || calls != 0 {
		t.Fatalf("empty filter accepted: err=%v calls=%d", err, calls)
	}
}

func TestHistoryInitialStateIsPreserved(t *testing.T) {
	start := time.Date(2026, 9, 1, 10, 0, 0, 123, time.FixedZone("offset", 8*3600))
	end := start.Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authorization")
		}
		if r.URL.Query().Get("filter_entity_id") != "light.a,light.b" {
			t.Errorf("filter=%s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("end_time") != end.UTC().Format(time.RFC3339Nano) {
			t.Error("end lost UTC/nanoseconds")
		}
		json.NewEncoder(w).Encode([][]HistoryEntry{{{EntityID: "light.a", State: "off", Timestamp: start}, {EntityID: "light.a", State: "on", Timestamp: start.Add(time.Second)}, {EntityID: "light.a", State: "off", Timestamp: end}}, {{EntityID: "light.b", State: "on", Timestamp: start.Add(time.Second)}}})
	}))
	defer server.Close()
	c := NewHomeAssistantClient(server.URL, "fixture", time.Second)
	entries, err := c.GetHistoryForEntities(context.Background(), []string{"light.b", "light.a", "light.a"}, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ObservationKind != "initial" || entries[1].ObservationKind != "change" || entries[2].ObservationKind != "change" {
		t.Fatalf("provenance/end=%#v", entries)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.GetHistoryForEntities(ctx, []string{"light.a"}, start, end); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestHistoryWindowCheckpointReplay(t *testing.T) {
	store, _ := NewDeviceStore(t.TempDir())
	start := time.Date(2026, 9, 1, 0, 0, 0, 1, time.UTC)
	end := start.Add(time.Nanosecond)
	entries := []HistoryEntry{{EntityID: "light.a", State: "on", Timestamp: start, ObservationKind: "change"}}
	checkpoint := filepath.Join(store.basePath, "history-checkpoint.json")
	if err := os.Mkdir(checkpoint, 0755); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistoryWindow(start, end, entries); err == nil {
		t.Fatal("checkpoint failure hidden")
	}
	if err := os.Remove(checkpoint); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.SaveHistoryWindow(start, end, entries); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveHistoryWindow(end, end.Add(time.Nanosecond), []HistoryEntry{{EntityID: "light.b", State: "off", Timestamp: end}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetHistoryRange("", time.Time{}, time.Time{})
	if err != nil || len(got) != 2 {
		t.Fatalf("replay/precision=%#v %v", got, err)
	}
	reopened, _ := NewDeviceStore(store.basePath)
	cp, err := reopened.LoadHistoryCheckpoint()
	if err != nil || !cp.Equal(end.Add(time.Nanosecond)) {
		t.Fatalf("checkpoint=%s %v", cp, err)
	}
}
func TestCollectorFailedBatchDoesNotAdvanceCheckpoint(t *testing.T) {
	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	fail := true
	var starts []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			var states []EntityState
			for i := 0; i < 51; i++ {
				states = append(states, EntityState{EntityID: fmt.Sprintf("light.%s%d", strings.Repeat("x", 180), i)})
			}
			json.NewEncoder(w).Encode(states)
			return
		}
		if len(r.URL.String()) > 6000 {
			t.Error("oversized encoded URL")
		}
		queryStart, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(r.URL.Path, "/api/history/period/"))
		if err != nil {
			t.Error(err)
		}
		starts = append(starts, queryStart)
		if fail && len(starts) == 2 {
			http.Error(w, "recorder unavailable", 503)
			return
		}
		id := strings.Split(r.URL.Query().Get("filter_entity_id"), ",")[0]
		json.NewEncoder(w).Encode([][]HistoryEntry{{{EntityID: id, State: "off", Timestamp: queryStart}, {EntityID: id, State: "on", Timestamp: queryStart.Add(30 * time.Second)}}})
	}))
	defer server.Close()
	store, _ := NewDeviceStore(t.TempDir())
	if err := store.SaveHistoryWindow(start.Add(-time.Hour), start, nil); err != nil {
		t.Fatal(err)
	}
	c := NewCollector(NewHomeAssistantClient(server.URL, "", time.Second), store, 3600, zap.NewNop())
	if err := c.collect(context.Background()); err == nil {
		t.Fatal("batch failure swallowed")
	}
	cp, _ := store.LoadHistoryCheckpoint()
	if !cp.Equal(start) {
		t.Fatal("partial window advanced")
	}
	if !starts[0].Equal(start.Add(-time.Minute)) {
		t.Fatal("overlap query missing")
	}
	fail = false
	starts = nil
	if err := c.collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !starts[0].Equal(start.Add(-time.Minute)) {
		t.Fatal("retry did not resume checkpoint")
	}
	entries, _ := store.GetHistoryRange("", time.Time{}, time.Time{})
	found := false
	for _, e := range entries {
		if e.Timestamp.Equal(start.Add(-30*time.Second)) && e.ObservationKind == "change" {
			found = true
		}
	}
	if !found {
		t.Fatal("real overlap event clipped")
	}
}

func TestCollectorFirstFailedWindowSurvivesRestart(t *testing.T) {
	var bounds []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			fmt.Fprint(w, `[{"entity_id":"light.a","state":"on"}]`)
			return
		}
		bounds = append(bounds, r.URL.String())
		http.Error(w, "failure", 503)
	}))
	defer server.Close()
	store, _ := NewDeviceStore(t.TempDir())
	for i := 0; i < 2; i++ {
		c := NewCollector(NewHomeAssistantClient(server.URL, "", time.Second), store, 3600, zap.NewNop())
		if err := c.collect(context.Background()); err == nil {
			t.Fatal("failure swallowed")
		}
	}
	if len(bounds) != 2 || bounds[0] != bounds[1] {
		t.Fatalf("failed first window changed across restart: %#v", bounds)
	}
}

func TestHistoryEndBoundaryReplayedByNextOverlap(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	boundary := start.Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryStart, _ := time.Parse(time.RFC3339Nano, strings.TrimPrefix(r.URL.Path, "/api/history/period/"))
		queryEnd, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end_time"))
		entries := []HistoryEntry{{EntityID: "light.a", State: "off", Timestamp: queryStart}}
		if boundary.Before(queryEnd) {
			entries = append(entries, HistoryEntry{EntityID: "light.a", State: "on", Timestamp: boundary})
		}
		json.NewEncoder(w).Encode([][]HistoryEntry{entries})
	}))
	defer server.Close()
	client := NewHomeAssistantClient(server.URL, "", time.Second)
	store, _ := NewDeviceStore(t.TempDir())
	first, err := client.GetHistoryForEntities(context.Background(), []string{"light.a"}, start, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatal("end boundary should be excluded")
	}
	store.SaveHistoryWindow(start, boundary, first)
	next, err := client.GetHistoryForEntities(context.Background(), []string{"light.a"}, boundary.Add(-time.Minute), boundary.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	store.SaveHistoryWindow(boundary, boundary.Add(time.Hour), next)
	entries, _ := store.GetHistoryRange("", start, boundary.Add(time.Hour))
	transitions := stateTransitions(entries)
	if len(transitions) != 1 || !transitions[0].At.Equal(boundary) || transitions[0].To != "on" {
		t.Fatalf("boundary lost/duplicated=%#v", transitions)
	}
}
