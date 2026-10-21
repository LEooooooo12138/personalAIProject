package smarthome

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type SuggestionRevisionRequest struct {
	ExpectedVersion string           `json:"expected_version"`
	Intent          SuggestionIntent `json:"intent"`
	PresenceBinding *PresenceBinding `json:"presence_binding,omitempty"`
}
type SuggestionRevisionChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// CanReviseSuggestion is a structural check only. Every save also validates the
// current catalog, household timezone and entity states before committing.
func CanReviseSuggestion(s RuleSuggestion) bool {
	if s.Status != "pending" || s.SupersededBy != "" || s.Intent == nil {
		return false
	}
	probe := s
	if s.Intent.Kind == "time" && s.PresenceBinding == nil {
		// Validation-only sentinel; never persisted or selected for the household.
		probe.PresenceBinding = &PresenceBinding{EntityID: "binary_sensor.validation", State: "on"}
	}
	_, err := BuildAutomation(probe)
	return err == nil
}

func validateSuggestionRevision(source RuleSuggestion, req SuggestionRevisionRequest) (RuleSuggestion, error) {
	if req.ExpectedVersion == "" || req.ExpectedVersion != source.ID || source.Status != "pending" || source.SupersededBy != "" {
		return RuleSuggestion{}, ErrSuggestionConflict
	}
	return validateRevisionContent(source, req)
}

// Pure restricted-content validation shared with isolated managed drafts.
// The ordinary revision entry point retains its pending-head checks above.
func validateRevisionContent(source RuleSuggestion, req SuggestionRevisionRequest) (RuleSuggestion, error) {
	invalid := func(reason string) (RuleSuggestion, error) {
		return RuleSuggestion{}, fmt.Errorf("%w: %s", ErrUnsupportedRule, reason)
	}
	if req.ExpectedVersion == "" || req.ExpectedVersion != source.ID {
		return RuleSuggestion{}, ErrSuggestionConflict
	}
	probe := source
	probe.Status = "pending"
	probe.SupersededBy = ""
	if !CanReviseSuggestion(probe) {
		return invalid("known structured intent required")
	}
	original := source.Intent
	next := req.Intent
	if next.SchemaVersion != original.SchemaVersion || next.Kind != original.Kind || next.TimeZone != original.TimeZone || !reflect.DeepEqual(next.RequiredConditions, original.RequiredConditions) {
		return invalid("intent schema, kind, timezone and required conditions are immutable")
	}
	if next.Kind == "correlation" && (!revisionSourceSupported(next.EntityID) || next.EntityID == next.RelatedID) {
		return invalid("unsupported correlation source")
	}
	if next.Kind == "time" && req.PresenceBinding == nil {
		return invalid("explicit presence mapping required")
	}
	revision := RuleSuggestion{SchemaVersion: 1, ID: "validation", Status: "pending", CreatedAt: time.Now().UTC(), SourceSuggestionID: source.ID, Intent: &next}
	next.RequiredConditions = append([]string{}, next.RequiredConditions...)
	if req.PresenceBinding != nil {
		p := *req.PresenceBinding
		revision.PresenceBinding = &p
	}
	renderSuggestion(&revision)
	if _, err := BuildAutomation(revision); err != nil {
		return RuleSuggestion{}, err
	}
	revision.RevisionDiff = suggestionRevisionDiff(source, revision)
	if len(revision.RevisionDiff) == 0 {
		return invalid("revision has no changes")
	}
	return revision, nil
}
func revisionSourceSupported(id string) bool {
	switch strings.Split(id, ".")[0] {
	case "light", "switch", "fan", "binary_sensor", "input_boolean":
		return true
	}
	return false
}
func suggestionRevisionDiff(before, after RuleSuggestion) []SuggestionRevisionChange {
	changes := []SuggestionRevisionChange{}
	add := func(field, a, b string) {
		if a != b {
			changes = append(changes, SuggestionRevisionChange{field, a, b})
		}
	}
	if before.Intent.Kind == "time" {
		add("time", before.Intent.At, after.Intent.At)
		add("target", before.Intent.EntityID, after.Intent.EntityID)
	} else {
		add("source", before.Intent.EntityID, after.Intent.EntityID)
		add("target", before.Intent.RelatedID, after.Intent.RelatedID)
	}
	a, b := PresenceBinding{}, PresenceBinding{}
	if before.PresenceBinding != nil {
		a = *before.PresenceBinding
	}
	if after.PresenceBinding != nil {
		b = *after.PresenceBinding
	}
	add("presence_entity", a.EntityID, b.EntityID)
	add("presence_state", a.State, b.State)
	return changes
}

