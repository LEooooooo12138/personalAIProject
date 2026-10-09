package chain

import (
	"context"
	"errors"
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
