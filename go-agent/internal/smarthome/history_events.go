package smarthome

import (
	"time"
	_ "time/tzdata"
)

type StateTransition struct {
	EntityID, From, To string
	At                 time.Time
}

// Each timestamp is an unordered observation set. Conflicts break continuity.
func stateTransitions(entries []HistoryEntry) []StateTransition {
	ordered := unambiguousHistory(entries)
	previous := map[string]string{}
	var result []StateTransition
	for i := 0; i < len(ordered); {
		j := i + 1
		for j < len(ordered) && ordered[j].EntityID == ordered[i].EntityID && ordered[j].Timestamp.Equal(ordered[i].Timestamp) {
			j++
		}
		e := ordered[i]
		if j-i > 1 || e.ObservationKind == "" || e.State == "unknown" || e.State == "unavailable" || e.State == "" {
			delete(previous, e.EntityID)
			i = j
			continue
		}
		from, known := previous[e.EntityID]
		if known && e.ObservationKind == "change" && from != e.State {
			result = append(result, StateTransition{EntityID: e.EntityID, From: from, To: e.State, At: e.Timestamp})
		}
		previous[e.EntityID] = e.State
		i = j
	}
	return result
}
func turnOnEntries(entries []HistoryEntry) []HistoryEntry {
	var result []HistoryEntry
	for _, e := range stateTransitions(entries) {
		if e.From == "off" && e.To == "on" {
			result = append(result, HistoryEntry{EntityID: e.EntityID, State: e.To, Timestamp: e.At, ObservationKind: "change"})
		}
	}
	return result
}

// A conflicting instant establishes no state; both durations and event analysis
// must wait for another unambiguous observation instead of guessing input order.
func unambiguousHistory(entries []HistoryEntry) []HistoryEntry {
	ordered := deduplicateHistory(entries)
	var result []HistoryEntry
	for i := 0; i < len(ordered); {
		j := i + 1
		for j < len(ordered) && ordered[j].EntityID == ordered[i].EntityID && ordered[j].Timestamp.Equal(ordered[i].Timestamp) {
			j++
		}
		e := ordered[i]
		if j-i > 1 {
			e.State = "unknown"
			e.ObservationKind = ""
		}
		result = append(result, e)
		i = j
	}
	return result
}
