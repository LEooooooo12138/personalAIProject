package smarthome

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// Called only under DeviceStore.mu. A preexisting different backup is ambiguous,
// so migration preserves both files and refuses to proceed.
func (s *DeviceStore) backupInstallSchema1() error {
	raw, err := os.ReadFile(filepath.Join(s.basePath, "rules", "install-state.json"))
	if err != nil {
		return err
	}
	backup := filepath.Join(s.basePath, "rules", "install-state.pre-management-v2.json")
	info, err := os.Lstat(backup)
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("install schema backup is not a regular file")
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) {
		if err = atomicWrite(backup, raw); err != nil {
			return err
		}
	}
	saved, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	if !bytes.Equal(saved, raw) {
		return fmt.Errorf("install schema backup mismatch")
	}
	return nil
}

func validManagementDigest(d string) bool {
	_, err := hex.DecodeString(d)
	return len(d) == 64 && err == nil
}
func validManagementID(id string) bool {
	_, err := hex.DecodeString(id)
	return len(id) == 32 && err == nil
}
func installAnchor(op InstallOperation) string {
	if op.AuthorizationSuggestionID != "" {
		return op.AuthorizationSuggestionID
	}
	return op.SuggestionID
}
func stateManaged(st *installState, id string) (*ManagedAutomation, error) {
	for i := range st.Managed {
		if st.Managed[i].ID == id {
			return &st.Managed[i], nil
		}
	}
	return nil, ErrManagementNotFound
}
func stateManagement(st *installState, id string) (*ManagementOperation, error) {
	for i := range st.Management {
		if st.Management[i].ID == id {
			return &st.Management[i], nil
		}
	}
	return nil, ErrManagementNotFound
}
func stateDraft(st *installState, id string) (*ManagedAutomationDraft, error) {
	for i := range st.Drafts {
		if st.Drafts[i].ID == id {
			return &st.Drafts[i], nil
		}
	}
	return nil, ErrManagementNotFound
}
func stateInstall(st *installState, id string) (*InstallOperation, error) {
	for i := range st.Operations {
		if st.Operations[i].ID == id {
			return &st.Operations[i], nil
		}
	}
	return nil, ErrManagementNotFound
}

