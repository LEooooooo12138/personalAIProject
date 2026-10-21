package gateway

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"time"
)

// Closed projection: never serialize raw observations, upstream text or config.
type consoleInstallOperationView struct {
	ID                string     `json:"id"`
	SuggestionID      string     `json:"suggestion_id"`
	SuggestionVersion string     `json:"suggestion_version"`
	Status            string     `json:"status"`
	Phase             string     `json:"phase"`
	ErrorCode         string     `json:"error_code,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ConfigStatus      string     `json:"config_status"`
	MappingStatus     string     `json:"mapping_status"`
	LoadStatus        string     `json:"load_status"`
	ArchiveStatus     string     `json:"archive_status"`
	ArchiveErrorCode  string     `json:"archive_error_code,omitempty"`
	Ownership         string     `json:"ownership"`
	ObservedState     string     `json:"observed_state,omitempty"`
	ObservedAt        *time.Time `json:"observed_at,omitempty"`
	CanReconcile      bool       `json:"can_reconcile"`
	LegacyUnverified  bool       `json:"legacy_unverified"`
	Historical        bool       `json:"historical"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
}

func installErrorCode(code string) string {
	switch code {
	case "install_preflight_unavailable", "install_preexisting_unowned", "install_preexisting_conflict", "install_create_unknown", "install_create_rejected", "install_reload_unknown", "install_load_unverified", "install_config_drift", "install_interrupted", "install_legacy_outcome_unknown", "install_legacy_confirmed_unverified", "install_archive_pending":
		return code
	default:
		return ""
	}
}
func installOperationProjection(op smarthome.InstallOperation) consoleInstallOperationView {
	// Applying can outlive a failed projection/persistence response. Reconciliation
	// waits on the Manager action lock and never dispatches external writes.
	out := consoleInstallOperationView{ID: op.ID, SuggestionID: op.SuggestionID, SuggestionVersion: op.SuggestionVersion, Status: op.Status, Phase: op.Phase, ErrorCode: installErrorCode(op.ErrorCode), CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, ConfigStatus: "unknown", MappingStatus: "unknown", LoadStatus: "unknown", ArchiveStatus: "not_ready", Ownership: "unverified", CanReconcile: op.OwnAttempt && op.Create.Status != "rejected", LegacyUnverified: op.Phase == "legacy"}
	switch out.Phase {
	case "preflight", "create", "reload", "reconcile", "archive", "complete", "legacy":
	default:
		out.Phase = "reconcile"
	}
	if op.Create.Status == "acknowledged" {
		out.ConfigStatus = "saved"
	}
	switch op.Archive.Status {
	case "pending", "complete":
		out.ArchiveStatus = op.Archive.Status
	}
	if op.Archive.ErrorCode != "" {
		out.ArchiveErrorCode = "install_archive_pending"
	}
	if op.ManagedAutomationID != "" {
		out.Ownership = "managed"
	}
	if o := op.Observation; o != nil {
		if o.Config.SourceStatus == smarthome.SourceAvailable && o.Config.Exists != nil {
			if !*o.Config.Exists {
				out.ConfigStatus = "missing"
			} else if o.Config.Digest == op.ExpectedDigest {
				out.ConfigStatus = "matching"
			} else {
				out.ConfigStatus = "drift"
			}
		}
		if o.MappingStatus == smarthome.SourceAvailable && o.MappingErrorCode == "" && o.EntityID != "" {
			out.MappingStatus = "verified"
		}
		if o.RuntimeLoadProof.Verified && o.RuntimeLoadProof.SourceStatus == smarthome.SourceAvailable && o.RuntimeLoadProof.ExpectedDigest == op.ExpectedDigest && o.RuntimeLoadProof.ObservedDigest == op.ExpectedDigest {
			out.LoadStatus = "verified"
		} else if o.RuntimeLoadProof.SourceStatus == smarthome.SourceAvailable && o.RuntimeLoadProof.ObservedDigest != "" && o.RuntimeLoadProof.ObservedDigest != op.ExpectedDigest {
			out.LoadStatus = "drift"
		}
		if o.StateStatus == smarthome.SourceAvailable {
			switch o.State {
			case "on", "off", "unknown", "unavailable":
				out.ObservedState = o.State
			}
		}
		if !o.RuntimeLoadProof.ObservedAt.IsZero() {
			at := o.RuntimeLoadProof.ObservedAt
			out.ObservedAt = &at
		}
	}
	return out
}
func (s *Server) consoleSuggestionInstallation(item smarthome.RuleSuggestion, view consoleSuggestionView, admin bool) (consoleSuggestionView, error) {
	op, err := s.smartHome.GetInstallOperationForSuggestion(item.ID)
	if errors.Is(err, smarthome.ErrInstallOperationNotFound) {
		if item.Status == "confirmed" || item.Status == "applying" || item.Status == "failed" {
			view.InstallationState = "legacy_unverified"
			view.CanConfirm = false
			if item.Status != "confirmed" {
				view.Status = "unknown"
			}
		}
		return view, nil
	}
	if err != nil {
		return view, err
	}
	var deletedAt *time.Time
	if op.ManagedAutomationID != "" {
		managed, e := s.smartHome.GetManagedAutomation(op.ManagedAutomationID)
		if e != nil {
			return view, e
		}
		op, e = s.smartHome.GetInstallOperation(managed.OperationID)
		if e != nil {
			return view, e
		}
		deletedAt = managed.DeletedAt
	}
	view.CanConfirm = false
	view.CanBind = false
	view.CanRevise = false
	view.CanIgnore = false
	view.Status = op.Status
	view.InstallationState = "unknown"
	if op.Status == "confirmed" {
		view.InstallationState = "verified"
	}
	if op.Phase == "legacy" {
		view.InstallationState = "legacy_unverified"
		if item.Status == "confirmed" {
			view.Status = "confirmed"
		}
	}
	if deletedAt != nil {
		view.InstallationState = "deleted"
	}
	if admin {
		projected := installOperationProjection(op)
		if deletedAt != nil {
			projected.Historical, projected.DeletedAt, projected.CanReconcile, projected.Ownership = true, deletedAt, false, "historical"
		}
		view.Installation = &projected
	}
	return view, nil
}

