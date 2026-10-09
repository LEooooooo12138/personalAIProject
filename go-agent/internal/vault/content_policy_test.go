package vault

import (
	"context"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Missing path exclusion must fail all consumer-visible surfaces and leave no
// old operational titles, terms, or chunks in the rewritten caches.
func TestContentPolicyExcludesUntaggedArchiveAndOldCaches(t *testing.T) {
	personal, agent := t.TempDir(), t.TempDir()
	archive := filepath.Join(agent, "smart-home")
	if err := os.MkdirAll(filepath.Join(archive, "rules"), 0700); err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, agent, "public.md", "Orion PUBLIC_FACT_82741")
	if err := os.WriteFile(filepath.Join(archive, "rules/old.md"), []byte("---\ntitle: HAGhost\ncategory: entity\n---\nOrion HA_ARCHIVE_SECRET_82741"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "index.md"), []byte("- [Public](public.md)\n- [HA_ARCHIVE_SECRET_82741](smart-home/rules/old.md)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	legacy := NewFileReader(personal, agent)
	if _, err := legacy.Search(ctx, "agent", "HA_ARCHIVE_SECRET_82741"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScopedEmbeddingStore(fixedEmbedder{}, legacy, agent, "agent", "fixture", zap.NewNop()).Search(ctx, "Orion", 10); err != nil {
		t.Fatal(err)
	}
	policy, err := NewContentPolicy(map[string]string{"personal": personal, "agent": agent}, []string{archive})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewFileReaderWithPolicy(personal, agent, policy)
	if _, err := reader.ReadPage(ctx, "agent", "smart-home/rules/old.md"); err == nil {
		t.Error("excluded archive remains readable")
	}
	if _, err := reader.ReadPage(ctx, "agent", "index.md"); err == nil {
		t.Error("raw index bypasses excluded-title filtering")
	}
	entries, err := reader.ReadIndex(ctx, "agent")
	if err != nil || len(entries) != 1 || entries[0].Path != "public.md" {
		t.Errorf("index leaks exclusion: %+v %v", entries, err)
	}
	hits, err := reader.Search(ctx, "agent", "HA_ARCHIVE_SECRET_82741")
	if err != nil || len(hits) != 0 {
		t.Errorf("old BM25 cache leaks exclusion: %+v %v", hits, err)
	}
	status, err := reader.Status(ctx)
	if err != nil || status.Agent.PageCount != 1 {
		t.Errorf("excluded directory still counted: %+v %v", status, err)
	}
	entities, err := reader.TriggerEntities(ctx, "agent")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		if e == "HAGhost" {
			t.Error("archive title used as trigger")
		}
	}
	store := NewScopedEmbeddingStoreWithPolicy(fixedEmbedder{}, reader, agent, "agent", "fixture", zap.NewNop(), policy)
	dense, err := store.Search(ctx, "Orion", 10)
	if err != nil || len(dense) != 1 || dense[0].PagePath != "public.md" {
		t.Errorf("old dense cache leaks exclusion: %+v %v", dense, err)
	}
	// Even an unfiltered Reader must not let an embedding store load excluded pages.
	dense, err = NewScopedEmbeddingStoreWithPolicy(fixedEmbedder{}, legacy, agent, "agent", "fixture", zap.NewNop(), policy).Search(ctx, "Orion", 10)
	if err != nil || len(dense) != 1 || dense[0].PagePath != "public.md" {
		t.Errorf("embedding policy relies only on Reader: %+v %v", dense, err)
	}
	for _, file := range []string{"bm25.json", "embeddings.json"} {
		data, err := os.ReadFile(filepath.Join(agent, ".rag-cache", file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "HA_ARCHIVE_SECRET_82741") || strings.Contains(string(data), "old.md") {
			t.Errorf("rewritten %s retains excluded source: %s", file, data)
		}
	}
}

// Invalid configuration cannot produce a permissive partial policy.
func TestContentPolicyValidatesDirectoryRelationships(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "agent")
	personal := filepath.Join(root, "personal")
	for _, dir := range []string{agent, personal} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	roots := map[string]string{"agent": agent, "personal": personal}
	for _, tc := range []struct {
		name, path string
		wantError  bool
	}{
		{"nested missing archive", filepath.Join(agent, "smart-home"), false},
		{"external directory", filepath.Join(root, "external"), false},
		{"vault itself", agent, true},
		{"ancestor of vault", root, true},
		{"relative", "smart-home", true},
		{"traversal", agent + string(filepath.Separator) + ".." + string(filepath.Separator) + "external", true},
		{"unclean", agent + string(filepath.Separator) + "." + string(filepath.Separator) + "smart-home", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewContentPolicy(roots, []string{tc.path})
			if (err != nil) != tc.wantError {
				t.Fatalf("policy error=%v wantError=%v", err, tc.wantError)
			}
		})
	}
	policy, err := NewContentPolicy(roots, []string{filepath.Join(agent, "smart-home")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		vault, path string
		allowed     bool
	}{
		{"agent", "public.md", true}, {"personal", "smart-home/public.md", true}, {"agent", "smart-home-other/public.md", true},
		{"agent", "smart-home/rules/old.md", false}, {"agent", `smart-home\rules\old.md`, false},
		{"agent", "../public.md", false}, {"agent", "./public.md", false}, {"agent", "nested/../public.md", false}, {"agent", "nested//public.md", false}, {"missing", "public.md", false},
	} {
		if got := policy.Allows(tc.vault, tc.path); got != tc.allowed {
			t.Errorf("Allows(%q,%q)=%v want %v", tc.vault, tc.path, got, tc.allowed)
		}
	}
}

// A symlink cannot relocate a nested exclusion outside its expected Vault or
// expose the excluded subtree under an apparently public filename.
func TestContentPolicyRejectsSymlinkEscapeAndAlias(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := NewContentPolicy(map[string]string{"agent": root}, []string{filepath.Join(root, "escape")}); err == nil {
		t.Error("nested exclusion resolves outside Vault")
	}
	archive := filepath.Join(root, "smart-home")
	if err := os.Mkdir(archive, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "secret.md"), []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(archive, "secret.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	policy, err := NewContentPolicy(map[string]string{"agent": root}, []string{archive})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Allows("agent", "alias.md") {
		t.Error("public-looking symlink aliases excluded source")
	}
	if _, err := NewFileReaderWithPolicy(root, root, policy).ReadPage(context.Background(), "agent", "alias.md"); err == nil {
		t.Error("source reread followed excluded symlink")
	}
}

// A dangling link is an unsafe relationship, not a not-yet-created archive.
func TestContentPolicyRejectsDanglingOperationalLink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "smart-home")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := NewContentPolicy(map[string]string{"agent": root}, []string{link}); err == nil {
		t.Fatal("dangling operational link accepted as missing directory")
	}
}

