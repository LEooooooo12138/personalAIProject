package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"
)

var ErrInstallOperationNotFound = errors.New("install operation not found")
var ErrInstallPreexisting = errors.New("unowned automation already exists")

// AttemptedAt is persisted BEFORE dispatch. A missing response never means no write.
type InstallStage struct {
	AttemptedAt *time.Time `json:"attempted_at,omitempty"`
	Status      string     `json:"status"`
	ErrorCode   string     `json:"error_code,omitempty"`
}
type InstallArchive struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
}
type InstallOperation struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	SuggestionID  string `json:"suggestion_id"`
	// Immutable suggestion IDs are version identities; revisions receive new IDs.
	SuggestionVersion         string                      `json:"suggestion_version"`
	ConfigKey                 string                      `json:"config_key"`
	ExpectedDigest            string                      `json:"expected_digest,omitempty"`
	Phase                     string                      `json:"phase"`
	Status                    string                      `json:"status"`
	ErrorCode                 string                      `json:"error_code,omitempty"`
	CreatedAt                 time.Time                   `json:"created_at"`
	UpdatedAt                 time.Time                   `json:"updated_at"`
	OwnAttempt                bool                        `json:"own_attempt"`
	Preflight                 AutomationConfigObservation `json:"preflight"`
	Create                    InstallStage                `json:"create"`
	Reload                    InstallStage                `json:"reload"`
	Observation               *AutomationObservation      `json:"observation,omitempty"`
	Archive                   InstallArchive              `json:"archive"`
	ManagedAutomationID       string                      `json:"managed_automation_id,omitempty"`
	AuthorizationSuggestionID string                      `json:"authorization_suggestion_id,omitempty"`
	PreviousOperationID       string                      `json:"previous_operation_id,omitempty"`
}
type ManagedAutomation struct {
	SchemaVersion     int                   `json:"schema_version"`
	ID                string                `json:"id"`
	OperationID       string                `json:"operation_id"`
	SuggestionID      string                `json:"suggestion_id"`
	SuggestionVersion string                `json:"suggestion_version"`
	ConfigKey         string                `json:"config_key"`
	EntityID          string                `json:"entity_id"`
	ExpectedDigest    string                `json:"expected_digest"`
	MappingSource     string                `json:"mapping_source"`
	LastObservation   AutomationObservation `json:"last_observation"`
	DeletedAt         *time.Time            `json:"deleted_at,omitempty"`
	DeletedBy         string                `json:"deleted_by,omitempty"`
}

func newInstallOperation(s RuleSuggestion, digest string) (InstallOperation, error) {
	id, err := randomAnalysisID()
	now := time.Now().UTC()
	return InstallOperation{SchemaVersion: 1, ID: id, SuggestionID: s.ID, SuggestionVersion: s.ID, ConfigKey: s.ID, ExpectedDigest: digest, Phase: "preflight", Status: "applying", CreatedAt: now, UpdatedAt: now, Create: InstallStage{Status: "not_attempted"}, Reload: InstallStage{Status: "not_attempted"}, Archive: InstallArchive{Status: "not_ready"}}, err
}
func (m *Manager) GetInstallOperation(id string) (InstallOperation, error) {
	return m.store.installOperation(id, false)
}
func (m *Manager) GetInstallOperationForSuggestion(id string) (InstallOperation, error) {
	return m.store.installOperation(id, true)
}
func (m *Manager) GetManagedAutomation(id string) (ManagedAutomation, error) {
	if m.store == nil {
		return ManagedAutomation{}, ErrInstallOperationNotFound
	}
	return m.store.managedAutomation(id)
}

func (m *Manager) applyInstallation(ctx context.Context, s *RuleSuggestion, a AutomationConfig) (*RuleSuggestion, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	_, digest, err := canonicalAutomationConfig(raw)
	if err != nil {
		return nil, err
	}
	// Read-only failures leave the original pending suggestion reviewable. No
	// operation exists until preflight passes; existing operations never enter here.
	preflight, err := m.client.ReadAutomationConfig(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: installation preflight: %w", ErrHARequest, err)
	}
	if preflight.Exists == nil {
		return nil, fmt.Errorf("%w: installation preflight unavailable", ErrHARequest)
	}
	if *preflight.Exists {
		return nil, ErrInstallPreexisting
	}
	// Editable-file absence alone does not exclude package/include/runtime rules.
	// Registry failure is unknown; an existing exact tuple is unowned, never overwritten.
	release, registryErr := m.client.acquireObservation(ctx)
	var entities []RegistryEntity
	if registryErr == nil {
		entities, registryErr = m.client.readAutomationRegistry(ctx)
		release()
	}
	occupied := false
	for _, entity := range entities {
		if entity.Platform != nil && *entity.Platform == "automation" && entity.UniqueID != nil && *entity.UniqueID == a.ID {
			occupied = true
		}
	}
	if registryErr != nil {
		return nil, fmt.Errorf("%w: installation preflight: %w", ErrHARequest, registryErr)
	}
	if occupied {
		return nil, ErrInstallPreexisting
	}
	op, err := newInstallOperation(*s, digest)
	if err != nil {
		return nil, err
	}
	op.Preflight = preflight
	now := time.Now().UTC()
	op.OwnAttempt = true
	op.Phase = "create"
	op.Create = InstallStage{Status: "unknown", AttemptedAt: &now}
	if err = m.store.saveInstallOperation(op); err != nil {
		return nil, err
	}
	if err = m.store.projectInstallSuggestion(op); err != nil {
		return nil, err
	}
	err = m.client.CreateAutomation(ctx, a)
	if err != nil {
		op.Status = "unknown"
		op.ErrorCode = "install_create_unknown"
		op.Create.ErrorCode = HAErrorCode(err)
		var status *haHTTPStatusError
		if errors.As(err, &status) && (status.status == http.StatusBadRequest || status.status == http.StatusUnauthorized || status.status == http.StatusForbidden) {
			observed, readErr := m.client.ReadAutomationConfig(ctx, op.ConfigKey)
			if readErr == nil && observed.Exists != nil && !*observed.Exists {
				op.Status = "failed"
				op.ErrorCode = "install_create_rejected"
				op.Create.Status = "rejected"
			}
		}
		if saveErr := m.persistInstallResult(&op); saveErr != nil {
			return nil, saveErr
		}
		return m.findSuggestion(s.ID)
	}
	op.Create.Status = "acknowledged"
	op.Phase = "reload"
	now = time.Now().UTC()
	op.Reload = InstallStage{Status: "unknown", AttemptedAt: &now}
	if err = m.store.saveInstallOperation(op); err != nil {
		return nil, err
	}
	if err = m.client.ReloadAutomations(ctx); err != nil {
		op.Status = "unknown"
		op.ErrorCode = "install_reload_unknown"
		op.Reload.ErrorCode = HAErrorCode(err)
		if saveErr := m.persistInstallResult(&op); saveErr != nil {
			return nil, saveErr
		}
		return m.findSuggestion(s.ID)
	}
	op.Reload.Status = "acknowledged"
	op.Phase = "reconcile"
	if err = m.store.saveInstallOperation(op); err != nil {
		return nil, err
	}
	if _, err = m.reconcileInstallation(ctx, op); err != nil {
		return nil, err
	}
	return m.findSuggestion(s.ID)
}
func (m *Manager) persistInstallResult(op *InstallOperation) error {
	op.UpdatedAt = time.Now().UTC()
	if err := m.store.saveInstallOperation(*op); err != nil {
		return err
	}
	return m.store.projectInstallSuggestion(*op)
}

