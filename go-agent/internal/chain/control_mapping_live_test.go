package chain

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
)

// This opt-in check reaches only the local model with synthetic switch IDs.
// No HA client or device service is constructed.
func TestHAIntentConfirmedMappingLive(t *testing.T) {
	if os.Getenv("HA_MAPPING_LIVE") != "1" {
		t.Skip("set HA_MAPPING_LIVE=1 for the local confirmed-mapping check")
	}
	target := HATarget{EntityID: "switch.synthetic_study_primary", Name: "书房灯", Aliases: []string{"书房的灯", "书房进门开关1", "书房进门开关 1"}, AreaName: "书房", Domain: "switch"}
	candidates := HACandidates{Query: []HATarget{target}, Control: []HATarget{target}}
	client := inference.NewOllamaClient("http://127.0.0.1:11434", 60*time.Second)
	parser := NewHAIntentParser(client, "gemma4:12b")
	for _, tc := range []struct{ query, kind, action string }{
		{"打开书房灯", "on_off", "turn_on"},
		{"把书房的灯打开", "on_off", "turn_on"},
		{"关闭书房灯", "on_off", "turn_off"},
		{"书房灯现在开着吗", "query", ""},
	} {
		t.Run(tc.query, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			start := time.Now()
			got, err := parser.Parse(ctx, tc.query, candidates)
			if err != nil {
				t.Fatalf("local intent validation failed: %v", err)
			}
			if got.Kind != tc.kind || got.EntityID != target.EntityID || got.Action != tc.action || got.Question != "" {
				t.Fatalf("unexpected intent: %+v", got)
			}
			t.Logf("kind=%s action=%s entity=%s latency=%s", got.Kind, got.Action, got.EntityID, time.Since(start).Round(time.Millisecond))
		})
	}
}
