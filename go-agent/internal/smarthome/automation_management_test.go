package smarthome

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func managementAuthority(user, session string, check func(context.Context) error) context.Context {
	return WithManagementAuthority(context.Background(), ManagementAuthority{UserID: user, SessionID: session, Check: check})
}
func adminManagementContext() context.Context {
	return managementAuthority("admin", "session", func(context.Context) error { return nil })
}

func TestManagementRequiresFreshConfirmedAuthority(t *testing.T) {
	for _, mode := range []string{"success", "missing_authority", "member", "other_session", "revoked", "revoked_during_read", "expired", "drift", "missing_mapping", "version"} {
		t.Run(mode, func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			if mode == "missing_authority" {
				ctx = context.Background()
			}
			if mode == "member" {
				ctx = managementAuthority("member", "session", func(context.Context) error { return errors.New("forbidden") })
			}
			op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
			if mode == "missing_authority" || mode == "member" {
				if err == nil {
					t.Fatal("unauthorized prepare accepted")
				}
				if len(f.writes) != 0 {
					t.Fatal("unauthorized write")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(f.writes) != 0 || op.Status != "prepared" || op.ExpiresAt.Sub(op.CreatedAt) != 5*time.Minute {
				t.Fatalf("unsafe prepare: %+v", op)
			}
			switch mode {
			case "other_session":
				ctx = managementAuthority("admin", "different", func(context.Context) error { return nil })
			case "revoked":
				ctx = managementAuthority("admin", "session", func(context.Context) error { return errors.New("revoked") })
			case "revoked_during_read":
				valid := true
				ctx = managementAuthority("admin", "session", func(context.Context) error {
					if !valid {
						return errors.New("revoked")
					}
					return nil
				})
				f.onRead = func() { valid = false }
			case "expired":
				m.store.mu.Lock()
				st, _ := m.store.readInstallState()
				for i := range st.Management {
					if st.Management[i].ID == op.ID {
						st.Management[i].CreatedAt = time.Now().Add(-6 * time.Minute)
						st.Management[i].ExpiresAt = st.Management[i].CreatedAt.Add(5 * time.Minute)
					}
				}
				m.store.writeInstallState(st)
				m.store.mu.Unlock()
			case "drift":
				f.stored = bytes.Replace(f.stored, []byte("18:00"), []byte("18:01"), 1)
			case "missing_mapping":
				f.missingMapping = true
			case "version":
				f.version = "9999.1.1"
			}
			got, err := m.ConfirmAutomationAction(ctx, op.ID)
			if mode == "success" {
				if err != nil || got.Status != "confirmed" || len(f.writes) != 1 {
					t.Fatalf("confirm %+v %v writes=%v", got, err, f.writes)
				}
			} else if err == nil || len(f.writes) != 0 {
				t.Fatalf("guard bypass %s %+v %v writes=%v", mode, got, err, f.writes)
			}
		})
	}
}

func TestManagementUnknownNeverReplays(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	stop := false
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "disable", ExpectedDigest: managed.ExpectedDigest, StopActions: &stop})
	if err != nil {
		t.Fatal(err)
	}
	f.loseResponse = true
	got, err := m.ConfirmAutomationAction(ctx, op.ID)
	if err != nil || got.Status != "unknown" || len(f.writes) != 1 {
		t.Fatalf("lost response %+v %v", got, err)
	}
	if f.payloads[0]["entity_id"] != "automation.installed" || f.payloads[0]["stop_actions"] != false {
		t.Fatalf("wrong disable payload %v", f.payloads)
	}
	m.ConfirmAutomationAction(ctx, op.ID)
	if _, err = m.CancelAutomationAction(ctx, op.ID); err == nil {
		t.Fatal("unknown cancelled")
	}
	if _, err = m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest}); err == nil {
		t.Fatal("unknown bypassed with new review")
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	reopened.ConfirmAutomationAction(ctx, op.ID)
	f.loseResponse = false
	got, err = reopened.ReconcileAutomationAction(ctx, op.ID)
	if err != nil || got.Status != "confirmed" || len(f.writes) != 1 {
		t.Fatalf("read-only reconcile %+v %v writes=%v", got, err, f.writes)
	}
}

