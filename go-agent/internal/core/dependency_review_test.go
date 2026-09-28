package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
)

type cancelAfterSummary struct{ cancel context.CancelFunc }

func (s cancelAfterSummary) Summarize(context.Context, string) (*memory.StructuredSummary, error) {
	s.cancel()
	return &memory.StructuredSummary{Title: "database design", Decisions: "use transactions", FollowUps: "benchmark"}, nil
}

func TestCancelledSedimentationDoesNotWriteAfterSummarization(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sed := memory.NewSedimenter(root, cancelAfterSummary{cancel}, zap.NewNop())
	conv := &memory.Conversation{ChannelID: "audit", EndedAt: time.Now(), Messages: []memory.ConvMessage{{Role: "user", Content: "database design?"}, {Role: "assistant", Content: "use transactions"}, {Role: "user", Content: "remember the decision"}}}
	result := sed.Process(ctx, memory.DefaultSedimentConfig(root), conv)
	files, _ := filepath.Glob(filepath.Join(root, "_memory", "*.md"))
	if result.Error == "" || len(files) != 0 {
		t.Fatalf("cancelled pipeline persisted memory: error=%q files=%d", result.Error, len(files))
	}
}

func TestVaultSearchSnippetPreservesChineseUTF8(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("界", 20) + "needle" + strings.Repeat("界", 20)
	if err := os.WriteFile(filepath.Join(root, "page.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	hits, err := vault.NewFileReader(root, root).Search(context.Background(), "personal", "needle")
	if err != nil || len(hits) != 1 {
		t.Fatalf("search failed: %v hits=%d", err, len(hits))
	}
	if !utf8.ValidString(hits[0].Snippet) {
		t.Fatal("retrieval snippet split a Chinese UTF-8 code point")
	}
}
