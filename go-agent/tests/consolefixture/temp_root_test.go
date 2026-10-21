package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureUsesControlledTempRoot(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "other-batch.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONSOLE_FIXTURE_TEMP_ROOT", root)
	f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if filepath.Dir(f.dir) != root {
		t.Fatalf("fixture dir %q is outside owned batch root %q", f.dir, root)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatalf("fixture child remains: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("parent/sibling data changed: %v", err)
	}
}

func TestFixtureRejectsInvalidControlledTempRoot(t *testing.T) {
	for _, root := range []string{"relative-fixture-root", filepath.Join(t.TempDir(), "missing")} {
		t.Run(root, func(t *testing.T) {
			t.Setenv("CONSOLE_FIXTURE_TEMP_ROOT", root)
			f, err := newFixture("127.0.0.1:18081", "http://127.0.0.1:18081")
			if f != nil {
				_ = f.Close()
			}
			if err == nil {
				t.Fatal("invalid explicit root silently fell back to system TEMP")
			}
		})
	}
}
