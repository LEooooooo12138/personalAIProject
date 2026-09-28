package chain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
)

type unscopedPrivateSearch struct{}

func (unscopedPrivateSearch) Search(context.Context, string, int) ([]vault.EmbeddingResult, error) {
	return []vault.EmbeddingResult{{PagePath: "private.md", Title: "private", ChunkContent: "private secret", Score: 1}}, nil
}

func TestAgentSearchRejectsUnscopedDenseAndInternalPages(t *testing.T) {
	personal := t.TempDir()
	agent := t.TempDir()
	os.WriteFile(filepath.Join(agent, "internal.md"), []byte("---\ntitle: internal\ntags: [visibility/internal]\n---\nneedle"), 0644)
	os.WriteFile(filepath.Join(agent, "public.md"), []byte("---\ntitle: public\ntags: [user-facing]\n---\nneedle"), 0644)
	state := NewChainState("needle", "agent", nil)
	step := NewVaultSearchStep(vault.NewFileReader(personal, agent), unscopedPrivateSearch{}, 5, zap.NewNop())
	if err := step.Run(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if len(state.Sources) != 1 || state.Sources[0].Title != "public" {
		t.Fatalf("agent received non-public sources: %+v", state.Sources)
	}
}

func TestVaultWriteRejectsGeneratedCategoryTraversal(t *testing.T) {
	root := t.TempDir()
	personal := filepath.Join(root, "personal")
	os.MkdirAll(personal, 0755)
	state := NewChainState("", "personal", nil)
	state.Set("wiki_output", "---\ntitle: escaped\ncategory: ../outside\n---\ncontent")
	step := NewVaultWriteStep(vault.NewFileWriter(personal, personal), zap.NewNop())
	if err := step.Run(context.Background(), state); err == nil {
		t.Fatal("generated category traversal accepted")
	}
}

func TestIndexUpdateWritesIndexAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	writer := vault.NewFileWriter(root, root)
	reader := vault.NewFileReader(root, root)
	state := NewChainState("", "personal", nil)
	state.Set("written_title", "Example")
	state.Set("written_path", "concepts/example.md")
	step := NewIndexUpdateStep(writer, reader, root, root, zap.NewNop())
	for i := 0; i < 2; i++ {
		if err := step.Run(context.Background(), state); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil {
		t.Fatalf("index not written: %v", err)
	}
	if strings.Count(string(data), "[Example](concepts/example.md)") != 1 {
		t.Fatalf("wrong index entries: %s", data)
	}
}

func TestVaultSearchReportsUnavailableKnowledgeBase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	state := NewChainState("needle", "personal", nil)
	step := NewVaultSearchStep(vault.NewFileReader(missing, missing), nil, 5, zap.NewNop())
	if err := step.Run(context.Background(), state); err == nil {
		t.Fatal("unavailable vault was reported as an empty successful search")
	}
}

func TestIndexUpdateKeepsDifferentPagesWithSameTitle(t *testing.T) {
	root := t.TempDir()
	writer := vault.NewFileWriter(root, root)
	reader := vault.NewFileReader(root, root)
	step := NewIndexUpdateStep(writer, reader, root, root, zap.NewNop())
	for _, path := range []string{"concepts/example.md", "projects/example.md"} {
		state := NewChainState("", "personal", nil)
		state.Set("written_title", "Example")
		state.Set("written_path", path)
		if err := step.Run(context.Background(), state); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := reader.ReadIndex(context.Background(), "personal")
	if err != nil || len(entries) != 2 {
		t.Fatalf("same-title page missing: %+v %v", entries, err)
	}
}
