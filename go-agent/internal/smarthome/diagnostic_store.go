package smarthome

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

var ErrDiagnosticNotFound = errors.New("diagnostic_not_found")
var ErrDiagnosticStore = errors.New("diagnostic_store_unavailable")

type diagnosticRecord struct {
	SchemaVersion  int                                            `json:"schema_version"`
	Snapshot       DiagnosticSnapshot                             `json:"snapshot"`
	Active         map[string]DiagnosticCheck                     `json:"active_findings"`
	ActiveVersions map[string]map[string]DiagnosticManagedVersion `json:"active_versions,omitempty"`
}

func (s *DeviceStore) diagnosticPath(ref AutomationRef) string {
	digest := sha256.Sum256([]byte(ref.Kind + ":" + ref.ID))
	return filepath.Join(s.basePath, "diagnostics", hex.EncodeToString(digest[:])+".json")
}
func (s *DeviceStore) readDiagnosticRecord(ref AutomationRef) (diagnosticRecord, error) {
	var record diagnosticRecord
	f, err := os.Open(s.diagnosticPath(ref))
	if errors.Is(err, os.ErrNotExist) {
		return record, ErrDiagnosticNotFound
	}
	if err != nil {
		return record, ErrDiagnosticStore
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 || json.Unmarshal(b, &record) != nil || record.SchemaVersion != 1 || record.Snapshot.SchemaVersion != 1 || record.Snapshot.Ref != ref || len(record.Snapshot.Checks) > 256 || len(record.Active) > 512 || record.Active == nil || !validDiagnosticVersions(record.Snapshot.ManagedVersions) {
		return diagnosticRecord{}, ErrDiagnosticStore
	}
	if len(record.ActiveVersions) > 512 {
		return diagnosticRecord{}, ErrDiagnosticStore
	}
	for key, versions := range record.ActiveVersions {
		if _, ok := record.Active[key]; !ok || !validDiagnosticVersions(versions) {
			return diagnosticRecord{}, ErrDiagnosticStore
		}
	}
	return record, nil
}
func (s *DeviceStore) saveDiagnosticSnapshot(snapshot DiagnosticSnapshot) (DiagnosticSnapshot, error) {
	if !validDiagnosticRef(snapshot.Ref) || snapshot.SchemaVersion != 1 || validateHistoryWindow(snapshot.Window) != nil || len(snapshot.Checks) > 256 || !validDiagnosticVersions(snapshot.ManagedVersions) {
		return snapshot, ErrDiagnosticStore
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.readDiagnosticRecord(snapshot.Ref)
	if err != nil && !errors.Is(err, ErrDiagnosticNotFound) {
		return snapshot, err
	}
	if record.Active == nil {
		record.Active = map[string]DiagnosticCheck{}
	}
	if record.ActiveVersions == nil {
		record.ActiveVersions = map[string]map[string]DiagnosticManagedVersion{}
	}
	snapshot.Changes = []DiagnosticChange{}
	for _, check := range snapshot.Checks {
		previous, exists := record.Active[check.Key]
		versions, complete := diagnosticCheckVersions(check, snapshot.ManagedVersions)
		oldVersions := record.ActiveVersions[check.Key]
		_, oldComplete := diagnosticCheckVersions(previous, oldVersions)
		sameGeneration := complete && oldComplete && reflect.DeepEqual(versions, oldVersions)
		switch check.Status {
		case "finding":
			if exists && !complete {
				continue
			}
			kind := ""
			if !exists || complete && !sameGeneration {
				kind = "new"
			} else if previous.Severity != check.Severity {
				kind = "severity_changed"
			}
			if kind != "" {
				snapshot.Changes = append(snapshot.Changes, DiagnosticChange{Kind: kind, Check: check})
			}
			record.Active[check.Key] = check
			if len(versions) > 0 {
				record.ActiveVersions[check.Key] = versions
			} else {
				delete(record.ActiveVersions, check.Key)
			}
		case "pass":
			if exists && sameGeneration && diagnosticRecoveryProven(previous, check) {
				delete(record.Active, check.Key)
				delete(record.ActiveVersions, check.Key)
				snapshot.Changes = append(snapshot.Changes, DiagnosticChange{Kind: "recovered", Check: check})
			} else if exists && complete && oldComplete && !sameGeneration {
				// A known replacement generation starts a new baseline, never a
				// recovery claim about the old version. Missing proof retains it.
				delete(record.Active, check.Key)
				delete(record.ActiveVersions, check.Key)
			}
		case "unknown": // Preserve prior finding until this exact check proves recovery.
		default:
			return snapshot, ErrDiagnosticStore
		}
	}
	if len(record.Active) > 512 {
		return snapshot, ErrDiagnosticStore
	}
	snapshot.Summary = DiagnosticSummary(snapshot.Checks)
	record.SchemaVersion = 1
	record.Snapshot = snapshot
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil || len(b) > 1<<20 {
		return snapshot, ErrDiagnosticStore
	}
	if err = os.MkdirAll(filepath.Join(s.basePath, "diagnostics"), 0700); err != nil {
		return snapshot, ErrDiagnosticStore
	}
	if err = atomicWrite(s.diagnosticPath(snapshot.Ref), b); err != nil {
		return snapshot, ErrDiagnosticStore
	}
	return snapshot, nil
}

// Historical execution errors never recover merely because another run succeeds,
// or because an old trace disappears. Only the named current fact can recover.
func diagnosticRecoveryProven(old, current DiagnosticCheck) bool {
	if len(old.EvidenceRefs) == 0 {
		return false
	}
	for _, before := range old.EvidenceRefs {
		fresh := false
		for _, after := range current.EvidenceRefs {
			if before.Source == after.Source && before.RuleRef == after.RuleRef && !before.ObservedAt.IsZero() && !after.ObservedAt.Before(before.ObservedAt) {
				fresh = true
				break
			}
		}
		if !fresh {
			return false
		}
	}
	switch old.Reason {
	case "entity_unavailable", "entity_missing":
		return current.Reason == "entity_available"
	case "automation_unavailable":
		return current.Reason == "enabled_observed" || current.Reason == "disabled_observed"
	case "config_missing":
		return current.Reason == "config_exists"
	case "config_drift":
		return current.Reason == "installed_version_matches"
	case "opposite_actions_same_scope":
		return current.Reason == "no_conflict_in_comparison"
	}
	return false
}
func (m *Manager) GetLatestAutomationDiagnostic(ref AutomationRef) (DiagnosticSnapshot, error) {
	if !validDiagnosticRef(ref) {
		return DiagnosticSnapshot{}, ErrDiagnosticNotFound
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	r, err := m.store.readDiagnosticRecord(ref)
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	r.Snapshot.Summary = DiagnosticSummary(r.Snapshot.Checks)
	return r.Snapshot, nil
}
