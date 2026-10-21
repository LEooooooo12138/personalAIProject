package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

func TestConsoleAutomationEvidenceProjection(t *testing.T) {
	s, writes, dir := suggestionsFixtureWithArchive(t)
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("shared", "pending"), persistedTimeRule("legacy", "confirmed"), persistedTimeRule("archived", "ignored")})
	if _, err := s.smartHome.ConfirmSuggestion(context.Background(), "shared"); err != nil {
		t.Fatal(err)
	}
	op, err := s.smartHome.GetInstallOperationForSuggestion("shared")
	if err != nil || op.ManagedAutomationID == "" {
		t.Fatalf("fixture did not install: %+v %v", op, err)
	}
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	sharing, _ := s.consoleStore.GetSharing()
	if _, err := s.consoleStore.PutSharing(sharing.Revision, []string{"light.room", "binary_sensor.house"}, []string{"shared", "legacy"}); err != nil {
		t.Fatal(err)
	}
	ref := smarthome.AutomationRef{Kind: "managed", ID: op.ManagedAutomationID}
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	visible := smarthome.DiagnosticCheck{Key: "execution", Status: "unknown", Category: "execution", Severity: "info", Reason: "run_evidence_missing", EvidenceRefs: []smarthome.DiagnosticEvidenceRef{{Source: "trace", RuleRef: ref, EntityIDs: []string{"automation.test_rule"}, ObservedAt: at}}}
	private := smarthome.DiagnosticCheck{Key: "PRIVATE_ROOM_KEY", Status: "finding", Category: "reference", Severity: "warning", Reason: "entity_unavailable", EvidenceRefs: []smarthome.DiagnosticEvidenceRef{{Source: "entity", RuleRef: ref, EntityIDs: []string{"light.private_room"}, Ref: "PRIVATE_TRACE", ObservedAt: at}}}
	peer := smarthome.DiagnosticCheck{Key: "hidden-rule", Status: "finding", Category: "conflict", Severity: "warning", Reason: "opposite_actions_same_scope", EvidenceRefs: []smarthome.DiagnosticEvidenceRef{{Source: "config", RuleRef: ref, EntityIDs: []string{"light.room"}, ObservedAt: at}, {Source: "peer_config", RuleRef: smarthome.AutomationRef{Kind: "managed", ID: "hidden-rule"}, EntityIDs: []string{"light.room"}, ObservedAt: at}}}
	snapshot := smarthome.DiagnosticSnapshot{SchemaVersion: 1, ID: strings.Repeat("d", 32), Ref: ref, Ownership: "managed", CheckedAt: at, Window: smarthome.TimeWindow{Start: at.Add(-24 * time.Hour), End: at}, Summary: "attention", Sources: map[string]smarthome.DiagnosticSource{"peers": {SourceStatus: smarthome.SourceAvailable, ReadCount: 99, EventRefs: []string{"PRIVATE_ROOM_EVENT"}}, "trace": {SourceStatus: smarthome.SourceUnavailable, ErrorCode: "PRIVATE_ERROR", EventRefs: []string{"PRIVATE_TRACE"}}}, Checks: []smarthome.DiagnosticCheck{visible, private, peer}, Changes: []smarthome.DiagnosticChange{{Kind: "new", Check: private}, {Kind: "new", Check: peer}}}
	hash := sha256.Sum256([]byte(ref.Kind + ":" + ref.ID))
	current, _, err := s.smartHome.GetManagedAutomationVersion(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ManagedVersions = map[string]smarthome.DiagnosticManagedVersion{ref.ID: {OperationID: current.OperationID, SuggestionVersion: current.SuggestionVersion}}
	path := filepath.Join(dir, "diagnostics", hex.EncodeToString(hash[:])+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "snapshot": snapshot, "active_findings": map[string]any{}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := writes.Load()
	endpoint := consolePrefix + "/automations/" + ref.ID + "/diagnostics/latest"
	response := consoleRequest(s, "GET", endpoint, "", member, "", "", "")
	result := consoleJSON(t, response, 200)["diagnostic"].(map[string]any)
	for _, hidden := range []string{"private_room", "hidden-rule", "PRIVATE", "read_count", "event_refs"} {
		if strings.Contains(response.Body.String(), hidden) {
			t.Fatalf("member evidence leaked %q: %s", hidden, response.Body.String())
		}
	}
	if result["summary"] != "unknown" || len(result["checks"].([]any)) != 1 || len(result["changes"].([]any)) != 0 {
		t.Fatalf("unfiltered conclusions: %+v", result)
	}
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", member, "", "", ""), 200)
	if len(list["automations"].([]any)) != 1 || len(list["external_candidates"].([]any)) != 0 {
		t.Fatalf("member rule scope %+v", list)
	}
	adminList := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations", "", admin, "", "", ""), 200)
	if len(adminList["automations"].([]any)) != 2 {
		t.Fatalf("missing historical pending-check row: %+v", adminList)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/automations/hidden-rule/diagnostics/latest", "", member, "", "", ""), 404)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automations/"+ref.ID+"/diagnostics", `{}`, admin, csrf, testConsoleOrigin, ""), 422)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automations/"+ref.ID+"/diagnostics", `{"path":"/private"}`, admin, csrf, testConsoleOrigin, ""), 422)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automations/"+ref.ID+"/diagnostics", `{}`, admin, "", testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automations/"+ref.ID+"/diagnostics", `{}`, member, "", testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/automation-diagnostics", `{"ref":{"kind":"external","id":"automation.guessed"},"window":{"start":"2026-10-01T00:00:00Z","end":"2026-10-02T00:00:00Z"}}`, admin, csrf, testConsoleOrigin, ""), 404)
	if writes.Load() != before {
		t.Fatal("list/diagnostics authorization caused HA writes")
	}
	// Revoke the grant after policy capture but before response publication.
	block := &automationPublicationCatalog{source: s.areaCatalog, entered: make(chan struct{}), release: make(chan struct{})}
	s.areaCatalog = block
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- consoleRequest(s, "GET", consolePrefix+"/automations", "", member, "", "", "") }()
	select {
	case <-block.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog did not enter")
	}
	latestSharing, _ := s.consoleStore.GetSharing()
	if _, err := s.consoleStore.PutSharing(latestSharing.Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	close(block.release)
	denied := <-done
	consoleJSON(t, denied, 409)
	if strings.Contains(denied.Body.String(), op.ManagedAutomationID) {
		t.Fatal("old projection published after grant withdrawal")
	}
}

type automationPublicationCatalog struct {
	source  consoleCatalogProvider
	entered chan struct{}
	release chan struct{}
}

func (p *automationPublicationCatalog) Catalog(ctx context.Context) (smarthome.CatalogSnapshot, error) {
	close(p.entered)
	select {
	case <-p.release:
		return p.source.Catalog(ctx)
	case <-ctx.Done():
		return smarthome.CatalogSnapshot{}, ctx.Err()
	}
}
