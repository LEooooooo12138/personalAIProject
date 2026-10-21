package smarthome

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type diagnosticPeerAttributor interface {
	managedDiagnosticPeerRef(AutomationRef, AutomationObservation) AutomationRef
}

func TestManagedDiagnosticPeerExcludesDeletedOwnership(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ref := AutomationRef{Kind: "external", ID: managed.EntityID}
	observed, err := m.ObserveAutomation(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.managedDiagnosticPeerRef(ref, observed); got.Kind != "managed" {
		t.Fatalf("live provenance not attributed: %+v", got)
	}
	op, err := m.PrepareAutomationAction(adminManagementContext(), AutomationActionRequest{ManagedID: managed.ID, Action: "delete", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfirmAutomationAction(adminManagementContext(), op.ID); err != nil {
		t.Fatal(err)
	}
	writes := len(f.writes)
	// Even an identical replacement mapping cannot reuse the deleted ownership.
	if got := m.managedDiagnosticPeerRef(ref, observed); got != ref {
		t.Fatalf("deleted owner attributed to recreated entity: %+v", got)
	}
	if len(f.writes) != writes {
		t.Fatal("attribution performed a write")
	}
}

func seedDiagnosticManagedPeer(t *testing.T, m *Manager, id, managedID string) (InstallOperation, ManagedAutomation, AutomationObservation) {
	t.Helper()
	o, err := m.ObserveAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation." + id})
	if err != nil || !o.RuntimeLoadProof.Verified {
		t.Fatalf("fixture installation observation: %v %+v", err, o)
	}
	op, err := newInstallOperation(RuleSuggestion{ID: id}, o.Config.Digest)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	op.OwnAttempt, op.Create.AttemptedAt = true, &now
	op.ManagedAutomationID, op.Status, op.Observation = managedID, "confirmed", &o
	managed := ManagedAutomation{SchemaVersion: 1, ID: managedID, OperationID: op.ID, SuggestionID: id, SuggestionVersion: id, ConfigKey: id, EntityID: o.EntityID, ExpectedDigest: o.Config.Digest, MappingSource: o.MappingSource, LastObservation: o}
	if err := m.store.saveInstallManaged(op, managed); err != nil {
		t.Fatal(err)
	}
	return op, managed, o
}

// Entity identity alone is insufficient: reused IDs, unknown mapping and
// ambiguous durable records must never authorize another rule's evidence.
func TestManagedDiagnosticPeerRefRequiresObservedMappingAndProvenance(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	_, managed, observed := seedDiagnosticManagedPeer(t, m, "b", strings.Repeat("b", 32))
	lookup, ok := any(m).(diagnosticPeerAttributor)
	if !ok {
		t.Fatal("missing snapshot-time managed peer attribution")
	}
	ref := AutomationRef{Kind: "external", ID: "automation.b"}
	managedRef := AutomationRef{Kind: "managed", ID: managed.ID}
	path := filepath.Join(m.store.basePath, "rules", "install-state.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reads := [4]int32{f.profileReads.Load(), f.registryReads.Load(), f.configReads.Load(), f.stateReads.Load()}
	for _, tc := range []struct {
		name        string
		change      func(*AutomationRef, *AutomationObservation)
		wantManaged bool
	}{
		{"exact_mapping", func(*AutomationRef, *AutomationObservation) {}, true},
		{"reused_entity_other_key", func(_ *AutomationRef, o *AutomationObservation) { o.Config.ConfigKey = "replacement" }, false},
		{"mapping_unknown", func(_ *AutomationRef, o *AutomationObservation) { o.MappingStatus = SourceUnavailable }, false},
		{"mapping_error_despite_available", func(_ *AutomationRef, o *AutomationObservation) { o.MappingErrorCode = "ha_mapping_ambiguous" }, false},
		{"mapping_source_missing", func(_ *AutomationRef, o *AutomationObservation) { o.MappingSource = "" }, false},
		{"different_observed_entity", func(_ *AutomationRef, o *AutomationObservation) { o.EntityID = "automation.a" }, false},
		{"different_observed_selection", func(_ *AutomationRef, o *AutomationObservation) { o.Ref.ID = "automation.a" }, false},
		{"missing_key", func(_ *AutomationRef, o *AutomationObservation) { o.Config.ConfigKey = "" }, false},
		{"unverified_selection", func(r *AutomationRef, o *AutomationObservation) { r.Kind = "unverified"; o.Ref = *r }, false},
		{"unknown_durable_rule", func(r *AutomationRef, o *AutomationObservation) {
			r.ID = "automation.unknown"
			o.Ref = *r
			o.EntityID = r.ID
			o.Config.ConfigKey = "unknown"
		}, false},
		{"managed_drift", func(_ *AutomationRef, o *AutomationObservation) { o.Config.Digest = strings.Repeat("f", 64) }, true},
		{"config_read_failed_mapping_known", func(_ *AutomationRef, o *AutomationObservation) {
			o.Config.SourceStatus = SourceUnavailable
			o.Config.ErrorCode = "ha_unavailable"
		}, true},
		{"runtime_read_failed_mapping_known", func(_ *AutomationRef, o *AutomationObservation) {
			o.RuntimeLoadProof.Verified = false
			o.RuntimeLoadProof.SourceStatus = SourceForbidden
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, o := ref, observed
			tc.change(&r, &o)
			want := r
			if tc.wantManaged {
				want = managedRef
			}
			if got := lookup.managedDiagnosticPeerRef(r, o); got != want {
				t.Fatalf("peer attribution got=%+v want=%+v", got, want)
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("lookup mutated durable ownership: %v", err)
	}
	if got := [4]int32{f.profileReads.Load(), f.registryReads.Load(), f.configReads.Load(), f.stateReads.Load()}; got != reads || f.writes.Load() != 0 {
		t.Fatalf("lookup performed HA activity: before=%v after=%v writes=%d", reads, got, f.writes.Load())
	}
	// Both records are valid individually; duplicate EntityID must still fail
	// closed even when only one record has the observed config key.
	_, other, _ := seedDiagnosticManagedPeer(t, m, "a", strings.Repeat("a", 32))
	state, err := m.store.readInstallState()
	if err != nil {
		t.Fatal(err)
	}
	for i := range state.Managed {
		if state.Managed[i].ID == other.ID {
			state.Managed[i].EntityID = observed.EntityID
		}
	}
	if err := m.store.writeInstallState(state); err != nil {
		t.Fatal(err)
	}
	if got := lookup.managedDiagnosticPeerRef(ref, observed); got != ref {
		t.Fatalf("ambiguous record authorized: %+v", got)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"managed":`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := lookup.managedDiagnosticPeerRef(ref, observed); got != ref {
		t.Fatalf("corrupt record authorized: %+v", got)
	}
	empty := &Manager{}
	if got := any(empty).(diagnosticPeerAttributor).managedDiagnosticPeerRef(ref, observed); got != ref {
		t.Fatalf("missing store authorized: %+v", got)
	}
}

// Exercise the real HA readers, evaluator and durable snapshot. Mapping now
// must be fixed in evidence before persistence, never reconstructed by a reader.
func TestManagedDiagnosticPeerCompositionPersistsAttribution(t *testing.T) {
	f, m := newDiagnosisFixture(t)
	selected := AutomationRef{Kind: "external", ID: "automation.a"}
	old, err := m.DiagnoseAutomation(context.Background(), selected, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	oldCheck := diagnosisCheck(t, old, "conflict:external:automation.b")
	_, peer, _ := seedDiagnosticManagedPeer(t, m, "b", strings.Repeat("b", 32))
	latest, err := m.GetLatestAutomationDiagnostic(selected)
	if err != nil || latest.ID != old.ID || !reflect.DeepEqual(diagnosisCheck(t, latest, oldCheck.Key), oldCheck) {
		t.Fatalf("old external snapshot was upgraded: err=%v snapshot=%+v", err, latest)
	}
	// Different complete digest remains the same owned config identity. Keep
	// private semantic scopes in evidence for the later all-scope permission check.
	f.configs["b"] = strings.Replace(f.configs["b"], "PRIVATE text", "PRIVATE drift", 1)
	f.configs["b"] = strings.Replace(f.configs["b"], `"input_boolean.home"`, `"input_boolean.private_room"`, 1)
	before := [4]int32{f.profileReads.Load(), f.registryReads.Load(), f.configReads.Load(), f.stateReads.Load()}
	snapshot, err := m.DiagnoseAutomation(context.Background(), selected, evidenceWindow)
	if err != nil {
		t.Fatal(err)
	}
	got := diagnosisCheck(t, snapshot, "conflict:managed:"+peer.ID)
	if len(got.EvidenceRefs) != 2 || got.EvidenceRefs[0].RuleRef != selected || got.EvidenceRefs[1].RuleRef != (AutomationRef{Kind: "managed", ID: peer.ID}) {
		t.Fatalf("lost independent rule scopes: %+v", got.EvidenceRefs)
	}
	if !reflect.DeepEqual(got.EvidenceRefs[1].EntityIDs, []string{"input_boolean.private_room", "light.room"}) {
		t.Fatalf("private evidence scope was dropped before authorization: %+v", got.EvidenceRefs)
	}
	foundChange := false
	for _, change := range snapshot.Changes {
		if change.Check.Key == got.Key {
			foundChange = reflect.DeepEqual(change.Check.EvidenceRefs, got.EvidenceRefs)
		}
	}
	if !foundChange {
		t.Fatal("new finding did not retain managed attribution in changes")
	}
	latest, err = m.GetLatestAutomationDiagnostic(selected)
	if err != nil || !reflect.DeepEqual(latest, snapshot) {
		t.Fatalf("durable attribution differs: %v", err)
	}
	after := [4]int32{f.profileReads.Load(), f.registryReads.Load(), f.configReads.Load(), f.stateReads.Load()}
	delta := [4]int32{}
	for i := range delta {
		delta[i] = after[i] - before[i]
	}
	// Selected observation, trace profile, timezone and peer observation; three
	// registry reads; two config reads; two states per rule plus two references.
	if delta != [4]int32{4, 3, 2, 6} || f.writes.Load() != 0 {
		t.Fatalf("attribution added HA activity: reads=%v writes=%d", delta, f.writes.Load())
	}
}
