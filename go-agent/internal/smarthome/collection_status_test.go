package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func collectionView(t *testing.T, c *Collector) map[string]any {
	t.Helper()
	raw, err := json.Marshal(c.Status())
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

// This catches conflating snapshot success with completed history, advancing a
// partial checkpoint, fabricated batch counts, and restart success inference.
func TestCollectionStatusPartialHistoryFailurePreservesCheckpoint(t *testing.T) {
	batches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			var states []EntityState
			for i := 0; i < 51; i++ {
				states = append(states, EntityState{EntityID: fmt.Sprintf("light.x%d", i), State: "off"})
			}
			json.NewEncoder(w).Encode(states)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/history/period/") {
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		batches++
		if batches == 2 {
			http.Error(w, "PRIVATE_UPSTREAM_SECRET", 503)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	if err = store.SaveHistoryWindow(checkpoint.Add(-time.Hour), checkpoint, nil); err != nil {
		t.Fatal(err)
	}
	c := NewCollector(NewHomeAssistantClient(server.URL, "fixture", time.Second), store, 3600, zap.NewNop())
	before := collectionView(t, c)
	if before["last_success_at"] != nil {
		t.Fatal("new process inferred success from checkpoint")
	}
	if err = c.collect(context.Background()); err == nil {
		t.Fatal("history failure swallowed")
	}
	status := collectionView(t, c)
	if status["phase"] != "failed" || status["snapshot_at"] == nil || status["last_success_at"] != nil || status["last_failure_at"] == nil || status["completed_batches"] != float64(1) || status["total_batches"] != float64(2) {
		t.Fatalf("partial run misrepresented: %v", status)
	}
	cp, err := store.LoadHistoryCheckpoint()
	if err != nil || !cp.Equal(checkpoint) {
		t.Fatalf("advanced partial checkpoint %v %v", cp, err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatal("upstream raw error leaked")
	}
	restarted := NewCollector(c.client, store, 3600, zap.NewNop())
	after := collectionView(t, restarted)
	if after["last_attempt_at"] != nil || after["last_success_at"] != nil || after["snapshot_at"] != nil {
		t.Fatal("restart fabricated process observations")
	}
}
func TestAnalysisRejectsConcurrentRun(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" {
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) == 1 {
			close(entered)
			<-release
		}
		fmt.Fprint(w, `{"time_zone":"UTC"}`)
	}))
	defer server.Close()
	manager, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	done := make(chan error, 1)
	go func() { _, err := manager.TriggerAnalysis(context.Background(), 14); done <- err }()
	<-entered
	_, secondErr := manager.TriggerAnalysis(context.Background(), 14)
	close(release)
	if firstErr := <-done; firstErr != nil {
		t.Fatal(firstErr)
	}
	if secondErr == nil || !strings.Contains(secondErr.Error(), "busy") {
		t.Fatalf("concurrent analysis was not rejected: %v", secondErr)
	}
}
func TestCollectionStatusSuccessRestartAndCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/states" {
			fmt.Fprint(w, `[{"entity_id":"light.room","state":"off"}]`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/history/period/") {
			if block.Load() {
				close(entered)
				select {
				case <-r.Context().Done():
					return
				case <-release:
				}
			}
			fmt.Fprint(w, `[]`)
			return
		}
		t.Errorf("unexpected %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()
	directory := t.TempDir()
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: directory}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	if err = m.collector.collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := m.CollectionStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "idle" || status.LastSuccessAt == nil || status.SnapshotAt == nil || status.Checkpoint == nil || status.TotalBatches != 1 || status.CompletedBatches != 1 {
		t.Fatalf("complete run misrepresented: %+v", status)
	}
	clone := m.collector.Status()
	clone.LastSuccessAt.AddDate(1, 0, 0)
	*clone.LastSuccessAt = time.Time{}
	if m.collector.Status().LastSuccessAt.IsZero() {
		t.Fatal("status caller mutated collector state")
	}
	restarted, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: directory}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	restartStatus, err := restarted.CollectionStatus()
	if err != nil {
		t.Fatal(err)
	}
	if restartStatus.LastSuccessAt != nil || restartStatus.LastAttemptAt != nil || restartStatus.SnapshotAt == nil || restartStatus.Checkpoint == nil {
		t.Fatalf("restart conflated persisted and process success: %+v", restartStatus)
	}
	oldCheckpoint := *status.Checkpoint
	block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.collector.collect(ctx) }()
	<-entered
	cancel()
	if err = <-done; err == nil {
		t.Fatal("cancelled collection succeeded")
	}
	close(release)
	cancelled, err := m.CollectionStatus()
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Phase != "failed" || cancelled.ErrorCode == nil || *cancelled.ErrorCode != "cancelled" || cancelled.CompletedBatches != 0 || cancelled.LastSuccessAt == nil || !cancelled.Checkpoint.Equal(oldCheckpoint) {
		t.Fatalf("cancellation lost previous success/progress: %+v", cancelled)
	}
}
