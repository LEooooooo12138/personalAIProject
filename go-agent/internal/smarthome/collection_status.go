package smarthome

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CollectionStatus separates current-process observations from persisted progress.
type CollectionStatus struct {
	Phase            string     `json:"phase"`
	LastAttemptAt    *time.Time `json:"last_attempt_at"`
	LastSuccessAt    *time.Time `json:"last_success_at"`
	LastFailureAt    *time.Time `json:"last_failure_at"`
	ErrorCode        *string    `json:"error_code"`
	SnapshotAt       *time.Time `json:"snapshot_at"`
	WindowStart      *time.Time `json:"window_start"`
	WindowEnd        *time.Time `json:"window_end"`
	CompletedBatches int        `json:"completed_batches"`
	TotalBatches     int        `json:"total_batches"`
	Checkpoint       *time.Time `json:"checkpoint"`
}

func utcNow() *time.Time { now := time.Now().UTC(); return &now }
func (c *Collector) observe(update func(*CollectionStatus)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	update(&c.status)
}
func (c *Collector) Status() CollectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.status
	if out.Phase == "" {
		out.Phase = "idle"
	}
	clone := func(value *time.Time) *time.Time {
		if value == nil {
			return nil
		}
		v := *value
		return &v
	}
	out.LastAttemptAt = clone(out.LastAttemptAt)
	out.LastSuccessAt = clone(out.LastSuccessAt)
	out.LastFailureAt = clone(out.LastFailureAt)
	out.SnapshotAt = clone(out.SnapshotAt)
	out.WindowStart = clone(out.WindowStart)
	out.WindowEnd = clone(out.WindowEnd)
	if out.ErrorCode != nil {
		code := *out.ErrorCode
		out.ErrorCode = &code
	}
	return out
}
func (m *Manager) CollectionStatus() (CollectionStatus, error) {
	status := m.collector.Status()
	checkpoint, err := m.store.LoadHistoryCheckpoint()
	if err != nil {
		return CollectionStatus{}, err
	}
	if !checkpoint.IsZero() {
		status.Checkpoint = &checkpoint
	}
	// A persisted snapshot is an actual success timestamp, unlike a process run.
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(m.store.basePath, "snapshots", "latest.json"))
	if os.IsNotExist(err) {
		return status, nil
	}
	if err != nil {
		return CollectionStatus{}, err
	}
	var snapshot deviceSnapshotFile
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return CollectionStatus{}, err
	}
	if !snapshot.UpdatedAt.IsZero() {
		timestamp := snapshot.UpdatedAt.UTC()
		status.SnapshotAt = &timestamp
	}
	return status, nil
}
