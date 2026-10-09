package chain

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// The local model can infer a lamp-to-switch mapping that the registry has never
// established. Keep that guess rejected even when there is only one writable switch.
func TestHAIntentUnmappedStudyLampCannotSelectSwitch(t *testing.T) {
	target := HATarget{EntityID: "switch.study_test_1", Name: "书房进门开关1（授权通道测试）", AreaName: "书房（登记区域）", Domain: "switch", Aliases: []string{"书房进门开关1", "书房进门开关 1"}}
	candidates := HACandidates{Query: []HATarget{target}, Control: []HATarget{target}}
	for _, tc := range []struct {
		query    string
		accepted bool
	}{
		{"打开书房灯", false},
		{"打开书房进门开关1", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			client := &haIntentTestClient{response: haIntentResponse(`{"kind":"on_off","entity_id":"switch.study_test_1","action":"turn_on","question":""}`)}
			got, err := NewHAIntentParser(client, "synthetic").Parse(context.Background(), tc.query, candidates)
			if !tc.accepted {
				if !errors.Is(err, ErrHAIntentInvalid) || got.Kind != "" {
					t.Fatalf("unverified load alias accepted: intent=%+v err=%v", got, err)
				}
			} else if err != nil || got.Kind != "on_off" || got.EntityID != target.EntityID {
				t.Fatalf("explicit switch name rejected: intent=%+v err=%v", got, err)
			}
		})
	}
}

func TestHAIntentConfirmedStudyLampNameAndAliases(t *testing.T) {
	target := HATarget{EntityID: "switch.study_test_1", Name: "书房灯", AreaName: "书房", Domain: "switch", Aliases: []string{"书房的灯", "书房进门1", "书房进门开关1"}}
	candidates := HACandidates{Query: []HATarget{target}, Control: []HATarget{target}}
	for _, tc := range []struct{ query, kind, action string }{
		{"打开书房灯", "on_off", "turn_on"},
		{"把书房的灯打开", "on_off", "turn_on"},
		{"打开书房进门1", "on_off", "turn_on"},
		{"打开书房进门开关1", "on_off", "turn_on"},
		{"书房灯状态", "query", ""},
		{"书房的灯现在开着吗", "query", ""},
		{"书房进门1状态", "query", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			client := &haIntentTestClient{response: haIntentResponse(fmt.Sprintf(`{"kind":%q,"entity_id":"switch.study_test_1","action":%q,"question":""}`, tc.kind, tc.action))}
			got, err := NewHAIntentParser(client, "synthetic").Parse(context.Background(), tc.query, candidates)
			if err != nil || got.Kind != tc.kind || got.EntityID != target.EntityID || got.Action != tc.action {
				t.Fatalf("confirmed name/alias rejected: %+v %v", got, err)
			}
		})
	}
}