func TestManagementDeleteRequiresBackup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "backup_failure"}[fail], func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			// Unknown fields must survive the full backup: this observed configuration
			// is made the original managed baseline before the management operation.
			original := append([]byte(nil), f.stored...)
			op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "delete", ExpectedDigest: managed.ExpectedDigest})
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if err = os.MkdirAll(filepath.Join(m.store.basePath, "rules", "management-backups", op.ID+".json"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.ConfirmAutomationAction(ctx, op.ID)
			if fail {
				if err == nil || len(f.writes) != 0 {
					t.Fatalf("delete without backup %+v %v", got, err)
				}
				return
			}
			if err != nil || got.Status != "confirmed" || len(f.writes) != 1 || f.writes[0] != "DELETE /api/config/automation/config/rule-kitchen" {
				t.Fatalf("delete %+v %v %v", got, err, f.writes)
			}
			raw, e := os.ReadFile(filepath.Join(m.store.basePath, "rules", "management-backups", op.ID+".json"))
			if e != nil {
				t.Fatal(e)
			}
			var backup struct {
				Config   json.RawMessage `json:"config"`
				Recovery string          `json:"recovery"`
			}
			json.Unmarshal(raw, &backup)
			_, want, _ := canonicalAutomationConfig(original)
			_, actual, _ := canonicalAutomationConfig(backup.Config)
			if actual != want || backup.Recovery == "" {
				t.Fatal("incomplete recovery material")
			}
			tombstone, e := m.GetManagedAutomation(managed.ID)
			if e != nil || tombstone.DeletedAt == nil {
				t.Fatalf("missing tombstone %+v %v", tombstone, e)
			}
			if _, e = m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest}); e == nil {
				t.Fatal("tombstone granted write")
			}
		})
	}
}

func TestManagementUpdateSameKeyAndCurrentVersion(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	original, _ := m.findSuggestion(managed.SuggestionID)
	draft, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*original))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ConfirmSuggestion(ctx, draft.ID); !errors.Is(err, ErrSuggestionNotFound) {
		t.Fatalf("managed draft entered ordinary confirmation: %v", err)
	}
	unchanged, _ := m.findSuggestion(original.ID)
	if unchanged.Status != "confirmed" || unchanged.SupersededBy != "" {
		t.Fatal("installed version superseded before confirmation")
	}
	if len(f.writes) != 0 {
		t.Fatal("draft wrote HA")
	}
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.beforeWrite = func() {
		durable, e := m.GetAutomationAction(ctx, op.ID)
		if e != nil || durable.Write.AttemptedAt == nil || durable.InstallOperationID == "" {
			t.Error("update was not journaled")
		}
		install, e := m.GetInstallOperation(durable.InstallOperationID)
		if e != nil || install.ConfigKey != "rule-kitchen" || install.PreviousOperationID != managed.OperationID || install.AuthorizationSuggestionID != "rule-kitchen" {
			t.Error("update lineage missing before write")
		}
	}
	got, err := m.ConfirmAutomationAction(ctx, op.ID)
	if err != nil || got.Status != "confirmed" {
		t.Fatalf("update %+v %v", got, err)
	}
	if len(f.writes) != 1 || f.writes[0] != "POST /api/config/automation/config/rule-kitchen" || f.payloads[0]["id"] != "rule-kitchen" {
		t.Fatalf("update did not replace exactly original key: %v %v", f.writes, f.payloads)
	}
	current, content, err := m.GetManagedAutomationVersion(managed.ID)
	if err != nil || current.ID != managed.ID || current.SuggestionID != "rule-kitchen" || current.SuggestionVersion != draft.ID || current.OperationID == managed.OperationID || content.ID != draft.ID || content.Intent.EntityID != "switch.b" || content.Intent.At != "19:30" {
		t.Fatalf("wrong current lineage %+v %+v %v", current, content, err)
	}
	if _, err = m.ReconcileInstallation(ctx, managed.OperationID); err != nil {
		t.Fatal(err)
	}
	after, _, err := m.GetManagedAutomationVersion(managed.ID)
	if err != nil || after.OperationID != current.OperationID || after.ExpectedDigest != current.ExpectedDigest {
		t.Fatalf("old reconcile rolled back update %+v %v", after, err)
	}
	if len(f.writes) != 1 {
		t.Fatal("old reconcile wrote")
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	after, content, err = reopened.GetManagedAutomationVersion(managed.ID)
	if err != nil || after.OperationID != current.OperationID || content.ID != draft.ID {
		t.Fatalf("lost current version on restart %+v %v", after, err)
	}
}

func TestManagementCompletedActionRemainsHistorical(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	first, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	first, err = m.ConfirmAutomationAction(ctx, first.ID)
	if err != nil || first.Status != "confirmed" {
		t.Fatal(err)
	}
	stop := true
	second, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "disable", ExpectedDigest: managed.ExpectedDigest, StopActions: &stop})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ConfirmAutomationAction(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	old, err := m.ReconcileAutomationAction(ctx, first.ID)
	if err != nil || old.Status != "confirmed" || len(f.writes) != 2 {
		t.Fatalf("historical result reclassified: %+v %v", old, err)
	}
	if _, err = m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest}); err != nil {
		t.Fatalf("historical action blocks new review: %v", err)
	}
}