// ReviseSuggestion saves a new immutable pending version. It performs no HA
// writes; installation remains a separate explicit confirmation of its new ID.
func (m *Manager) ReviseSuggestion(ctx context.Context, id string, req SuggestionRevisionRequest) (*RuleSuggestion, error) {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.requireUnattemptedSuggestion(id); err != nil {
		return nil, err
	}
	source, err := m.findSuggestion(id)
	if err != nil {
		return nil, err
	}
	revision, err := validateSuggestionRevision(*source, req)
	if err != nil {
		return nil, err
	}
	if err = m.validateRevisionCatalog(ctx, revision); err != nil {
		return nil, err
	}
	if err = m.validateIntentEntities(ctx, revision); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return m.store.saveSuggestionRevision(id, req)
}

func (m *Manager) validateRevisionCatalog(ctx context.Context, revision RuleSuggestion) error {
	if m.catalog == nil {
		return ErrCatalogUnavailable
	}
	snapshot, err := m.catalog.Get(ctx)
	if err != nil {
		return err
	}
	if snapshot.Meta.Freshness != "fresh" || snapshot.Meta.Connection != "connected" {
		return ErrCatalogUnavailable
	}
	required := map[string]bool{revision.Intent.EntityID: true}
	if revision.Intent.RelatedID != "" {
		required[revision.Intent.RelatedID] = true
	}
	if revision.PresenceBinding != nil {
		required[revision.PresenceBinding.EntityID] = true
	}
	// This is the same catalog used by the room/device/entity picker. Names never
	// select an entity and unavailable/hidden/disabled entries cannot be saved.
	for _, devices := range snapshot.devices {
		for _, device := range devices {
			for _, entity := range device.Entities {
				if !required[entity.EntityID] {
					continue
				}
				if entity.Hidden || entity.Disabled || entity.State == nil || strings.TrimSpace(*entity.State) == "" || *entity.State == "unknown" || *entity.State == "unavailable" {
					return fmt.Errorf("%w: selected entity is not available in the catalog", ErrUnsupportedRule)
				}
				if revision.Intent.Kind == "correlation" && entity.EntityID == revision.Intent.EntityID && *entity.State != "on" && *entity.State != "off" {
					return fmt.Errorf("%w: source needs on/off states", ErrUnsupportedRule)
				}
				delete(required, entity.EntityID)
			}
		}
	}
	if len(required) > 0 {
		return fmt.Errorf("%w: selected entity is missing from catalog", ErrUnsupportedRule)
	}
	return nil
}

// One existing atomic file is the transaction boundary: either the old head is
// pending, or it is superseded and the new version exists. CAS is repeated here,
// under the store lock, so callers cannot commit against a stale observation.
func (s *DeviceStore) saveSuggestionRevision(id string, req SuggestionRevisionRequest) (*RuleSuggestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	suggestions, err := s.readSuggestions()
	if err != nil {
		return nil, err
	}
	for i := range suggestions {
		if suggestions[i].ID != id {
			continue
		}
		revision, err := validateSuggestionRevision(suggestions[i], req)
		if err != nil {
			return nil, err
		}
		token, err := randomAnalysisID()
		if err != nil {
			return nil, err
		}
		revision.ID = "revision-" + token
		for _, existing := range suggestions {
			if existing.ID == revision.ID {
				return nil, ErrSuggestionConflict
			}
		}
		suggestions[i].Status = "superseded"
		suggestions[i].SupersededBy = revision.ID
		suggestions = append(suggestions, revision)
		if err = s.writeSuggestions(suggestions); err != nil {
			return nil, err
		}
		return &revision, nil
	}
	return nil, ErrSuggestionNotFound
}

// requireUnattemptedSuggestion prevents a new version ID from bypassing a
// durable install whose suggestion status projection has not caught up.
func (m *Manager) requireUnattemptedSuggestion(id string) error {
	_, err := m.GetInstallOperationForSuggestion(id)
	if errors.Is(err, ErrInstallOperationNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrSuggestionConflict
}
