package main

import (
	"context"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
)

func TestFixtureInstallUsesCurrentConfigAndReadOnlyReconcile(t *testing.T) {
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := f.app.SmartHome
	s, err := m.BindSuggestion(context.Background(), "fixture_time", smarthome.SuggestionBindings{Presence: &smarthome.PresenceBinding{EntityID: "group.family_home", State: "on"}})
	if err != nil {
		t.Fatal(err)
	}
	s, err = m.ConfirmSuggestion(context.Background(), s.ID)
	if err != nil || s.Status != "confirmed" {
		t.Fatalf("strict fixture install %+v %v", s, err)
	}
	op, err := m.GetInstallOperationForSuggestion(s.ID)
	if err != nil || op.Observation == nil || !op.Observation.RuntimeLoadProof.Verified || op.ManagedAutomationID == "" {
		t.Fatalf("missing real current proof %+v %v", op, err)
	}
	before := f.dependencies.automationWrites.Load()
	reloadBefore := f.dependencies.automationReloads.Load()
	if before != 1 || reloadBefore != 1 {
		t.Fatalf("fixture dispatches create=%d reload=%d", before, reloadBefore)
	}
	op, err = m.ReconcileInstallation(context.Background(), op.ID)
	if err != nil || op.Status != "confirmed" || (f.dependencies.automationWrites.Load() != before || f.dependencies.automationReloads.Load() != reloadBefore) {
		t.Fatalf("reconciliation %+v %v", op, err)
	}
}