// Reconciliation only reads HA. It may finish local records and archive, never create/reload.
func (m *Manager) ReconcileInstallation(ctx context.Context, id string) (InstallOperation, error) {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	op, err := m.GetInstallOperation(id)
	if err != nil {
		return op, err
	}
	return m.reconcileInstallation(ctx, op)
}
func (m *Manager) reconcileInstallation(ctx context.Context, op InstallOperation) (InstallOperation, error) {
	if err := ctx.Err(); err != nil {
		return op, err
	}
	if op.PreviousOperationID != "" {
		// Managed updates have their own authority-bound read-only reconciliation.
		// The ordinary installation route cannot bypass that boundary.
		return op, nil
	}
	if op.ManagedAutomationID != "" {
		current, err := m.GetManagedAutomation(op.ManagedAutomationID)
		if err != nil {
			return op, err
		}
		if current.OperationID != op.ID || current.DeletedAt != nil {
			return op, nil
		}
	}
	if !op.OwnAttempt || op.ExpectedDigest == "" || op.Create.Status == "rejected" {
		return op, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	observation, _ := m.observeAutomationConfigKey(bounded, op.ConfigKey, op.ExpectedDigest)
	op.Observation = &observation
	op.Phase = "reconcile"
	op.Status = "unknown"
	op.ErrorCode = "install_load_unverified"
	if observation.Config.Digest != "" && observation.Config.Digest != op.ExpectedDigest {
		op.ErrorCode = "install_config_drift"
	}
	matched := observation.Config.SourceStatus == SourceAvailable && observation.Config.Exists != nil && *observation.Config.Exists && observation.Config.Digest == op.ExpectedDigest && observation.MappingStatus == SourceAvailable && observation.MappingErrorCode == "" && validAutomationEntity(observation.EntityID) && observation.RuntimeLoadProof.Verified && observation.RuntimeLoadProof.ExpectedDigest == op.ExpectedDigest
	if matched {
		op.Status = "confirmed"
		op.ErrorCode = ""
		op.Phase = "archive"
		if op.ManagedAutomationID == "" {
			id, err := randomAnalysisID()
			if err != nil {
				return op, err
			}
			op.ManagedAutomationID = id
		}
		managed := ManagedAutomation{SchemaVersion: 1, ID: op.ManagedAutomationID, OperationID: op.ID, SuggestionID: op.SuggestionID, SuggestionVersion: op.SuggestionVersion, ConfigKey: op.ConfigKey, EntityID: observation.EntityID, ExpectedDigest: op.ExpectedDigest, MappingSource: observation.MappingSource, LastObservation: observation}
		op.Archive = InstallArchive{Status: "pending"}
		// Record verified external facts and ownership atomically before attempting local archive.
		op.UpdatedAt = time.Now().UTC()
		if err := m.store.saveInstallManaged(op, managed); err != nil {
			return op, err
		}
		s, err := m.findSuggestion(op.SuggestionID)
		if err == nil {
			var a AutomationConfig
			a, err = BuildAutomation(*s)
			if err == nil {
				raw, _ := json.Marshal(a)
				_, digest, e := canonicalAutomationConfig(raw)
				if e != nil || digest != op.ExpectedDigest {
					err = fmt.Errorf("archive intent changed")
				}
			}
			if err == nil {
				s.HAAutomationID = op.ConfigKey
				err = atomicWrite(filepath.Join(m.store.basePath, "rules", s.ID+".md"), []byte(RuleDocument(*s)))
			}
		}
		if err != nil {
			op.Archive.ErrorCode = "install_archive_pending"
		} else {
			op.Archive.Status = "complete"
			op.Phase = "complete"
		}
	}
	return op, m.persistInstallResult(&op)
}