func TestManagementExplicitDisableAndPreparedLifecycle(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	for _, req := range []AutomationActionRequest{
		{ManagedID: managed.ID, Action: "disable", ExpectedDigest: managed.ExpectedDigest},
		{ManagedID: managed.ID, Action: "trigger", ExpectedDigest: managed.ExpectedDigest},
		{ManagedID: managed.ID, Action: "enable", ExpectedDigest: "bad"},
		{ManagedID: managed.ID, Action: "delete", ExpectedDigest: managed.ExpectedDigest, RevisionID: "unexpected"},
	} {
		if _, err := m.PrepareAutomationAction(ctx, req); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	req := AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest}
	first, err := m.PrepareAutomationAction(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.PrepareAutomationAction(ctx, req); err == nil {
		t.Fatal("parallel prepared operation accepted")
	}
	cancelled, err := m.CancelAutomationAction(ctx, first.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatal(err)
	}
	if _, err = m.ConfirmAutomationAction(ctx, first.ID); err == nil {
		t.Fatal("cancelled operation wrote")
	}
	next, err := m.PrepareAutomationAction(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	m.store.mu.Lock()
	st, _ := m.store.readInstallState()
	for i := range st.Management {
		if st.Management[i].ID == next.ID {
			st.Management[i].CreatedAt = time.Now().UTC().Add(-6 * time.Minute)
			st.Management[i].ExpiresAt = st.Management[i].CreatedAt.Add(5 * time.Minute)
		}
	}
	m.store.writeInstallState(st)
	m.store.mu.Unlock()
	if _, err = m.PrepareAutomationAction(ctx, req); err != nil {
		t.Fatal(err)
	}
	expired, err := m.GetAutomationAction(ctx, next.ID)
	if err != nil || expired.Status != "expired" || len(f.writes) != 0 {
		t.Fatalf("expiry %+v %v", expired, err)
	}
}

func TestManagementInterruptedAttemptRecoveryNeverDispatches(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	// Revoke at the precise persisted-attempt boundary. The runtime callback is
	// trusted application code; it checks the real durable journal.
	ctx = managementAuthority("admin", "session", func(context.Context) error {
		durable, e := m.store.managementOperation(op.ID)
		if e == nil && durable.Write.AttemptedAt != nil {
			return errors.New("revoked")
		}
		return nil
	})
	if _, err = m.ConfirmAutomationAction(ctx, op.ID); err == nil {
		t.Fatal("late revocation ignored")
	}
	durable, err := m.GetAutomationAction(adminManagementContext(), op.ID)
	if err != nil || durable.Status != "applying" || durable.Write.AttemptedAt == nil || len(f.writes) != 0 {
		t.Fatalf("attempt boundary %+v %v", durable, err)
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	recovered, err := reopened.GetAutomationAction(adminManagementContext(), op.ID)
	if err != nil || recovered.Status != "unknown" {
		t.Fatalf("recovery %+v %v", recovered, err)
	}
	reopened.ConfirmAutomationAction(adminManagementContext(), op.ID)
	if len(f.writes) != 0 {
		t.Fatal("interrupted operation replayed")
	}
}

func TestManagementPreparedUpdateRejectsChangedDraft(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	source, _ := m.findSuggestion(managed.SuggestionID)
	draft, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
	if err != nil {
		t.Fatal(err)
	}
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.store.mu.Lock()
	st, _ := m.store.readInstallState()
	st.Drafts[0].Version.Intent.At = "21:00"
	m.store.writeInstallState(st)
	m.store.mu.Unlock()
	if _, err = m.ConfirmAutomationAction(ctx, op.ID); err == nil || len(f.writes) != 0 {
		t.Fatalf("changed reviewed draft executed: %v %v", err, f.writes)
	}
}

func TestManagementUpdateUnknownReconcileAndArchiveFailure(t *testing.T) {
	for _, mode := range []string{"lost_response", "delayed_load", "archive"} {
		t.Run(mode, func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			source, _ := m.findSuggestion(managed.SuggestionID)
			draft, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
			if err != nil {
				t.Fatal(err)
			}
			op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
			if err != nil {
				t.Fatal(err)
			}
			f.loseResponse = mode == "lost_response"
			f.delayLoad = mode == "delayed_load"
			archivePath := filepath.Join(m.store.basePath, "rules", draft.ID+".md")
			if mode == "archive" {
				if err = os.Mkdir(archivePath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.ConfirmAutomationAction(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			current, _, err := m.GetManagedAutomationVersion(managed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "archive" {
				if got.Status != "unknown" || current.OperationID != managed.OperationID {
					t.Fatal("uncertain update advanced current version")
				}
			} else {
				install, e := m.GetInstallOperation(got.InstallOperationID)
				if e != nil || got.Status != "confirmed" || install.Archive.Status != "pending" {
					t.Fatalf("archive failure changed HA fact %+v %v", install, e)
				}
				if err = os.Remove(archivePath); err != nil {
					t.Fatal(err)
				}
			}
			m.ConfirmAutomationAction(ctx, op.ID)
			f.loseResponse = false
			f.runtime = append([]byte(nil), f.stored...)
			got, err = m.ReconcileAutomationAction(ctx, op.ID)
			if err != nil || got.Status != "confirmed" || len(f.writes) != 1 {
				t.Fatalf("update reconcile %+v %v %v", got, err, f.writes)
			}
			current, content, err := m.GetManagedAutomationVersion(managed.ID)
			if err != nil || current.OperationID == managed.OperationID || content.ID != draft.ID {
				t.Fatal("update did not converge")
			}
			install, _ := m.GetInstallOperation(got.InstallOperationID)
			if install.Archive.Status != "complete" {
				t.Fatal("local archive did not recover")
			}
		})
	}
}

func TestManagementFullGenericBackup(t *testing.T) {
	_, m, managed := newManagementFixture(t)
	raw := json.RawMessage(`{"id":"rule-kitchen","alias":"original","unknown":{"array":[null,false,3,{"private":"retain locally"}]},"triggers":[]}`)
	_, digest, _ := canonicalAutomationConfig(raw)
	op := ManagementOperation{ID: "0123456789abcdef0123456789abcdef", AutomationActionRequest: AutomationActionRequest{ManagedID: managed.ID, ExpectedDigest: digest}, ConfigKey: "rule-kitchen"}
	ref, err := m.store.saveManagementBackup(op, raw)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(m.store.basePath, "rules", "management-backups", ref))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Config json.RawMessage `json:"config"`
	}
	json.Unmarshal(saved, &envelope)
	if !bytes.Contains(envelope.Config, []byte(`"unknown"`)) || !bytes.Contains(envelope.Config, []byte(`"private"`)) || !bytes.Contains(envelope.Config, []byte(`null`)) {
		t.Fatal("generic backup dropped fields")
	}
	_, savedDigest, err := canonicalAutomationConfig(envelope.Config)
	if err != nil || savedDigest != digest {
		t.Fatal("generic backup changed complete config")
	}
}

func TestManagementSchema2RejectsBrokenLineage(t *testing.T) {
	for _, mode := range []string{"anchor", "predecessor", "missing_install", "draft_digest", "current_version", "attempt_missing", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			source, _ := m.findSuggestion(managed.SuggestionID)
			draft, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
			if err != nil {
				t.Fatal(err)
			}
			op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
			if err != nil {
				t.Fatal(err)
			}
			f.loseResponse = true
			if _, err = m.ConfirmAutomationAction(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			m.store.mu.Lock()
			st, err := m.store.readInstallState()
			if err != nil {
				m.store.mu.Unlock()
				t.Fatal(err)
			}
			switch mode {
			case "anchor":
				st.Operations[1].AuthorizationSuggestionID = "other-rule"
			case "predecessor":
				st.Operations[1].PreviousOperationID = "missing"
			case "missing_install":
				st.Operations = st.Operations[:1]
			case "draft_digest":
				st.Drafts[0].Digest = managed.ExpectedDigest
			case "current_version":
				st.Managed[0].SuggestionVersion = draft.ID
			case "attempt_missing":
				st.Management[0].Write.AttemptedAt = nil
			case "cycle":
				st.Operations[1].PreviousOperationID = st.Operations[1].ID
			}
			if err = m.store.writeInstallState(st); err != nil {
				m.store.mu.Unlock()
				t.Fatal(err)
			}
			m.store.mu.Unlock()
			before := len(f.writes)
			if _, _, err = m.GetManagedAutomationVersion(managed.ID); err == nil {
				t.Fatal("broken provenance accepted")
			}
			if _, err = m.ConfirmAutomationAction(ctx, op.ID); err == nil {
				t.Fatal("corrupt operation accepted")
			}
			if _, err = NewManager(m.cfg, zap.NewNop()); err == nil {
				t.Fatal("corrupt restart accepted")
			}
			if len(f.writes) != before {
				t.Fatal("corrupt store wrote HA")
			}
		})
	}
}

func TestManagementMigrationPreservesOwnedSchema1(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	m.store.mu.Lock()
	st, err := m.store.readInstallState()
	m.store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	st.SchemaVersion = 1
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.store.basePath, "rules", "install-state.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	record, version, err := reopened.GetManagedAutomationVersion(managed.ID)
	if err != nil || record.OperationID != managed.OperationID || record.ExpectedDigest != managed.ExpectedDigest || version.ID != "rule-kitchen" || len(f.writes) != 0 {
		t.Fatalf("lost migrated ownership %+v %v", record, err)
	}
	backup, err := os.ReadFile(filepath.Join(m.store.basePath, "rules", "install-state.pre-management-v2.json"))
	if err != nil || !bytes.Equal(backup, raw) {
		t.Fatal("ownership migration backup mismatch")
	}
}

func TestManagementPostWritePersistenceFailureNeverReplays(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.store.basePath, "rules", "install-state.json")
	saved := path + ".attempt"
	f.beforeWrite = func() {
		if e := os.Rename(path, saved); e != nil {
			t.Error(e)
		}
		if e := os.Mkdir(path, 0700); e != nil {
			t.Error(e)
		}
	}
	got, err := m.ConfirmAutomationAction(ctx, op.ID)
	if !errors.Is(err, ErrManagementStore) || got.Write.Status != "acknowledged" || len(f.writes) != 1 {
		t.Fatalf("lost independent write fact %+v %v", got, err)
	}
	f.beforeWrite = nil
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if _, err = reopened.ConfirmAutomationAction(ctx, op.ID); err == nil {
		t.Fatal("failed local persistence allowed replay")
	}
	got, err = reopened.ReconcileAutomationAction(ctx, op.ID)
	if err != nil || got.Status != "confirmed" || len(f.writes) != 1 {
		t.Fatalf("persistence recovery %+v %v", got, err)
	}
}

func TestManagementSuccessiveUpdatesKeepAnchorAndRejectStaleDraft(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	source, _ := m.findSuggestion(managed.SuggestionID)
	stale, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
	if err != nil {
		t.Fatal(err)
	}
	var firstInstall string
	for i := 0; i < 2; i++ {
		current, content, e := m.GetManagedAutomationVersion(managed.ID)
		if e != nil {
			t.Fatal(e)
		}
		draft := first
		if i == 1 {
			req := revisionRequest(content)
			req.Intent.At = "20:00"
			draft, e = m.ReviseManagedAutomation(ctx, managed.ID, req)
			if e != nil {
				t.Fatal(e)
			}
		}
		op, e := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: current.ExpectedDigest, RevisionID: draft.ID})
		if e != nil {
			t.Fatal(e)
		}
		op, e = m.ConfirmAutomationAction(ctx, op.ID)
		if e != nil || op.Status != "confirmed" {
			t.Fatalf("update %d %+v %v", i, op, e)
		}
		if i == 0 {
			firstInstall = op.InstallOperationID
		}
	}
	current, content, err := m.GetManagedAutomationVersion(managed.ID)
	if err != nil || current.SuggestionID != "rule-kitchen" || content.Intent.At != "20:00" {
		t.Fatalf("anchor changed %+v %v", current, err)
	}
	m.ReconcileInstallation(ctx, firstInstall)
	m.ReconcileInstallation(ctx, managed.OperationID)
	after, _, err := m.GetManagedAutomationVersion(managed.ID)
	if err != nil || after.OperationID != current.OperationID {
		t.Fatal("predecessor overwrote current")
	}
	if _, err = m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: current.ExpectedDigest, RevisionID: stale.ID}); err == nil {
		t.Fatal("stale draft used after current version advanced")
	}
	if len(f.writes) != 2 {
		t.Fatal("unexpected update write count")
	}
}

