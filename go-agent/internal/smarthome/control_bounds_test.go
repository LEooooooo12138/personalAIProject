package smarthome

import (
	"strings"
	"testing"
)

func TestControlPolicyMatchesIntentProjectionBounds(t *testing.T) {
	base := ControlTarget{EntityID: "light.test", Name: "灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on"}}
	for _, kind := range []string{"duplicate_alias", "many_aliases", "long_name", "long_area", "long_alias"} {
		t.Run(kind, func(t *testing.T) {
			v := base
			switch kind {
			case "duplicate_alias":
				v.Aliases = []string{"灯泡", "灯泡"}
			case "many_aliases":
				v.Aliases = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
			case "long_name":
				v.Name = strings.Repeat("灯", 100)
			case "long_area":
				v.AreaName = strings.Repeat("厅", 100)
			case "long_alias":
				v.Aliases = []string{strings.Repeat("灯", 100)}
			}
			if _, _, err := normalizeControlConfig(ControlConfig{Targets: []ControlTarget{v}}); err == nil {
				t.Fatal("accepted unparseable policy")
			}
		})
	}
}