// Schema2 validates lineage as a graph of immutable operations, never as caller
// supplied anchor strings. Management and installation share one atomic file.
func validateManagementState(st installState) error {
	invalid := func() error { return fmt.Errorf("invalid management state") }
	if st.SchemaVersion == 1 {
		if len(st.Management) > 0 || len(st.Drafts) > 0 {
			return invalid()
		}
		for _, op := range st.Operations {
			if op.PreviousOperationID != "" || op.AuthorizationSuggestionID != "" {
				return invalid()
			}
		}
		for _, m := range st.Managed {
			if m.DeletedAt != nil || m.DeletedBy != "" {
				return invalid()
			}
		}
		return nil
	}
	drafts := map[string]bool{}
	for _, d := range st.Drafts {
		if d.SchemaVersion != 1 || !validManagementID(d.ID) || drafts[d.ID] || d.Version.ID != d.ID || d.Version.Status != "managed_draft" || d.Version.Intent == nil || d.UserID == "" || d.SessionID == "" || d.CreatedAt.IsZero() || !validManagementDigest(d.ExpectedDigest) {
			return invalid()
		}
		old, err := stateInstall(&st, d.CurrentOperationID)
		if err != nil || old.ManagedAutomationID != d.ManagedID || old.SuggestionVersion != d.CurrentVersion || old.ExpectedDigest != d.ExpectedDigest || old.ConfigKey != d.ConfigKey || installAnchor(*old) != d.AuthorizationSuggestionID || d.Version.SourceSuggestionID != d.CurrentVersion {
			return invalid()
		}
		digest, err := managedVersionDigest(d.Version, d.ConfigKey)
		if err != nil || digest != d.Digest {
			return invalid()
		}
		drafts[d.ID] = true
	}
	ids := map[string]bool{}
	for _, op := range st.Management {
		if op.SchemaVersion != 1 || !validManagementID(op.ID) || ids[op.ID] || !validManagementRequest(op.AutomationActionRequest) || op.UserID == "" || op.SessionID == "" || op.CreatedAt.IsZero() || !op.ExpiresAt.Equal(op.CreatedAt.Add(5*time.Minute)) || !validAutomationEntity(op.EntityID) || op.ConcurrencyRisk != "external_edit_after_read" {
			return invalid()
		}
		old, err := stateInstall(&st, op.CurrentOperationID)
		if err != nil || old.ManagedAutomationID != op.ManagedID || old.ConfigKey != op.ConfigKey || old.SuggestionVersion != op.CurrentVersion || old.ExpectedDigest != op.ExpectedDigest {
			return invalid()
		}
		if _, err = stateManaged(&st, op.ManagedID); err != nil {
			return invalid()
		}
		switch op.Status {
		case "prepared", "expired", "cancelled":
			if op.Write.AttemptedAt != nil {
				return invalid()
			}
		case "applying", "unknown", "confirmed":
			if op.Write.AttemptedAt == nil {
				return invalid()
			}
		case "failed":
		default:
			return invalid()
		}
		if op.Write.AttemptedAt != nil && op.Action == "delete" && op.BackupRef != op.ID+".json" {
			return invalid()
		}
		if op.Action == "update" {
			d, e := stateDraft(&st, op.RevisionID)
			if e != nil || d.ManagedID != op.ManagedID || d.CurrentOperationID != op.CurrentOperationID || d.UserID != op.UserID || d.SessionID != op.SessionID || op.RevisionDigest != d.Digest {
				return invalid()
			}
			if op.Write.AttemptedAt != nil && op.InstallOperationID == "" {
				return invalid()
			}
			if op.InstallOperationID != "" {
				install, e := stateInstall(&st, op.InstallOperationID)
				if e != nil || install.PreviousOperationID != op.CurrentOperationID || install.SuggestionID != d.ID || install.ExpectedDigest != d.Digest || install.ManagedAutomationID != op.ManagedID {
					return invalid()
				}
			}
		} else if op.InstallOperationID != "" || op.RevisionDigest != "" {
			return invalid()
		}
		ids[op.ID] = true
	}
	for _, op := range st.Operations {
		if op.PreviousOperationID == "" {
			if op.AuthorizationSuggestionID != "" {
				return invalid()
			}
			continue
		}
		old, err := stateInstall(&st, op.PreviousOperationID)
		if err != nil || old.ID == op.ID || old.ConfigKey != op.ConfigKey || old.ManagedAutomationID != op.ManagedAutomationID || installAnchor(*old) != op.AuthorizationSuggestionID || op.SuggestionVersion != op.SuggestionID {
			return invalid()
		}
		d, err := stateDraft(&st, op.SuggestionID)
		if err != nil || d.CurrentOperationID != old.ID || d.AuthorizationSuggestionID != op.AuthorizationSuggestionID {
			return invalid()
		}
		found := false
		for _, a := range st.Management {
			if a.InstallOperationID == op.ID && a.RevisionID == d.ID && a.CurrentOperationID == old.ID && a.Write.AttemptedAt != nil {
				found = true
			}
		}
		if !found {
			return invalid()
		}
		// Detect cyclic predecessor chains explicitly rather than trusting timestamps.
		visited := map[string]bool{op.ID: true}
		cursor := old
		for {
			if visited[cursor.ID] {
				return invalid()
			}
			visited[cursor.ID] = true
			if cursor.PreviousOperationID == "" {
				break
			}
			cursor, err = stateInstall(&st, cursor.PreviousOperationID)
			if err != nil {
				return invalid()
			}
		}
	}
	for _, m := range st.Managed {
		if (m.DeletedAt == nil) != (m.DeletedBy == "") {
			return invalid()
		}
		if m.DeletedAt != nil {
			op, e := stateManagement(&st, m.DeletedBy)
			if e != nil || op.Action != "delete" || op.ManagedID != m.ID || op.Status != "confirmed" || op.Deletion == nil || !op.Deletion.ConfigAbsent || !op.Deletion.MappingAbsent || !op.Deletion.StateAbsent {
				return invalid()
			}
		}
	}
	return nil
}

