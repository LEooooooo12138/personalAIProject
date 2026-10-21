package smarthome

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// Isolated from suggestions.json: a draft can only be consumed by a separately
// confirmed managed update, with the same original sharing anchor and config key.
type ManagedAutomationDraft struct {
	SchemaVersion             int            `json:"schema_version"`
	ID                        string         `json:"id"`
	ManagedID                 string         `json:"managed_id"`
	AuthorizationSuggestionID string         `json:"authorization_suggestion_id"`
	CurrentOperationID        string         `json:"current_operation_id"`
	CurrentVersion            string         `json:"current_version"`
	ExpectedDigest            string         `json:"expected_digest"`
	Digest                    string         `json:"digest"`
	ConfigKey                 string         `json:"config_key"`
	UserID                    string         `json:"user_id"`
	SessionID                 string         `json:"session_id"`
	CreatedAt                 time.Time      `json:"created_at"`
	Version                   RuleSuggestion `json:"version"`
}

// This is a read-only projection helper, not management authorization. The
// caller authorizes the stable record.SuggestionID AND all returned entities.
func (m *Manager) GetManagedAutomationVersion(id string) (ManagedAutomation, RuleSuggestion, error) {
	if m.store == nil {
		return ManagedAutomation{}, RuleSuggestion{}, ErrManagementNotFound
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	st, err := m.store.readInstallState()
	if err != nil {
		return ManagedAutomation{}, RuleSuggestion{}, err
	}
	record, err := stateManaged(&st, id)
	if err != nil {
		return ManagedAutomation{}, RuleSuggestion{}, err
	}
	op, err := stateInstall(&st, record.OperationID)
	if err != nil {
		return *record, RuleSuggestion{}, err
	}
	var version RuleSuggestion
	if op.PreviousOperationID != "" {
		d, e := stateDraft(&st, record.SuggestionVersion)
		if e != nil {
			return *record, version, e
		}
		version = d.Version
	} else {
		suggestions, e := m.store.readSuggestions()
		if e != nil {
			return *record, version, e
		}
		found := false
		for _, s := range suggestions {
			if s.ID == record.SuggestionVersion {
				version = s
				found = true
				break
			}
		}
		if !found {
			return *record, version, ErrSuggestionNotFound
		}
	}
	a, err := BuildAutomation(version)
	if err != nil {
		return *record, RuleSuggestion{}, err
	}
	a.ID = record.ConfigKey
	raw, err := json.Marshal(a)
	if err != nil {
		return *record, RuleSuggestion{}, err
	}
	_, digest, err := canonicalAutomationConfig(raw)
	if err != nil || digest != record.ExpectedDigest {
		return *record, RuleSuggestion{}, ErrManagementConflict
	}
	version.Status = "confirmed"
	version.HAAutomationID = record.ConfigKey
	return *record, version, nil
}

func (m *Manager) ReviseManagedAutomation(ctx context.Context, id string, req SuggestionRevisionRequest) (ManagedAutomationDraft, error) {
	ctx, finish, authority, err := m.managementScope(ctx)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	defer finish()
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	record, source, err := m.GetManagedAutomationVersion(id)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	if record.DeletedAt != nil {
		return ManagedAutomationDraft{}, ErrManagementConflict
	}
	revision, err := validateRevisionContent(source, req)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	if err = m.validateRevisionCatalog(ctx, revision); err != nil {
		return ManagedAutomationDraft{}, err
	}
	if err = m.validateIntentEntities(ctx, revision); err != nil {
		return ManagedAutomationDraft{}, err
	}
	if _, err = m.freshManagementObservation(ctx, record); err != nil {
		return ManagedAutomationDraft{}, err
	}
	if _, err = managementAuthorization(ctx); err != nil {
		return ManagedAutomationDraft{}, err
	}
	token, err := randomAnalysisID()
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	revision.ID = token
	revision.Status = "managed_draft"
	digest, err := managedVersionDigest(revision, record.ConfigKey)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	d := ManagedAutomationDraft{SchemaVersion: 1, ID: token, ManagedID: id, AuthorizationSuggestionID: record.SuggestionID, CurrentOperationID: record.OperationID, CurrentVersion: record.SuggestionVersion, ExpectedDigest: record.ExpectedDigest, Digest: digest, ConfigKey: record.ConfigKey, UserID: authority.UserID, SessionID: authority.SessionID, CreatedAt: time.Now().UTC(), Version: revision}
	err = m.store.managementTransaction(func(st *installState) error {
		current, e := stateManaged(st, id)
		if e != nil {
			return e
		}
		if current.DeletedAt != nil || current.OperationID != d.CurrentOperationID {
			return ErrManagementConflict
		}
		for _, op := range st.Management {
			if op.ManagedID == id && (op.Status == "unknown" || op.Status == "applying") {
				return ErrManagementConflict
			}
		}
		st.Drafts = append(st.Drafts, d)
		return nil
	})
	return d, err
}

func (m *Manager) GetManagedAutomationDraft(ctx context.Context, id string) (ManagedAutomationDraft, error) {
	a, err := managementAuthorization(ctx)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	st, err := m.store.readInstallState()
	if err != nil {
		return ManagedAutomationDraft{}, managementStoreError(err)
	}
	d, err := stateDraft(&st, id)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	if d.UserID != a.UserID || d.SessionID != a.SessionID {
		return ManagedAutomationDraft{}, ErrManagementForbidden
	}
	return *d, nil
}

// Only the original reviewing identity may recover an unconsumed draft. A
// current installed version is resolved separately by GetManagedAutomationVersion.
func (m *Manager) GetLatestManagedAutomationDraft(ctx context.Context, managedID string) (ManagedAutomationDraft, error) {
	authority, err := managementAuthorization(ctx)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	st, err := m.store.readInstallState()
	if err != nil {
		return ManagedAutomationDraft{}, managementStoreError(err)
	}
	record, err := stateManaged(&st, managedID)
	if err != nil {
		return ManagedAutomationDraft{}, err
	}
	if record.DeletedAt != nil {
		return ManagedAutomationDraft{}, ErrManagementNotFound
	}
	var latest *ManagedAutomationDraft
	for i := range st.Drafts {
		draft := &st.Drafts[i]
		if draft.ManagedID != managedID || draft.CurrentOperationID != record.OperationID || draft.CurrentVersion != record.SuggestionVersion || draft.ExpectedDigest != record.ExpectedDigest || draft.UserID != authority.UserID || draft.SessionID != authority.SessionID {
			continue
		}
		if latest == nil || draft.CreatedAt.After(latest.CreatedAt) || (draft.CreatedAt.Equal(latest.CreatedAt) && draft.ID > latest.ID) {
			latest = draft
		}
	}
	if latest == nil {
		return ManagedAutomationDraft{}, ErrManagementNotFound
	}
	if err = ctx.Err(); err != nil {
		return ManagedAutomationDraft{}, err
	}
	return *latest, nil
}
func (m *Manager) validateManagementDraft(ctx context.Context, record ManagedAutomation, id string) error {
	d, err := m.GetManagedAutomationDraft(ctx, id)
	if err != nil {
		return err
	}
	if record.DeletedAt != nil || d.ManagedID != record.ID || d.CurrentOperationID != record.OperationID || d.CurrentVersion != record.SuggestionVersion || d.ExpectedDigest != record.ExpectedDigest || d.AuthorizationSuggestionID != record.SuggestionID {
		return ErrManagementConflict
	}
	_, source, err := m.GetManagedAutomationVersion(record.ID)
	if err != nil {
		return err
	}
	if _, err = validateRevisionContent(source, SuggestionRevisionRequest{ExpectedVersion: source.ID, Intent: *d.Version.Intent, PresenceBinding: d.Version.PresenceBinding}); err != nil {
		return err
	}
	if err = m.validateRevisionCatalog(ctx, d.Version); err != nil {
		return err
	}
	return m.validateIntentEntities(ctx, d.Version)
}

func managedVersionDigest(version RuleSuggestion, key string) (string, error) {
	a, err := BuildAutomation(version)
	if err != nil {
		return "", err
	}
	a.ID = key
	raw, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	_, digest, err := canonicalAutomationConfig(raw)
	return digest, err
}
func (m *Manager) dispatchManagedUpdate(ctx context.Context, op ManagementOperation) error {
	d, err := m.GetManagedAutomationDraft(ctx, op.RevisionID)
	if err != nil {
		return err
	}
	a, err := BuildAutomation(d.Version)
	if err != nil {
		return err
	}
	a.ID = op.ConfigKey
	// Draft loading and construction can outlive the earlier dispatch check.
	// Resolve authority and expiry again after that work, before the only POST.
	if _, err = managementAuthorization(ctx); err != nil {
		return err
	}
	if !time.Now().Before(op.ExpiresAt) {
		return ErrManagementExpired
	}
	return m.client.CreateAutomation(ctx, a)
}

func (m *Manager) reconcileManagedUpdate(ctx context.Context, op ManagementOperation) (ManagementOperation, error) {
	install, err := m.GetInstallOperation(op.InstallOperationID)
	if err != nil {
		return op, err
	}
	current, err := m.GetManagedAutomation(op.ManagedID)
	if err != nil {
		return op, err
	}
	if current.DeletedAt != nil || (current.OperationID != op.CurrentOperationID && current.OperationID != install.ID) {
		return op, ErrManagementConflict
	}
	observed, _ := m.observeAutomationConfigKey(ctx, op.ConfigKey, install.ExpectedDigest)
	op.Observation = &observed
	op.Status = "unknown"
	op.ErrorCode = "management_readback_unverified"
	install.Observation = &observed
	install.Status = "unknown"
	install.ErrorCode = "install_load_unverified"
	install.Phase = "reconcile"
	install.Create = op.Write
	if managementObservationMatches(observed, current, install.ExpectedDigest) {
		op.Status = "confirmed"
		op.ErrorCode = ""
		install.Status = "confirmed"
		install.ErrorCode = ""
		install.Phase = "archive"
		install.Archive = InstallArchive{Status: "pending"}
	}
	if _, err = managementAuthorization(ctx); err != nil {
		return op, err
	}
	err = m.store.managementTransaction(func(st *installState) error {
		record, e := stateManaged(st, op.ManagedID)
		if e != nil {
			return e
		}
		if record.DeletedAt != nil || (record.OperationID != op.CurrentOperationID && record.OperationID != install.ID) {
			return ErrManagementConflict
		}
		if e = replaceInstallOperation(st, install); e != nil {
			return e
		}
		if op.Status == "confirmed" {
			record.OperationID = install.ID
			record.SuggestionVersion = install.SuggestionVersion
			record.ExpectedDigest = install.ExpectedDigest
			record.EntityID = observed.EntityID
			record.MappingSource = observed.MappingSource
			record.LastObservation = observed
		}
		return replaceManagement(st, op)
	})
	if err != nil {
		return op, err
	}
	if op.Status == "confirmed" {
		return op, m.archiveManagedUpdate(op, install)
	}
	return op, nil
}

func (m *Manager) archiveManagedUpdate(op ManagementOperation, install InstallOperation) error {
	if install.Archive.Status == "complete" {
		return nil
	}
	// Local archive failure is an independent fact, never HA failure/retry permission.
	current, version, archiveErr := m.GetManagedAutomationVersion(op.ManagedID)
	if archiveErr == nil && (current.OperationID != install.ID || current.DeletedAt != nil) {
		return nil
	}
	if archiveErr == nil {
		archiveErr = atomicWrite(filepath.Join(m.store.basePath, "rules", version.ID+".md"), []byte(RuleDocument(version)))
	}
	if archiveErr != nil {
		install.Archive.ErrorCode = "install_archive_pending"
	} else {
		install.Archive.Status = "complete"
		install.Archive.ErrorCode = ""
		install.Phase = "complete"
	}
	if err := m.store.saveInstallOperation(install); err != nil {
		return fmt.Errorf("%w: update archive record", ErrManagementStore)
	}
	return nil
}