func (s *Server) consoleCurrentInstallProjection(op smarthome.InstallOperation) (consoleInstallOperationView, error) {
	view := installOperationProjection(op)
	if op.ManagedAutomationID == "" {
		return view, nil
	}
	managed, err := s.smartHome.GetManagedAutomation(op.ManagedAutomationID)
	if err != nil {
		return view, err
	}
	if managed.DeletedAt != nil || managed.OperationID != op.ID {
		view.Historical, view.DeletedAt, view.CanReconcile, view.Ownership = true, managed.DeletedAt, false, "historical"
	}
	return view, nil
}
func (s *Server) handleConsoleInstallOperation(c *gin.Context) { s.consoleInstallOperation(c, false) }
func (s *Server) handleConsoleReconcileInstallation(c *gin.Context) {
	s.consoleInstallOperation(c, true)
}
func (s *Server) consoleInstallOperation(c *gin.Context, reconcile bool) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	id := c.Param("id")
	if !safeSuggestionID(id) {
		s.consoleSmartHomeFailure(c, 404, "not_found", "Installation operation not found")
		return
	}
	if reconcile {
		var body struct{}
		if !decodeConsoleJSON(c, &body) {
			return
		}
	}
	op, err := s.smartHome.GetInstallOperation(id)
	if err == nil && reconcile {
		view, projectionErr := s.consoleCurrentInstallProjection(op)
		if projectionErr != nil {
			s.consoleSuggestionError(c, projectionErr)
			return
		}
		if view.Historical {
			s.consoleSmartHomeFailure(c, 409, "historical_installation", "Historical installation is read-only")
			return
		}
	}
	if err == nil && reconcile {
		op, err = s.smartHome.ReconcileInstallation(ctx, id)
	}
	if errors.Is(err, smarthome.ErrInstallOperationNotFound) {
		s.consoleSmartHomeFailure(c, 404, "not_found", "Installation operation not found")
		return
	}
	if err != nil {
		s.consoleSuggestionError(c, err)
		return
	}
	view, err := s.consoleCurrentInstallProjection(op)
	if err != nil {
		s.consoleSuggestionError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, gin.H{"operation": view})
}
