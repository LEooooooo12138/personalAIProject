package memory

import (
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidMetadataMemoryIsSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "_memory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"bad.md": "---\ntags: internal\n---\nprivate", "good.md": "public"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := NewSedimenter(root, nil, zap.NewNop()).loadExistingMemories(dir)
	if err != nil || len(docs) != 1 || docs[0].content != "public" {
		t.Fatalf("bad memory not skipped independently: %+v %v", docs, err)
	}
}

func TestInvalidMetadataMemoryDoesNotHideReadErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "_memory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.md")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if docs, err := NewSedimenter(root, nil, zap.NewNop()).loadExistingMemories(dir); err == nil || len(docs) != 0 {
		t.Fatalf("read error hidden: %v %v", docs, err)
	}
}
