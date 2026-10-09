package core

import (
	"encoding/json"
	"testing"
)

func TestControlAttachmentSurvivesMessageJSON(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":"请确认","attachments":[{"kind":"control_proposal","proposal_id":"opaque-id"}]}`), &m); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	a, ok := got["attachments"].([]any)
	if !ok || len(a) != 1 {
		t.Fatalf("lost proposal reference: %s", b)
	}
	if a[0].(map[string]any)["proposal_id"] != "opaque-id" {
		t.Fatal("wrong reference")
	}
}
func TestControlAttachmentCloneIsIndependent(t *testing.T) {
	original := &Session{Messages: []Message{{Role: "assistant", Attachments: []MessageAttachment{{Kind: "control_proposal", ProposalID: "original"}}}}}
	clone := CloneSession(original)
	clone.Messages[0].Attachments[0].ProposalID = "changed"
	if original.Messages[0].Attachments[0].ProposalID != "original" {
		t.Fatal("attachment alias leaked across session snapshots")
	}
}
