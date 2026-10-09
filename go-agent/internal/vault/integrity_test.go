package vault

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// These tests exercise the filesystem boundary and cache consumer-visible results.
func TestVaultRejectsOutsidePaths(t *testing.T) {
	root := t.TempDir()
	personal := filepath.Join(root, "personal")
	os.MkdirAll(personal, 0755)
	outside := filepath.Join(root, "outside.md")
	os.WriteFile(outside, []byte("original"), 0644)
	reader := NewFileReader(personal, personal)
	writer := NewFileWriter(personal, personal)
	for _, path := range []string{"../outside.md", `..\outside.md`, outside} {
		if _, err := reader.ReadPage(context.Background(), "personal", path); err == nil {
			t.Errorf("read outside path accepted: %q", path)
		}
		if err := writer.WritePage(context.Background(), "personal", path, []byte("changed")); err == nil {
			t.Errorf("write outside path accepted: %q", path)
		}
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "original" {
		t.Errorf("outside file changed: %q", data)
	}
}

func TestRRFUsesHighestRankedChunkPerPage(t *testing.T) {
	hits := NewRetrievalService(nil, nil, 60).RRFMerge(nil, []EmbeddingResult{{PagePath: "a.md", Title: "A", ChunkContent: "best", Score: 0.9}, {PagePath: "a.md", Title: "A", ChunkContent: "worse", Score: 0.4}, {PagePath: "b.md", Title: "B", ChunkContent: "other", Score: 0.35}})
	if len(hits) != 2 || hits[0].Body != "best" {
		t.Fatalf("less relevant chunk replaced best: %+v", hits)
	}
	if hits[0].Score > 0.017 {
		t.Errorf("one page received duplicate rank votes: %f", hits[0].Score)
	}
}

func TestChunkIDsRemainUniqueBeyondTenSections(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 12; i++ {
		body.WriteString("\n## heading\n" + strings.Repeat("text ", 100))
	}
	chunks := ChunkPage(&Page{Title: "page", Body: body.String()})
	seen := map[string]bool{}
	for _, chunk := range chunks {
		if seen[chunk.ID] {
			t.Fatalf("chunk ID reused: %s", chunk.ID)
		}
		seen[chunk.ID] = true
	}
}

func TestVaultRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writer := NewFileWriter(root, root)
	if err := writer.WritePage(context.Background(), "personal", "escape/new.md", []byte("changed")); err == nil {
		t.Fatal("symlink escaped vault root")
	}
}

func writeKnowledge(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte("---\ntitle: "+name+"\ntags: [user-facing]\n---\n"+body), 0644); err != nil {
		t.Fatal(err)
	}
}

type fixedEmbedder struct{}

