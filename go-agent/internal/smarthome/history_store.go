package smarthome

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// deduplicateHistory retains genuine changes when an overlapping initial agrees.
func deduplicateHistory(entries []HistoryEntry) []HistoryEntry {
	byKey := map[string]HistoryEntry{}
	for _, e := range entries {
		key := e.EntityID + "\x00" + e.Timestamp.UTC().Format(time.RFC3339Nano) + "\x00" + e.State
		old, ok := byKey[key]
		if !ok || e.ObservationKind == "change" || old.ObservationKind == "" {
			byKey[key] = e
		}
	}
	result := make([]HistoryEntry, 0, len(byKey))
	for _, e := range byKey {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].Timestamp.Equal(result[j].Timestamp) {
			return result[i].Timestamp.Before(result[j].Timestamp)
		}
		if result[i].EntityID != result[j].EntityID {
			return result[i].EntityID < result[j].EntityID
		}
		return result[i].State < result[j].State
	})
	return result
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
	if !end.After(start) {
		return fmt.Errorf("invalid history window")
	}
	data, err := json.Marshal(deduplicateHistory(entries))
	if err != nil {
		return err
	}
	name := fmt.Sprintf("window-%d-%d.json", start.UnixNano(), end.UnixNano())
	if err = atomicWrite(filepath.Join(s.basePath, "snapshots", name), data); err != nil {
		return err
	}
	data, err = json.Marshal(end)
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
