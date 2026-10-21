package smarthome

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrBackfillBusy     = errors.New("backfill_busy")
	ErrBackfillConflict = errors.New("backfill_conflict")
	ErrBackfillNotFound = errors.New("backfill_not_found")
)

// BackfillRun persists progress independently of live collection. Gaps describe
// uncompleted request windows, never proven recorder retention or missing events.
type BackfillRun struct {
	SchemaVersion    int          `json:"schema_version"`
	ID               string       `json:"id"`
	OwnerID          string       `json:"owner_id"`
	Status           string       `json:"status"`
	RequestedWindow  TimeWindow   `json:"requested_window"`
	EntityIDs        []string     `json:"entity_ids"`
	Cursor           time.Time    `json:"cursor"`
	CompletedBatches int          `json:"completed_batches"`
	Gaps             []TimeWindow `json:"gaps"`
	Retention        string       `json:"retention"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	ErrorCode        string       `json:"error_code,omitempty"`
	LastFailureCode  string       `json:"last_failure_code,omitempty"`
}

func copyBackfillRun(run BackfillRun) BackfillRun {
	run.EntityIDs = append([]string(nil), run.EntityIDs...)
	run.Gaps = append([]TimeWindow(nil), run.Gaps...)
	return run
}

// StartBackfill consumes scope on every path. Zero boundaries select the most
// recent 14 elapsed days. An explicit identical window and owner resumes an
// interrupted run with its original ID and entity snapshot; reconstruction alone
// never schedules work. Bounds are instants, including across DST transitions.
func (m *Manager) StartBackfill(scope JobScope, start, end time.Time) (BackfillRun, error) {
	ctx, finish, err := m.acquireJob(scope)
	if err != nil {
		return BackfillRun{}, err
	}
	accepted := false
	defer func() {
		if !accepted {
			finish()
		}
	}()
	now := time.Now().UTC()
	if end.IsZero() {
		end = now
	}
	if start.IsZero() {
		start = end.Add(-14 * 24 * time.Hour)
	}
	window := TimeWindow{Start: start.UTC(), End: end.UTC()}
	if validateHistoryWindow(window) != nil || end.After(now) || end.Sub(start) > 14*24*time.Hour {
		return BackfillRun{}, errors.New("invalid_backfill_window")
	}
	m.backfillMu.Lock()
	defer m.backfillMu.Unlock()
	if m.backfillActive != "" {
		return BackfillRun{}, ErrBackfillBusy
	}
	var run BackfillRun
	for _, previous := range m.backfillRuns {
		if previous.RequestedWindow.Start.Equal(window.Start) && previous.RequestedWindow.End.Equal(window.End) {
			if previous.OwnerID != scope.OwnerID {
				return BackfillRun{}, ErrBackfillConflict
			}
			run = copyBackfillRun(previous)
			if run.Status == "completed" {
				return run, nil
			}
			break
		}
	}
	if run.ID == "" {
		states, err := m.store.LatestSnapshot()
		if err != nil {
			return BackfillRun{}, errors.New("backfill_snapshot_unavailable")
		}
		var ids []string
		for _, state := range states {
			ids = append(ids, state.EntityID)
		}
		if validateHistoryIDs(ids) != nil {
			return BackfillRun{}, errors.New("backfill_entities_unavailable")
		}
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return BackfillRun{}, errors.New("backfill_id_unavailable")
		}
		run = BackfillRun{SchemaVersion: 1, ID: hex.EncodeToString(raw), OwnerID: scope.OwnerID, RequestedWindow: window, EntityIDs: uniqueEntityIDs(ids), Cursor: window.Start, CreatedAt: now}
	}
	// No reliable recorder-retention API is available in the existing HA client.
	// A successful/empty request cannot replace this explicit capability unknown.
	run.Retention = "unknown"
	run.Status = "queued"
	run.ErrorCode = ""
	run.UpdatedAt = now
	run.Gaps = backfillGaps(run)
	if err := ctx.Err(); err != nil {
		return BackfillRun{}, err
	}
	if err := m.store.saveBackfillRun(run); err != nil {
		return BackfillRun{}, errors.New("backfill_store_failed")
	}
	jobCtx, cancel := context.WithCancel(ctx)
	m.backfillRuns[run.ID] = copyBackfillRun(run)
	m.backfillActive = run.ID
	m.backfillCancel = cancel
	accepted = true
	go func() {
		defer finish()
		defer cancel()
		m.executeBackfill(jobCtx, run)
	}()
	return copyBackfillRun(run), nil
}
func (m *Manager) GetBackfillRun(id string) (BackfillRun, error) {
	m.backfillMu.Lock()
	defer m.backfillMu.Unlock()
	run, ok := m.backfillRuns[id]
	if !ok {
		return BackfillRun{}, ErrBackfillNotFound
	}
	return copyBackfillRun(run), nil
}
func (m *Manager) CancelBackfill(id string) error {
	m.backfillMu.Lock()
	defer m.backfillMu.Unlock()
	if _, ok := m.backfillRuns[id]; !ok {
		return ErrBackfillNotFound
	}
	if m.backfillActive == id {
		m.backfillCancel()
	}
	return nil
}
func backfillGaps(run BackfillRun) []TimeWindow {
	if run.Cursor.Before(run.RequestedWindow.End) {
		return []TimeWindow{{Start: run.Cursor, End: run.RequestedWindow.End}}
	}
	return []TimeWindow{}
}
func backfillWindow(start, end time.Time) TimeWindow {
	next := start.Add(time.Hour)
	if next.After(end) {
		next = end
	}
	return TimeWindow{Start: start, End: next}
}
func (m *Manager) persistBackfill(run *BackfillRun) error {
	run.UpdatedAt = time.Now().UTC()
	run.Gaps = backfillGaps(*run)
	m.backfillMu.Lock()
	defer m.backfillMu.Unlock()
	// Preserve a terminal in-memory failure even if the disk has become unwritable.
	m.backfillRuns[run.ID] = copyBackfillRun(*run)
	if err := m.store.saveBackfillRun(*run); err != nil {
		return errors.New("backfill_store_failed")
	}
	return nil
}
func (m *Manager) executeBackfill(ctx context.Context, run BackfillRun) {
	run.Status = "running"
	err := m.persistBackfill(&run)
	if err == nil {
		err = m.backfillWindows(ctx, &run)
	}
	run.Status = "completed"
	run.ErrorCode = ""
	if err != nil {
		run.Status = "failed"
		run.ErrorCode = err.Error()
		if ctx.Err() != nil {
			run.Status = "cancelled"
			run.ErrorCode = "cancelled"
		}
		run.LastFailureCode = run.ErrorCode
	}
	// Publish the terminal status and release the active slot together so a
	// caller observing completion can immediately acknowledge/resume that run.
	run.UpdatedAt = time.Now().UTC()
	run.Gaps = backfillGaps(run)
	m.backfillMu.Lock()
	if err := m.store.saveBackfillRun(run); err != nil {
		run.Status = "failed"
		run.ErrorCode = "backfill_store_failed"
		run.LastFailureCode = run.ErrorCode
	}
	m.backfillRuns[run.ID] = copyBackfillRun(run)
	m.backfillActive = ""
	m.backfillCancel = nil
	m.backfillMu.Unlock()
}
func (m *Manager) backfillWindows(ctx context.Context, run *BackfillRun) error {
	// Reconstruct counts from durable receipts, including the crash boundary
	// between saving a batch and updating its run. Never trust a stale counter.
	completed := 0
	for start := run.RequestedWindow.Start; start.Before(run.Cursor); {
		if err := ctx.Err(); err != nil {
			return errors.New("cancelled")
		}
		window := backfillWindow(start, run.RequestedWindow.End)
		_, count, err := m.store.successfulHistoryEntities(window, run.EntityIDs, run.ID)
		if err != nil {
			return errors.New("backfill_store_failed")
		}
		completed += count
		start = window.End
	}
	run.CompletedBatches = completed
	for run.Cursor.Before(run.RequestedWindow.End) {
		release, err := m.collector.acquireCollection(ctx, false)
		if err != nil {
			return errors.New("cancelled")
		}
		err = m.backfillOneWindow(ctx, run, completed)
		release()
		if err != nil {
			return err
		}
		completed = run.CompletedBatches
	}
	return nil
}
func (m *Manager) backfillOneWindow(ctx context.Context, run *BackfillRun, completed int) error {
	window := backfillWindow(run.Cursor, run.RequestedWindow.End)
	successful, count, err := m.store.successfulHistoryEntities(window, run.EntityIDs, run.ID)
	if err != nil {
		return errors.New("backfill_store_failed")
	}
	run.CompletedBatches = completed + count
	var remaining []string
	for _, id := range run.EntityIDs {
		if !successful[id] {
			remaining = append(remaining, id)
		}
	}
	if len(remaining) > 0 {
		batches, err := m.collector.historyBatches(remaining, window)
		if err != nil {
			return errors.New("backfill_request_invalid")
		}
		for _, ids := range batches {
			entries, receipt, fetchErr := m.collector.fetchHistoryWindow(ctx, ids, window)
			receipt.Source = "backfill"
			receipt.AcquisitionScope = run.ID
			if err := m.store.SaveHistoryBatch(receipt, entries); err != nil {
				return errors.New("backfill_store_failed")
			}
			if fetchErr != nil {
				return errors.New(receipt.ErrorCode)
			}
			run.CompletedBatches++
			if err := m.persistBackfill(run); err != nil {
				return err
			}
		}
	}
	if ctx.Err() != nil {
		return errors.New("cancelled")
	}
	if err := m.store.CommitScopedHistoryWindow(run.ID, window.Start, window.End); err != nil {
		return errors.New("backfill_store_failed")
	}
	run.Cursor = window.End
	return m.persistBackfill(run)
}
