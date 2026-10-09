package core

import (
	"context"
	"slices"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type mappingSnapshotProvider struct{ snapshot smarthome.CatalogSnapshot }

func (p mappingSnapshotProvider) Get(context.Context) (smarthome.CatalogSnapshot, error) {
	return p.snapshot, nil
}

type mappingCandidateInspector struct{ candidates chain.HACandidates }

func (p *mappingCandidateInspector) Parse(_ context.Context, _ string, candidates chain.HACandidates) (chain.HAIntent, error) {
	p.candidates = candidates
	return chain.HAIntent{Kind: "chat"}, nil
}

func TestControlChatPreservesConfirmedQueryAliasWithoutWriteCandidates(t *testing.T) {
	catalog := smarthome.NewCatalogService(context.Background(), clarificationCandidateRegistry{})
	defer catalog.Close()
	snapshot, err := catalog.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Meta.Freshness = "stale"
	target := smarthome.ControlTarget{EntityID: "switch.channel_1", Name: "书房灯", AreaName: "书房", Aliases: []string{"书房的灯", "书房进门开关1"}, AllowedActions: []string{"turn_on", "turn_off"}, LoadLocationVerified: true}
	service, err := smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{target}}, mappingSnapshotProvider{snapshot}, unusedControlHA{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser := &mappingCandidateInspector{}
	_, err = NewControlChat(service, parser).Handle(context.Background(), smarthome.ControlActor{UserID: "owner", SessionID: "chat"}, "request", "书房的灯现在开着吗")
	if err != nil {
		t.Fatal(err)
	}
	if len(parser.candidates.Control) != 0 {
		t.Fatal("stale directory exposed write candidates")
	}
	found := false
	for _, candidate := range parser.candidates.Query {
		if candidate.EntityID == target.EntityID {
			found = true
			if candidate.Name != target.Name || candidate.AreaName != target.AreaName || !slices.Contains(candidate.Aliases, "书房的灯") {
				t.Fatalf("confirmed read-only mapping lost: %+v", candidate)
			}
		}
	}
	if !found {
		t.Fatal("confirmed query target missing")
	}
}