func (fixedEmbedder) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"data":[{"embedding":[1,0]}]}`), nil
}

func TestEmbeddingCacheReloadKeepsEverySource(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "a.md", "alpha")
	writeKnowledge(t, root, "b.md", "beta")
	vr := NewFileReader(root, root)
	initial := NewEmbeddingStore(fixedEmbedder{}, vr, root, zap.NewNop())
	if _, err := initial.Search(context.Background(), "query", 10); err != nil {
		t.Fatal(err)
	}
	reloaded := NewEmbeddingStore(fixedEmbedder{}, vr, root, zap.NewNop())
	hits, err := reloaded.Search(context.Background(), "query", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, hit := range hits {
		found[hit.PagePath] = true
	}
	if !found["a.md"] || !found["b.md"] || found[""] {
		t.Fatalf("cache lost source paths: %+v", hits)
	}
}

func TestEmbeddingRefreshesAddedChangedDeletedPages(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "a.md", "old alpha")
	writeKnowledge(t, root, "b.md", "beta")
	store := NewEmbeddingStore(fixedEmbedder{}, NewFileReader(root, root), root, zap.NewNop())
	if _, err := store.Search(context.Background(), "query", 10); err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, root, "a.md", "new alpha")
	writeKnowledge(t, root, "c.md", "gamma")
	os.Remove(filepath.Join(root, "b.md"))
	hits, err := store.Search(context.Background(), "query", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, hit := range hits {
		found[hit.PagePath] = hit.ChunkContent
	}
	if len(found) != 2 || found["a.md"] != "new alpha" || found["c.md"] != "gamma" {
		t.Fatalf("stale dense index: %+v", found)
	}
}

func TestBM25FindsAddedPageAfterCacheBuild(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "a.md", "alpha")
	reader := NewFileReader(root, root)
	if _, err := reader.Search(context.Background(), "personal", "alpha"); err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, root, "b.md", "newunique")
	hits, err := reader.Search(context.Background(), "personal", "newunique")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "b.md" {
		t.Fatalf("new page absent from index: %+v", hits)
	}
}

func TestBM25CorruptCacheRebuildsInsteadOfPanicking(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "a.md", "needle")
	reader := NewFileReader(root, root)
	reader.Search(context.Background(), "personal", "needle")
	path := filepath.Join(root, ".rag-cache", "bm25.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cache invertedIndexCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	cache.Postings["needle"] = []postingEntryJSON{{DocID: 999, Freq: 1}}
	data, _ = json.Marshal(cache)
	os.WriteFile(path, data, 0600)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("corrupt cache crashed search: %v", r)
		}
	}()
	hits, err := reader.Search(context.Background(), "personal", "needle")
	if err != nil || len(hits) != 1 {
		t.Fatalf("cache recovery failed: %+v %v", hits, err)
	}
}

func TestScopedEmbeddingReadsOnlySelectedVault(t *testing.T) {
	personal := t.TempDir()
	agent := t.TempDir()
	writeKnowledge(t, personal, "same.md", "private-only")
	writeKnowledge(t, agent, "same.md", "public-only")
	store := NewScopedEmbeddingStore(fixedEmbedder{}, NewFileReader(personal, agent), agent, "agent", "test-model", zap.NewNop())
	hits, err := store.Search(context.Background(), "query", 10)
	if err != nil || len(hits) != 1 || hits[0].ChunkContent != "public-only" {
		t.Fatalf("wrong vault indexed: %+v %v", hits, err)
	}
}

func TestBM25RefreshesChangedAndDeletedPages(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "a.md", "oldunique")
	writeKnowledge(t, root, "b.md", "deletedunique")
	reader := NewFileReader(root, root)
	if _, err := reader.Search(context.Background(), "personal", "oldunique"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, root, "a.md", "newunique")
	os.Chtimes(filepath.Join(root, "a.md"), info.ModTime(), info.ModTime())
	os.Remove(filepath.Join(root, "b.md"))
	hits, err := reader.Search(context.Background(), "personal", "newunique")
	if err != nil || len(hits) != 1 {
		t.Fatalf("updated page missed: %+v %v", hits, err)
	}
	hits, err = reader.Search(context.Background(), "personal", "deletedunique")
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted page retained: %+v %v", hits, err)
	}
}

type cancellingEmbedder struct{ cancel context.CancelFunc }

func (c cancellingEmbedder) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	c.cancel()
	return json.RawMessage(`{"data":[{"embedding":[1,0]}]}`), nil
}
func TestCancelledEmbeddingBuildDoesNotPublishCache(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "a.md", "alpha")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewEmbeddingStore(cancellingEmbedder{cancel}, NewFileReader(root, root), root, zap.NewNop())
	if _, err := store.Search(ctx, "query", 10); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled build succeeded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rag-cache", "embeddings.json")); !os.IsNotExist(err) {
		t.Errorf("cancelled build wrote cache: %v", err)
	}
}

func TestEntityExtractionDoesNotFollowOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeKnowledge(t, outside, "out.md", "content")
	if err := os.Symlink(filepath.Join(outside, "out.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	entities, err := ExtractTriggerEntities(root, nil)
	if err == nil {
		t.Fatal("outside symlink failure was swallowed")
	}
	if len(entities) != 0 {
		t.Fatalf("read external source through symlink: %+v", entities)
	}
}
