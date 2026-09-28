package smarthome

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrSuggestionNotFound = errors.New("suggestion not found")
	ErrSuggestionConflict = errors.New("suggestion state conflict")
)

// DeviceStore persists device state snapshots and rule suggestions to disk.
// Data is stored as JSON files under the agent vault's smart-home directory.
type DeviceStore struct {
	basePath string
	mu       sync.RWMutex
}

// deviceSnapshotFile is the on-disk format for stored device states.
type deviceSnapshotFile struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Entries   []EntityState `json:"entries"`
}

// rulesFile is the on-disk format for stored rule suggestions.
type rulesFile struct {
	UpdatedAt   time.Time        `json:"updated_at"`
	Suggestions []RuleSuggestion `json:"suggestions"`
}

// NewDeviceStore creates a new store rooted at basePath.
// It creates required subdirectories (snapshots/, rules/).
func NewDeviceStore(basePath string) (*DeviceStore, error) {
	for _, sub := range []string{"snapshots", "rules", "reports"} {
		if err := os.MkdirAll(filepath.Join(basePath, sub), 0755); err != nil {
			return nil, fmt.Errorf("smarthome store: create %s: %w", sub, err)
		}
	}
	return &DeviceStore{basePath: basePath}, nil
}

// SaveSnapshot persists a batch of entity states.
func (s *DeviceStore) SaveSnapshot(states []EntityState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := deviceSnapshotFile{
		UpdatedAt: time.Now(),
		Entries:   states,
	}

	path := filepath.Join(s.basePath, "snapshots", "latest.json")
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("smarthome store: marshal snapshot: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LatestSnapshot returns the most recent device state snapshot.
func (s *DeviceStore) LatestSnapshot() ([]EntityState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.basePath, "snapshots", "latest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("smarthome store: read snapshot: %w", err)
	}

	var snapshot deviceSnapshotFile
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("smarthome store: unmarshal snapshot: %w", err)
	}
	return snapshot.Entries, nil
}

// SaveHistory persists a batch of historical entries. Each call appends a
// timestamped file to the snapshots directory for later analysis.
func (s *DeviceStore) SaveHistory(entries []HistoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts := time.Now().UTC().Format("2006-01-02_150405")
	path := filepath.Join(s.basePath, "snapshots", ts+".json")
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("smarthome store: marshal history: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// GetHistoryRange returns all historical entries between start and end.
// It reads all stored snapshot files and filters by timestamp.
func (s *DeviceStore) GetHistoryRange(entityID string, start, end time.Time) ([]HistoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapDir := filepath.Join(s.basePath, "snapshots")
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		return nil, fmt.Errorf("smarthome store: read snapshots dir: %w", err)
	}

	var all []HistoryEntry
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "latest.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(snapDir, entry.Name()))
		if err != nil {
			continue
		}

		var batch []HistoryEntry
		if err := json.Unmarshal(data, &batch); err != nil {
			continue
		}

		for _, h := range batch {
			if entityID != "" && h.EntityID != entityID {
				continue
			}
			if !start.IsZero() && h.Timestamp.Before(start) {
				continue
			}
			if !end.IsZero() && h.Timestamp.After(end) {
				continue
			}
			all = append(all, h)
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})

	return all, nil
}

// SaveSuggestions persists rule suggestions.
func (s *DeviceStore) SaveSuggestions(suggestions []RuleSuggestion) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.readSuggestions()
	if err != nil {
		return err
	}
	byID := make(map[string]RuleSuggestion, len(existing))
	for _, suggestion := range existing {
		byID[suggestion.ID] = suggestion
	}
	for _, suggestion := range suggestions {
		if suggestion.ID == "" {
			suggestion.ID = stableSuggestionID(suggestion)
		}
		if !configKeyPattern.MatchString(suggestion.ID) {
			return fmt.Errorf("%w: invalid suggestion ID", ErrUnsupportedRule)
		}
		if previous, found := byID[suggestion.ID]; found {
			if stableSuggestionID(previous) != stableSuggestionID(suggestion) {
				return fmt.Errorf("%w: existing suggestion is immutable", ErrSuggestionConflict)
			}
			continue
		}
		// Publishing observations cannot confirm or ignore an action.
		suggestion.Status = "pending"
		suggestion.LastError = ""
		suggestion.HAAutomationID = ""
		if suggestion.CreatedAt.IsZero() {
			suggestion.CreatedAt = time.Now()
		}
		existing = append(existing, suggestion)
		byID[suggestion.ID] = suggestion
	}
	return s.writeSuggestions(existing)
}

// GetSuggestions returns all stored rule suggestions.
func (s *DeviceStore) GetSuggestions() ([]RuleSuggestion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readSuggestions()
}

func (s *DeviceStore) readSuggestions() ([]RuleSuggestion, error) {
	path := filepath.Join(s.basePath, "rules", "suggestions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("smarthome store: read suggestions: %w", err)
	}

	var rf rulesFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("smarthome store: unmarshal suggestions: %w", err)
	}
	return rf.Suggestions, nil
}

// UpdateSuggestionStatus updates the status of a specific suggestion.
func (s *DeviceStore) UpdateSuggestionStatus(id, status string) error {
	_, err := s.changeSuggestion(id, status, "", "")
	return err
}

func (s *DeviceStore) changeSuggestion(id, status, haID, lastError string) (*RuleSuggestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	suggestions, err := s.readSuggestions()
	if err != nil {
		return nil, err
	}
	for i := range suggestions {
		if suggestions[i].ID != id {
			continue
		}
		previous := suggestions[i].Status
		if !validSuggestionTransition(previous, status) {
			return nil, fmt.Errorf("%w: %s to %s", ErrSuggestionConflict, previous, status)
		}
		suggestions[i].Status = status
		suggestions[i].LastError = lastError
		if haID != "" {
			suggestions[i].HAAutomationID = haID
		}
		if err := s.writeSuggestions(suggestions); err != nil {
			return nil, err
		}
		return &suggestions[i], nil
	}
	return nil, ErrSuggestionNotFound
}

func validSuggestionTransition(from, to string) bool {
	if from == to {
		return from == "pending" || from == "applying" || from == "failed" || from == "confirmed" || from == "ignored"
	}
	switch from {
	case "pending":
		return to == "applying" || to == "ignored"
	case "failed":
		return to == "applying"
	case "applying":
		return to == "confirmed" || to == "failed"
	}
	return false
}

func (s *DeviceStore) writeSuggestions(suggestions []RuleSuggestion) error {
	data, err := json.MarshalIndent(rulesFile{UpdatedAt: time.Now(), Suggestions: suggestions}, "", "  ")
	if err != nil {
		return fmt.Errorf("smarthome store: marshal suggestions: %w", err)
	}
	return atomicWrite(filepath.Join(s.basePath, "rules", "suggestions.json"), data)
}

// atomicWrite prevents interrupted writes from exposing truncated state files.
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".smarthome-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// SaveReport persists a device report.
func (s *DeviceStore) SaveReport(report DeviceReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts := report.PeriodEnd.Format("2006-01-02")
	path := filepath.Join(s.basePath, "reports", ts+".json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("smarthome store: marshal report: %w", err)
	}
	return atomicWrite(path, data)
}
