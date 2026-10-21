package smarthome

import (
	"context"
	"time"
)

// A diagnostic holds the existing action mutex from before its first managed
// lookup through evidence capture and snapshot persistence. Waiting must also
// honor cancellation; an abandoned goroutine must never acquire this mutex.
func (m *Manager) acquireDiagnosticAction(ctx context.Context) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m.actionMu.TryLock() {
			if err := ctx.Err(); err != nil {
				m.actionMu.Unlock()
				return nil, err
			}
			return m.actionMu.Unlock, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func validDiagnosticVersions(versions map[string]DiagnosticManagedVersion) bool {
	if len(versions) > maxDiagnosticPeers+1 {
		return false
	}
	for id, version := range versions {
		if !validDiagnosticRef(AutomationRef{Kind: "managed", ID: id}) || !validManagementID(version.OperationID) || !configKeyPattern.MatchString(version.SuggestionVersion) {
			return false
		}
	}
	return true
}

func diagnosticCheckVersions(check DiagnosticCheck, bindings map[string]DiagnosticManagedVersion) (map[string]DiagnosticManagedVersion, bool) {
	var scoped map[string]DiagnosticManagedVersion
	for _, evidence := range check.EvidenceRefs {
		if evidence.RuleRef.Kind != "managed" {
			continue
		}
		version, ok := bindings[evidence.RuleRef.ID]
		if !ok || !validDiagnosticVersions(map[string]DiagnosticManagedVersion{evidence.RuleRef.ID: version}) {
			return nil, false
		}
		if scoped == nil {
			scoped = map[string]DiagnosticManagedVersion{}
		}
		scoped[evidence.RuleRef.ID] = version
	}
	return scoped, true
}

// Caller holds actionMu. Ref identities come from the original selection and
// peers already proven by their SAME observed mapping, never a later remapping.
func (m *Manager) bindDiagnosticVersions(snapshot *DiagnosticSnapshot, peers []DiagnosticPeer) error {
	ids := map[string]bool{}
	if snapshot.Ref.Kind == "managed" {
		ids[snapshot.Ref.ID] = true
	}
	for _, peer := range peers {
		if peer.Ref.Kind == "managed" {
			ids[peer.Ref.ID] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	state, err := m.store.readInstallState()
	if err != nil {
		return ErrDiagnosticStore
	}
	versions := map[string]DiagnosticManagedVersion{}
	for _, managed := range state.Managed {
		if ids[managed.ID] && managed.DeletedAt == nil {
			versions[managed.ID] = DiagnosticManagedVersion{OperationID: managed.OperationID, SuggestionVersion: managed.SuggestionVersion}
		}
	}
	if !validDiagnosticVersions(versions) {
		return ErrDiagnosticStore
	}
	if len(versions) > 0 {
		snapshot.ManagedVersions = versions
	}
	return nil
}
