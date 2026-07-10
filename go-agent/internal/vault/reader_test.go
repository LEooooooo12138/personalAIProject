package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePage_WithFrontmatter(t *testing.T) {
	content := `---
title: "Test Page"
tags: [go, testing]
category: concepts
created: 2026-07-02
updated: 2026-07-02
---

This is the body.
It has multiple lines.
`

	page, err := ParsePage([]byte(content))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}

	if page.Title != "Test Page" {
		t.Errorf("title = %q, want Test Page", page.Title)
	}
	if len(page.Tags) != 2 || page.Tags[0] != "go" {
		t.Errorf("tags = %v, want [go testing]", page.Tags)
	}
	if page.Category != "concepts" {
		t.Errorf("category = %q, want concepts", page.Category)
	}
	if page.Created != "2026-07-02" {
		t.Errorf("created = %q, want 2026-07-02", page.Created)
	}
	if page.Body != "This is the body.\nIt has multiple lines." {
		t.Errorf("body = %q", page.Body)
	}
}

func TestParsePage_NoFrontmatter(t *testing.T) {
	content := "Just a plain markdown file.\nNo frontmatter here."

	page, err := ParsePage([]byte(content))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}

	if page.Title != "untitled" {
		t.Errorf("title = %q, want untitled", page.Title)
	}
	if page.Body != "Just a plain markdown file.\nNo frontmatter here." {
		t.Errorf("body = %q", page.Body)
	}
}

func TestVaultPath(t *testing.T) {
	r := NewFileReader("/home/user/personal", "/home/user/agent")

	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"personal", "/home/user/personal", false},
		{"agent", "/home/user/agent", false},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		got, err := r.vaultPath(tt.name)
		if tt.wantErr {
			if err == nil {
				t.Errorf("vaultPath(%q): expected error", tt.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("vaultPath(%q): %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("vaultPath(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSearch_FindsKeyword(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock vault structure.
	conceptsDir := filepath.Join(tmpDir, "concepts")
	os.MkdirAll(conceptsDir, 0755)
	os.WriteFile(filepath.Join(conceptsDir, "rag.md"), []byte(`---
title: "RAG"
---
Retrieval-Augmented Generation is a technique.
It combines search with language models.
`), 0644)
	os.WriteFile(filepath.Join(conceptsDir, "unrelated.md"), []byte(`---
title: "Other"
---
This file has nothing to do with the search.
`), 0644)

	r := &FileReader{personalPath: tmpDir, agentPath: tmpDir}
	// "technique" is a standalone token; "augmented" is inside "retrieval-augmented"
	// and the bigram tokenizer preserves hyphens, so exact-token BM25 needs the full token.
	results, err := r.Search(context.Background(), "personal", "technique")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	// Title from YAML frontmatter, not filename.
	if results[0].Title != "RAG" {
		t.Errorf("result title = %q, want RAG", results[0].Title)
	}
}