func TestManagementStoppedAndCancelledWorkCannotWrite(t *testing.T) {
	for _, mode := range []string{"cancelled", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "stopped" {
				m.Stop()
			} else {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if _, err = m.ConfirmAutomationAction(ctx, op.ID); err == nil || len(f.writes) != 0 {
				t.Fatalf("work outlived lifecycle %v %v", err, f.writes)
			}
		})
	}
}

func TestManagementExpiryDuringFinalAuthorizationPreventsDispatch(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	m.store.mu.Lock()
	st, _ := m.store.readInstallState()
	for i := range st.Management {
		if st.Management[i].ID == op.ID {
			st.Management[i].CreatedAt = time.Now().UTC().Add(-5*time.Minute + 250*time.Millisecond)
			st.Management[i].ExpiresAt = st.Management[i].CreatedAt.Add(5 * time.Minute)
		}
	}
	m.store.writeInstallState(st)
	m.store.mu.Unlock()
	crossed := false
	ctx = managementAuthority("admin", "session", func(context.Context) error {
		durable, e := m.store.managementOperation(op.ID)
		if e == nil && durable.Write.AttemptedAt != nil {
			crossed = true
			time.Sleep(time.Until(durable.ExpiresAt) + 5*time.Millisecond)
		}
		return nil
	})
	_, err = m.ConfirmAutomationAction(ctx, op.ID)
	if !crossed {
		t.Fatal("fixture did not reach final authorization boundary")
	}
	if err == nil || len(f.writes) != 0 {
		t.Fatalf("expired review dispatched: %v %v", err, f.writes)
	}
}

