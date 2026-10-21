package smarthome

import (
	"crypto/rand"
	"encoding/hex"
	"reflect"
	"time"
)

// LocalEventReference identifies a local observation, never remote trace variables.
type LocalEventReference struct {
	EntityID string    `json:"entity_id"`
	At       time.Time `json:"at"`
}
type SuggestionEvidence struct {
	SchemaVersion    int                   `json:"schema_version"`
	ID               string                `json:"id"`
	RunID            string                `json:"run_id"`
	Window           TimeWindow            `json:"window"`
	AlgorithmVersion string                `json:"algorithm_version"`
	SampleSize       int                   `json:"sample_size"`
	PeriodDays       int                   `json:"period_days"`
	HitDays          *int                  `json:"hit_days,omitempty"`
	EligibleDays     *int                  `json:"eligible_days,omitempty"`
	Denominator      int                   `json:"denominator"`
	DenominatorType  string                `json:"denominator_type"`
	ExcludedCounts   map[string]int        `json:"excluded_counts"`
	EventRefs        []LocalEventReference `json:"event_refs"`
	CreatedAt        time.Time             `json:"created_at"`
}

func randomAnalysisID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}

// AppendSuggestionEvidence changes observations only. A run contributes at most
// one immutable record per semantic suggestion; replay is idempotent.
func (s *DeviceStore) AppendSuggestionEvidence(id string, evidence SuggestionEvidence) error {
	if validateHistoryScope(evidence.RunID) != nil || evidence.RunID == "" || validateHistoryWindow(evidence.Window) != nil || evidence.AlgorithmVersion == "" || evidence.SampleSize < 0 || evidence.PeriodDays < 1 || evidence.Denominator < 1 {
		return ErrSuggestionConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	suggestions, err := s.readSuggestions()
	if err != nil {
		return err
	}
	for i := range suggestions {
		if suggestions[i].ID != id {
			continue
		}
		for _, old := range suggestions[i].Evidence {
			if old.RunID == evidence.RunID {
				a, b := old, evidence
				a.ID = ""
				b.ID = ""
				a.CreatedAt = time.Time{}
				b.CreatedAt = time.Time{}
				a.SchemaVersion = 1
				b.SchemaVersion = 1
				if !reflect.DeepEqual(a, b) {
					return ErrSuggestionConflict
				}
				return nil
			}
		}
		evidence.ID, err = randomAnalysisID()
		if err != nil {
			return err
		}
		evidence.SchemaVersion = 1
		evidence.CreatedAt = time.Now().UTC()
		suggestions[i].Evidence = append(suggestions[i].Evidence, evidence)
		return s.writeSuggestions(suggestions)
	}
	return ErrSuggestionNotFound
}
func (m *Manager) AppendSuggestionEvidence(id string, e SuggestionEvidence) error {
	return m.store.AppendSuggestionEvidence(id, e)
}
