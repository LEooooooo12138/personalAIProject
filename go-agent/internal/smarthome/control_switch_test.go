package smarthome

import (
	"encoding/json"
	"errors"
	"testing"
)

func switchTestPolicyTarget(t *testing.T, authorization string, verified bool) ControlTarget {
	t.Helper()
	target := ControlTarget{EntityID: "switch.test_channel_1", Name: "授权通道测试", AreaName: "登记区域", AllowedActions: []string{"turn_on", "turn_off"}, LoadLocationVerified: verified}
	if authorization != "" {
		if err := json.Unmarshal([]byte(`{"switch_test_authorized":`+authorization+`}`), &target); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func TestSwitchTestAuthorizationRequiresExplicitOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, authorization string
		verified, accepted  bool
	}{
		{name: "absent"},
		{name: "false", authorization: "false"},
		{name: "authorized", authorization: "true", accepted: true},
		{name: "verified", verified: true, accepted: true},
		{name: "verified_and_authorized", authorization: "true", verified: true, accepted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := switchTestPolicyTarget(t, tc.authorization, tc.verified)
			cfg, revision, err := normalizeControlConfig(ControlConfig{Targets: []ControlTarget{target}})
			if !tc.accepted {
				if !errors.Is(err, ErrControlInvalid) {
					t.Fatalf("unverified switch without explicit authorization accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("authorized switch policy rejected: %v", err)
			}
			if revision == "" || len(cfg.Targets) != 1 || cfg.Targets[0].EntityID != "switch.test_channel_1" || cfg.Targets[0].Domain != "switch" || cfg.Targets[0].LoadLocationVerified != tc.verified {
				t.Fatalf("authorization changed the target or verification claim: %+v", cfg.Targets)
			}
		})
	}
}

func TestSwitchTestAuthorizationPreservesTargetBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ControlTarget)
	}{
		{"wildcard", func(v *ControlTarget) { v.EntityID = "switch.*" }},
		{"mismatched_domain", func(v *ControlTarget) { v.Domain = "light" }},
		{"missing_area", func(v *ControlTarget) { v.AreaName = "" }},
		{"unsupported_action", func(v *ControlTarget) { v.AllowedActions = []string{"toggle"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := switchTestPolicyTarget(t, "true", false)
			tc.mutate(&target)
			if _, _, err := normalizeControlConfig(ControlConfig{Targets: []ControlTarget{target}}); !errors.Is(err, ErrControlInvalid) {
				t.Fatalf("switch test authorization bypassed target constraints: %v", err)
			}
		})
	}
}

func TestSwitchTestAuthorizationChangesPolicyRevision(t *testing.T) {
	base := switchTestPolicyTarget(t, "false", true)
	_, before, err := normalizeControlConfig(ControlConfig{Targets: []ControlTarget{base}})
	if err != nil {
		t.Fatal(err)
	}
	authorized := switchTestPolicyTarget(t, "true", true)
	_, after, err := normalizeControlConfig(ControlConfig{Targets: []ControlTarget{authorized}})
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("changing switch test authorization did not invalidate the policy revision")
	}
}