func TestManagementUpdateExpiryAfterDraftReadPreventsDispatch(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	source, err := m.findSuggestion(managed.SuggestionID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
	if err != nil {
		t.Fatal(err)
	}
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.store.mu.Lock()
	st, readErr := m.store.readInstallState()
	if readErr == nil {
		for i := range st.Management {
			if st.Management[i].ID == op.ID {
				st.Management[i].CreatedAt = time.Now().UTC().Add(-5*time.Minute + 350*time.Millisecond)
				st.Management[i].ExpiresAt = st.Management[i].CreatedAt.Add(5 * time.Minute)
			}
		}
		readErr = m.store.writeInstallState(st)
	}
	m.store.mu.Unlock()
	if readErr != nil {
		t.Fatal(readErr)
	}
	postAttemptChecks := 0
	ctx = managementAuthority("admin", "session", func(context.Context) error {
		durable, e := m.store.managementOperation(op.ID)
		if e == nil && durable.Write.AttemptedAt != nil {
			postAttemptChecks++
			if postAttemptChecks == 2 {
				time.Sleep(time.Until(durable.ExpiresAt) + 5*time.Millisecond)
			}
		}
		return nil
	})
	_, _ = m.ConfirmAutomationAction(ctx, op.ID)
	if postAttemptChecks < 2 {
		t.Fatal("fixture did not cross expiry during the second post-attempt authority callback")
	}
	if len(f.writes) != 0 {
		t.Fatalf("expired update dispatched: %v", f.writes)
	}
	durable, err := m.GetAutomationAction(adminManagementContext(), op.ID)
	if err != nil || durable.Write.AttemptedAt == nil || (durable.Status != "unknown" && durable.Status != "applying") {
		t.Fatalf("attempt not retained: %+v %v", durable, err)
	}
	if _, err = m.ConfirmAutomationAction(adminManagementContext(), op.ID); err == nil || len(f.writes) != 0 {
		t.Fatalf("expired attempt replayed: %v %v", err, f.writes)
	}
	current, err := m.GetManagedAutomation(managed.ID)
	if err != nil || current.OperationID != managed.OperationID || current.ExpectedDigest != managed.ExpectedDigest {
		t.Fatalf("undispatched update advanced current version: %+v %v", current, err)
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if _, err = reopened.ConfirmAutomationAction(adminManagementContext(), op.ID); err == nil || len(f.writes) != 0 {
		t.Fatalf("restart replayed expired attempt: %v %v", err, f.writes)
	}
	if got, err := reopened.ReconcileAutomationAction(adminManagementContext(), op.ID); err != nil || got.Status != "unknown" || len(f.writes) != 0 {
		t.Fatalf("read-only recovery changed or replayed undispatched update: %+v %v %v", got, err, f.writes)
	}
}

func TestManagementWriteDeadlineIncludesLateTokenAcquisition(t *testing.T) {
	for _, action := range []string{"enable", "disable", "delete", "update"} {
		t.Run(action, func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			stop := false
			req := AutomationActionRequest{ManagedID: managed.ID, Action: action, ExpectedDigest: managed.ExpectedDigest}
			if action == "disable" {
				req.StopActions = &stop
			}
			if action == "update" {
				source, err := m.findSuggestion(managed.SuggestionID)
				if err != nil {
					t.Fatal(err)
				}
				draft, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
				if err != nil {
					t.Fatal(err)
				}
				req.RevisionID = draft.ID
			}
			op, err := m.PrepareAutomationAction(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			m.store.mu.Lock()
			st, readErr := m.store.readInstallState()
			if readErr == nil {
				for i := range st.Management {
					if st.Management[i].ID == op.ID {
						st.Management[i].CreatedAt = time.Now().UTC().Add(-5*time.Minute + 350*time.Millisecond)
						st.Management[i].ExpiresAt = st.Management[i].CreatedAt.Add(5 * time.Minute)
					}
				}
				readErr = m.store.writeInstallState(st)
			}
			m.store.mu.Unlock()
			if readErr != nil {
				t.Fatal(readErr)
			}
			crossed := false
			var tokenContextErr error
			m.client.tokenProvider = managementTokenProvider{token: func(tokenCtx context.Context) (string, error) {
				durable, e := m.store.managementOperation(op.ID)
				if !crossed && e == nil && durable.Write.AttemptedAt != nil {
					crossed = true
					// Deliberately return a usable token late, even if cancellation
					// occurred: the request context must still prevent transport.
					time.Sleep(time.Until(durable.ExpiresAt) + 5*time.Millisecond)
					tokenContextErr = tokenCtx.Err()
				}
				return "fixture-token", nil
			}}
			_, _ = m.ConfirmAutomationAction(ctx, op.ID)
			if !crossed || len(f.writes) != 0 {
				t.Fatalf("late token allowed dispatch: crossed=%v writes=%v", crossed, f.writes)
			}
			if !errors.Is(tokenContextErr, context.DeadlineExceeded) || ctx.Err() != nil {
				t.Fatalf("write deadline was not isolated: write=%v original=%v", tokenContextErr, ctx.Err())
			}
			durable, err := m.GetAutomationAction(ctx, op.ID)
			if err != nil || durable.Write.AttemptedAt == nil || durable.Status != "unknown" {
				t.Fatalf("attempt not retained: %+v %v", durable, err)
			}
			reopened, err := NewManager(m.cfg, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Stop()
			if _, err = reopened.ConfirmAutomationAction(ctx, op.ID); err == nil || len(f.writes) != 0 {
				t.Fatalf("restart replayed late-token attempt: %v %v", err, f.writes)
			}
			if _, err = reopened.ReconcileAutomationAction(ctx, op.ID); err != nil || len(f.writes) != 0 {
				t.Fatalf("read-only recovery failed or replayed: %v %v", err, f.writes)
			}
		})
	}
}

func TestManagementReviewedMappingRejectsRenameAfterOrdinaryReconcile(t *testing.T) {
	for _, action := range []string{"enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			f, m, managed := newManagementFixture(t)
			ctx := adminManagementContext()
			stop := false
			req := AutomationActionRequest{ManagedID: managed.ID, Action: action, ExpectedDigest: managed.ExpectedDigest}
			if action == "disable" {
				req.StopActions = &stop
			}
			op, err := m.PrepareAutomationAction(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.entityID = "automation.renamed"
			f.mu.Unlock()
			if _, err = m.ReconcileInstallation(ctx, managed.OperationID); err != nil {
				t.Fatal(err)
			}
			current, err := m.GetManagedAutomation(managed.ID)
			if err != nil || current.EntityID != "automation.renamed" || current.OperationID != managed.OperationID || current.SuggestionVersion != managed.SuggestionVersion || len(f.writes) != 0 {
				t.Fatalf("ordinary reconciliation did not refresh only the mapping: %+v %v %v", current, err, f.writes)
			}
			if _, err = m.ConfirmAutomationAction(ctx, op.ID); !errors.Is(err, ErrManagementConflict) || len(f.writes) != 0 {
				t.Fatalf("stale reviewed mapping dispatched: %v %v", err, f.writes)
			}
			historical, err := m.GetAutomationAction(ctx, op.ID)
			if err != nil || historical.EntityID != "automation.installed" || historical.Write.AttemptedAt != nil {
				t.Fatalf("historical mapping was corrupted: %+v %v", historical, err)
			}
			if _, err = m.CancelAutomationAction(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			fresh, err := m.PrepareAutomationAction(ctx, req)
			if err != nil || fresh.EntityID != "automation.renamed" {
				t.Fatalf("fresh review did not bind new mapping: %+v %v", fresh, err)
			}
			if _, err = m.ConfirmAutomationAction(ctx, fresh.ID); err != nil || len(f.writes) != 1 || f.payloads[0]["entity_id"] != "automation.renamed" {
				t.Fatalf("fresh review failed: %v %v %v", err, f.writes, f.payloads)
			}
		})
	}
}

func TestManagementRotatedAdministratorCanRecoverButCannotConfirm(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	rotated := managementAuthority("other-admin", "new-session", func(context.Context) error { return nil })
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ConfirmAutomationAction(rotated, op.ID); !errors.Is(err, ErrManagementForbidden) || len(f.writes) != 0 {
		t.Fatalf("rotated confirmation %v %v", err, f.writes)
	}
	got, err := m.CancelAutomationAction(rotated, op.ID)
	if err != nil || got.Status != "cancelled" || len(f.writes) != 0 {
		t.Fatalf("admin could not abandon prepared review %+v %v", got, err)
	}
	op, err = m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest})
	if err != nil {
		t.Fatal(err)
	}
	f.loseResponse = true
	if _, err = m.ConfirmAutomationAction(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	before := len(f.writes)
	got, err = m.GetAutomationAction(rotated, op.ID)
	if err != nil || got.Status != "unknown" {
		t.Fatalf("rotated GET %+v %v", got, err)
	}
	got, err = m.ReconcileAutomationAction(rotated, op.ID)
	if err != nil || got.Status != "confirmed" || got.UserID != "admin" || got.SessionID != "session" || len(f.writes) != before {
		t.Fatalf("rotated read-only recovery %+v %v %v", got, err, f.writes)
	}
	denied := managementAuthority("other-admin", "new-session", func(context.Context) error { return errors.New("revoked") })
	if _, err = m.GetAutomationAction(denied, op.ID); !errors.Is(err, ErrManagementForbidden) {
		t.Fatal("revoked admin read operation")
	}
	if _, err = m.ReconcileAutomationAction(denied, op.ID); !errors.Is(err, ErrManagementForbidden) {
		t.Fatal("revoked admin reconciled")
	}
}

func TestManagementLostIDDiscoveryIsReadOnlyAndCurrent(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	type reader interface {
		GetLatestAutomationAction(context.Context, string) (ManagementOperation, error)
		GetLatestManagedAutomationDraft(context.Context, string) (ManagedAutomationDraft, error)
	}
	api, ok := any(m).(reader)
	if !ok {
		t.Fatal("missing durable lost-ID recovery lookup")
	}
	if _, err := api.GetLatestAutomationAction(ctx, managed.ID); !errors.Is(err, ErrManagementNotFound) {
		t.Fatalf("invented action %v", err)
	}
	source, _ := m.findSuggestion(managed.SuggestionID)
	first, err := m.ReviseManagedAutomation(ctx, managed.ID, revisionRequest(*source))
	if err != nil {
		t.Fatal(err)
	}
	req := revisionRequest(*source)
	req.Intent.At = "20:30"
	latest, err := m.ReviseManagedAutomation(ctx, managed.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := api.GetLatestManagedAutomationDraft(ctx, managed.ID)
	if err != nil || draft.ID != latest.ID || draft.ID == first.ID {
		t.Fatalf("draft lookup %+v %v", draft, err)
	}
	rotated := managementAuthority("admin", "rotated", func(context.Context) error { return nil })
	if _, err = api.GetLatestManagedAutomationDraft(rotated, managed.ID); !errors.Is(err, ErrManagementNotFound) {
		t.Fatal("draft content crossed session scope")
	}
	op, err := m.PrepareAutomationAction(ctx, AutomationActionRequest{ManagedID: managed.ID, Action: "update", ExpectedDigest: managed.ExpectedDigest, RevisionID: latest.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Treat the returned ID as lost: lookup uses only the stable managed identity.
	path := filepath.Join(m.store.basePath, "rules", "install-state.json")
	before, _ := os.ReadFile(path)
	f.onRead = func() { t.Error("local discovery made an HA read") }
	found, err := api.GetLatestAutomationAction(rotated, managed.ID)
	if err != nil || found.ID != op.ID || found.Status != "prepared" {
		t.Fatalf("lost review %+v %v", found, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(f.writes) != 0 {
		t.Fatal("GET mutated journal or wrote HA")
	}
	f.onRead = nil
	f.loseResponse = true
	if _, err = m.ConfirmAutomationAction(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	found, err = api.GetLatestAutomationAction(rotated, managed.ID)
	if err != nil || found.ID != op.ID || found.Status != "unknown" {
		t.Fatalf("lost unknown %+v %v", found, err)
	}
	f.loseResponse = false
	if _, err = m.ReconcileAutomationAction(rotated, found.ID); err != nil {
		t.Fatal(err)
	}
	found, err = api.GetLatestAutomationAction(rotated, managed.ID)
	if err != nil || found.Status != "confirmed" || len(f.writes) != 1 {
		t.Fatalf("lost terminal outcome %+v %v", found, err)
	}
	if _, err = api.GetLatestManagedAutomationDraft(ctx, managed.ID); !errors.Is(err, ErrManagementNotFound) {
		t.Fatalf("stale draft lookup %v", err)
	}
	if _, err = api.GetLatestAutomationAction(ctx, "unknown"); !errors.Is(err, ErrManagementNotFound) {
		t.Fatal("arbitrary scope accepted")
	}
	denied := managementAuthority("admin", "session", func(context.Context) error { return errors.New("revoked") })
	if _, err = api.GetLatestAutomationAction(denied, managed.ID); !errors.Is(err, ErrManagementForbidden) {
		t.Fatal("revoked discovery allowed")
	}
}

func TestManagementDuplicatePreparationStaysLocal(t *testing.T) {
	f, m, managed := newManagementFixture(t)
	ctx := adminManagementContext()
	req := AutomationActionRequest{ManagedID: managed.ID, Action: "enable", ExpectedDigest: managed.ExpectedDigest}
	if _, err := m.PrepareAutomationAction(ctx, req); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.store.basePath, "rules", "install-state.json")
	before, _ := os.ReadFile(path)
	f.onRead = func() { t.Error("duplicate preparation made an HA read") }
	if _, err := m.PrepareAutomationAction(ctx, req); !errors.Is(err, ErrManagementConflict) {
		t.Fatalf("duplicate preparation %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(f.writes) != 0 {
		t.Fatal("duplicate replaced review or wrote HA")
	}
}

// Losing schema1 data or overwriting its backup would make rollback unsafe.
func TestManagementSchemaMigrationBackup(t *testing.T) {
	for _, obstruction := range []string{"", "directory", "different_backup", "corrupt", "future"} {
		t.Run(obstruction, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "rules")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			raw := []byte(`{"schema_version":1,"operations":[],"managed":[]}`)
			if obstruction == "corrupt" {
				raw = []byte(`{"schema_version":1,"operations":null,"managed":[],"schema_version":1}`)
			}
			if obstruction == "future" {
				raw = []byte(`{"schema_version":99,"operations":[],"managed":[]}`)
			}
			path := filepath.Join(dir, "install-state.json")
			backup := filepath.Join(dir, "install-state.pre-management-v2.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if obstruction == "directory" {
				os.Mkdir(backup, 0700)
			}
			if obstruction == "different_backup" {
				os.WriteFile(backup, []byte("different"), 0600)
			}
			m, err := NewManager(HAConfig{BaseURL: "http://127.0.0.1:1", AgentVaultPath: base}, zap.NewNop())
			if obstruction != "" {
				if err == nil {
					m.Stop()
					t.Fatal("unsafe migration accepted")
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(raw, after) {
					t.Fatal("failed migration changed original")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			m.Stop()
			saved, err := os.ReadFile(backup)
			if err != nil || !bytes.Equal(saved, raw) {
				t.Fatalf("missing verified original backup: %v", err)
			}
			after, _ := os.ReadFile(path)
			var envelope struct {
				SchemaVersion int `json:"schema_version"`
			}
			json.Unmarshal(after, &envelope)
			if envelope.SchemaVersion != 2 {
				t.Fatal("management state did not migrate")
			}
			m, err = NewManager(HAConfig{BaseURL: "http://127.0.0.1:1", AgentVaultPath: base}, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			m.Stop()
			saved, _ = os.ReadFile(backup)
			if !bytes.Equal(saved, raw) {
				t.Fatal("restart changed original backup")
			}
		})
	}
}
