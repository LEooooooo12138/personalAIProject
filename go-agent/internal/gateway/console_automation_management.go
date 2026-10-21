package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

// These projections deliberately exclude durable authorship, HA config keys,
// raw observations/configuration, backup locations and upstream error strings.
type consoleManagedVersionView struct {
	ID              string                               `json:"id"`
	Intent          *smarthome.SuggestionIntent          `json:"intent"`
	PresenceBinding *smarthome.PresenceBinding           `json:"presence_binding,omitempty"`
	Diff            []smarthome.SuggestionRevisionChange `json:"diff"`
}
type consoleManagedDraftView struct {
	ID                 string                    `json:"id"`
	ManagedID          string                    `json:"managed_id"`
	CurrentOperationID string                    `json:"current_operation_id"`
	CurrentVersion     string                    `json:"current_version"`
	ExpectedDigest     string                    `json:"expected_digest"`
	Digest             string                    `json:"digest"`
	CreatedAt          time.Time                 `json:"created_at"`
	Version            consoleManagedVersionView `json:"version"`
}
type consoleManagementOperationView struct {
	ID                 string                                   `json:"id"`
	ManagedID          string                                   `json:"managed_id"`
	Action             string                                   `json:"action"`
	Status             string                                   `json:"status"`
	CurrentOperationID string                                   `json:"current_operation_id"`
	CurrentVersion     string                                   `json:"current_version"`
	ExpectedDigest     string                                   `json:"expected_digest"`
	RevisionID         string                                   `json:"revision_id,omitempty"`
	Revision           *consoleManagedVersionView               `json:"revision,omitempty"`
	StopActions        *bool                                    `json:"stop_actions,omitempty"`
	CreatedAt          time.Time                                `json:"created_at"`
	UpdatedAt          time.Time                                `json:"updated_at"`
	ExpiresAt          time.Time                                `json:"expires_at"`
	AttemptedAt        *time.Time                               `json:"attempted_at,omitempty"`
	WriteStatus        string                                   `json:"write_status"`
	ErrorCode          string                                   `json:"error_code,omitempty"`
	BackupStatus       string                                   `json:"backup_status"`
	ArchiveStatus      string                                   `json:"archive_status"`
	Installation       *consoleInstallOperationView             `json:"installation,omitempty"`
	Deletion           *smarthome.ManagementDeletionObservation `json:"deletion,omitempty"`
	ObservedState      string                                   `json:"observed_state,omitempty"`
	ObservedAt         *time.Time                               `json:"observed_at,omitempty"`
	CanConfirm         bool                                     `json:"can_confirm"`
	CanCancel          bool                                     `json:"can_cancel"`
	CanReconcile       bool                                     `json:"can_reconcile"`
	ConcurrencyRisk    string                                   `json:"concurrency_risk"`
}
type consoleManagementView struct {
	ManagedID          string                          `json:"managed_id"`
	SuggestionID       string                          `json:"suggestion_id"`
	CurrentOperationID string                          `json:"current_operation_id"`
	CurrentVersion     string                          `json:"current_version"`
	ExpectedDigest     string                          `json:"expected_digest"`
	Current            consoleManagedVersionView       `json:"current"`
	Shared             bool                            `json:"shared"`
	DeletedAt          *time.Time                      `json:"deleted_at,omitempty"`
	CanPrepare         bool                            `json:"can_prepare"`
	LatestOperation    *consoleManagementOperationView `json:"latest_operation"`
	Draft              *consoleManagedDraftView        `json:"draft"`
}

