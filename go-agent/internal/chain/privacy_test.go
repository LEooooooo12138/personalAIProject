package chain

import (
	"context"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestEntityRoutingDoesNotLogUserText(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.md"), []byte("---\ntitle: project\ncategory: project\n---\npublic"), 0600); err != nil {
		t.Fatal(err)
	}
	step := NewEntityTriggerDecideStep(vault.NewFileReader(root, root), zap.New(core))
	state := NewChainState("project password=private-fixture", "agent", nil)
	if err := step.Run(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if decision, _ := state.GetString("llm_decision"); decision != "search" {
		t.Fatal("entity route was not exercised")
	}
	if strings.Contains(fmt.Sprint(logs.All()), "private-fixture") {
		t.Fatal("entity routing logged user message text")
	}
}
