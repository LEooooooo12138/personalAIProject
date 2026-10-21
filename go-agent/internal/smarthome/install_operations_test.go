package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type installFixture struct {
	registryUnavailable bool
	manager             *Manager
	stored              []byte
	runtime             []byte
	writes              atomic.Int32
	loseCreate          bool
	rejectCreate        bool
	failReload          bool
	runtimeMissing      bool
	registryExisting    bool
	durableBeforeWrite  atomic.Bool
}

func newInstallFixture(t *testing.T) (*installFixture, *Manager) {
	t.Helper()
	f := &installFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			f.writes.Add(1)
			op, err := f.manager.GetInstallOperationForSuggestion("rule-kitchen")
			if err == nil && op.ID != "" && op.Create.AttemptedAt != nil {
				f.durableBeforeWrite.Store(true)
			} else {
				t.Error("write preceded durable operation")
			}
		}
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(w).Encode(map[string]any{"version": "2026.7.2", "components": []string{"automation", "config"}, "time_zone": "UTC"})
		case "/api/config/automation/config/rule-kitchen":
			if r.Method == "POST" {
				if f.rejectCreate {
					w.WriteHeader(400)
					return
				}
				f.stored, _ = io.ReadAll(r.Body)
				if f.loseCreate {
					c, _, _ := w.(http.Hijacker).Hijack()
					c.Close()
					return
				}
				io.WriteString(w, `{}`)
				return
			}
			if f.stored == nil {
				w.WriteHeader(404)
				return
			}
			w.Write(f.stored)
		case "/api/services/automation/reload":
			if f.failReload {
				w.WriteHeader(503)
				return
			}
			f.runtime = append([]byte(nil), f.stored...)
			io.WriteString(w, `[]`)
		case "/api/states/automation.installed":
			io.WriteString(w, `{"entity_id":"automation.installed","state":"off","attributes":{}}`)
		case "/api/websocket":
			c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			c.WriteJSON(map[string]any{"type": "auth_required"})
			var auth any
			c.ReadJSON(&auth)
			c.WriteJSON(map[string]any{"type": "auth_ok"})
			for {
				var req map[string]any
				if c.ReadJSON(&req) != nil {
					return
				}
				res := map[string]any{"id": req["id"], "type": "result", "success": true}
				switch req["type"] {
				case "config/entity_registry/list":
					if f.registryUnavailable {
						res["success"] = false
						res["error"] = map[string]any{"code": "unauthorized"}
						break
					}
					res["result"] = []map[string]any{}
					if f.stored != nil || f.registryExisting {
						res["result"] = []map[string]any{{"entity_id": "automation.installed", "platform": "automation", "unique_id": "rule-kitchen"}}
					}
				case "automation/config":
					if f.runtimeMissing || f.runtime == nil {
						res["success"] = false
						res["error"] = map[string]any{"code": "not_found"}
					} else {
						res["result"] = map[string]any{"config": json.RawMessage(f.runtime)}
					}
				default:
					t.Errorf("unexpected WS %v", req["type"])
					return
				}
				if c.WriteJSON(res) != nil {
					return
				}
			}
		default:
			t.Errorf("unexpected HTTP %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(server.Close)
	m, err := NewManager(HAConfig{BaseURL: server.URL, AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	f.manager = m
	if err = m.store.SaveSuggestions([]RuleSuggestion{decodeSuggestion(t, executableSuggestion)}); err != nil {
		t.Fatal(err)
	}
	return f, m
}
func TestInstallOperationAmbiguousOutcome(t *testing.T) {
	for _, mode := range []string{"lost_create", "reload", "runtime"} {
		t.Run(mode, func(t *testing.T) {
			f, m := newInstallFixture(t)
			f.loseCreate = mode == "lost_create"
			f.failReload = mode == "reload"
			f.runtimeMissing = mode == "runtime"
			m.ConfirmSuggestion(context.Background(), "rule-kitchen")
			op, err := m.GetInstallOperationForSuggestion("rule-kitchen")
			if err != nil || op.Status != "unknown" || !f.durableBeforeWrite.Load() {
				t.Fatalf("op=%+v err=%v", op, err)
			}
			before := f.writes.Load()
			m.ConfirmSuggestion(context.Background(), "rule-kitchen")
			if f.writes.Load() != before {
				t.Fatal("ambiguous attempt replayed")
			}
			reopened, err := NewManager(m.cfg, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			reopened.ConfirmSuggestion(context.Background(), "rule-kitchen")
			if f.writes.Load() != before {
				t.Fatal("restart replayed write")
			}
		})
	}
}
func TestInstallReconcileNeverWrites(t *testing.T) {
	f, m := newInstallFixture(t)
	f.loseCreate = true
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	op, _ := m.GetInstallOperationForSuggestion("rule-kitchen")
	before := f.writes.Load()
	got, _ := m.ReconcileInstallation(context.Background(), op.ID)
	if got.Status != "unknown" {
		t.Fatalf("stored only became %s", got.Status)
	}
	f.runtime = append([]byte(nil), f.stored...)
	got, err := m.ReconcileInstallation(context.Background(), op.ID)
	if err != nil || got.Status != "confirmed" {
		t.Fatalf("%+v %v", got, err)
	}
	if f.writes.Load() != before {
		t.Fatal("reconcile wrote HA")
	}
	observed, err := m.ObserveAutomation(context.Background(), AutomationRef{Kind: "managed", ID: got.ManagedAutomationID})
	if err != nil || observed.Ownership != "managed" || observed.State != "off" {
		t.Fatalf("managed=%+v %v", observed, err)
	}
	expected := got.ExpectedDigest
	f.stored = []byte(`{"id":"rule-kitchen","alias":"drift"}`)
	f.runtime = f.stored
	got, _ = m.ReconcileInstallation(context.Background(), op.ID)
	if got.ExpectedDigest != expected || got.Status != "unknown" {
		t.Fatalf("drift %+v", got)
	}
}
func TestInstallArchiveFailureIsSeparate(t *testing.T) {
	f, m := newInstallFixture(t)
	os.Mkdir(filepath.Join(m.store.basePath, "rules", "rule-kitchen.md"), 0700)
	s, err := m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	if err != nil || s.Status != "confirmed" {
		t.Fatalf("HA install misreported %v %v", s, err)
	}
	op, _ := m.GetInstallOperationForSuggestion("rule-kitchen")
	if op.Status != "confirmed" || op.Archive.Status != "pending" || op.Archive.ErrorCode == "" {
		t.Fatalf("%+v", op)
	}
	before := f.writes.Load()
	os.Remove(filepath.Join(m.store.basePath, "rules", "rule-kitchen.md"))
	op, err = m.ReconcileInstallation(context.Background(), op.ID)
	if err != nil || op.Archive.Status != "complete" || f.writes.Load() != before {
		t.Fatalf("%+v %v", op, err)
	}
}
func TestInstallDoesNotAdoptPreexisting(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "conflict"}[matching], func(t *testing.T) {
			f, m := newInstallFixture(t)
			s, _ := m.findSuggestion("rule-kitchen")
			a, _ := BuildAutomation(*s)
			f.stored, _ = json.Marshal(a)
			if !matching {
				f.stored = []byte(`{"id":"rule-kitchen","alias":"other"}`)
			}
			f.runtime = f.stored
			if _, err := m.ConfirmSuggestion(context.Background(), s.ID); !errors.Is(err, ErrInstallPreexisting) {
				t.Fatalf("preexisting error %v", err)
			}
			if _, err := m.GetInstallOperationForSuggestion(s.ID); !errors.Is(err, ErrInstallOperationNotFound) {
				t.Fatalf("preflight created operation %v", err)
			}
			got, _ := m.findSuggestion(s.ID)
			if got.Status != "pending" || f.writes.Load() != 0 {
				t.Fatal("preexisting rule changed")
			}
			if _, err := m.IgnoreSuggestion(s.ID); err != nil {
				t.Fatalf("unattempted suggestion locked: %v", err)
			}
		})
	}
}
func TestInstallExplicitRejectionAndRestart(t *testing.T) {
	f, m := newInstallFixture(t)
	f.rejectCreate = true
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	op, _ := m.GetInstallOperationForSuggestion("rule-kitchen")
	if op.Status != "failed" {
		t.Fatalf("%+v", op)
	}
	before := f.writes.Load()
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	if before != f.writes.Load() {
		t.Fatal("failed replayed")
	}
	op.Status = "applying"
	op.UpdatedAt = time.Now().UTC()
	if err := m.store.saveInstallOperation(op); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewManager(m.cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reopened.GetInstallOperation(op.ID)
	if got.Status != "unknown" {
		t.Fatalf("restart %+v", got)
	}
}

func TestInstallLegacyMigrationPreservesBackup(t *testing.T) {
	for _, status := range []string{"applying", "failed", "confirmed"} {
		t.Run(status, func(t *testing.T) {
			f, m := newInstallFixture(t)
			s, _ := m.findSuggestion("rule-kitchen")
			s.Status = status
			m.store.mu.Lock()
			err := m.store.writeSuggestions([]RuleSuggestion{*s})
			m.store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(m.store.basePath, "rules", "suggestions.json"))
			reopened, err := NewManager(m.cfg, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			op, err := reopened.GetInstallOperationForSuggestion(s.ID)
			if err != nil || op.Status != "unknown" || op.OwnAttempt {
				t.Fatalf("%+v %v", op, err)
			}
			if status != "confirmed" {
				backup, e := os.ReadFile(filepath.Join(m.store.basePath, "rules", "suggestions.pre-install-v1.json"))
				if e != nil || string(backup) != string(before) {
					t.Fatalf("backup %v", e)
				}
			}
			reopened.ConfirmSuggestion(context.Background(), s.ID)
			reopened.ReconcileInstallation(context.Background(), op.ID)
			if f.writes.Load() != 0 {
				t.Fatal("legacy replay")
			}
		})
	}
}
func TestInstallCorruptStateFailsClosed(t *testing.T) {
	for _, raw := range []string{`{`, `null`, `{}`, `{"schema_version":2}`, `{"schema_version":1,"operations":[{}]}`, `{"schema_version":1,"schema_version":1}`} {
		t.Run(raw, func(t *testing.T) {
			f, m := newInstallFixture(t)
			p := filepath.Join(m.store.basePath, "rules", "install-state.json")
			os.WriteFile(p, []byte(raw), 0600)
			if _, err := NewManager(m.cfg, zap.NewNop()); err == nil {
				t.Fatal("corrupt/unsupported state accepted")
			}
			if _, err := m.ConfirmSuggestion(context.Background(), "rule-kitchen"); err == nil {
				t.Fatal("corrupt state dispatched")
			}
			after, _ := os.ReadFile(p)
			if string(after) != raw || f.writes.Load() != 0 {
				t.Fatal("corrupt state reset or wrote HA")
			}
		})
	}
}
func TestInstallJournalFailurePreventsWrite(t *testing.T) {
	f, m := newInstallFixture(t)
	os.Mkdir(filepath.Join(m.store.basePath, "rules", "install-state.json"), 0700)
	if _, err := m.ConfirmSuggestion(context.Background(), "rule-kitchen"); err == nil {
		t.Fatal("journal failure hidden")
	}
	if f.writes.Load() != 0 {
		t.Fatal("write without journal")
	}
}
func TestInstallRejectedCannotAdoptLaterConfig(t *testing.T) {
	f, m := newInstallFixture(t)
	f.rejectCreate = true
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	op, _ := m.GetInstallOperationForSuggestion("rule-kitchen")
	s, _ := m.findSuggestion("rule-kitchen")
	a, _ := BuildAutomation(*s)
	f.stored, _ = json.Marshal(a)
	f.runtime = f.stored
	got, _ := m.ReconcileInstallation(context.Background(), op.ID)
	if got.Status != "failed" || got.ManagedAutomationID != "" {
		t.Fatalf("adopted after rejection: %+v", got)
	}
}

func TestInstallDoesNotOverwriteRuntimeOnlyPreexisting(t *testing.T) {
	f, m := newInstallFixture(t)
	f.registryExisting = true
	_, err := m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	s, _ := m.findSuggestion("rule-kitchen")
	if !errors.Is(err, ErrInstallPreexisting) || s.Status != "pending" || f.writes.Load() != 0 {
		t.Fatalf("runtime-only external overwritten: %+v %v writes=%d", s, err, f.writes.Load())
	}
}
func TestInstallUnattemptedPreflightCanRecoverByExplicitConfirmation(t *testing.T) {
	f, m := newInstallFixture(t)
	f.registryUnavailable = true
	if _, err := m.ConfirmSuggestion(context.Background(), "rule-kitchen"); err == nil {
		t.Fatal("unavailable preflight hidden")
	}
	if _, err := m.GetInstallOperationForSuggestion("rule-kitchen"); !errors.Is(err, ErrInstallOperationNotFound) {
		t.Fatalf("unattempted failure locked suggestion: %v", err)
	}
	s, _ := m.findSuggestion("rule-kitchen")
	if s.Status != "pending" || f.writes.Load() != 0 {
		t.Fatal("preflight mutated suggestion or HA")
	}
	f.registryUnavailable = false
	s, err := m.ConfirmSuggestion(context.Background(), s.ID)
	if err != nil || s.Status != "confirmed" || f.writes.Load() != 2 {
		t.Fatalf("explicit recovery %+v %v writes=%d", s, err, f.writes.Load())
	}
}
func TestInstallMigrationBackupFailurePreservesOriginal(t *testing.T) {
	_, m := newInstallFixture(t)
	s, _ := m.findSuggestion("rule-kitchen")
	s.Status = "applying"
	m.store.mu.Lock()
	err := m.store.writeSuggestions([]RuleSuggestion{*s})
	m.store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.store.basePath, "rules", "suggestions.json")
	before, _ := os.ReadFile(path)
	os.Mkdir(filepath.Join(m.store.basePath, "rules", "suggestions.pre-install-v1.json"), 0700)
	if _, err = NewManager(m.cfg, zap.NewNop()); err == nil {
		t.Fatal("missing backup accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("migration changed original despite backup failure")
	}
}
func TestInstallArchiveDoesNotClaimEnabled(t *testing.T) {
	_, m := newInstallFixture(t)
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	raw, err := os.ReadFile(filepath.Join(m.store.basePath, "rules", "rule-kitchen.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "status: active") {
		t.Fatal("installed off automation archived as active")
	}
}

func TestInstallStateRejectsMissingManagedProvenance(t *testing.T) {
	_, m := newInstallFixture(t)
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	path := filepath.Join(m.store.basePath, "rules", "install-state.json")
	raw, _ := os.ReadFile(path)
	var state installState
	json.Unmarshal(raw, &state)
	state.Managed = nil
	raw, _ = json.Marshal(state)
	os.WriteFile(path, raw, 0600)
	if _, err := NewManager(m.cfg, zap.NewNop()); err == nil {
		t.Fatal("confirmed state without managed provenance accepted")
	}
}
func TestInstallExpectedDigestImmutable(t *testing.T) {
	_, m := newInstallFixture(t)
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	op, _ := m.GetInstallOperationForSuggestion("rule-kitchen")
	expected := op.ExpectedDigest
	op.ExpectedDigest = strings.Repeat("0", 64)
	if err := m.store.saveInstallOperation(op); err == nil {
		t.Fatal("expected digest changed")
	}
	got, err := m.GetInstallOperation(op.ID)
	if err != nil || got.ExpectedDigest != expected {
		t.Fatalf("expected digest lost %+v %v", got, err)
	}
}

func TestInstallPendingProjectionCannotBeIgnored(t *testing.T) {
	f, m := newInstallFixture(t)
	f.loseCreate = true
	m.ConfirmSuggestion(context.Background(), "rule-kitchen")
	s, _ := m.findSuggestion("rule-kitchen")
	s.Status = "pending"
	m.store.mu.Lock()
	err := m.store.writeSuggestions([]RuleSuggestion{*s})
	m.store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.IgnoreSuggestion(s.ID); !errors.Is(err, ErrSuggestionConflict) {
		t.Fatalf("ambiguous attempt ignored after projection failure: %v", err)
	}
}
