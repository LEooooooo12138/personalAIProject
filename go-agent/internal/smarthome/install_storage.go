package smarthome

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type installState struct {
	SchemaVersion int                      `json:"schema_version"`
	Operations    []InstallOperation       `json:"operations"`
	Managed       []ManagedAutomation      `json:"managed"`
	Management    []ManagementOperation    `json:"management,omitempty"`
	Drafts        []ManagedAutomationDraft `json:"drafts,omitempty"`
}

func (s *DeviceStore) readInstallState() (installState, error) {
	state := installState{SchemaVersion: 2}
	f, err := os.Open(filepath.Join(s.basePath, "rules", "install-state.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return state, err
	}
	if _, _, err := canonicalAutomationConfig(raw); err != nil {
		return state, fmt.Errorf("invalid install state")
	}
	if len(raw) > 8<<20 {
		return state, fmt.Errorf("install state exceeds limit")
	}
	state.SchemaVersion = 0
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, fmt.Errorf("invalid install state: %w", err)
	}
	if state.SchemaVersion != 1 && state.SchemaVersion != 2 {
		return state, fmt.Errorf("unsupported install schema")
	}
	if state.SchemaVersion == 2 {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope["operations"] == nil || envelope["managed"] == nil {
			return state, fmt.Errorf("incomplete install schema")
		}
	}
	ids := map[string]bool{}
	suggestions := map[string]bool{}
	for _, op := range state.Operations {
		_, digestErr := hex.DecodeString(op.ExpectedDigest)
		if op.SchemaVersion != 1 || op.ID == "" || ids[op.ID] || suggestions[op.SuggestionID] || !configKeyPattern.MatchString(op.ConfigKey) || op.SuggestionID == "" || op.SuggestionVersion == "" || op.CreatedAt.IsZero() || (op.ExpectedDigest != "" && (len(op.ExpectedDigest) != 64 || digestErr != nil)) || (op.OwnAttempt && (op.ExpectedDigest == "" || op.Create.AttemptedAt == nil)) {
			return state, fmt.Errorf("invalid install operation")
		}
		switch op.Status {
		case "applying", "unknown", "failed", "confirmed":
		default:
			return state, fmt.Errorf("invalid install status")
		}
		ids[op.ID] = true
		suggestions[op.SuggestionID] = true
	}
	managedIDs := map[string]bool{}
	for _, m := range state.Managed {
		if m.SchemaVersion != 1 || m.ID == "" || managedIDs[m.ID] || !validAutomationEntity(m.EntityID) || !ids[m.OperationID] {
			return state, fmt.Errorf("invalid managed record")
		}
		found := false
		for _, op := range state.Operations {
			if op.ID == m.OperationID {
				anchor := op.SuggestionID
				if op.AuthorizationSuggestionID != "" {
					anchor = op.AuthorizationSuggestionID
				}
				found = op.OwnAttempt && op.ManagedAutomationID == m.ID && op.ConfigKey == m.ConfigKey && op.ExpectedDigest == m.ExpectedDigest && anchor == m.SuggestionID && op.SuggestionVersion == m.SuggestionVersion
			}
		}
		if !found {
			return state, fmt.Errorf("invalid managed provenance")
		}
		managedIDs[m.ID] = true
	}
	for _, op := range state.Operations {
		if op.ManagedAutomationID != "" && !managedIDs[op.ManagedAutomationID] {
			return state, fmt.Errorf("missing managed provenance")
		}
		if op.Status == "confirmed" && (!op.OwnAttempt || op.ManagedAutomationID == "" || op.Observation == nil || !op.Observation.RuntimeLoadProof.Verified || op.Observation.Config.Digest != op.ExpectedDigest || op.Observation.RuntimeLoadProof.ExpectedDigest != op.ExpectedDigest) {
			return state, fmt.Errorf("invalid confirmation proof")
		}
	}
	if err := validateManagementState(state); err != nil {
		return state, err
	}
	return state, nil
}
func (s *DeviceStore) writeInstallState(state installState) error {
	if state.SchemaVersion == 1 {
		if err := s.backupInstallSchema1(); err != nil {
			return err
		}
		state.SchemaVersion = 2
	}
	if state.SchemaVersion != 2 {
		return fmt.Errorf("unsupported install schema")
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return fmt.Errorf("install state exceeds limit")
	}
	return atomicWrite(filepath.Join(s.basePath, "rules", "install-state.json"), raw)
}
func (s *DeviceStore) installOperation(id string, bySuggestion bool) (InstallOperation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, err := s.readInstallState()
	if err != nil {
		return InstallOperation{}, err
	}
	for _, op := range state.Operations {
		if (!bySuggestion && op.ID == id) || (bySuggestion && op.SuggestionID == id) {
			return op, nil
		}
	}
	return InstallOperation{}, ErrInstallOperationNotFound
}
func (s *DeviceStore) managedAutomation(id string) (ManagedAutomation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, err := s.readInstallState()
	if err != nil {
		return ManagedAutomation{}, err
	}
	for _, m := range state.Managed {
		if m.ID == id {
			return m, nil
		}
	}
	return ManagedAutomation{}, ErrInstallOperationNotFound
}
func replaceInstallOperation(state *installState, op InstallOperation) error {
	for i, old := range state.Operations {
		if old.ID == op.ID {
			if old.SuggestionID != op.SuggestionID || old.SuggestionVersion != op.SuggestionVersion || old.ConfigKey != op.ConfigKey || old.ExpectedDigest != op.ExpectedDigest || old.AuthorizationSuggestionID != op.AuthorizationSuggestionID || old.PreviousOperationID != op.PreviousOperationID || (old.OwnAttempt && !op.OwnAttempt) {
				return fmt.Errorf("install identity changed")
			}
			state.Operations[i] = op
			return nil
		}
		if old.SuggestionID == op.SuggestionID {
			return fmt.Errorf("duplicate install suggestion")
		}
	}
	state.Operations = append(state.Operations, op)
	return nil
}
func (s *DeviceStore) saveInstallOperation(op InstallOperation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readInstallState()
	if err != nil {
		return err
	}
	if err = replaceInstallOperation(&state, op); err != nil {
		return err
	}
	return s.writeInstallState(state)
}
func (s *DeviceStore) saveInstallManaged(op InstallOperation, m ManagedAutomation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readInstallState()
	if err != nil {
		return err
	}
	if err = replaceInstallOperation(&state, op); err != nil {
		return err
	}
	found := false
	for i, old := range state.Managed {
		if old.ID == m.ID {
			if old.OperationID != op.ID || old.DeletedAt != nil {
				return ErrManagementConflict
			}
			state.Managed[i] = m
			found = true
			break
		}
	}
	if !found {
		state.Managed = append(state.Managed, m)
	}
	return s.writeInstallState(state)
}

