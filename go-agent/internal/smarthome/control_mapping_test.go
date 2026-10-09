package smarthome

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type mappingTestHA struct {
	reads  []string
	writes int
}

func (h *mappingTestHA) GetState(_ context.Context, id string) (*EntityState, error) {
	h.reads = append(h.reads, id)
	return &EntityState{EntityID: id, State: "off"}, nil
}
func (h *mappingTestHA) CallService(context.Context, string, string, map[string]interface{}) error {
	h.writes++
	return errors.New("unexpected control write")
}

func mappedStudySwitch() ControlTarget {
	return ControlTarget{EntityID: "switch.study_channel_1", Name: "书房灯", AreaName: "书房", Aliases: []string{"书房的灯", "书房进门开关1"}, AllowedActions: []string{"turn_on", "turn_off"}, LoadLocationVerified: true}
}

func mappingFixture(t *testing.T, entries []QueryTarget, targets []ControlTarget, freshness string) (*ControlService, *controlTestCatalog, *mappingTestHA) {
	t.Helper()
	snap := CatalogSnapshot{Meta: CatalogMeta{Freshness: freshness, Connection: "connected"}, areas: map[string]AreaRef{}, devices: map[string]map[string]DeviceView{}}
	for _, entry := range entries {
		snap.areas[entry.AreaName] = AreaRef{ID: entry.AreaName, Name: entry.AreaName}
		if snap.devices[entry.AreaName] == nil {
			snap.devices[entry.AreaName] = map[string]DeviceView{}
		}
		snap.devices[entry.AreaName][entry.EntityID] = DeviceView{Entities: []EntityView{{EntityID: entry.EntityID, Name: entry.Name, Domain: "switch"}}}
	}
	cat, ha := &controlTestCatalog{snap: snap}, &mappingTestHA{}
	svc, err := NewControlService(ControlConfig{Targets: targets}, cat, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return svc, cat, ha
}

func studyMappingEntries(unrelated int) []QueryTarget {
	entries := []QueryTarget{{EntityID: "switch.study_channel_1", Name: "开关1", AreaName: "书房", Domain: "switch"}}
	for i := 0; i < unrelated; i++ {
		entries = append(entries, QueryTarget{EntityID: fmt.Sprintf("switch.unrelated_%02d", i), Name: fmt.Sprintf("辅助通道%02d", i), AreaName: "书房", Domain: "switch"})
	}
	return entries
}

func TestControlMappingQueryUsesConfiguredNameWithoutResurrectingMissingEntity(t *testing.T) {
	entries := append(studyMappingEntries(0), QueryTarget{EntityID: "switch.hallway", Name: "走廊开关", AreaName: "走廊", Domain: "switch"})
	svc, cat, ha := mappingFixture(t, entries, []ControlTarget{mappedStudySwitch()}, "fresh")
	for _, tc := range []struct{ id, name string }{{"switch.study_channel_1", "书房灯"}, {"switch.hallway", "走廊开关"}} {
		result, err := svc.Query(context.Background(), tc.id)
		if err != nil || result.EntityID != tc.id || result.Name != tc.name || result.State != "off" {
			t.Fatalf("incorrect query display identity: result=%+v err=%v", result, err)
		}
	}
	delete(cat.snap.devices["书房"], "switch.study_channel_1")
	if result, err := svc.Query(context.Background(), "switch.study_channel_1"); result != nil || !errors.Is(err, ErrControlNotFound) {
		t.Fatalf("mapping resurrected absent entity: result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(ha.reads, []string{"switch.study_channel_1", "switch.hallway"}) || ha.writes != 0 {
		t.Fatalf("query changed HA access: reads=%v writes=%d", ha.reads, ha.writes)
	}
}

func TestControlMappingExplicitCandidatesBeatUnrelatedRoomEntities(t *testing.T) {
	for _, query := range []string{"打开书房灯", "把书房的灯打开", "打开书房进门开关1", "查询switch.study_channel_1"} {
		t.Run(query, func(t *testing.T) {
			svc, _, ha := mappingFixture(t, studyMappingEntries(25), []ControlTarget{mappedStudySwitch()}, "fresh")
			queries, controls, err := svc.Candidates(context.Background(), query)
			if err != nil || len(queries) != 1 || len(controls) != 1 || queries[0].EntityID != "switch.study_channel_1" || controls[0].EntityID != "switch.study_channel_1" {
				t.Fatalf("room-only matches overwhelmed explicit target: query=%v control=%v err=%v", queries, controls, err)
			}
			if len(ha.reads) != 0 || ha.writes != 0 {
				t.Fatal("candidate selection touched a device")
			}
		})
	}
}

func TestControlMappingKeepsReadOnlyDuplicatesAndMultipleNamedTargets(t *testing.T) {
	for _, tc := range []struct {
		name, query, otherName, otherArea string
	}{
		{"same_room_name", "打开书房灯", "书房灯", "书房"},
		{"other_room_name", "打开书房灯", "书房灯", "客厅"},
		{"same_alias", "把书房的灯打开", "书房的灯", "书房"},
		{"multiple_named", "打开书房灯和走廊开关", "走廊开关", "走廊"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := append(studyMappingEntries(25), QueryTarget{EntityID: "switch.read_only", Name: tc.otherName, AreaName: tc.otherArea, Domain: "switch"})
			svc, _, _ := mappingFixture(t, entries, []ControlTarget{mappedStudySwitch()}, "fresh")
			queries, controls, err := svc.Candidates(context.Background(), tc.query)
			if err != nil || len(queries) != 2 || len(controls) != 1 {
				t.Fatalf("lost ambiguity or kept unrelated room matches: query=%v control=%v err=%v", queries, controls, err)
			}
			if queries[0].EntityID != "switch.read_only" || queries[1].EntityID != "switch.study_channel_1" || controls[0].EntityID != "switch.study_channel_1" {
				t.Fatalf("read-only candidate was hidden or authorized: query=%v control=%v", queries, controls)
			}
		})
	}
}

func TestControlMappingFallbackAndEmptyQueryKeepExistingBounds(t *testing.T) {
	for _, tc := range []struct {
		name, query      string
		unrelated, count int
		wantError        bool
	}{
		{"area_fallback", "书房设备有哪些", 2, 3, false},
		{"no_match", "未知房间设备", 2, 0, false},
		{"empty", "", 2, 3, false},
		{"area_limit", "书房设备有哪些", 20, 0, true},
		{"empty_limit", "", 20, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := mappingFixture(t, studyMappingEntries(tc.unrelated), []ControlTarget{mappedStudySwitch()}, "fresh")
			queries, controls, err := svc.Candidates(context.Background(), tc.query)
			if tc.wantError {
				if !errors.Is(err, ErrControlUnsupported) || queries != nil || controls != nil {
					t.Fatalf("over-limit candidates were truncated: query=%v control=%v err=%v", queries, controls, err)
				}
				return
			}
			if err != nil || len(queries) != tc.count {
				t.Fatalf("changed fallback: query=%v control=%v err=%v", queries, controls, err)
			}
		})
	}
}

