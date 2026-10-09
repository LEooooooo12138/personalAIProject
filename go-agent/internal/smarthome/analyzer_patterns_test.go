package smarthome

import (
	"encoding/json"
	"go.uber.org/zap"
	"reflect"
	"testing"
	"time"
)

func patternHistory(start time.Time, days int) []HistoryEntry {
	var entries []HistoryEntry
	for day := 0; day < days; day++ {
		at := start.AddDate(0, 0, day)
		entries = append(entries, HistoryEntry{EntityID: "light.a", State: "off", Timestamp: at, ObservationKind: "initial"}, HistoryEntry{EntityID: "light.a", State: "on", Timestamp: at.Add(time.Hour), ObservationKind: "change"})
	}
	return entries
}
func TestPatternsUseHomeTimezone(t *testing.T) {
	store, _ := NewDeviceStore(t.TempDir())
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	store.SaveHistory(patternHistory(start, 14))
	a := NewAnalyzer(store, zap.NewNop())
	api, ok := any(a).(interface {
		AnalyzeInLocation(time.Time, int, *time.Location) (*DeviceReport, error)
	})
	if !ok {
		t.Fatal("missing explicit home timezone analysis")
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	report, err := api.AnalyzeInLocation(time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), 14, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Patterns) != 1 || report.Patterns[0].TimeOfDay != "18:00" || report.Patterns[0].Confidence != 1 {
		t.Fatalf("patterns=%#v", report.Patterns)
	}
	if report.PeriodEnd.In(loc).Hour() != 0 {
		t.Fatal("period includes partial today")
	}
	if _, err = api.AnalyzeInLocation(start, 14, nil); err == nil {
		t.Fatal("nil timezone accepted")
	}
}
func TestInitialStatesDoNotCreateTimePatterns(t *testing.T) {
	store, _ := NewDeviceStore(t.TempDir())
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var entries []HistoryEntry
	for h := 0; h < 14*24; h++ {
		entries = append(entries, HistoryEntry{EntityID: "light.a", State: "on", Timestamp: start.Add(time.Duration(h) * time.Hour), ObservationKind: "initial"})
	}
	store.SaveHistory(entries)
	r, err := NewAnalyzer(store, zap.NewNop()).Analyze(start.AddDate(0, 0, 14), 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Patterns) != 0 {
		t.Fatalf("initial snapshots invented %d patterns", len(r.Patterns))
	}
	if len(r.Devices) != 1 || r.Devices[0].OnCount != 0 || r.Devices[0].TotalOnTime != 336 {
		t.Fatalf("summary=%#v", r.Devices)
	}
}
func TestCorrelationsAreBidirectionalAndDeterministic(t *testing.T) {
	a := NewAnalyzer(nil, zap.NewNop())
	start := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	by := map[string][]HistoryEntry{}
	for day := 0; day < 14; day++ {
		for n, id := range []string{"light.a", "switch.b"} {
			at := start.AddDate(0, 0, day).Add(time.Duration(n) * time.Minute)
			for k := 0; k < 4; k++ {
				by[id] = append(by[id], HistoryEntry{EntityID: id, State: "off", Timestamp: at.Add(time.Duration(k)*2*time.Minute - time.Second), ObservationKind: "initial"}, HistoryEntry{EntityID: id, State: "on", Timestamp: at.Add(time.Duration(k) * 2 * time.Minute), ObservationKind: "change"})
			}
		}
	}
	want := a.detectCorrelations(by, 14)
	if len(want) != 2 {
		t.Fatalf("wanted both directions: %#v", want)
	}
	for i := 0; i < 30; i++ {
		got := a.detectCorrelations(map[string][]HistoryEntry{"switch.b": by["switch.b"], "light.a": by["light.a"]}, 14)
		if !reflect.DeepEqual(got, want) {
			x, _ := json.Marshal(got)
			t.Fatalf("unstable: %s", x)
		}
	}
}

func TestTransitionsRejectUnverifiedAndConflictingEvidence(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		entries []HistoryEntry
		want    int
	}{
		{"first on", []HistoryEntry{{State: "on", ObservationKind: "change"}}, 0},
		{"on update", []HistoryEntry{{State: "on", ObservationKind: "initial"}, {State: "on", ObservationKind: "change"}}, 0},
		{"unknown", []HistoryEntry{{State: "off", ObservationKind: "initial"}, {State: "unknown", ObservationKind: "change"}, {State: "on", ObservationKind: "change"}}, 0},
		{"legacy", []HistoryEntry{{State: "off"}, {State: "on", ObservationKind: "change"}}, 0},
		{"verified", []HistoryEntry{{State: "off", ObservationKind: "initial"}, {State: "on", ObservationKind: "change"}}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.entries {
				tc.entries[i].EntityID = "light.a"
				tc.entries[i].Timestamp = at.Add(time.Duration(i) * time.Second)
			}
			if got := stateTransitions(tc.entries); len(got) != tc.want {
				t.Fatalf("transitions=%#v", got)
			}
		})
	}
	conflict := []HistoryEntry{{EntityID: "light.a", State: "off", Timestamp: at, ObservationKind: "initial"}, {EntityID: "light.a", State: "off", Timestamp: at.Add(time.Second), ObservationKind: "change"}, {EntityID: "light.a", State: "on", Timestamp: at.Add(time.Second), ObservationKind: "change"}, {EntityID: "light.a", State: "off", Timestamp: at.Add(2 * time.Second), ObservationKind: "change"}}
	if got := stateTransitions(conflict); len(got) != 0 {
		t.Fatalf("conflict invented ordering: %#v", got)
	}
}
func TestCorrelationsNeedSevenDistinctDays(t *testing.T) {
	a := NewAnalyzer(nil, zap.NewNop())
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	by := map[string][]HistoryEntry{}
	for n := 0; n < 20; n++ {
		for k, id := range []string{"light.a", "switch.b"} {
			when := at.Add(time.Duration(n*10+k) * time.Minute)
			by[id] = append(by[id], HistoryEntry{EntityID: id, State: "off", Timestamp: when, ObservationKind: "initial"}, HistoryEntry{EntityID: id, State: "on", Timestamp: when.Add(time.Second), ObservationKind: "change"})
		}
	}
	if patterns := a.detectCorrelations(by, 14); len(patterns) != 0 {
		t.Fatalf("same-day repetitions accepted: %#v", patterns)
	}
}
func TestNextAnalysisUsesLocalCalendarAcrossDST(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	for _, tc := range []struct{ now, want string }{{"2026-03-07T18:00:00-05:00", "2026-03-08T18:00:00-04:00"}, {"2026-10-31T18:00:00-04:00", "2026-11-01T18:00:00-05:00"}} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		got := nextAnalysisTime(now, 18, loc)
		if got.Format(time.RFC3339) != tc.want {
			t.Fatalf("next=%s want=%s", got, tc.want)
		}
	}
}
