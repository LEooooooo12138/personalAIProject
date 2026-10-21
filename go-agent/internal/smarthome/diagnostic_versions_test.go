package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type diagnosticRoundTrip func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAutomationDiagnosticsActionLockCoversEvidence(t *testing.T) {
	_, m := newDiagnosisFixture(t)
	reads := 0
	m.client.httpClient.Transport = diagnosticRoundTrip(func(r *http.Request) (*http.Response, error) {
		reads++
		if m.actionMu.TryLock() {
			m.actionMu.Unlock()
			t.Error("evidence read ran outside management serialization")
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	snapshot, err := m.DiagnoseAutomation(context.Background(), AutomationRef{Kind: "external", ID: "automation.a"}, evidenceWindow)
	if err != nil && !errors.Is(err, ErrDiagnosticSourcesPartial) {
		t.Fatal(err)
	}
	if reads == 0 {
		t.Fatal("no real evidence read exercised")
	}
	persisted, err := m.GetLatestAutomationDiagnostic(snapshot.Ref)
	if err != nil || persisted.ID != snapshot.ID {
		t.Fatal("snapshot not persisted before return")
	}
	if !m.actionMu.TryLock() {
		t.Fatal("diagnostic failed to release action lock")
	}
	m.actionMu.Unlock()
}

type diagnosticEnteredContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *diagnosticEnteredContext) Err() error {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Err()
}

func TestAutomationDiagnosticsCancelledActionWait(t *testing.T) {
	for _, mode := range []string{"caller_cancel", "caller_deadline", "manager_stop"} {
		t.Run(mode, func(t *testing.T) {
			f, m := newDiagnosisFixture(t)
			m.serviceCtx, m.cancel = context.WithCancel(context.Background())
			m.logger = zap.NewNop()
			m.catalog = NewCatalogService(context.Background(), m.client)
			m.collector = NewCollector(m.client, m.store, 3600, m.logger)
			t.Cleanup(m.Stop)
			m.actionMu.Lock()
			defer m.actionMu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "caller_deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			entered := &diagnosticEnteredContext{Context: ctx, entered: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				_, err := m.DiagnoseAutomation(entered, AutomationRef{Kind: "external", ID: "automation.a"}, evidenceWindow)
				result <- err
			}()
			<-entered.entered
			stopped := make(chan struct{})
			if mode == "manager_stop" {
				go func() { m.Stop(); close(stopped) }()
			} else if mode == "caller_cancel" {
				cancel()
			}
			select {
			case err := <-result:
				want := error(context.Canceled)
				if mode == "caller_deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("wait cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("diagnostic waited for locked action after cancellation")
			}
			if mode == "manager_stop" {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("shutdown waited for action holder")
				}
			}
			if f.profileReads.Load()+f.registryReads.Load()+f.configReads.Load()+f.stateReads.Load()+f.writes.Load() != 0 {
				t.Fatal("cancelled waiter reached HA")
			}
		})
	}
}

