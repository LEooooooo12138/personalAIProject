package vault

import (
	"os"
	"path/filepath"
)

// ── BM25 Scorer ──
//
// Okapi BM25 ranking over a persistent inverted index.
// On first call, the index is built from vault .md files and cached to
// $VAULT/.rag-cache/bm25.json. Subsequent calls validate the cache via
// mtime comparison and reuse it, avoiding per-query file I/O.
//
// Parameters: k1=1.5, b=0.75 (standard values from TREC experiments).

const (
	bm25k1 = 1.5
	bm25b  = 0.75

	bm25CacheDir  = ".rag-cache"
	bm25CacheFile = "bm25.json"
)

// bm25Result is a scored document.
type bm25Result struct {
	Path  string
	Score float64
}

// bm25Search ranks vault markdown files by BM25 relevance to the query tokens.
// Uses a persistent inverted index cache to avoid reading every file per query.
func bm25Search(root string, tokens []string, systemFiles map[string]bool) ([]bm25Result, error) {
	if len(tokens) == 0 {
		return nil, nil
	}

	cachePath := filepath.Join(root, bm25CacheDir, bm25CacheFile)
	idx := &InvertedIndex{}

	// Try loading cached index.
	if err := idx.LoadFromFile(cachePath); err == nil && idx.Validate(root) {
		return idx.SearchWithBM25(tokens, bm25k1, bm25b), nil
	}

	// Cache miss or stale — build from scratch.
	if err := idx.BuildIndex(root, systemFiles); err != nil {
		return nil, err
	}

	// Ensure cache directory exists.
	os.MkdirAll(filepath.Dir(cachePath), 0755)

	// Best-effort persist.
	if err := idx.SaveToFile(cachePath); err != nil {
		// Non-fatal: search still works, just slower next time.
	}

	return idx.SearchWithBM25(tokens, bm25k1, bm25b), nil
}

// minMatchCount returns the minimum number of distinct query tokens
// that must appear in a document for it to be considered a match.
// For short queries (1-2 tokens), require 1. For longer, require at least 2.
func minMatchCount(tokens []string) int {
	if len(tokens) <= 2 {
		return 1
	}
	return 2
}
