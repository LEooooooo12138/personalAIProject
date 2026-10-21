package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

// Fixed historical bytes pass through the real startup recovery and Console
// boundary. No current RuleSuggestion builder/serializer manufactures the input.
func TestConsoleAutomationLegacyJSONMigrationAndRestart(t *testing.T) {
	fixturePath := filepath.Join("testdata", "automation-migration", "legacy-suggestions.json")
	original, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	s, _, archive := suggestionsFixtureWithArchive(t)
	s.smartHome.Stop()
	path := filepath.Join(archive, "rules", "suggestions.json")
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	var requests, writes atomic.Int32
	// Every HA transport attempt is observable, including GET/WS upgrades.
	// Migration, legacy confirmation and unowned reconciliation need none.
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writes.Add(1)
		}
		http.Error(w, "unexpected synthetic HA request", http.StatusServiceUnavailable)
	}))
	defer ha.Close()
	open := func() *smarthome.Manager {
		t.Helper()
		m, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "fixture-only", AgentVaultPath: archive}, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Stop)
		return m
	}
	s.smartHome = open()
	if requests.Load() != 0 {
		t.Fatal("startup contacted HA")
	}
	backupPath := filepath.Join(archive, "rules", "suggestions.pre-install-v1.json")
	backup, err := os.ReadFile(backupPath)
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatalf("startup lost exact legacy backup: %v", err)
	}
	journalPath := filepath.Join(archive, "rules", "install-state.json")
	journal, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SchemaVersion int                           `json:"schema_version"`
		Operations    []smarthome.InstallOperation  `json:"operations"`
		Managed       []smarthome.ManagedAutomation `json:"managed"`
	}
	if err = json.Unmarshal(journal, &envelope); err != nil || envelope.SchemaVersion != 2 || len(envelope.Operations) != 3 || len(envelope.Managed) != 0 {
		t.Fatalf("migration manufactured ownership: %s err=%v", journal, err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "MigrationMember")
	list := func(cookie *http.Cookie) map[string]any {
		t.Helper()
		response := consoleRequest(s, "GET", consolePrefix+"/suggestions", "", cookie, "", "", "")
		if strings.Contains(response.Body.String(), "LEGACY_RAW_SECRET") {
			t.Fatal("raw historical text leaked")
		}
		return consoleJSON(t, response, 200)
	}
	before := list(admin)
	items := before["suggestions"].([]any)
	if before["count"] != float64(4) || len(items) != 4 {
		t.Fatalf("legacy records dropped: %+v", before)
	}
	byID := map[string]map[string]any{}
	for _, raw := range items {
		item := raw.(map[string]any)
		id := item["id"].(string)
		byID[id] = item
		evidence := item["evidence"].(map[string]any)
		for _, field := range []string{"sample_size", "period_days"} {
			value, exists := evidence[field]
			if !exists || value != nil {
				t.Fatalf("legacy %s fabricated %s: %+v", id, field, evidence)
			}
		}
		for _, field := range []string{"hit_days", "eligible_days", "denominator", "algorithm_version", "event_refs"} {
			if _, exists := evidence[field]; exists {
				t.Fatalf("legacy %s fabricated %s: %+v", id, field, evidence)
			}
		}
	}
	pending := byID["legacy_no_evidence"]
	if pending["status"] != "pending" || pending["installation"] != nil || pending["can_confirm"] != false {
		t.Fatalf("unbound legacy pending promoted: %+v", pending)
	}
	if _, err = s.smartHome.GetInstallOperationForSuggestion("legacy_no_evidence"); err != smarthome.ErrInstallOperationNotFound {
		t.Fatalf("pending gained an install operation: %v", err)
	}
	for _, tc := range []struct{ id, status, code string }{
		{"legacy_confirmed", "confirmed", "install_legacy_confirmed_unverified"},
		{"legacy_applying", "unknown", "install_legacy_outcome_unknown"},
		{"legacy_failed", "unknown", "install_legacy_outcome_unknown"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			view := byID[tc.id]
			if view["status"] != tc.status || view["installation_state"] != "legacy_unverified" {
				t.Fatalf("legacy outcome promoted %+v", view)
			}
			for _, capability := range []string{"can_confirm", "can_bind", "can_revise", "can_ignore"} {
				if view[capability] != false {
					t.Fatalf("legacy mutation advertised %s: %+v", capability, view)
				}
			}
			op, err := s.smartHome.GetInstallOperationForSuggestion(tc.id)
			if err != nil || op.Status != "unknown" || op.Phase != "legacy" || op.ErrorCode != tc.code || op.OwnAttempt || op.ExpectedDigest != "" || op.ManagedAutomationID != "" || op.Create.AttemptedAt != nil {
				t.Fatalf("legacy operation invented provenance %+v %v", op, err)
			}
			projected := view["installation"].(map[string]any)
			if projected["id"] != op.ID || projected["ownership"] != "unverified" || projected["legacy_unverified"] != true || projected["can_reconcile"] != false || projected["mapping_status"] != "unknown" || projected["load_status"] != "unknown" || projected["archive_status"] != "not_ready" {
				t.Fatalf("legacy API invented proof %+v", projected)
			}
			endpoint := consolePrefix + "/admin/install-operations/" + op.ID
			consoleJSON(t, consoleRequest(s, "GET", endpoint, "", member, "", "", ""), 403)
			consoleJSON(t, consoleRequest(s, "POST", endpoint+"/reconcile", `{}`, admin, "", testConsoleOrigin, ""), 403)
			for i := 0; i < 2; i++ {
				got := consoleJSON(t, consoleRequest(s, "POST", endpoint+"/reconcile", `{}`, admin, csrf, testConsoleOrigin, ""), 200)["operation"].(map[string]any)
				if got["status"] != "unknown" || got["ownership"] != "unverified" || got["can_reconcile"] != false {
					t.Fatalf("unowned reconcile adopted rule %+v", got)
				}
			}
			if tc.id != "legacy_confirmed" {
				for i := 0; i < 2; i++ {
					got := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/"+tc.id+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)["suggestion"].(map[string]any)
					if got["status"] != "unknown" || got["can_confirm"] != false {
						t.Fatalf("interrupted legacy confirmation became replayable %+v", got)
					}
				}
			}
		})
	}
	visible := list(member)
	if visible["count"] != float64(0) || len(visible["suggestions"].([]any)) != 0 {
		t.Fatalf("unshared historical records leaked %+v", visible)
	}
	automations := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)
	for _, raw := range automations["automations"].([]any) {
		row := raw.(map[string]any)
		if row["ownership"] != "unverified" || row["installation_state"] != "unknown" || row["can_diagnose"] != false || row["ref"] != nil {
			t.Fatalf("legacy management ownership promoted %+v", row)
		}
	}
	if requests.Load() != 0 || writes.Load() != 0 {
		t.Fatalf("migration/API replay contacted HA requests=%d writes=%d", requests.Load(), writes.Load())
	}
	files := map[string][]byte{}
	for _, p := range []string{path, backupPath, journalPath} {
		files[p], err = os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
	}
	s.smartHome.Stop()
	s.smartHome = open()
	if after := list(admin); !reflect.DeepEqual(before, after) {
		t.Fatalf("second restart changed safe projection before=%+v after=%+v", before, after)
	}
	for p, want := range files {
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("second restart changed migrated file %s: %v", filepath.Base(p), err)
		}
	}
	if requests.Load() != 0 || writes.Load() != 0 {
		t.Fatalf("second restart contacted HA requests=%d writes=%d", requests.Load(), writes.Load())
	}
	unchanged, err := os.ReadFile(fixturePath)
	if err != nil || !bytes.Equal(unchanged, original) {
		t.Fatalf("source legacy copy changed: %v", err)
	}
}