func TestHAIntentSwitchQueryRequiresLiteralUniqueTarget(t *testing.T) {
	study := HATarget{EntityID: "switch.study_test_1", Name: "书房灯", AreaName: "书房", Domain: "switch", Aliases: []string{"书房的灯", "书房进门1"}}
	living := HATarget{EntityID: "switch.living_test_1", Name: "客厅灯", AreaName: "客厅", Domain: "switch"}
	unmapped := HATarget{EntityID: "switch.study_test_1", Name: "书房进门开关1", AreaName: "书房", Domain: "switch"}
	for _, tc := range []struct {
		name, query string
		candidates  HACandidates
		accepted    bool
	}{
		{"unmapped_lamp", "书房灯状态", HACandidates{Query: []HATarget{unmapped}, Control: []HATarget{unmapped}}, false},
		{"room_only", "书房设备状态", HACandidates{Query: []HATarget{study}, Control: []HATarget{study}}, false},
		{"unrelated_query", "客厅灯状态", HACandidates{Query: []HATarget{study, living}, Control: []HATarget{study}}, false},
		{"forged_entity_suffix", "switch.study_test_1_forged状态", HACandidates{Query: []HATarget{study}}, false},
		{"two_named_targets", "书房灯和客厅灯状态", HACandidates{Query: []HATarget{study, living}, Control: []HATarget{study}}, false},
		{"two_explicit_entities", "switch.study_test_1和switch.living_test_1状态", HACandidates{Query: []HATarget{study, living}}, false},
		{"explicit_name", "书房灯状态", HACandidates{Query: []HATarget{study, living}, Control: []HATarget{study}}, true},
		{"configured_alias", "书房的灯状态", HACandidates{Query: []HATarget{study, living}}, true},
		{"explicit_entity", "switch.study_test_1状态", HACandidates{Query: []HATarget{study, living}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &haIntentTestClient{response: haIntentResponse(`{"kind":"query","entity_id":"switch.study_test_1","action":"","question":""}`)}
			got, err := NewHAIntentParser(client, "synthetic").Parse(context.Background(), tc.query, tc.candidates)
			if tc.accepted {
				if err != nil || got.Kind != "query" || got.EntityID != study.EntityID {
					t.Fatalf("explicit query rejected: %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrHAIntentInvalid) || got.Kind != "" {
				t.Fatalf("ambiguous or guessed query accepted: %+v %v", got, err)
			}
		})
	}
}

func TestHAIntentSwitchQueryDisambiguatesAcrossAllVisibleTargets(t *testing.T) {
	study := HATarget{EntityID: "switch.study_test_1", Name: "台灯", AreaName: "书房", Domain: "switch", Aliases: []string{"阅读灯"}}
	living := HATarget{EntityID: "switch.living_test_1", Name: "台灯", AreaName: "客厅", Domain: "switch", Aliases: []string{"阅读灯"}}
	candidates := HACandidates{Query: []HATarget{study, living}, Control: []HATarget{study}}
	for _, tc := range []struct {
		query    string
		accepted bool
	}{
		{"台灯状态", false}, {"阅读灯状态", false}, {"客厅台灯状态", false},
		{"书房和客厅台灯状态", false}, {"书房台灯状态", true}, {"书房阅读灯状态", true},
		{"switch.study_test_1状态", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			client := &haIntentTestClient{response: haIntentResponse(`{"kind":"query","entity_id":"switch.study_test_1","action":"","question":""}`)}
			got, err := NewHAIntentParser(client, "synthetic").Parse(context.Background(), tc.query, candidates)
			if tc.accepted {
				if err != nil || got.EntityID != study.EntityID || got.Kind != "query" {
					t.Fatalf("room-selected query rejected: %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrHAIntentInvalid) || got.Kind != "" {
				t.Fatalf("read-only target ambiguity ignored: %+v %v", got, err)
			}
		})
	}
}

func TestHAIntentSwitchQueryRejectsAmbiguousSecondReference(t *testing.T) {
	study := HATarget{EntityID: "switch.study_test_1", Name: "书房灯", AreaName: "书房", Domain: "switch", Aliases: []string{"书房的灯"}}
	living := HATarget{EntityID: "switch.living_test_1", Name: "台灯", AreaName: "客厅", Domain: "switch"}
	bedroom := HATarget{EntityID: "switch.bedroom_test_1", Name: "台灯", AreaName: "卧室", Domain: "switch"}
	sharedAlias := study
	sharedAlias.Aliases = []string{"书房的灯", "台灯"}
	connectorName := study
	connectorName.Name, connectorName.Aliases = "书房和室灯", nil
	shorterName := living
	shorterName.Name = "室灯"
	shortSelected := study
	shortSelected.Name, shortSelected.Aliases = "室灯", nil
	longOther := living
	longOther.Name = "书房和室灯"
	aliasWithShortName := study
	aliasWithShortName.Aliases = []string{"书房进门开关1"}
	shortSwitchName := living
	shortSwitchName.Name = "开关1"
	for _, tc := range []struct {
		name, query string
		selected    HATarget
		others      []HATarget
		accepted    bool
	}{
		{"ambiguous_second_name", "书房灯和台灯状态", study, []HATarget{living, bedroom}, false},
		{"ambiguous_second_without_connector", "书房灯 台灯状态", study, []HATarget{living, bedroom}, false},
		{"ambiguous_second_is_selected_alias", "书房灯和台灯状态", sharedAlias, []HATarget{living, bedroom}, false},
		{"shared_alias_room_disambiguation", "书房台灯状态", sharedAlias, []HATarget{living, bedroom}, true},
		{"negative_state_question", "书房灯是不是没打开", study, []HATarget{living, bedroom}, true},
		{"two_unambiguous_names_for_one_target", "书房灯也就是书房的灯现在什么状态", study, []HATarget{living, bedroom}, true},
		{"connector_inside_complete_name", "书房和室灯是不是关着", connectorName, []HATarget{living, bedroom}, true},
		{"nested_shorter_name_is_not_second_reference", "书房和室灯状态", connectorName, []HATarget{shorterName}, true},
		{"selected_short_substring_is_not_requested_name", "书房和室灯状态", shortSelected, []HATarget{longOther}, false},
		{"same_target_alias_with_nested_other", "书房灯（书房进门开关1）状态", aliasWithShortName, []HATarget{shortSwitchName}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets := append([]HATarget{tc.selected}, tc.others...)
			candidates := HACandidates{Query: targets, Control: []HATarget{tc.selected}}
			client := &haIntentTestClient{response: haIntentResponse(`{"kind":"query","entity_id":"switch.study_test_1","action":"","question":""}`)}
			got, err := NewHAIntentParser(client, "synthetic").Parse(context.Background(), tc.query, candidates)
			if tc.accepted {
				if err != nil || got.Kind != "query" || got.EntityID != tc.selected.EntityID {
					t.Fatalf("unique query rejected: %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrHAIntentInvalid) || got.Kind != "" {
				t.Fatalf("ambiguous second reference accepted: %+v %v", got, err)
			}
		})
	}
}
