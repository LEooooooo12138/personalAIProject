package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// deduplicateHistory retains genuine changes when an overlapping initial agrees.
func deduplicateHistory(entries []HistoryEntry) []HistoryEntry {
	r, _ := deduplicateHistoryContext(context.Background(), entries)
	return r
}

// Sort bounded chunks, then merge with cancellation checks; one giant history
// file must not delay revocation through an uninterruptible whole-input sort.
func deduplicateHistoryContext(ctx context.Context, entries []HistoryEntry) ([]HistoryEntry, error) {
	byKey := map[string]HistoryEntry{}
	for i, e := range entries {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		key := e.EntityID + "\x00" + e.Timestamp.UTC().Format(time.RFC3339Nano) + "\x00" + e.State
		old, ok := byKey[key]
		if !ok || e.ObservationKind == "change" || old.ObservationKind == "" {
			byKey[key] = e
		}
	}
	result := make([]HistoryEntry, 0, len(byKey))
	for _, e := range byKey {
		if len(result)%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		result = append(result, e)
	}
	less := func(a, b HistoryEntry) bool {
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if a.EntityID != b.EntityID {
			return a.EntityID < b.EntityID
		}
		return a.State < b.State
	}
	const chunk = 512
	for start := 0; start < len(result); start += chunk {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part := result[start:min(start+chunk, len(result))]
		sort.Slice(part, func(i, j int) bool { return less(part[i], part[j]) })
	}
	buf := make([]HistoryEntry, len(result))
	for width := chunk; width < len(result); width *= 2 {
		for start := 0; start < len(result); start += 2 * width {
			mid, end := min(start+width, len(result)), min(start+2*width, len(result))
			i, j := start, mid
			for k := start; k < end; k++ {
				if k%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				if i < mid && (j >= end || !less(result[j], result[i])) {
					buf[k] = result[i]
					i++
				} else {
					buf[k] = result[j]
					j++
				}
			}
		}
		result, buf = buf, result
	}
	return result, ctx.Err()
}

func (s *DeviceStore) LoadHistoryCheckpoint() (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.basePath, "history-checkpoint.json"))
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var end time.Time
	err = json.Unmarshal(data, &end)
	return end, err
}
func (s *DeviceStore) SaveHistoryWindow(start, end time.Time, entries []HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commitHistoryWindow(TimeWindow{Start: start.UTC(), End: end.UTC()}, entries); err != nil {
		return err
	}
	data, err := json.Marshal(end.UTC())
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.basePath, "history-checkpoint.json"), data)
}

type historyWindow struct{ Start, End time.Time }

func (s *DeviceStore) pendingHistoryWindow() (historyWindow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.basePath, "history-pending.json"))
	if os.IsNotExist(err) {
		return historyWindow{}, nil
	}
	if err != nil {
		return historyWindow{}, err
	}
	var window historyWindow
	err = json.Unmarshal(data, &window)
	if err == nil && !window.End.After(window.Start) {
		err = fmt.Errorf("invalid pending history window")
	}
	return window, err
}
func (s *DeviceStore) savePendingHistoryWindow(start, end time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(historyWindow{start, end})
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.basePath, "history-pending.json"), data)
}

// successfulHistoryEntities reads durable batch receipts from this exact window.
// Entries and receipts are staged atomically, so an interrupted window can skip
// successful requests and publish only once its remaining batches have succeeded.
func (s *DeviceStore) successfulHistoryEntities(window TimeWindow, ids []string, scope string) (map[string]bool, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, err := s.readScopedHistoryWindow(window, scope)
	if err != nil {
		return nil, 0, err
	}
	requested := map[string]bool{}
	for _, id := range ids {
		requested[id] = true
	}
	successful := map[string]bool{}
	batches := 0
	for _, receipt := range f.Receipts {
		relevant := false
		for _, id := range receipt.SuccessfulEntityIDs {
			if requested[id] {
				successful[id] = true
				relevant = true
			}
		}
		if relevant && receipt.Source == "backfill" && receipt.ErrorCode == "" {
			batches++
		}
	}
	return successful, batches, nil
}
func (s *DeviceStore) saveBackfillRun(run BackfillRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.basePath, "backfill")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, run.ID+".json"), data)
}
func (s *DeviceStore) loadBackfillRuns() (map[string]BackfillRun, error) {
	runs := map[string]BackfillRun{}
	dir := filepath.Join(s.basePath, "backfill")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return runs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		var run BackfillRun
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, fmt.Errorf("invalid backfill record")
		}
		if err := validateBackfillRun(run, file.Name()); err != nil {
			return nil, err
		}
		if run.Status == "queued" || run.Status == "running" {
			run.Status = "cancelled"
			run.ErrorCode = "interrupted"
			run.LastFailureCode = "interrupted"
			run.UpdatedAt = time.Now().UTC()
			if err := s.saveBackfillRun(run); err != nil {
				return nil, err
			}
		}
		runs[run.ID] = run
	}
	return runs, nil
}
func validateBackfillRun(run BackfillRun, filename string) error {
	invalid := func() error { return fmt.Errorf("invalid backfill record") }
	if len(run.ID) != 32 || filename != run.ID+".json" {
		return invalid()
	}
	for _, char := range run.ID {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return invalid()
		}
	}
	if run.SchemaVersion != 1 || run.OwnerID == "" || run.Retention != "unknown" || run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() || run.CompletedBatches < 0 {
		return invalid()
	}
	if validateHistoryWindow(run.RequestedWindow) != nil || run.RequestedWindow.End.Sub(run.RequestedWindow.Start) > 14*24*time.Hour || validateHistoryIDs(run.EntityIDs) != nil {
		return invalid()
	}
	if run.Cursor.Before(run.RequestedWindow.Start) || run.Cursor.After(run.RequestedWindow.End) {
		return invalid()
	}
	if !run.Cursor.Equal(run.RequestedWindow.End) && run.Cursor.Sub(run.RequestedWindow.Start)%time.Hour != 0 {
		return invalid()
	}
	switch run.Status {
	case "queued", "running", "failed", "cancelled":
	case "completed":
		if !run.Cursor.Equal(run.RequestedWindow.End) {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}