// This projection is derived exclusively from an already durable operation. It
// does not grant a suggestion status permission to dispatch external writes.
func (s *DeviceStore) projectInstallSuggestion(op InstallOperation) error {
	if op.PreviousOperationID != "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	suggestions, err := s.readSuggestions()
	if err != nil {
		return err
	}
	for i := range suggestions {
		if suggestions[i].ID == op.SuggestionID {
			suggestions[i].Status = op.Status
			suggestions[i].LastError = op.ErrorCode
			if op.Create.AttemptedAt != nil {
				suggestions[i].HAAutomationID = op.ConfigKey
			}
			return s.writeSuggestions(suggestions)
		}
	}
	return ErrSuggestionNotFound
}

// Recovery performs no HA traffic. Old failed conflated write/reload/archive
// failures, so neither failed nor applying may be replayed after migration.
func (s *DeviceStore) recoverInstallOperations() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readInstallState()
	if err != nil {
		return err
	}
	suggestions, err := s.readSuggestions()
	if err != nil {
		return err
	}
	changed := false
	rulesChanged := false
	for i := range state.Operations {
		if state.Operations[i].Status == "applying" {
			state.Operations[i].Status = "unknown"
			state.Operations[i].ErrorCode = "install_interrupted"
			state.Operations[i].UpdatedAt = time.Now().UTC()
			changed = true
		}
	}
	for i := range suggestions {
		var existing *InstallOperation
		for j := range state.Operations {
			if state.Operations[j].SuggestionID == suggestions[i].ID {
				existing = &state.Operations[j]
				break
			}
		}
		if existing != nil {
			if suggestions[i].Status != existing.Status && !(suggestions[i].Status == "confirmed" && !existing.OwnAttempt) {
				suggestions[i].Status = existing.Status
				suggestions[i].LastError = existing.ErrorCode
				rulesChanged = true
			}
			continue
		}
		switch suggestions[i].Status {
		case "applying", "failed", "confirmed":
		default:
			continue
		}
		op, e := newInstallOperation(suggestions[i], "")
		if e != nil {
			return e
		}
		op.Status = "unknown"
		op.Phase = "legacy"
		op.ErrorCode = "install_legacy_outcome_unknown"
		if suggestions[i].Status == "confirmed" {
			op.ErrorCode = "install_legacy_confirmed_unverified"
		} else {
			suggestions[i].Status = "unknown"
			suggestions[i].LastError = op.ErrorCode
			rulesChanged = true
		}
		state.Operations = append(state.Operations, op)
		changed = true
	}
	if rulesChanged {
		backup := filepath.Join(s.basePath, "rules", "suggestions.pre-install-v1.json")
		info, backupErr := os.Stat(backup)
		if backupErr == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("install migration backup is not a file")
		}
		if err = backupErr; errors.Is(err, os.ErrNotExist) {
			raw, e := os.ReadFile(filepath.Join(s.basePath, "rules", "suggestions.json"))
			if e != nil {
				return e
			}
			if e = atomicWrite(backup, raw); e != nil {
				return e
			}
		} else if err != nil {
			return err
		}
	}
	if changed {
		if err = s.writeInstallState(state); err != nil {
			return err
		}
	}
	if rulesChanged {
		return s.writeSuggestions(suggestions)
	}
	return nil
}
