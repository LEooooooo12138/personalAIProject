package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
)

type fixedSummary struct{}

func (fixedSummary) Summarize(context.Context, string) (*StructuredSummary, error) {
	return &StructuredSummary{Title: "database design", Decisions: "use transactions for atomic updates", FollowUps: "measure query performance", Confidence: 0.8}, nil
}

func TestSedimentationRejectsMemoryDirectorySymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "_memory")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	sed := NewSedimenter(root, fixedSummary{}, zap.NewNop())
	result := sed.Process(context.Background(), DefaultSedimentConfig(root), memoryConversation())
	files, _ := os.ReadDir(outside)
	if result.Error == "" || len(files) != 0 {
		t.Fatalf("escaped memory root: result=%+v files=%d", result, len(files))
	}
}
func memoryConversation() *Conversation {
	return &Conversation{ChannelID: "test", EndedAt: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), Messages: []ConvMessage{{Role: "user", Content: "how to design a database?"}, {Role: "assistant", Content: "use transactions"}, {Role: "user", Content: "remember this decision"}}}
}

func TestSedimentationSkipsDuplicateWithoutWritingAnotherFile(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultSedimentConfig(root)
	sed := NewSedimenter(root, fixedSummary{}, zap.NewNop())
	conv := memoryConversation()
	first := sed.Process(context.Background(), cfg, conv)
	if first.Error != "" {
		t.Fatal(first.Error)
	}
	conv.EndedAt = conv.EndedAt.Add(time.Minute)
	second := sed.Process(context.Background(), cfg, conv)
	if second.Error != "" {
		t.Fatal(second.Error)
	}
	files, err := os.ReadDir(cfg.MemoryDir)
	if err != nil {
		t.Fatal(err)
	}
	if second.IsNew || second.FilePath != first.FilePath || len(files) != 1 {
		t.Fatalf("duplicate was rewritten: first=%+v second=%+v count=%d", first, second, len(files))
	}
}

func TestSedimentationHonorsMinMessages(t *testing.T) {
	root := t.TempDir()
	sed := NewSedimenter(root, fixedSummary{}, zap.NewNop())
	conv := memoryConversation()
	conv.Messages = conv.Messages[:1]
	result := sed.Process(context.Background(), DefaultSedimentConfig(root), conv)
	if result.Worthy || result.FilePath != "" {
		t.Fatalf("short conversation persisted: %+v", result)
	}
}

func TestWriteMemoryDoesNotOverwriteSameMinuteTitle(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultSedimentConfig(root)
	sed := NewSedimenter(root, fixedSummary{}, zap.NewNop())
	conv := memoryConversation()
	summary := &StructuredSummary{Title: "same title", Decisions: "first"}
	first, err := sed.WriteMemory(cfg, conv, summary, &dedupResult{IsNew: true})
	if err != nil {
		t.Fatal(err)
	}
	summary.Decisions = "second"
	second, err := sed.WriteMemory(cfg, conv, summary, &dedupResult{IsNew: true})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("same-minute memory overwrote original")
	}
}

func TestWriteMemoryEscapesGeneratedTitle(t *testing.T) {
	root := t.TempDir()
	sed := NewSedimenter(root, fixedSummary{}, zap.NewNop())
	path, err := sed.WriteMemory(DefaultSedimentConfig(root), memoryConversation(), &StructuredSummary{Title: `Use "RAG": notes`}, &dedupResult{IsNew: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := vault.ParsePage(data)
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != `对话摘要：Use "RAG": notes` {
		t.Fatalf("invalid generated frontmatter: %q", page.Title)
	}
}
