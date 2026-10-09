package smarthome

import (
	"fmt"
	"strings"
	"time"
)

// Structured intents are the sole command source for newly generated suggestions.
func automationFromIntent(intent *SuggestionIntent, presence *PresenceBinding) (AutomationConfig, error) {
	invalid := func(reason string) (AutomationConfig, error) {
		return AutomationConfig{}, fmt.Errorf("%w: %s", ErrUnsupportedRule, reason)
	}
	if intent == nil || intent.SchemaVersion != 1 {
		return invalid("reanalysis required: unsupported intent version")
	}
	if intent.TimeZone == "" || intent.TimeZone == "Local" {
		return invalid("generation timezone is required")
	}
	if _, err := time.LoadLocation(intent.TimeZone); err != nil {
		return invalid("invalid generation timezone")
	}
	target := intent.EntityID
	if intent.Kind == "correlation" {
		target = intent.RelatedID
	}
	domain := strings.Split(target, ".")[0]
	if domain != "light" && domain != "switch" && domain != "fan" {
		return invalid("action domain is unsupported: " + domain)
	}
	config := AutomationConfig{Mode: "single", Action: []AutomationAction{{Service: domain + ".turn_on", Target: AutomationTarget{EntityID: target}}}}
	switch intent.Kind {
	case "time":
		if intent.RelatedID != "" || len(intent.RequiredConditions) != 1 || intent.RequiredConditions[0] != "presence_home" {
			return invalid("time rule must retain presence_home")
		}
		if presence == nil {
			return invalid("presence_home requires an administrator's aggregate occupancy mapping")
		}
		presenceDomain := strings.Split(presence.EntityID, ".")[0]
		// The administrator declares that this helper/group represents aggregate
		// household occupancy. Device names do not establish that meaning.
		if (presenceDomain != "group" && presenceDomain != "binary_sensor" && presenceDomain != "input_boolean") || !entityIDPattern.MatchString(presence.EntityID) || (presence.State != "on" && presence.State != "home") || (presenceDomain != "group" && presence.State != "on") {
			return invalid("presence mapping needs an aggregate entity and on/home state")
		}
		config.Trigger = []AutomationTrigger{{Platform: "time", At: intent.At}}
		config.Condition = []AutomationCondition{{Condition: "state", EntityID: presence.EntityID, State: presence.State}}
	case "correlation":
		if presence != nil || intent.At != "" || len(intent.RequiredConditions) != 1 || intent.RequiredConditions[0] != "sunset_to_midnight" {
			return invalid("correlation must retain sunset_to_midnight")
		}
		config.Trigger = []AutomationTrigger{{Platform: "state", EntityID: intent.EntityID, From: "off", To: "on"}}
		config.Condition = []AutomationCondition{{Condition: "sun", After: "sunset"}}
	default:
		return invalid("unsupported intent kind")
	}
	// Validate every field before publishing the executable projection.
	config.ID = "validation"
	if err := validateAutomation(config); err != nil {
		return AutomationConfig{}, err
	}
	config.ID = ""
	return config, nil
}
func renderSuggestion(s *RuleSuggestion) {
	i := s.Intent
	if i == nil {
		return
	}
	s.MissingBindings = nil
	s.UnsupportedReason = ""
	s.Automation = nil
	if i.Kind == "time" {
		s.Title = fmt.Sprintf("Scheduled: Turn on %s at %s", friendlyName(i.EntityID), i.At)
		s.Trigger = i.At + " (" + i.TimeZone + ")"
		s.Condition = "someone is home"
		s.Action = i.EntityID
		if s.PresenceBinding == nil {
			s.MissingBindings = []string{"presence_home"}
		} else {
			s.Condition = fmt.Sprintf("someone is home: %s is %s", s.PresenceBinding.EntityID, s.PresenceBinding.State)
		}
	} else {
		s.Title = fmt.Sprintf("Auto: When %s changes off to on, turn on %s", friendlyName(i.EntityID), friendlyName(i.RelatedID))
		s.Trigger = i.EntityID + " off -> on"
		s.Condition = "after sunset until local midnight (" + i.TimeZone + ")"
		s.Action = i.RelatedID
	}
	config, err := automationFromIntent(i, s.PresenceBinding)
	if err != nil {
		s.UnsupportedReason = err.Error()
		return
	}
	s.Automation = &config
}