func TestAutomationDiagnosticsPerCheckGenerationLedger(t *testing.T) {
	own, peer := strings.Repeat("1", 32), strings.Repeat("2", 32)
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v1 := DiagnosticManagedVersion{OperationID: strings.Repeat("a", 32), SuggestionVersion: "v1"}
	v2 := DiagnosticManagedVersion{OperationID: strings.Repeat("b", 32), SuggestionVersion: "v2"}
	check := func(key, id, status string) DiagnosticCheck {
		reason := "entity_unavailable"
		if status == "pass" {
			reason = "entity_available"
		}
		return DiagnosticCheck{Key: key, Status: status, Reason: reason, Severity: "warning", EvidenceRefs: []DiagnosticEvidenceRef{{Source: "entity", RuleRef: AutomationRef{Kind: "managed", ID: id}, EntityIDs: []string{"light.private"}, ObservedAt: evidenceWindow.End}}}
	}
	ref := AutomationRef{Kind: "external", ID: "automation.a"}
	save := func(checks []DiagnosticCheck, versions map[string]DiagnosticManagedVersion) DiagnosticSnapshot {
		t.Helper()
		s, e := store.saveDiagnosticSnapshot(DiagnosticSnapshot{SchemaVersion: 1, Ref: ref, Window: evidenceWindow, Checks: checks, ManagedVersions: versions})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	versions := map[string]DiagnosticManagedVersion{own: v1, peer: v1}
	save([]DiagnosticCheck{check("private", own, "finding"), check(peer, peer, "finding")}, versions)
	versions[peer] = v2
	if s := save([]DiagnosticCheck{check("private", own, "unknown"), check(peer, peer, "unknown")}, versions); len(s.Changes) != 0 {
		t.Fatal("unknown emitted change")
	}
	record, e := store.readDiagnosticRecord(ref)
	if e != nil || len(record.Active) != 2 || record.ActiveVersions[peer][peer] != v1 {
		t.Fatalf("unknown/peer change erased findings: %+v %v", record, e)
	}
	// Omitted peer capture does not erase that peer or rediscover an unrelated finding.
	if s := save([]DiagnosticCheck{check("private", own, "finding")}, map[string]DiagnosticManagedVersion{own: v1}); len(s.Changes) != 0 {
		t.Fatal("unrelated finding reset on peer omission")
	}
	if s := save([]DiagnosticCheck{check(peer, peer, "pass")}, nil); len(s.Changes) != 0 {
		t.Fatal("missing binding proves no recovery")
	}
	record, _ = store.readDiagnosticRecord(ref)
	if len(record.Active) != 2 {
		t.Fatal("unbound pass erased active finding")
	}
	if s := save([]DiagnosticCheck{check(peer, peer, "pass")}, versions); len(s.Changes) != 0 {
		t.Fatal("changed generation claimed old recovery")
	}
	record, _ = store.readDiagnosticRecord(ref)
	if len(record.Active) != 1 || record.Active["private"].Reason != "entity_unavailable" {
		t.Fatal("changed peer removed unrelated private finding")
	}
	if s := save([]DiagnosticCheck{check("private", own, "pass")}, versions); len(s.Changes) != 1 || s.Changes[0].Kind != "recovered" {
		t.Fatal("same generation scoped recovery lost")
	}
	// Existing legacy findings with no captured stamp remain conservative.
	save([]DiagnosticCheck{check("legacy", own, "finding")}, nil)
	if s := save([]DiagnosticCheck{check("legacy", own, "pass")}, versions); len(s.Changes) != 0 {
		t.Fatal("legacy finding falsely recovered")
	}
	record, _ = store.readDiagnosticRecord(ref)
	if _, ok := record.Active["legacy"]; !ok {
		t.Fatal("legacy unknown stamp erased")
	}
}

func TestAutomationDiagnosticsVersionMetadataBounds(t *testing.T) {
	store, err := NewDeviceStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]DiagnosticManagedVersion{}
	for i := 0; i < 9; i++ {
		versions[fmt.Sprintf("%032x", i+1)] = DiagnosticManagedVersion{OperationID: strings.Repeat("a", 32), SuggestionVersion: "v1"}
	}
	ref := AutomationRef{Kind: "external", ID: "automation.a"}
	s := DiagnosticSnapshot{SchemaVersion: 1, Ref: ref, Window: evidenceWindow, ManagedVersions: versions}
	if _, e := store.saveDiagnosticSnapshot(s); e != nil {
		t.Fatal(e)
	}
	versions["overflow"] = versions[fmt.Sprintf("%032x", 1)]
	if _, e := store.saveDiagnosticSnapshot(s); !errors.Is(e, ErrDiagnosticStore) {
		t.Fatal("accepted >9 bindings")
	}
	delete(versions, "overflow")
	versions[fmt.Sprintf("%032x", 1)] = DiagnosticManagedVersion{OperationID: "invalid", SuggestionVersion: "v1"}
	if _, e := store.saveDiagnosticSnapshot(s); !errors.Is(e, ErrDiagnosticStore) {
		t.Fatal("accepted invalid operation")
	}
	record, e := store.readDiagnosticRecord(ref)
	if e != nil {
		t.Fatal(e)
	}
	record.ActiveVersions = map[string]map[string]DiagnosticManagedVersion{"orphan": {}}
	b, _ := json.Marshal(record)
	if e = os.WriteFile(store.diagnosticPath(ref), b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = store.readDiagnosticRecord(ref); !errors.Is(e, ErrDiagnosticStore) {
		t.Fatal("accepted orphan stamp")
	}
}
