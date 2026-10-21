package core

import (
	"testing"

	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

func TestMessageRequiresLocalSpeech(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message Message
		want    bool
	}{
		{"ordinary_local", Message{AssistantMode: "local"}, false},
		{"ordinary_cloud", Message{AssistantMode: "cloud"}, false},
		{"ordinary_hybrid", Message{AssistantMode: "hybrid"}, false},
		{"legacy_or_knowledge", Message{}, true},
		{"unknown_mode", Message{AssistantMode: "unexpected"}, true},
		{"local_event", Message{AssistantMode: "local", EventID: "event"}, true},
		{"cloud_event", Message{AssistantMode: "cloud", EventID: "event"}, true},
		{"hybrid_result", Message{AssistantMode: "hybrid", DeviceResult: &smarthome.DeviceQueryResult{}}, true},
		{"local_result", Message{AssistantMode: "local", DeviceResult: &smarthome.DeviceQueryResult{}}, true},
		{"cloud_attachment", Message{AssistantMode: "cloud", Attachments: []MessageAttachment{{Kind: "control_proposal"}}}, true},
		{"unknown_attachment", Message{AssistantMode: "local", Attachments: []MessageAttachment{{Kind: "future-sensitive-kind"}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MessageRequiresLocalSpeech(tc.message); got != tc.want {
				t.Fatalf("local speech required=%v want=%v", got, tc.want)
			}
		})
	}
}
