package vault

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── Inverted Index ──
//
// Persistent term→document index for BM25 keyword search.
// Eliminates the need to read every .md file on every query.
//
// Cache file: $VAULT/.rag-cache/bm25.json
// Validation: each document's mtime is compared against the filesystem.
//   If mtime matches → use cached tf data.
//   If mtime differs → rebuild that document (and save on next cache write).

const invertedCacheVersion = 2

// ── In-memory types ──

// docMeta holds per-document metadata for BM25 scoring.
type docMeta struct {
	path   string // relative path from vault root
	title  string // frontmatter title (resolved at build time)
	length int    // total token count
	mtime  int64  // last modification time (Unix nanoseconds)
	hash   string
}

// postingEntry is one entry in a term's posting list.
type postingEntry struct {
	docID int // index into idx.docs
	freq  int // term frequency in this document
}

// InvertedIndex maps every token to the documents that contain it.
type InvertedIndex struct {
	docs     []docMeta
	postings map[string][]postingEntry // term → sorted posting list
	avgdl    float64                   // average document length
}

// ── JSON-serializable cache types ──

type docMetaJSON struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Length int    `json:"length"`
	MTime  int64  `json:"mtime"`
	Hash   string `json:"hash"`
}

type postingEntryJSON struct {
	DocID int `json:"doc_id"`
	Freq  int `json:"freq"`
}

type invertedIndexCache struct {
	Version  int                           `json:"version"`
	BuiltAt  int64                         `json:"built_at"`
	Docs     []docMetaJSON                 `json:"docs"`
	Postings map[string][]postingEntryJSON `json:"postings"`
}

// ── Build ──

// BuildIndex walks the vault directory and constructs the inverted index.
// systemFiles are skipped (index.md, log.md, etc.).
func (idx *InvertedIndex) BuildIndex(root string, systemFiles map[string]bool) error {
	idx.docs = nil
	idx.postings = make(map[string][]postingEntry)
	var totalLen int

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if systemFiles[filepath.Base(path)] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		data, err := readRootFile(root, relPath)
		if err != nil {
			return err
		}

		// Resolve frontmatter title.
		title := strings.TrimSuffix(filepath.Base(path), ".md")
		if page, err := ParsePage(data); err == nil && page.Title != "" {
			title = page.Title
		}

		content := strings.ToLower(string(data))
		docTok := tokenize(content)
		tf := make(map[string]int)
		for _, t := range docTok {
			tf[t]++
		}
		for term, freq := range tf {
			idx.postings[term] = append(idx.postings[term], postingEntry{
				docID: len(idx.docs),
				freq:  freq,
			})
		}

		idx.docs = append(idx.docs, docMeta{
			path:   relPath,
			title:  title,
			length: len(docTok),
			mtime:  info.ModTime().UnixNano(),
			hash:   hashContent(string(data)),
		})
		totalLen += len(docTok)
		return nil
	})
	if err != nil {
		return fmt.Errorf("build inverted index: %w", err)
	}

	if len(idx.docs) > 0 {
		idx.avgdl = float64(totalLen) / float64(len(idx.docs))
	}
	return nil
}

// ── Persistence ──

// SaveToFile serialises the index to a JSON file.
func (idx *InvertedIndex) SaveToFile(path string) error {
	data, err := idx.marshalCache()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (idx *InvertedIndex) marshalCache() ([]byte, error) {
	docs := make([]docMetaJSON, len(idx.docs))
	for i, d := range idx.docs {
		docs[i] = docMetaJSON{
			Path:   d.path,
			Title:  d.title,
			Length: d.length,
			MTime:  d.mtime,
			Hash:   d.hash,
		}
	}

	postings := make(map[string][]postingEntryJSON, len(idx.postings))
	for term, entries := range idx.postings {
		pej := make([]postingEntryJSON, len(entries))
		for j, e := range entries {
			pej[j] = postingEntryJSON{DocID: e.docID, Freq: e.freq}
		}
		postings[term] = pej
	}

	cache := invertedIndexCache{
		Version:  invertedCacheVersion,
		BuiltAt:  time.Now().Unix(),
		Docs:     docs,
		Postings: postings,
	}

	return json.Marshal(cache)
}

// LoadFromFile reads a serialised index from disk.
func (idx *InvertedIndex) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return idx.loadCache(data)
}

