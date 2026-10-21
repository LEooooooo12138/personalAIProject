package smarthome

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	ErrManagementForbidden = errors.New("management_forbidden")
	ErrManagementConflict  = errors.New("management_conflict")
	ErrManagementExpired   = errors.New("management_expired")
	ErrManagementInvalid   = errors.New("management_invalid")
	ErrManagementNotFound  = errors.New("management_not_found")
	ErrManagementStore     = errors.New("management_store_unavailable")
)

// Authority is supplied by trusted application code, never decoded from a body.
// Check must re-resolve the administrator and session and honor cancellation.
type ManagementAuthority struct {
	UserID, SessionID string
	Check             func(context.Context) error
}
type managementAuthorityKey struct{}

func WithManagementAuthority(ctx context.Context, authority ManagementAuthority) context.Context {
	return context.WithValue(ctx, managementAuthorityKey{}, authority)
}
func managementAuthorization(ctx context.Context) (ManagementAuthority, error) {
	if err := ctx.Err(); err != nil {
		return ManagementAuthority{}, err
	}
	a, ok := ctx.Value(managementAuthorityKey{}).(ManagementAuthority)
	if !ok || a.UserID == "" || a.SessionID == "" || len(a.UserID) > 128 || len(a.SessionID) > 128 || a.Check == nil {
		return a, ErrManagementForbidden
	}
	if err := a.Check(ctx); err != nil {
		return a, ErrManagementForbidden
	}
	return a, ctx.Err()
}
func (m *Manager) managementScope(ctx context.Context) (context.Context, func(), ManagementAuthority, error) {
	a, err := managementAuthorization(ctx)
	if err != nil {
		return nil, nil, a, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	job, finish, err := m.acquireJob(JobScope{Context: bounded, Release: cancel, OwnerID: a.UserID})
	return job, finish, a, err
}

type AutomationActionRequest struct {
	ManagedID      string `json:"managed_id"`
	Action         string `json:"action"`
	ExpectedDigest string `json:"expected_digest"`
	RevisionID     string `json:"revision_id,omitempty"`
	StopActions    *bool  `json:"stop_actions,omitempty"`
}
type ManagementDeletionObservation struct {
	ConfigAbsent  bool      `json:"config_absent"`
	MappingAbsent bool      `json:"mapping_absent"`
	StateAbsent   bool      `json:"state_absent"`
	CheckedAt     time.Time `json:"checked_at"`
}
type ManagementOperation struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	AutomationActionRequest
	UserID             string                         `json:"user_id"`
	SessionID          string                         `json:"session_id"`
	CurrentOperationID string                         `json:"current_operation_id"`
	CurrentVersion     string                         `json:"current_version"`
	ConfigKey          string                         `json:"config_key"`
	EntityID           string                         `json:"entity_id"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
	ExpiresAt          time.Time                      `json:"expires_at"`
	Status             string                         `json:"status"`
	ErrorCode          string                         `json:"error_code,omitempty"`
	Write              InstallStage                   `json:"write"`
	Observation        *AutomationObservation         `json:"observation,omitempty"`
	Deletion           *ManagementDeletionObservation `json:"deletion,omitempty"`
	BackupRef          string                         `json:"backup_ref,omitempty"`
	InstallOperationID string                         `json:"install_operation_id,omitempty"`
	RevisionDigest     string                         `json:"revision_digest,omitempty"`
	// HA has no compare-and-swap; a read-then-write external edit race remains.
	ConcurrencyRisk string `json:"concurrency_risk"`
}

func validManagementRequest(req AutomationActionRequest) bool {
	if req.ManagedID == "" || !validManagementDigest(req.ExpectedDigest) {
		return false
	}
	switch req.Action {
	case "enable", "delete":
		return req.StopActions == nil && req.RevisionID == ""
	case "disable":
		return req.StopActions != nil && req.RevisionID == ""
	case "update":
		return req.StopActions == nil && req.RevisionID != ""
	}
	return false
}
func managementObservationMatches(o AutomationObservation, record ManagedAutomation, digest string) bool {
	return record.DeletedAt == nil && o.Config.SourceStatus == SourceAvailable && o.Config.Exists != nil && *o.Config.Exists && o.Config.Digest == digest && o.Config.ConfigKey == record.ConfigKey && o.MappingStatus == SourceAvailable && o.MappingErrorCode == "" && o.MappingSource == "config/entity_registry/list" && o.EntityID == record.EntityID && validAutomationEntity(o.EntityID) && o.RuntimeLoadProof.Verified && o.RuntimeLoadProof.ExpectedDigest == digest
}
func (m *Manager) freshManagementObservation(ctx context.Context, record ManagedAutomation) (AutomationObservation, error) {
	o, err := m.observeAutomationConfigKey(ctx, record.ConfigKey, record.ExpectedDigest)
	if err != nil {
		return o, err
	}
	if !managementObservationMatches(o, record, record.ExpectedDigest) {
		return o, ErrManagementConflict
	}
	return o, nil
}

func (m *Manager) PrepareAutomationAction(ctx context.Context, req AutomationActionRequest) (ManagementOperation, error) {
	ctx, finish, a, err := m.managementScope(ctx)
	if err != nil {
		return ManagementOperation{}, err
	}
	defer finish()
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	if !validManagementRequest(req) {
		return ManagementOperation{}, ErrManagementInvalid
	}
	record, err := m.GetManagedAutomation(req.ManagedID)
	if err != nil {
		return ManagementOperation{}, err
	}
	if record.DeletedAt != nil || record.ExpectedDigest != req.ExpectedDigest {
		return ManagementOperation{}, ErrManagementConflict
	}
	if err = m.store.managementPreparationAvailable(record.ID); err != nil {
		return ManagementOperation{}, err
	}
	// Draft content is checked separately; this observation proves only the old version.
	if req.Action == "update" {
		if err = m.validateManagementDraft(ctx, record, req.RevisionID); err != nil {
			return ManagementOperation{}, err
		}
	}
	var revisionDigest string
	if req.Action == "update" {
		draft, e := m.GetManagedAutomationDraft(ctx, req.RevisionID)
		if e != nil {
			return ManagementOperation{}, e
		}
		revisionDigest = draft.Digest
	}
	observed, err := m.freshManagementObservation(ctx, record)
	if err != nil {
		return ManagementOperation{}, err
	}
	if _, err = managementAuthorization(ctx); err != nil {
		return ManagementOperation{}, err
	}
	id, err := randomAnalysisID()
	if err != nil {
		return ManagementOperation{}, err
	}
	now := time.Now().UTC()
	if req.StopActions != nil {
		v := *req.StopActions
		req.StopActions = &v
	}
	op := ManagementOperation{SchemaVersion: 1, ID: id, AutomationActionRequest: req, UserID: a.UserID, SessionID: a.SessionID, CurrentOperationID: record.OperationID, CurrentVersion: record.SuggestionVersion, ConfigKey: record.ConfigKey, EntityID: record.EntityID, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(5 * time.Minute), Status: "prepared", Write: InstallStage{Status: "not_attempted"}, Observation: &observed, ConcurrencyRisk: "external_edit_after_read"}
	op.RevisionDigest = revisionDigest
	return op, m.store.prepareManagement(op)
}

// GET is local-only and does not reconcile, expire or replay anything.
func (m *Manager) GetAutomationAction(ctx context.Context, id string) (ManagementOperation, error) {
	_, err := managementAuthorization(ctx)
	if err != nil {
		return ManagementOperation{}, err
	}
	op, err := m.store.managementOperation(id)
	if err != nil {
		return op, err
	}
	return op, nil
}

// Recovery can start with a known managed ID even when the browser lost the
// prepare/confirm response. No remote reads, expiry transition or retry occurs.
func (m *Manager) GetLatestAutomationAction(ctx context.Context, managedID string) (ManagementOperation, error) {
	if _, err := managementAuthorization(ctx); err != nil {
		return ManagementOperation{}, err
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	st, err := m.store.readInstallState()
	if err != nil {
		return ManagementOperation{}, managementStoreError(err)
	}
	if _, err = stateManaged(&st, managedID); err != nil {
		return ManagementOperation{}, err
	}
	var latest *ManagementOperation
	for i := range st.Management {
		candidate := &st.Management[i]
		if candidate.ManagedID == managedID && (latest == nil || candidate.CreatedAt.After(latest.CreatedAt) || (candidate.CreatedAt.Equal(latest.CreatedAt) && candidate.ID > latest.ID)) {
			latest = candidate
		}
	}
	if latest == nil {
		return ManagementOperation{}, ErrManagementNotFound
	}
	if err = ctx.Err(); err != nil {
		return ManagementOperation{}, err
	}
	return *latest, nil
}
func (m *Manager) CancelAutomationAction(ctx context.Context, id string) (ManagementOperation, error) {
	ctx, finish, _, err := m.managementScope(ctx)
	if err != nil {
		return ManagementOperation{}, err
	}
	defer finish()
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	op, err := m.GetAutomationAction(ctx, id)
	if err != nil {
		return op, err
	}
	if op.Status != "prepared" {
		return op, ErrManagementConflict
	}
	op.Status = "cancelled"
	if !time.Now().Before(op.ExpiresAt) {
		op.Status = "expired"
	}
	return op, m.store.saveManagement(op)
}
func (m *Manager) ConfirmAutomationAction(ctx context.Context, id string) (ManagementOperation, error) {
	ctx, finish, authority, err := m.managementScope(ctx)
	if err != nil {
		return ManagementOperation{}, err
	}
	defer finish()
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	op, err := m.GetAutomationAction(ctx, id)
	if err != nil {
		return op, err
	}
	if op.UserID != authority.UserID || op.SessionID != authority.SessionID {
		return ManagementOperation{}, ErrManagementForbidden
	}
	if op.Status != "prepared" {
		return op, ErrManagementConflict
	}
	if !time.Now().Before(op.ExpiresAt) {
		op.Status = "expired"
		if err = m.store.saveManagement(op); err != nil {
			return op, err
		}
		return op, ErrManagementExpired
	}
	record, err := m.GetManagedAutomation(op.ManagedID)
	if err != nil {
		return op, err
	}
	if record.DeletedAt != nil || record.OperationID != op.CurrentOperationID || record.ExpectedDigest != op.ExpectedDigest || record.SuggestionVersion != op.CurrentVersion || record.ConfigKey != op.ConfigKey || record.EntityID != op.EntityID {
		return op, ErrManagementConflict
	}
	if op.Action == "update" {
		if err = m.validateManagementDraft(ctx, record, op.RevisionID); err != nil {
			return op, err
		}
	}
	observed, err := m.freshManagementObservation(ctx, record)
	if err != nil {
		return op, err
	}
	if observed.Config.ConfigKey != op.ConfigKey || observed.EntityID != op.EntityID {
		return op, ErrManagementConflict
	}
	op.Observation = &observed
	if op.Action == "delete" {
		if op.BackupRef, err = m.store.saveManagementBackup(op, observed.Config.RawConfig); err != nil {
			return op, err
		}
		if err = m.store.saveManagement(op); err != nil {
			return op, err
		}
	}
	if _, err = managementAuthorization(ctx); err != nil {
		return op, err
	}
	if !time.Now().Before(op.ExpiresAt) {
		op.Status = "expired"
		if err = m.store.saveManagement(op); err != nil {
			return op, err
		}
		return op, ErrManagementExpired
	}
	now := time.Now().UTC()
	op.Status = "applying"
	op.Write = InstallStage{AttemptedAt: &now, Status: "unknown"}
	if err = m.store.beginManagement(&op); err != nil {
		return op, err
	}
	// Persisting the attempt may itself take time. Re-resolve immediately before
	// dispatch too; an attempt record never grants authority to a later caller.
	if _, err = managementAuthorization(ctx); err != nil {
		return op, err
	}
	if !time.Now().Before(op.ExpiresAt) {
		return op, ErrManagementExpired
	}
	// Token acquisition may block after the explicit guard. Carry the review
	// deadline through transport, while keeping read-only verification on ctx.
	writeCtx, cancelWrite := context.WithDeadline(ctx, op.ExpiresAt)
	switch op.Action {
	case "enable":
		err = m.client.enableManagedAutomation(writeCtx, op.EntityID)
	case "disable":
		err = m.client.disableManagedAutomation(writeCtx, op.EntityID, *op.StopActions)
	case "delete":
		err = m.client.deleteManagedAutomation(writeCtx, op.ConfigKey)
	case "update":
		err = m.dispatchManagedUpdate(writeCtx, op)
	}
	cancelWrite()
	op.Status = "unknown"
	if err != nil {
		op.ErrorCode = "management_write_unknown"
		op.Write.ErrorCode = HAErrorCode(err)
		if saveErr := m.store.saveManagement(op); saveErr != nil {
			return op, saveErr
		}
		return op, nil
	}
	op.Write.Status = "acknowledged"
	if err = m.store.saveManagement(op); err != nil {
		return op, err
	}
	return m.reconcileManagement(ctx, op)
}
func (m *Manager) ReconcileAutomationAction(ctx context.Context, id string) (ManagementOperation, error) {
	ctx, finish, _, err := m.managementScope(ctx)
	if err != nil {
		return ManagementOperation{}, err
	}
	defer finish()
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	op, err := m.GetAutomationAction(ctx, id)
	if err != nil {
		return op, err
	}
	if op.Write.AttemptedAt == nil || (op.Status != "unknown" && op.Status != "applying" && op.Status != "confirmed") {
		return op, ErrManagementConflict
	}
	// A completed action is a historical fact. Later enable/disable/delete or
	// external edits belong to new observations, not a reopened old attempt.
	if op.Status == "confirmed" {
		if op.Action == "update" {
			install, err := m.GetInstallOperation(op.InstallOperationID)
			if err != nil {
				return op, err
			}
			return op, m.archiveManagedUpdate(op, install)
		}
		return op, nil
	}
	return m.reconcileManagement(ctx, op)
}
func (m *Manager) reconcileManagement(ctx context.Context, op ManagementOperation) (ManagementOperation, error) {
	if op.Action == "update" {
		return m.reconcileManagedUpdate(ctx, op)
	}
	record, err := m.GetManagedAutomation(op.ManagedID)
	if err != nil {
		return op, err
	}
	if record.OperationID != op.CurrentOperationID || (record.DeletedAt != nil && record.DeletedBy != op.ID) {
		return op, ErrManagementConflict
	}
	observed, _ := m.observeAutomationConfigKey(ctx, op.ConfigKey, op.ExpectedDigest)
	op.Observation = &observed
	op.Status = "unknown"
	op.ErrorCode = "management_readback_unverified"
	if op.Action == "delete" {
		deletion := ManagementDeletionObservation{CheckedAt: time.Now().UTC(), ConfigAbsent: observed.Config.SourceStatus == SourceAvailable && observed.Config.Exists != nil && !*observed.Config.Exists, MappingAbsent: observed.MappingStatus == SourceAvailable && observed.MappingErrorCode == "ha_mapping_unknown"}
		if deletion.ConfigAbsent && deletion.MappingAbsent {
			_, stateErr := m.client.GetState(ctx, op.EntityID)
			var status *haHTTPStatusError
			deletion.StateAbsent = errors.As(stateErr, &status) && status.status == http.StatusNotFound
		}
		op.Deletion = &deletion
		if deletion.ConfigAbsent && deletion.MappingAbsent && deletion.StateAbsent {
			op.Status = "confirmed"
			op.ErrorCode = ""
		}
	} else if managementObservationMatches(observed, record, op.ExpectedDigest) && ((op.Action == "enable" && observed.State == "on") || (op.Action == "disable" && observed.State == "off")) {
		op.Status = "confirmed"
		op.ErrorCode = ""
	}
	if _, err = managementAuthorization(ctx); err != nil {
		return op, err
	}
	return op, m.store.finishManagement(op)
}

func managementStoreError(err error) error { return fmt.Errorf("%w: %v", ErrManagementStore, err) }