// Resolving a root alias once must bind actual reads to that verified root.
func TestContentPolicyReaderBindsValidatedRoot(t *testing.T) {
	parent := t.TempDir()
	original, replacement := t.TempDir(), t.TempDir()
	writeKnowledge(t, original, "public.md", "APPROVED_ROOT_FACT")
	writeKnowledge(t, replacement, "public.md", "UNVALIDATED_ROOT_SECRET")
	alias := filepath.Join(parent, "vault")
	if err := os.Symlink(original, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy, err := NewContentPolicy(map[string]string{"agent": alias}, []string{filepath.Join(alias, "smart-home")})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewFileReaderWithPolicy(alias, alias, policy)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, alias); err != nil {
		t.Fatal(err)
	}
	page, err := reader.ReadPage(context.Background(), "agent", "public.md")
	if err != nil {
		t.Fatal(err)
	}
	if page.Body != "APPROVED_ROOT_FACT" {
		t.Fatalf("reader follows root alias after policy validation: %q", page.Body)
	}
}

// Replacing the validated physical root with a new link must not authorize the
// newly targeted directory under the previously trusted root pathname.
func TestContentPolicyRejectsReplacedPhysicalRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	outside := t.TempDir()
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, outside, "public.md", "NEW_ROOT_SECRET")
	policy, err := NewContentPolicy(map[string]string{"agent": root}, []string{filepath.Join(root, "smart-home")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(parent, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := NewFileReaderWithPolicy(root, root, policy).ReadPage(context.Background(), "agent", "public.md"); err == nil {
		t.Fatal("read followed replacement symlink at validated physical root")
	}
}

// Filtering link targets is insufficient: the index source itself may alias
// an excluded archive and expose its secret title via an otherwise public path.
func TestContentPolicyReadIndexRejectsExcludedSourceAlias(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "smart-home")
	if err := os.Mkdir(archive, 0700); err != nil {
		t.Fatal(err)
	}
	writeKnowledge(t, root, "public.md", "PUBLIC_INDEX_TARGET_FACT")
	source := filepath.Join(archive, "private-index.md")
	if err := os.WriteFile(source, []byte("- [INDEX_SOURCE_SECRET_93271](public.md)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, "index.md")
	if err := os.Symlink(filepath.Join("smart-home", "private-index.md"), index); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy, err := NewContentPolicy(map[string]string{"agent": root}, []string{archive})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := NewFileReaderWithPolicy(root, root, policy).ReadIndex(context.Background(), "agent")
	if err == nil || len(entries) != 0 {
		t.Fatalf("excluded index source title leaked through public target: %+v error=%v", entries, err)
	}
	legacy, err := NewFileReader(root, root).ReadIndex(context.Background(), "agent")
	if err != nil || len(legacy) != 1 || legacy[0].Title != "INDEX_SOURCE_SECRET_93271" {
		t.Fatalf("zero-policy legacy behavior changed: %+v %v", legacy, err)
	}
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("- [Public knowledge](public.md)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err = NewFileReaderWithPolicy(root, root, policy).ReadIndex(context.Background(), "agent")
	if err != nil || len(entries) != 1 || entries[0].Title != "Public knowledge" {
		t.Fatalf("ordinary plain index unavailable: %+v %v", entries, err)
	}
}
