package smarthome

import (
	"context"
	"fmt"
	"path/filepath"
)

// ConfirmSuggestion executes exactly the persisted rule the user confirmed.
// Serializing the transition prevents concurrent confirmations from duplicating HA requests.
// A stable config_key makes an explicit retry after an uncertain response an upsert.
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
	switch suggestion.Status {
	case "confirmed":
		return suggestion, nil
	case "pending", "failed", "applying":
		// "applying" may remain after a process interruption; reuse its config_key.
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
	if _, err := m.store.changeSuggestion(id, "applying", id, ""); err != nil {
		return nil, err
	}
	suggestion.HAAutomationID = id
	if err := ExecuteRule(ctx, m.client, *suggestion); err != nil {
		return nil, m.recordConfirmationFailure(id, err)
	}
	if err := atomicWrite(filepath.Join(m.store.basePath, "rules", id+".md"), []byte(RuleDocument(*suggestion))); err != nil {
		return nil, m.recordConfirmationFailure(id, fmt.Errorf("archive rule: %w", err))
	}
	confirmed, err := m.store.changeSuggestion(id, "confirmed", id, "")
	if err != nil {
		return nil, fmt.Errorf("persist confirmed rule: %w", err)
	}
	return confirmed, nil
}

func (m *Manager) recordConfirmationFailure(id string, cause error) error {
	if _, err := m.store.changeSuggestion(id, "failed", id, cause.Error()); err != nil {
		return fmt.Errorf("%w; persist failure: %w", cause, err)
	}
	return cause
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
