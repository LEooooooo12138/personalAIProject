package smarthome

import (
	"context"
	"errors"
	"fmt"
)

// ConfirmSuggestion executes exactly the persisted rule the user confirmed.
// Serializing the transition prevents concurrent confirmations from duplicating HA requests.
// A durable operation prevents replay even when the external outcome is unknown.
func (m *Manager) ConfirmSuggestion(ctx context.Context, id string) (*RuleSuggestion, error) {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	suggestion, err := m.findSuggestion(id)
	if err != nil {
		return nil, err
	}
	if op, lookupErr := m.GetInstallOperationForSuggestion(id); lookupErr == nil {
		if err := m.store.projectInstallSuggestion(op); err != nil {
			return nil, err
		}
		return m.findSuggestion(id)
	} else if !errors.Is(lookupErr, ErrInstallOperationNotFound) {
		return nil, lookupErr
	}
	switch suggestion.Status {
	case "confirmed":
		return suggestion, nil
	case "pending":
		// Only an unattempted pending suggestion may start an operation.
	default:
		return nil, fmt.Errorf("%w: cannot confirm %s suggestion", ErrSuggestionConflict, suggestion.Status)
	}
	automation, err := BuildAutomation(*suggestion)
	if err != nil {
		return nil, err
	}
	if suggestion.Intent != nil {
		if err := m.validateIntentEntities(ctx, *suggestion); err != nil {
			return nil, err
		}
	} else {
		// Legacy explicit payloads have no recorded generation zone, but HA must
		// still supply a valid current household zone for clock or solar semantics.
		usesHomeTime := false
		for _, trigger := range automation.Trigger {
			usesHomeTime = usesHomeTime || trigger.Platform == "time"
		}
		for _, condition := range automation.Condition {
			usesHomeTime = usesHomeTime || condition.Condition == "sun"
		}
		if usesHomeTime {
			if _, err := m.homeLocation(ctx); err != nil {
				return nil, err
			}
		}
	}
	return m.applyInstallation(ctx, suggestion, automation)
}

// IgnoreSuggestion only affects an unexecuted suggestion; it never disables an HA rule.
func (m *Manager) IgnoreSuggestion(id string) (*RuleSuggestion, error) {
	return m.IgnoreSuggestionContext(context.Background(), id)
}

// IgnoreSuggestionContext preserves the existing transition while checking a
// caller revoked during serialization before changing local state.
func (m *Manager) IgnoreSuggestionContext(ctx context.Context, id string) (*RuleSuggestion, error) {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	suggestion, err := m.findSuggestion(id)
	if err != nil {
		return nil, err
	}
	if _, lookupErr := m.GetInstallOperationForSuggestion(id); lookupErr == nil {
		return nil, fmt.Errorf("%w: installation already recorded", ErrSuggestionConflict)
	} else if !errors.Is(lookupErr, ErrInstallOperationNotFound) {
		return nil, lookupErr
	}
	if suggestion.Status == "ignored" {
		return suggestion, nil
	}
	if suggestion.Status != "pending" {
		return nil, fmt.Errorf("%w: cannot ignore %s suggestion", ErrSuggestionConflict, suggestion.Status)
	}
	return m.store.changeSuggestion(id, "ignored", "", "")
}

func (m *Manager) findSuggestion(id string) (*RuleSuggestion, error) {
	suggestions, err := m.store.GetSuggestions()
	if err != nil {
		return nil, err
	}
	for _, suggestion := range suggestions {
		if suggestion.ID == id {
			return &suggestion, nil
		}
	}
	return nil, ErrSuggestionNotFound
}
