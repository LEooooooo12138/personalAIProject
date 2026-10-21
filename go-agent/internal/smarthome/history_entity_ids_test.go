package smarthome

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestHistoryReadsAcceptHANumericObjectID(t *testing.T) {
	const entity = "sensor.3d_printer_power"
	now := time.Now().UTC().Truncate(time.Second)
	entry := HistoryEntry{EntityID: entity, State: "3", Timestamp: now.Add(-30 * time.Minute)}
	store := coverageStore(t)
	// Exercise an actual persisted legacy array, not a copy of the validator.
	raw, err := json.Marshal([]HistoryEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(store.basePath, "snapshots", "legacy-numeric.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	read, err := store.ReadHistory(context.Background(), HistoryQuery{EntityIDs: []string{entity}, Window: TimeWindow{Start: now.Add(-time.Hour), End: now}})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Entries) != 1 || read.Coverage.CorruptFiles != 0 || read.Coverage.SuccessfulUnionDuration != 0 {
		t.Fatalf("legacy history lost or coverage fabricated: entries=%d corrupt=%d success=%s", len(read.Entries), read.Coverage.CorruptFiles, read.Coverage.SuccessfulUnionDuration)
	}
	ids, corrupt, err := store.analysisEntityIDs(context.Background(), TimeWindow{Start: now.Add(-time.Hour), End: now})
	if err != nil || corrupt != 0 || len(ids) != 1 || ids[0] != entity {
		t.Fatalf("analysis discovery failed: ids=%v corrupt=%d err=%v", ids, corrupt, err)
	}
	var historyCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("read-only collection made %s", r.Method)
			http.Error(w, "write forbidden", 405)
			return
		}
		if r.URL.Path == "/api/states" {
			json.NewEncoder(w).Encode([]EntityState{{EntityID: entity, State: "3"}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/history/period/") {
			if r.URL.Query().Get("filter_entity_id") != entity {
				t.Error("history filter changed")
			}
			historyCalls++
			json.NewEncoder(w).Encode([][]HistoryEntry{{entry}})
			return
		}
		t.Errorf("unexpected route %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := NewHomeAssistantClient(server.URL, "fixture", time.Second)
	defer client.Close()
	collector := NewCollector(client, store, 3600, zap.NewNop())
	if err = collector.collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if historyCalls != 1 || collector.Status().Phase != "idle" || collector.Status().LastSuccessAt == nil {
		t.Fatalf("collection did not complete: calls=%d status=%+v", historyCalls, collector.Status())
	}
	checkpoint, err := store.LoadHistoryCheckpoint()
	if err != nil || checkpoint.IsZero() {
		t.Fatal("successful collection did not persist checkpoint")
	}
	// History acceptance must not expand the independent device-write contract.
	if entityIDPattern.MatchString(entity) {
		t.Fatal("device-write entity validator was broadened")
	}
}

func TestHistoryIDsMatchHARuntimeSlugBoundary(t *testing.T) {
	for _, id := range []string{"sensor.3d_printer_power", "sensor.123", "9domain.value", "sensor.٣d_printer", "light.kitchen"} {
		if err := validateHistoryIDs([]string{id}); err != nil {
			t.Errorf("HA-valid read ID %q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"", "sensor.", ".value", "Sensor.value", "sensor.UPPER", "sensor.a/b", "sensor.a,b", "sensor.a?b", "sensor.a%b", "sensor.a\nb", "sensor._start", "sensor.end_", "sensor.double__underscore", "sensor.设备", "sensor.Ⅲvalue"} {
		if err := validateHistoryIDs([]string{id}); err == nil {
			t.Errorf("invalid history ID %q accepted", id)
		}
	}
}
