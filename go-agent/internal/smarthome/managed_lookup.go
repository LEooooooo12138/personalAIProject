package smarthome

// managedDiagnosticPeerRef binds a peer's evidence to existing durable ownership
// at snapshot creation, using the SAME observation as its configuration evidence.
// Entity IDs can be reused, so neither an ID alone nor a later registry read can
// establish this attribution. Unknown, ambiguous or corrupt provenance remains
// external. This read-only helper never creates ownership or requires a matching
// digest: external edits to an owned configuration still need diagnosis.
func (m *Manager) managedDiagnosticPeerRef(selected AutomationRef, observed AutomationObservation) AutomationRef {
	if selected.Kind != "external" || !validAutomationEntity(selected.ID) || observed.Ref != selected || observed.EntityID != selected.ID || observed.MappingStatus != SourceAvailable || observed.MappingErrorCode != "" || observed.MappingSource != "config/entity_registry/list" || !validAutomationKey(observed.Config.ConfigKey) || m.store == nil {
		return selected
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	state, err := m.store.readInstallState()
	if err != nil {
		return selected
	}
	var match *ManagedAutomation
	for i := range state.Managed {
		if state.Managed[i].DeletedAt != nil || state.Managed[i].EntityID != observed.EntityID {
			continue
		}
		if match != nil {
			return selected
		}
		match = &state.Managed[i]
	}
	if match == nil || match.ConfigKey != observed.Config.ConfigKey {
		return selected
	}
	ref := AutomationRef{Kind: "managed", ID: match.ID}
	if !validDiagnosticRef(ref) {
		return selected
	}
	return ref
}