func TestControlMappingDoesNotTruncateExplicitDuplicates(t *testing.T) {
	var entries []QueryTarget
	for i := 0; i < 21; i++ {
		entries = append(entries, QueryTarget{EntityID: fmt.Sprintf("switch.duplicate_%02d", i), Name: "书房灯", AreaName: "书房", Domain: "switch"})
	}
	svc, _, _ := mappingFixture(t, entries, nil, "fresh")
	queries, controls, err := svc.Candidates(context.Background(), "打开书房灯")
	if !errors.Is(err, ErrControlUnsupported) || queries != nil || controls != nil {
		t.Fatalf("explicit ambiguity was truncated: query=%v control=%v err=%v", queries, controls, err)
	}
}

func TestControlMappingStaleCandidatesRetainReadOnlyNamesAndAliases(t *testing.T) {
	entries := studyMappingEntries(25)
	entries[0].AreaName = "控制器安装区"
	svc, _, ha := mappingFixture(t, entries, []ControlTarget{mappedStudySwitch()}, "stale")
	queries, controls, err := svc.Candidates(context.Background(), "书房的灯现在是什么状态")
	if err != nil || len(queries) != 1 || len(controls) != 0 {
		t.Fatalf("stale aliases were lost or writable: query=%v control=%v err=%v", queries, controls, err)
	}
	if queries[0].Name != "书房灯" || queries[0].AreaName != "书房" || !reflect.DeepEqual(queries[0].Aliases, []string{"书房的灯", "书房进门开关1"}) {
		t.Fatalf("stale query lost configured identity: %+v", queries[0])
	}
	queries[0].Aliases[0] = "unexpected mutation"
	again, _, err := svc.Candidates(context.Background(), "书房的灯现在是什么状态")
	if err != nil || len(again) != 1 || again[0].Aliases[0] != "书房的灯" {
		t.Fatalf("returned aliases changed policy: %+v %v", again, err)
	}
	if len(ha.reads) != 0 || ha.writes != 0 {
		t.Fatal("read-only candidate selection touched a device")
	}
}
