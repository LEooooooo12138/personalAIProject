package main

import (
	"context"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"strings"
	"testing"
)

func TestFixtureKnowledgeAndSuggestionUseRealLocalServices(t *testing.T) {
	f, e := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	ctx := context.Background()
	public, e := f.app.VaultR.Search(ctx, "agent", "紫藤")
	if e != nil || len(public) != 1 || public[0].Title != "家庭公开指南" {
		t.Fatalf("public %+v %v", public, e)
	}
	state := chain.NewChainState("紫藤", "personal", map[string]string{"channel": "console"})
	result, e := f.app.ChainExecutor.Run(ctx, "rag-answer", state)
	if e != nil || result.Error != nil || !strings.Contains(result.FinalAnswer, "P42") || len(state.Sources) == 0 {
		t.Fatalf("RAG %+v %v", result, e)
	}
	ingest := chain.NewChainState("wiki-ingest", "personal", map[string]string{"channel": "console"})
	ingest.Data["raw_content"] = "紫藤导入内容"
	result, e = f.app.ChainExecutor.Run(ctx, "wiki-ingest", ingest)
	if e != nil || result.Error != nil {
		t.Fatalf("ingest %+v %v", result, e)
	}
	path, _ := ingest.GetString("written_path")
	if _, e = f.app.VaultR.ReadPage(ctx, "personal", path); e != nil {
		t.Fatal(e)
	}
	if _, e = f.app.VaultR.ReadPage(ctx, "agent", path); e == nil {
		t.Fatal("private import published")
	}
	next, e := f.app.SmartHome.BindSuggestion(ctx, "fixture_time", smarthome.SuggestionBindings{Presence: &smarthome.PresenceBinding{EntityID: "group.family_home", State: "on"}})
	if e != nil {
		t.Fatal(e)
	}
	if next.ID == "fixture_time" || f.dependencies.automationWrites.Load() != 0 {
		t.Fatal("binding executed automation or mutated version")
	}
	confirmed, e := f.app.SmartHome.ConfirmSuggestion(ctx, next.ID)
	if e != nil || confirmed.Status != "confirmed" {
		t.Fatalf("confirm %+v %v", confirmed, e)
	}
	_, e = f.app.SmartHome.ConfirmSuggestion(ctx, next.ID)
	if e != nil || f.dependencies.automationWrites.Load() != 1 {
		t.Fatal("duplicate mock HA installation")
	}
	if e := f.app.SmartHome.GetClient().CallService(ctx, "light", "turn_on", nil); e == nil {
		t.Fatal("device service unexpectedly allowed")
	}
}
