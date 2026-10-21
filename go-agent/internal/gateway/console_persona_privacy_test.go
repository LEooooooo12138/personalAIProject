package gateway

import (
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
)

func TestCloudHistoryRejectsStructuredDeviceFactsEvenWithCloudLabel(t *testing.T) {
	s := &core.Session{Messages: []core.Message{{Role: "user", Content: "query"}, {Role: "assistant", Content: "private device observation", AssistantMode: "cloud", DeviceResult: &smarthome.DeviceQueryResult{EntityID: "sensor.private"}}}}
	if history := buildCloudHistory(s); len(history) != 0 {
		t.Fatalf("structured local fact leaked into cloud history: %v", history)
	}
}
