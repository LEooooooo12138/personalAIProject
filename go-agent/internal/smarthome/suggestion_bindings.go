package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (m *Manager) homeLocation(ctx context.Context) (*time.Location, error) {
	config, err := m.client.GetConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: read household timezone: %w", ErrHARequest, err)
	}
	name, _ := config["time_zone"].(string)
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("%w: HA time_zone must be an explicit region or UTC", ErrUnsupportedRule)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: HA time_zone %q is invalid", ErrUnsupportedRule, name)
	}
	if m.cfg.TimeZone != "" && m.cfg.TimeZone != name {
		return nil, fmt.Errorf("%w: expected time_zone %q differs from HA %q", ErrUnsupportedRule, m.cfg.TimeZone, name)
	}
	return loc, nil
}
func (m *Manager) validateIntentEntities(ctx context.Context, s RuleSuggestion) error {
	automation, err := BuildAutomation(s)
	if err != nil {
		return err
	}
	loc, err := m.homeLocation(ctx)
	if err != nil {
		return err
	}
	if s.Intent.TimeZone != loc.String() {
		return fmt.Errorf("%w: HA timezone changed; reanalyze this rule", ErrUnsupportedRule)
	}
	states, err := m.client.GetStates(ctx)
	if err != nil {
		return fmt.Errorf("%w: read entity catalog: %w", ErrHARequest, err)
	}
	byID := map[string]EntityState{}
	for _, state := range states {
		byID[state.EntityID] = state
	}
	required := map[string]bool{}
	for _, trigger := range automation.Trigger {
		if trigger.EntityID != "" {
			required[trigger.EntityID] = true
		}
	}
	for _, condition := range automation.Condition {
		if condition.EntityID != "" {
			required[condition.EntityID] = true
		}
	}
	for _, action := range automation.Action {
		required[action.Target.EntityID] = true
	}
	for id := range required {
		state, ok := byID[id]
		if !ok || state.State == "" || state.State == "unknown" || state.State == "unavailable" {
			return fmt.Errorf("%w: entity %s is missing or unavailable", ErrUnsupportedRule, id)
		}
	}
	return nil
}

// BindSuggestion adds only the explicitly requested presence condition to a persisted intent.
func (m *Manager) BindSuggestion(ctx context.Context, id string, bindings SuggestionBindings) (*RuleSuggestion, error) {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := m.findSuggestion(id)
	if err != nil {
		return nil, err
	}
	if source.Status != "pending" && source.Status != "superseded" {
		return nil, fmt.Errorf("%w: cannot bind %s suggestion", ErrSuggestionConflict, source.Status)
	}
	if source.Intent == nil || source.Intent.Kind != "time" || bindings.Presence == nil {
		return nil, fmt.Errorf("%w: time intent and explicit presence_home mapping required; reanalyze legacy suggestions", ErrUnsupportedRule)
	}
	revision := *source
	presence := *bindings.Presence
	revision.PresenceBinding = &presence
	revision.SourceSuggestionID = id
	revision.SupersededBy = ""
	revision.Status = "pending"
	revision.CreatedAt = time.Now()
	revision.HAAutomationID = ""
	revision.LastError = ""
	renderSuggestion(&revision)
	revision.ID = stableSuggestionID(revision)
	if source.Status == "superseded" {
		if source.SupersededBy != revision.ID {
			return nil, ErrSuggestionConflict
		}
		return m.findSuggestion(revision.ID)
	}
	if revision.ID == source.ID {
		return source, nil
	}
	if err := m.validateIntentEntities(ctx, revision); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.store.SaveBoundSuggestion(id, revision)
}
func (s *DeviceStore) SaveBoundSuggestion(sourceID string, revision RuleSuggestion) (*RuleSuggestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	suggestions, err := s.readSuggestions()
	if err != nil {
		return nil, err
	}
	sourceIndex := -1
	for i := range suggestions {
		if suggestions[i].ID == sourceID {
			sourceIndex = i
			break
		}
	}
	if sourceIndex < 0 {
		return nil, ErrSuggestionNotFound
	}
	source := suggestions[sourceIndex]
	if source.Status == "superseded" && source.SupersededBy == revision.ID {
		for _, existing := range suggestions {
			if existing.ID == revision.ID {
				return &existing, nil
			}
		}
	}
	if source.Status != "pending" {
		return nil, ErrSuggestionConflict
	}
	a, _ := json.Marshal(source.Intent)
	b, _ := json.Marshal(revision.Intent)
	if source.Intent == nil || string(a) != string(b) || revision.ID != stableSuggestionID(revision) || revision.ID == sourceID || revision.SourceSuggestionID != sourceID {
		return nil, fmt.Errorf("%w: binding cannot change intent", ErrUnsupportedRule)
	}
	if _, err = BuildAutomation(revision); err != nil {
		return nil, err
	}
	for _, existing := range suggestions {
		if existing.ID == revision.ID {
			return nil, fmt.Errorf("%w: revision already belongs to another source", ErrSuggestionConflict)
		}
	}
	revision.Status = "pending"
	revision.SupersededBy = ""
	revision.LastError = ""
	revision.HAAutomationID = ""
	suggestions[sourceIndex].Status = "superseded"
	suggestions[sourceIndex].SupersededBy = revision.ID
	suggestions = append(suggestions, revision)
	if err = s.writeSuggestions(suggestions); err != nil {
		return nil, err
	}
	return &revision, nil
}
