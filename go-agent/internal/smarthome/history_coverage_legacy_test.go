package smarthome

import (
	"testing"
	"time"
)

func TestHistoryCoverageLegacySameWindowMerge(t *testing.T) {
	s, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	if err = s.SaveHistoryWindow(start, end, []HistoryEntry{{EntityID: "light.a", State: "on", Timestamp: start}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveHistoryWindow(start, end, []HistoryEntry{{EntityID: "light.b", State: "off", Timestamp: start}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetHistoryRange("", start, end)
	if err != nil || len(got) != 2 {
		t.Fatalf("same-window batch overwrote prior entity: got=%#v err=%v", got, err)
	}
}
