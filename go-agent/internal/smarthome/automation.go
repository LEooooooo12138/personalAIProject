package smarthome

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrUnsupportedRule = errors.New("unsupported automation rule")
	ErrHARequest       = errors.New("home assistant request failed")
	configKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	entityIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
)

// BuildAutomation creates an AutomationConfig from a RuleSuggestion.
func BuildAutomation(suggestion RuleSuggestion) (AutomationConfig, error) {
	if suggestion.Automation == nil {
		return AutomationConfig{}, fmt.Errorf("%w: explicit trigger, conditions and actions are required", ErrUnsupportedRule)
	}
	a := *suggestion.Automation
	a.ID, a.Alias, a.Description = suggestion.ID, suggestion.Title, suggestion.Description
	if strings.TrimSpace(suggestion.Condition) != "" && len(a.Condition) == 0 {
		return AutomationConfig{}, fmt.Errorf("%w: condition %q has no entity mapping", ErrUnsupportedRule, suggestion.Condition)
	}
	if a.Mode == "" {
		a.Mode = "single"
	}
	return a, validateAutomation(a)
}

func validateAutomation(a AutomationConfig) error {
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrUnsupportedRule, reason) }
	if !configKeyPattern.MatchString(a.ID) {
		return invalid("invalid config_key")
	}
	if len(a.Trigger) == 0 || len(a.Action) == 0 {
		return invalid("trigger and action must not be empty")
	}
	if a.Mode != "single" && a.Mode != "" {
		return invalid("only single execution mode is supported")
	}
	for _, trigger := range a.Trigger {
		switch trigger.Platform {
		case "time":
			_, err := time.Parse("15:04", trigger.At)
			if err != nil {
				_, err = time.Parse("15:04:05", trigger.At)
			}
			if err != nil || trigger.EntityID != "" || trigger.From != "" || trigger.To != "" {
				return invalid("invalid time trigger")
			}
		case "state":
			if !entityIDPattern.MatchString(trigger.EntityID) || trigger.To == "" || trigger.At != "" {
				return invalid("invalid state trigger")
			}
		default:
			return invalid("unsupported trigger platform")
		}
	}
	for _, condition := range a.Condition {
		if condition.Condition != "state" || !entityIDPattern.MatchString(condition.EntityID) || condition.State == "" {
			return invalid("unsupported condition")
		}
	}
	for _, action := range a.Action {
		parts := strings.Split(action.Service, ".")
		if len(parts) != 2 || (parts[0] != "light" && parts[0] != "switch" && parts[0] != "fan") || (parts[1] != "turn_on" && parts[1] != "turn_off") {
			return invalid("unsupported service")
		}
		if !entityIDPattern.MatchString(action.Target.EntityID) || !strings.HasPrefix(action.Target.EntityID, parts[0]+".") {
			return invalid("service and target domain do not match")
		}
	}
	return nil
}

// stableSuggestionID depends on rule semantics, not analysis order or observation date.
func stableSuggestionID(s RuleSuggestion) string {
	semantic := struct {
		Trigger, Condition, Action string
		Automation                 *AutomationConfig
	}{s.Trigger, s.Condition, s.Action, s.Automation}
	data, _ := json.Marshal(semantic)
	digest := sha256.Sum256(data)
	return "rule-" + hex.EncodeToString(digest[:12])
}

// ExecuteRule creates the validated automation in HA and reloads automations.
// Manager owns the persistent suggestion transition and rule document.
func ExecuteRule(ctx context.Context, client *HomeAssistantClient, suggestion RuleSuggestion) error {
	automation, err := BuildAutomation(suggestion)
	if err != nil {
		return err
	}

	if err := client.CreateAutomation(ctx, automation); err != nil {
		return fmt.Errorf("%w: create automation: %w", ErrHARequest, err)
	}

	if err := client.ReloadAutomations(ctx); err != nil {
		return fmt.Errorf("%w: reload automations: %w", ErrHARequest, err)
	}

	return nil
}

// RuleDocument generates a markdown document for a confirmed rule suitable for agent-vault.
func RuleDocument(suggestion RuleSuggestion) string {
	config, _ := BuildAutomation(suggestion)
	payload, _ := json.MarshalIndent(config, "", "  ")
	quote := func(value string) string {
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	return fmt.Sprintf(`---
title: %s
created: %s
source: agent-analysis
ha_automation_id: %s
trigger: %s
action: %s
confidence: %.2f
data_source: %s
status: active
---

## Discovered Pattern
%s

## Rule Design
- Trigger: %s
- Condition: %s
- Action: %s

## Confirmed HA configuration

`+"```json\n%s\n```\n"+`
`,
		quote(suggestion.Title),
		time.Now().Format("2006-01-02"),
		quote(suggestion.HAAutomationID),
		quote(suggestion.Trigger),
		quote(suggestion.Action),
		suggestion.Confidence,
		quote(suggestion.DataSource),
		suggestion.Description,
		suggestion.Trigger,
		suggestion.Condition,
		suggestion.Action,
		string(payload),
	)
}
