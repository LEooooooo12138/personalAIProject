package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlConfigRequiresAuthenticatedConsole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("inference:\n  endpoint: http://localhost:11434\nvaults:\n  personal: ./personal\n  agent: ./agent\nsmarthome:\n  enabled: true\n  base_url: http://localhost:8123\n  control:\n    enabled: true\n"), 0600)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("enabled control without authenticated console accepted")
	}
}
func TestControlConfigLoadsVerifiedMapping(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mapping.yaml")
	err := os.WriteFile(p, []byte("inference:\n  endpoint: http://localhost:11434\nvaults:\n  personal: ./personal\n  agent: ./agent\nsmarthome:\n  control:\n    proposal_ttl: 90s\n    targets:\n      - entity_id: light.test\n        name: 台灯\n        area_name: 客厅\n        aliases: [阅读灯]\n        allowed_actions: [turn_on, turn_off]\n        load_location_verified: true\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.SmartHome.Control.ServiceConfig()
	if cfg.SmartHome.Control.Enabled || len(c.Targets) != 1 || c.Targets[0].EntityID != "light.test" || !c.Targets[0].LoadLocationVerified || c.Targets[0].AreaName != "客厅" || len(c.Targets[0].AllowedActions) != 2 || c.Targets[0].Aliases[0] != "阅读灯" || c.ProposalTTL.String() != "1m30s" {
		t.Fatalf("lost mapping fields: %+v", c)
	}
}