func (s *DeviceStore) managementOperation(id string) (ManagementOperation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, err := s.readInstallState()
	if err != nil {
		return ManagementOperation{}, managementStoreError(err)
	}
	op, err := stateManagement(&st, id)
	if err != nil {
		return ManagementOperation{}, err
	}
	return *op, nil
}
func (s *DeviceStore) managementTransaction(fn func(*installState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readInstallState()
	if err != nil {
		return managementStoreError(err)
	}
	if err = fn(&st); err != nil {
		return err
	}
	if err = validateManagementState(st); err != nil {
		return managementStoreError(err)
	}
	if err = s.writeInstallState(st); err != nil {
		return managementStoreError(err)
	}
	return nil
}
func managementCurrent(st *installState, op ManagementOperation) error {
	m, err := stateManaged(st, op.ManagedID)
	if err != nil {
		return err
	}
	if m.DeletedAt != nil || m.OperationID != op.CurrentOperationID || m.ExpectedDigest != op.ExpectedDigest || m.SuggestionVersion != op.CurrentVersion || m.ConfigKey != op.ConfigKey || m.EntityID != op.EntityID {
		return ErrManagementConflict
	}
	return nil
}
func (s *DeviceStore) prepareManagement(op ManagementOperation) error {
	return s.managementTransaction(func(st *installState) error {
		if err := managementCurrent(st, op); err != nil {
			return err
		}
		for i := range st.Management {
			old := &st.Management[i]
			if old.ManagedID != op.ManagedID {
				continue
			}
			if old.Status == "prepared" && !time.Now().Before(old.ExpiresAt) {
				old.Status = "expired"
				old.UpdatedAt = time.Now().UTC()
			}
			if old.Status == "prepared" || old.Status == "applying" || old.Status == "unknown" {
				return ErrManagementConflict
			}
		}
		st.Management = append(st.Management, op)
		return nil
	})
}

// Duplicate reviews are rejected before any HA read. The final transaction
// repeats this guard and performs any elapsed prepared-review expiry atomically.
func (s *DeviceStore) managementPreparationAvailable(managedID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, err := s.readInstallState()
	if err != nil {
		return managementStoreError(err)
	}
	for _, op := range st.Management {
		if op.ManagedID == managedID && (op.Status == "applying" || op.Status == "unknown" || (op.Status == "prepared" && time.Now().Before(op.ExpiresAt))) {
			return ErrManagementConflict
		}
	}
	return nil
}
func replaceManagement(st *installState, op ManagementOperation) error {
	old, err := stateManagement(st, op.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(old.AutomationActionRequest, op.AutomationActionRequest) || old.UserID != op.UserID || old.SessionID != op.SessionID || old.CurrentOperationID != op.CurrentOperationID || old.CurrentVersion != op.CurrentVersion || old.ConfigKey != op.ConfigKey || old.EntityID != op.EntityID || old.RevisionDigest != op.RevisionDigest || !old.CreatedAt.Equal(op.CreatedAt) || !old.ExpiresAt.Equal(op.ExpiresAt) {
		return ErrManagementConflict
	}
	if old.Write.AttemptedAt != nil && (op.Write.AttemptedAt == nil || !old.Write.AttemptedAt.Equal(*op.Write.AttemptedAt) || op.Status == "prepared" || op.Status == "cancelled" || op.Status == "expired") {
		return ErrManagementConflict
	}
	if old.BackupRef != "" && old.BackupRef != op.BackupRef {
		return ErrManagementConflict
	}
	if old.InstallOperationID != "" && old.InstallOperationID != op.InstallOperationID {
		return ErrManagementConflict
	}
	op.UpdatedAt = time.Now().UTC()
	*old = op
	return nil
}
func (s *DeviceStore) saveManagement(op ManagementOperation) error {
	return s.managementTransaction(func(st *installState) error { return replaceManagement(st, op) })
}
func (s *DeviceStore) beginManagement(op *ManagementOperation) error {
	return s.managementTransaction(func(st *installState) error {
		if err := managementCurrent(st, *op); err != nil {
			return err
		}
		old, err := stateManagement(st, op.ID)
		if err != nil {
			return err
		}
		if old.Status != "prepared" || old.Write.AttemptedAt != nil {
			return ErrManagementConflict
		}
		if op.Action == "update" {
			d, e := stateDraft(st, op.RevisionID)
			if e != nil {
				return e
			}
			a, e := BuildAutomation(d.Version)
			if e != nil {
				return e
			}
			a.ID = op.ConfigKey
			raw, e := json.Marshal(a)
			if e != nil {
				return e
			}
			_, digest, e := canonicalAutomationConfig(raw)
			if e != nil {
				return e
			}
			install, e := newInstallOperation(d.Version, digest)
			if e != nil {
				return e
			}
			install.ConfigKey = op.ConfigKey
			install.AuthorizationSuggestionID = d.AuthorizationSuggestionID
			install.PreviousOperationID = op.CurrentOperationID
			install.ManagedAutomationID = op.ManagedID
			install.OwnAttempt = true
			install.Create = op.Write
			install.Reload.Status = "not_required"
			install.Phase = "create"
			op.InstallOperationID = install.ID
			if e = replaceInstallOperation(st, install); e != nil {
				return e
			}
		}
		return replaceManagement(st, *op)
	})
}
func (s *DeviceStore) finishManagement(op ManagementOperation) error {
	return s.managementTransaction(func(st *installState) error {
		m, err := stateManaged(st, op.ManagedID)
		if err != nil {
			return err
		}
		if m.OperationID != op.CurrentOperationID || (m.DeletedAt != nil && m.DeletedBy != op.ID) {
			return ErrManagementConflict
		}
		if op.Status == "confirmed" && op.Action == "delete" {
			now := time.Now().UTC()
			if m.DeletedAt == nil {
				m.DeletedAt = &now
			}
			m.DeletedBy = op.ID
		}
		if op.Observation != nil {
			m.LastObservation = *op.Observation
		}
		return replaceManagement(st, op)
	})
}
func (s *DeviceStore) saveManagementBackup(op ManagementOperation, raw json.RawMessage) (string, error) {
	canonical, digest, err := canonicalAutomationConfig(raw)
	if err != nil || digest != op.ExpectedDigest || !configHasID(canonical, op.ConfigKey) {
		return "", ErrManagementConflict
	}
	backup := struct {
		SchemaVersion int             `json:"schema_version"`
		OperationID   string          `json:"operation_id"`
		ManagedID     string          `json:"managed_id"`
		ConfigKey     string          `json:"config_key"`
		Digest        string          `json:"digest"`
		Config        json.RawMessage `json:"config"`
		Recovery      string          `json:"recovery"`
	}{1, op.ID, op.ManagedID, op.ConfigKey, digest, canonical, "Review this complete original configuration and current Home Assistant state. Restoring requires a NEW explicitly confirmed operation; this file never automatically restores or executes a rule. Deletion does not stop running actions or reverse physical effects."}
	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return "", managementStoreError(err)
	}
	path := filepath.Join(s.basePath, "rules", "management-backups", op.ID+".json")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", managementStoreError(err)
	}
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() {
			return "", managementStoreError(fmt.Errorf("backup is not a regular file"))
		}
		existing, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(existing, data) {
			return "", managementStoreError(fmt.Errorf("backup mismatch"))
		}
	} else if os.IsNotExist(e) {
		if err = atomicWrite(path, data); err != nil {
			return "", managementStoreError(err)
		}
	} else {
		return "", managementStoreError(e)
	}
	verified, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(verified, data) {
		return "", managementStoreError(fmt.Errorf("backup verification failed"))
	}
	return op.ID + ".json", nil
}

func (s *DeviceStore) recoverManagementOperations() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.readInstallState()
	if err != nil {
		return managementStoreError(err)
	}
	changed := false
	for i := range st.Management {
		op := &st.Management[i]
		if op.Status == "applying" {
			op.Status = "unknown"
			op.ErrorCode = "management_interrupted"
			op.UpdatedAt = time.Now().UTC()
			changed = true
		}
	}
	if changed {
		return s.writeInstallState(st)
	}
	return nil
}

func (s *DeviceStore) migrateManagementState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readInstallState()
	if err != nil {
		return err
	}
	if state.SchemaVersion == 1 {
		return s.writeInstallState(state)
	}
	return nil
}
