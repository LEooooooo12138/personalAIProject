package vault

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidFrontmatterIsRejected(t *testing.T) {
	for name, data := range map[string]string{
		"scalar tags":   "---\ntags: internal\n---\nsecret",
		"wrong created": "---\ntags: [internal]\ncreated: [bad]\n---\nsecret",
		"numeric title": "---\ntitle: 123\n---\nsecret",
		"duplicate":     "---\ntags: [internal]\ntags: []\n---\nsecret",
		"broken yaml":   "---\ntags: [internal\n---\nsecret",
		"unclosed":      "---\ntags: [internal]\nsecret",
	} {
		t.Run(name, func(t *testing.T) {
			if page, err := ParsePage([]byte(data)); !errors.Is(err, ErrInvalidFrontmatter) || page != nil {
				t.Fatalf("invalid metadata accepted: %+v, %v", page, err)
			}
		})
	}
}
func TestFrontmatterCompatibility(t *testing.T) {
	for _, data := range []string{"---\n---\nbody", "\ufeff---\r\ntags: []\r\n---\r\nbody", "---\ncreated: 2026-07-02\ncustom: {nested: yes}\n---\nbody", "plain body"} {
		page, err := ParsePage([]byte(data))
		if err != nil || page == nil {
			t.Fatalf("valid page rejected: %q: %v", data, err)
		}
	}
	page, err := ParsePage([]byte("---\ntitle: EOF\n---"))
	if err != nil || page.Title != "EOF" || page.Body != "" {
		t.Fatalf("EOF delimiter: %+v %v", page, err)
	}
}
func TestFilenameTitle(t *testing.T) {
	root := t.TempDir()
	reader := NewFileReader(root, root)
	for _, name := range []string{"Alpha", "Beta"} {
		if err := os.WriteFile(filepath.Join(root, name+".md"), []byte("unique body"), 0600); err != nil {
			t.Fatal(err)
		}
		page, err := reader.ReadPage(context.Background(), "agent", name+".md")
		if err != nil || page.Title != name {
			t.Fatalf("filename fallback %s: %+v %v", name, page, err)
		}
	}
}
func TestInvalidMetadataSearchAndDenseRefresh(t *testing.T) {
	root := t.TempDir()
	reader := NewFileReader(root, root)
	ctx := context.Background()
	store := NewScopedEmbeddingStore(fixedEmbedder{}, reader, root, "agent", "test", zap.NewNop())
	for _, tc := range []struct {
		meta string
		want int
	}{{"tags: []", 1}, {"tags: [internal]", 0}, {"tags: internal", 0}, {"tags: []", 1}} {
		if err := os.WriteFile(filepath.Join(root, "Orion.md"), []byte("---\ntitle: Orion\n"+tc.meta+"\n---\nneedle secret"), 0600); err != nil {
			t.Fatal(err)
		}
		hits, err := reader.Search(ctx, "agent", "needle")
		if err != nil || len(hits) != tc.want {
			t.Errorf("sparse %s: %+v %v", tc.meta, hits, err)
		}
		dense, err := store.Search(ctx, "needle", 10)
		if err != nil || len(dense) != tc.want {
			t.Errorf("dense %s: %+v %v", tc.meta, dense, err)
		}
	}
}

func TestInvalidMetadataBM25RetainsManifestWithoutPostings(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.md"), []byte("---\ntags: internal\n---\nsecretneedle"), 0600); err != nil {
		t.Fatal(err)
	}
	idx := &InvertedIndex{}
	if err := idx.BuildIndex(root, systemFiles); err != nil {
		t.Fatal(err)
	}
	if !idx.Validate(root) {
		t.Fatal("invalid page caused permanent manifest miss")
	}
	if hits := idx.SearchWithBM25([]string{"secretneedle"}, bm25k1, bm25b); len(hits) != 0 {
		t.Fatalf("invalid page retained postings: %+v", hits)
	}
	data, err := idx.marshalCache()
	if err != nil {
		t.Fatal(err)
	}
	reloaded := &InvertedIndex{}
	if err := reloaded.loadCache(data); err != nil {
		t.Fatal(err)
	}
	if !reloaded.Validate(root) {
		t.Fatal("reload lost invalid-page manifest")
	}
}

type failingPageReader struct {
	*FileReader
	failure error
}

func (r failingPageReader) ReadPage(context.Context, string, string) (*Page, error) {
	return nil, r.failure
}
func TestInvalidMetadataDoesNotHideOtherReaderErrors(t *testing.T) {
	root := t.TempDir()
	writeKnowledge(t, root, "public.md", "needle")
	for _, failure := range []error{os.ErrPermission, context.Canceled} {
		reader := failingPageReader{NewFileReader(root, root), failure}
		store := NewScopedEmbeddingStore(fixedEmbedder{}, reader, root, "agent", "test", zap.NewNop())
		if _, err := store.Search(context.Background(), "needle", 10); !errors.Is(err, failure) {
			t.Fatalf("reader failure hidden: got %v want %v", err, failure)
		}
	}
	reader := NewFileReader(root, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Search(ctx, "agent", "needle"); !errors.Is(err, context.Canceled) {
		t.Fatalf("search cancellation hidden: %v", err)
	}
	if _, err := reader.ReadPage(ctx, "agent", "public.md"); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation hidden: %v", err)
	}
}

func TestInvalidFrontmatterRejectsTrailingYAML(t *testing.T) {
	for _, content := range []string{"title: Public\n...\ntags: [internal]", "title: Public\n...\n[internal]"} {
		page, err := ParsePage([]byte("---\n" + content + "\n---\nprivate"))
		if !errors.Is(err, ErrInvalidFrontmatter) || page != nil {
			t.Fatalf("trailing metadata accepted: %+v %v", page, err)
		}
	}
}
