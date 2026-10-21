package gateway

import (
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"strings"
	"testing"
)

func TestConsoleAutomationPeerScopesFilterWholeChecksAndChanges(t *testing.T) {
	p := consoleAutomationScope{allowed: map[string]map[string]bool{"selected": {"light.public": true}, "peer": {"light.public": true}}}
	v := smarthome.DiagnosticManagedVersion{OperationID: strings.Repeat("a", 32), SuggestionVersion: "v1"}
	p.versions = map[string]smarthome.DiagnosticManagedVersion{"selected": v, "peer": v}
	base := smarthome.DiagnosticCheck{Status: "finding", Reason: "opposite_actions_same_scope", Category: "conflict", Severity: "warning", EvidenceRefs: []smarthome.DiagnosticEvidenceRef{
		{RuleRef: smarthome.AutomationRef{Kind: "managed", ID: "selected"}, EntityIDs: []string{"light.public"}},
		{RuleRef: smarthome.AutomationRef{Kind: "managed", ID: "peer"}, EntityIDs: []string{"light.public"}},
	}}
	for _, mode := range []string{"authorized", "private_peer_entity", "external_peer", "unshared_peer", "empty_evidence", "changed_peer", "unbound_peer"} {
		t.Run(mode, func(t *testing.T) {
			check := base
			check.EvidenceRefs = append([]smarthome.DiagnosticEvidenceRef(nil), base.EvidenceRefs...)
			versions := map[string]smarthome.DiagnosticManagedVersion{"selected": v, "peer": v}
			switch mode {
			case "private_peer_entity":
				check.EvidenceRefs[1].EntityIDs = []string{"light.public", "light.private"}
			case "external_peer":
				check.EvidenceRefs[1].RuleRef.Kind = "external"
			case "unshared_peer":
				check.EvidenceRefs[1].RuleRef.ID = "hidden"
			case "empty_evidence":
				check.EvidenceRefs = nil
			case "changed_peer":
				versions["peer"] = smarthome.DiagnosticManagedVersion{OperationID: strings.Repeat("b", 32), SuggestionVersion: "v2"}
			case "unbound_peer":
				delete(versions, "peer")
			}
			out := p.projectDiagnostic(smarthome.DiagnosticSnapshot{ManagedVersions: versions, Summary: "attention", Checks: []smarthome.DiagnosticCheck{check}, Changes: []smarthome.DiagnosticChange{{Kind: "new", Check: check}}})
			if mode == "authorized" {
				if len(out.Checks) != 1 || len(out.Changes) != 1 || out.Counts["finding"] != 1 || out.Summary != "attention" {
					t.Fatalf("authorized pair lost: %+v", out)
				}
			} else if len(out.Checks) != 0 || len(out.Changes) != 0 || out.Counts["finding"] != 0 || out.Counts["changes"] != 0 || out.Summary != "unknown" {
				t.Fatalf("partly private pair leaked conclusion: %+v", out)
			}
		})
	}
}

func TestConsoleAutomationNativeURLUsesTrustedDashboard(t *testing.T) {
	for _, base := range []string{"javascript:alert(1)", "https://user:secret@example.test", "//example.test", "file:///private"} {
		if got := consoleAutomationURL(base); got != "" {
			t.Fatalf("unsafe native link %q", got)
		}
	}
	got := consoleAutomationURL("https://ha.example.test/prefix/?token=PRIVATE#PRIVATE")
	if got != "https://ha.example.test/prefix/config/automation/dashboard" || strings.Contains(got, "PRIVATE") {
		t.Fatalf("native route %q", got)
	}
}
