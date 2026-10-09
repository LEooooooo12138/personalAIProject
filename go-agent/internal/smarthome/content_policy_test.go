package smarthome

import (
	"github.com/yuanleyao/ai-agent/internal/vault"
	"strings"
	"testing"
)

// Omitting the internal tag exposes newly archived operational rules to RAG.
func TestRuleDocumentIsInternalAndRetainsConfiguration(t *testing.T) {
	suggestion := decodeSuggestion(t, executableSuggestion)
	document := RuleDocument(suggestion)
	page, err := vault.ParsePage([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsInternalPage(page) {
		t.Error("confirmed rule document is publicly retrievable")
	}
	if !strings.Contains(page.Body, "Confirmed HA configuration") || !strings.Contains(page.Body, `"trigger"`) {
		t.Fatal("confirmed business configuration lost")
	}
}