func (idx *InvertedIndex) loadCache(data []byte) error {
	var cache invertedIndexCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return fmt.Errorf("parse bm25 cache: %w", err)
	}
	if cache.Version != invertedCacheVersion {
		return fmt.Errorf("version mismatch: cached %d, want %d", cache.Version, invertedCacheVersion)
	}

	idx.docs = make([]docMeta, len(cache.Docs))
	for i, d := range cache.Docs {
		if _, err := relativePath(d.Path); err != nil || d.Length < 0 {
			return fmt.Errorf("invalid cached document")
		}
		idx.docs[i] = docMeta{
			path:   d.Path,
			title:  d.Title,
			length: d.Length,
			mtime:  d.MTime,
			hash:   d.Hash,
		}
	}

	idx.postings = make(map[string][]postingEntry, len(cache.Postings))
	var totalLen int
	for term, entries := range cache.Postings {
		pe := make([]postingEntry, len(entries))
		for j, e := range entries {
			if e.DocID < 0 || e.DocID >= len(idx.docs) || e.Freq <= 0 || idx.docs[e.DocID].length == 0 {
				return fmt.Errorf("invalid cached posting")
			}
			pe[j] = postingEntry{docID: e.DocID, freq: e.Freq}
		}
		idx.postings[term] = pe
	}
	for _, d := range idx.docs {
		totalLen += d.length
	}
	if len(idx.docs) > 0 {
		idx.avgdl = float64(totalLen) / float64(len(idx.docs))
	}
	return nil
}

// Validate compares the complete file set and content hashes.
func (idx *InvertedIndex) Validate(root string) bool {
	manifest, err := markdownFiles(root, systemFiles)
	if err != nil || len(manifest) != len(idx.docs) {
		return false
	}
	for _, doc := range idx.docs {
		if hash, ok := manifest[doc.path]; !ok || hash != doc.hash {
			return false
		}
	}
	return true
}

// ── BM25 Search ──

// SearchWithBM25 scores every document against the query tokens using Okapi BM25.
func (idx *InvertedIndex) SearchWithBM25(tokens []string, k1, b float64) []bm25Result {
	if len(idx.docs) == 0 || len(tokens) == 0 {
		return nil
	}

	N := float64(len(idx.docs))

	// docScore accumulates BM25 score per document.
	type docAcc struct {
		score   float64
		matched int // distinct tokens matched
	}
	acc := make([]docAcc, len(idx.docs))

	for _, tok := range tokens {
		entries, ok := idx.postings[tok]
		if !ok {
			continue
		}
		n := float64(len(entries)) // document frequency for this term
		if n == 0 {
			n = 0.5
		}
		idf := math.Log((N-n+0.5)/(n+0.5) + 1.0)

		for _, e := range entries {
			f := float64(e.freq)
			dl := float64(idx.docs[e.docID].length)
			tfNorm := (f * (k1 + 1)) / (f + k1*(1-b+b*dl/idx.avgdl))
			acc[e.docID].score += idf * tfNorm
			acc[e.docID].matched++
		}
	}

	// Filter: require at least minMatchCount distinct tokens.
	minMatch := 1
	if len(tokens) > 2 {
		minMatch = 2
	}

	var results []bm25Result
	for i, a := range acc {
		if a.score > 0 && a.matched >= minMatch {
			results = append(results, bm25Result{
				Path:  idx.docs[i].path,
				Score: a.score,
			})
		}
	}

	// Sort descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	return results
}