// Returned only with a local-persistence error. These are facts retained by
// this request, not a reread or a claim about the successfully saved journal.
type consoleManagementPartialView struct {
	ID               string     `json:"id"`
	ManagedID        string     `json:"managed_id"`
	Action           string     `json:"action"`
	Status           string     `json:"status"`
	ObservedStatus   string     `json:"observed_status"`
	WriteStatus      string     `json:"write_status"`
	AttemptedAt      *time.Time `json:"attempted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	BackupStatus     string     `json:"backup_status"`
	ArchiveStatus    string     `json:"archive_status"`
	LocalPersistence string     `json:"local_persistence"`
	ResultSource     string     `json:"result_source"`
	CanConfirm       bool       `json:"can_confirm"`
	CanCancel        bool       `json:"can_cancel"`
	CanReconcile     bool       `json:"can_reconcile"`
}

func (s *Server) setupConsoleAutomationManagementRoutes(g *gin.RouterGroup) {
	g.GET("/admin/automations/:id/management", s.handleConsoleAutomationManagement)
	g.POST("/admin/automations/:id/revisions", s.handleConsoleManagedRevision)
	g.POST("/admin/automations/:id/actions", s.handleConsolePrepareAutomationAction)
	g.GET("/admin/automation-actions/:id", s.handleConsoleAutomationAction)
	g.POST("/admin/automation-actions/:id/confirm", s.handleConsoleConfirmAutomationAction)
	g.POST("/admin/automation-actions/:id/cancel", s.handleConsoleCancelAutomationAction)
	g.POST("/admin/automation-actions/:id/reconcile", s.handleConsoleReconcileAutomationAction)
}

func (s *Server) consoleManagementContext(c *gin.Context) (context.Context, func(), bool) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return nil, nil, false
	}
	p := requestConsolePrincipal(c.Request)
	token := consoleToken(c.Request)
	check := func(checkCtx context.Context) error {
		if err := checkCtx.Err(); err != nil {
			return err
		}
		current, err := s.consoleStore.Resolve(token)
		if err != nil {
			return err
		}
		if current.UserID != p.UserID || current.SessionID != p.SessionID || current.Role != "admin" || current.MustChangePassword {
			return smarthome.ErrManagementForbidden
		}
		return checkCtx.Err()
	}
	if err := check(ctx); err != nil {
		stop()
		s.consoleManagementError(c, err)
		return nil, nil, false
	}
	return smarthome.WithManagementAuthority(ctx, smarthome.ManagementAuthority{UserID: p.UserID, SessionID: p.SessionID, Check: check}), stop, true
}
func (s *Server) consoleManagementError(c *gin.Context, err error) {
	if !s.consoleSmartHomeSessionValid(c) {
		return
	}
	status, code := 503, "management_unavailable"
	switch {
	case errors.Is(err, smarthome.ErrManagementNotFound), errors.Is(err, smarthome.ErrInstallOperationNotFound), errors.Is(err, smarthome.ErrSuggestionNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, smarthome.ErrManagementForbidden), errors.Is(err, console.ErrForbidden):
		status, code = 403, "forbidden"
	case errors.Is(err, smarthome.ErrManagementInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, smarthome.ErrManagementConflict), errors.Is(err, smarthome.ErrSuggestionConflict):
		status, code = 409, "management_conflict"
	case errors.Is(err, smarthome.ErrManagementExpired):
		status, code = 410, "management_expired"
	case errors.Is(err, smarthome.ErrUnsupportedRule):
		status, code = 422, "unsupported_rule"
	case errors.Is(err, smarthome.ErrHARequest):
		status, code = 502, "management_observation_unavailable"
	}
	consoleError(c, status, code, "Automation request could not complete; recover its recorded state before taking another action")
}
func (s *Server) consoleManagementJSON(c *gin.Context, ctx context.Context, data any) {
	if !s.consoleSmartHomeSessionValid(c) {
		return
	}
	s.consoleSmartHomeJSON(c, ctx, data)
}

func (s *Server) consoleManagementOperationError(c *gin.Context, ctx context.Context, op smarthome.ManagementOperation, err error) {
	if !errors.Is(err, smarthome.ErrManagementStore) || !safeSuggestionID(op.ID) {
		s.consoleManagementError(c, err)
		return
	}
	// No Manager/store projection is safe to require here: the acknowledged
	// write can be followed by an unreadable journal. Reauthorize only against
	// the independent Console session store, then publish under its session lock.
	token := consoleToken(c.Request)
	current, authErr := s.consoleStore.Resolve(token)
	if authErr != nil {
		consoleStoreError(c, authErr)
		return
	}
	original := requestConsolePrincipal(c.Request)
	if current.UserID != original.UserID || current.SessionID != original.SessionID || current.Role != "admin" || current.MustChangePassword {
		consoleStoreError(c, console.ErrForbidden)
		return
	}
	view := consoleManagementPartialView{ID: op.ID, ManagedID: op.ManagedID, Action: op.Action, Status: "unknown", ObservedStatus: "unknown", WriteStatus: "unknown", AttemptedAt: op.Write.AttemptedAt, CreatedAt: op.CreatedAt, ExpiresAt: op.ExpiresAt, BackupStatus: "not_required", ArchiveStatus: "not_required", LocalPersistence: "unverified", ResultSource: "response_observation"}
	switch op.Status {
	case "prepared", "applying", "confirmed", "failed", "unknown", "expired", "cancelled":
		view.ObservedStatus = op.Status
	}
	switch op.Write.Status {
	case "not_attempted", "unknown", "acknowledged", "rejected":
		view.WriteStatus = op.Write.Status
	}
	if op.Action == "delete" {
		view.BackupStatus = "unknown"
		if op.BackupRef != "" {
			view.BackupStatus = "saved"
		}
	}
	if op.Action == "update" {
		view.ArchiveStatus = "unknown"
	}
	publicationErr := s.consoleStore.WithSession(ctx, token, func(bounded context.Context) error {
		if err := bounded.Err(); err != nil {
			return err
		}
		c.Header("Cache-Control", "no-store")
		c.AbortWithStatusJSON(503, gin.H{"error": gin.H{"code": "management_persistence_unverified", "message": "The observed operation result could not be verified in local storage; recover the recorded state before taking another action", "request_id": consoleRequestID(c)}, "operation": view})
		return nil
	})
	if publicationErr != nil {
		consoleStoreError(c, publicationErr)
	}
}
func managedVersionProjection(v smarthome.RuleSuggestion) consoleManagedVersionView {
	return consoleManagedVersionView{ID: v.ID, Intent: v.Intent, PresenceBinding: v.PresenceBinding, Diff: append([]smarthome.SuggestionRevisionChange{}, v.RevisionDiff...)}
}
func managedDraftProjection(d smarthome.ManagedAutomationDraft) consoleManagedDraftView {
	return consoleManagedDraftView{ID: d.ID, ManagedID: d.ManagedID, CurrentOperationID: d.CurrentOperationID, CurrentVersion: d.CurrentVersion, ExpectedDigest: d.ExpectedDigest, Digest: d.Digest, CreatedAt: d.CreatedAt, Version: managedVersionProjection(d.Version)}
}
func (s *Server) managementOperationProjection(ctx context.Context, op smarthome.ManagementOperation, p console.Principal) (consoleManagementOperationView, error) {
	out := consoleManagementOperationView{ID: op.ID, ManagedID: op.ManagedID, Action: op.Action, Status: op.Status, CurrentOperationID: op.CurrentOperationID, CurrentVersion: op.CurrentVersion, ExpectedDigest: op.ExpectedDigest, RevisionID: op.RevisionID, StopActions: op.StopActions, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, ExpiresAt: op.ExpiresAt, AttemptedAt: op.Write.AttemptedAt, WriteStatus: op.Write.Status, BackupStatus: "not_required", ArchiveStatus: "not_required", Deletion: op.Deletion, ConcurrencyRisk: "external_edit_after_read"}
	switch op.ErrorCode {
	case "management_interrupted", "management_write_unknown", "management_readback_unverified":
		out.ErrorCode = op.ErrorCode
	}
	if op.Action == "delete" {
		out.BackupStatus = "not_saved"
		if op.BackupRef != "" {
			out.BackupStatus = "saved"
		}
	}
	if op.Action == "update" {
		out.ArchiveStatus = "not_ready"
		if op.InstallOperationID != "" {
			install, err := s.smartHome.GetInstallOperation(op.InstallOperationID)
			if err != nil {
				return out, err
			}
			projected, err := s.consoleCurrentInstallProjection(install)
			if err != nil {
				return out, err
			}
			out.Installation = &projected
			out.ArchiveStatus = projected.ArchiveStatus
		}
		if op.UserID == p.UserID && op.SessionID == p.SessionID {
			draft, err := s.smartHome.GetManagedAutomationDraft(ctx, op.RevisionID)
			if err != nil {
				return out, err
			}
			review := managedVersionProjection(draft.Version)
			out.Revision = &review
		}
	}
	current, err := s.smartHome.GetManagedAutomation(op.ManagedID)
	if err != nil {
		return out, err
	}
	active := current.DeletedAt == nil && current.OperationID == op.CurrentOperationID && current.SuggestionVersion == op.CurrentVersion && current.ExpectedDigest == op.ExpectedDigest && current.ConfigKey == op.ConfigKey && current.EntityID == op.EntityID
	out.CanConfirm = active && op.Status == "prepared" && time.Now().Before(op.ExpiresAt) && op.UserID == p.UserID && op.SessionID == p.SessionID
	out.CanCancel = op.Status == "prepared"
	out.CanReconcile = op.Write.AttemptedAt != nil && (op.Status == "unknown" || op.Status == "applying" || (op.Status == "confirmed" && out.ArchiveStatus == "pending"))
	if o := op.Observation; o != nil {
		if o.StateStatus == smarthome.SourceAvailable {
			switch o.State {
			case "on", "off", "unknown", "unavailable":
				out.ObservedState = o.State
			}
		}
		if !o.StateObservedAt.IsZero() {
			at := o.StateObservedAt
			out.ObservedAt = &at
		}
	}
	return out, nil
}
func (s *Server) handleConsoleAutomationManagement(c *gin.Context) {
	ctx, stop, ok := s.consoleManagementContext(c)
	if !ok {
		return
	}
	defer stop()
	id := c.Param("id")
	if !safeSuggestionID(id) {
		s.consoleManagementError(c, smarthome.ErrManagementNotFound)
		return
	}
	record, version, err := s.smartHome.GetManagedAutomationVersion(id)
	if err != nil {
		s.consoleManagementError(c, err)
		return
	}
	view := consoleManagementView{ManagedID: record.ID, SuggestionID: record.SuggestionID, CurrentOperationID: record.OperationID, CurrentVersion: record.SuggestionVersion, ExpectedDigest: record.ExpectedDigest, Current: managedVersionProjection(version), DeletedAt: record.DeletedAt, CanPrepare: record.DeletedAt == nil}
	sharing, err := s.consoleStore.GetSharing()
	if err != nil {
		s.consoleManagementError(c, err)
		return
	}
	for _, id := range sharing.SuggestionIDs {
		if id == record.SuggestionID {
			view.Shared = true
		}
	}
	op, err := s.smartHome.GetLatestAutomationAction(ctx, id)
	if err == nil {
		projection, e := s.managementOperationProjection(ctx, op, requestConsolePrincipal(c.Request))
		if e != nil {
			s.consoleManagementError(c, e)
			return
		}
		view.LatestOperation = &projection
		if op.Status == "applying" || op.Status == "unknown" || (op.Status == "prepared" && time.Now().Before(op.ExpiresAt)) {
			view.CanPrepare = false
		}
	} else if !errors.Is(err, smarthome.ErrManagementNotFound) {
		s.consoleManagementError(c, err)
		return
	}
	draft, err := s.smartHome.GetLatestManagedAutomationDraft(ctx, id)
	if err == nil {
		projection := managedDraftProjection(draft)
		view.Draft = &projection
	} else if !errors.Is(err, smarthome.ErrManagementNotFound) {
		s.consoleManagementError(c, err)
		return
	}
	// Several local readers are used; refuse a mixture if a concurrent update or
	// deletion advanced the version while this recovery view was assembled.
	current, err := s.smartHome.GetManagedAutomation(id)
	if err != nil {
		s.consoleManagementError(c, err)
		return
	}
	if current.OperationID != record.OperationID || current.SuggestionVersion != record.SuggestionVersion || current.ExpectedDigest != record.ExpectedDigest || (current.DeletedAt == nil) != (record.DeletedAt == nil) {
		s.consoleManagementError(c, smarthome.ErrManagementConflict)
		return
	}
	s.consoleManagementJSON(c, ctx, gin.H{"management": view})
}
func (s *Server) handleConsoleManagedRevision(c *gin.Context) {
	ctx, stop, ok := s.consoleManagementContext(c)
	if !ok {
		return
	}
	defer stop()
	if !safeSuggestionID(c.Param("id")) {
		s.consoleManagementError(c, smarthome.ErrManagementNotFound)
		return
	}
	var body smarthome.SuggestionRevisionRequest
	if !decodeControlJSON(c, &body) {
		return
	}
	draft, err := s.smartHome.ReviseManagedAutomation(ctx, c.Param("id"), body)
	if err != nil {
		s.consoleManagementError(c, err)
		return
	}
	s.consoleManagementJSON(c, ctx, gin.H{"draft": managedDraftProjection(draft)})
}
func (s *Server) handleConsolePrepareAutomationAction(c *gin.Context) {
	ctx, stop, ok := s.consoleManagementContext(c)
	if !ok {
		return
	}
	defer stop()
	if !safeSuggestionID(c.Param("id")) {
		s.consoleManagementError(c, smarthome.ErrManagementNotFound)
		return
	}
	var body struct {
		Action         string `json:"action"`
		ExpectedDigest string `json:"expected_digest"`
		RevisionID     string `json:"revision_id,omitempty"`
		StopActions    *bool  `json:"stop_actions,omitempty"`
	}
	if !decodeControlJSON(c, &body) {
		return
	}
	op, err := s.smartHome.PrepareAutomationAction(ctx, smarthome.AutomationActionRequest{ManagedID: c.Param("id"), Action: body.Action, ExpectedDigest: body.ExpectedDigest, RevisionID: body.RevisionID, StopActions: body.StopActions})
	if err != nil {
		s.consoleManagementOperationError(c, ctx, op, err)
		return
	}
	s.consoleManagementOperationJSON(c, ctx, op)
}
func (s *Server) consoleManagementOperationJSON(c *gin.Context, ctx context.Context, op smarthome.ManagementOperation) {
	view, err := s.managementOperationProjection(ctx, op, requestConsolePrincipal(c.Request))
	if err != nil {
		s.consoleManagementError(c, err)
		return
	}
	s.consoleManagementJSON(c, ctx, gin.H{"operation": view})
}
func (s *Server) handleConsoleAutomationAction(c *gin.Context) { s.consoleAutomationAction(c, "get") }
func (s *Server) handleConsoleConfirmAutomationAction(c *gin.Context) {
	s.consoleAutomationAction(c, "confirm")
}
func (s *Server) handleConsoleCancelAutomationAction(c *gin.Context) {
	s.consoleAutomationAction(c, "cancel")
}
func (s *Server) handleConsoleReconcileAutomationAction(c *gin.Context) {
	s.consoleAutomationAction(c, "reconcile")
}
func (s *Server) consoleAutomationAction(c *gin.Context, action string) {
	ctx, stop, ok := s.consoleManagementContext(c)
	if !ok {
		return
	}
	defer stop()
	if !safeSuggestionID(c.Param("id")) {
		s.consoleManagementError(c, smarthome.ErrManagementNotFound)
		return
	}
	if action != "get" {
		var body struct{}
		if !decodeControlJSON(c, &body) {
			return
		}
	}
	var op smarthome.ManagementOperation
	var err error
	switch action {
	case "get":
		op, err = s.smartHome.GetAutomationAction(ctx, c.Param("id"))
	case "confirm":
		op, err = s.smartHome.ConfirmAutomationAction(ctx, c.Param("id"))
	case "cancel":
		op, err = s.smartHome.CancelAutomationAction(ctx, c.Param("id"))
	case "reconcile":
		op, err = s.smartHome.ReconcileAutomationAction(ctx, c.Param("id"))
	}
	if err != nil {
		s.consoleManagementOperationError(c, ctx, op, err)
		return
	}
	s.consoleManagementOperationJSON(c, ctx, op)
}
