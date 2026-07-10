package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

// ── Embedding Cache ──
//
// Pre-computes and caches embeddings for all vault chunks.
// Persisted to $VAULT/.rag-cache/embeddings.json so that restarts
// do not require a full re-index.
//
// Cache validation: each entry stores a SHA-256 content hash.
// On load, the hash is compared against the current chunk content.
// Mismatched entries are re-embedded; missing pages are dropped.

const (
	embeddingCacheVersion = 1      // bumped when cache format changes
	embeddingMinScore     = 0.3    // minimum cosine similarity for a hit
	embeddingCacheDir     = ".rag-cache"
	embeddingCacheFileName = "embeddings.json"
)

// embeddingCacheEntry is the serialised form of one embedded chunk.
type embeddingCacheEntry struct {
	ChunkID      string    `json:"chunk_id"`
	PagePath     string    `json:"page_path"`
	PageTitle    string    `json:"page_title"`
	SectionTitle string    `json:"section_title"`
	Content      string    `json:"content"`
	ContentHash  string    `json:"content_hash"` // SHA-256 hex, first 16 chars
	Embedding    []float32 `json:"embedding"`
	Tags         []string  `json:"tags"`
	Category     string    `json:"category"`
}

// embeddingCacheFile is the top-level cache document written to disk.
type embeddingCacheFile struct {
	Version int                   `json:"version"`
	BuiltAt int64                 `json:"built_at"` // Unix seconds
	Entries []embeddingCacheEntry `json:"entries"`
}

// embeddedChunk is a vault chunk with its vector embedding (in-memory form).
type embeddedChunk struct {
	chunk     vault.Chunk
	embedding []float32
}

// EmbeddingStore holds pre-computed chunk embeddings and provides semantic search.
type EmbeddingStore struct {
	mu        sync.RWMutex
	chunks    []embeddedChunk
	indexed   bool
	cachePath string // absolute path to embeddings.json
	infer     inference.Client
	vr        vault.Reader
	vaultDir  string
	logger    *zap.Logger
}

// NewEmbeddingStore creates an embedding-backed semantic search index.
// cachePath is derived from vaultDir/.rag-cache/embeddings.json.
func NewEmbeddingStore(infer inference.Client, vr vault.Reader, vaultDir string, logger *zap.Logger) *EmbeddingStore {
	cacheDir := filepath.Join(vaultDir, embeddingCacheDir)
	_ = os.MkdirAll(cacheDir, 0755) // best-effort; ensureIndexed will handle errors
	return &EmbeddingStore{
		infer:     infer,
		vr:        vr,
		vaultDir:  vaultDir,
		cachePath: filepath.Join(cacheDir, embeddingCacheFileName),
		logger:    logger,
	}
}

// Warmup triggers background index building so the first search request
// does not incur the full indexing cost. Safe to call multiple times.
func (es *EmbeddingStore) Warmup() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := es.ensureIndexed(ctx); err != nil {
		es.logger.Warn("embedding warmup failed", zap.Error(err))
	}
}

// Search performs semantic (dense) search over vault chunks.
// Returns up to k results sorted by cosine similarity.
func (es *EmbeddingStore) Search(ctx context.Context, query string, k int) ([]EmbeddingResult, error) {
	if err := es.ensureIndexed(ctx); err != nil {
		return nil, err
	}

	queryVec, err := es.embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	es.mu.RLock()
	defer es.mu.RUnlock()

	type scored struct {
		idx   int
		score float32
	}
	var scores []scored
	for i, ec := range es.chunks {
		s := cosineSimilarity(queryVec, ec.embedding)
		if s > embeddingMinScore {
			scores = append(scores, scored{idx: i, score: s})
		}
	}

	// Sort descending.
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	var results []EmbeddingResult
	for i, sc := range scores {
		if i >= k {
			break
		}
		ec := es.chunks[sc.idx]
		results = append(results, EmbeddingResult{
			PagePath:     ec.chunk.PagePath,
			Title:        ec.chunk.PageTitle,
			Score:        float64(sc.score),
			ChunkContent: ec.chunk.Content,
			SectionTitle: ec.chunk.SectionTitle,
		})
	}
	return results, nil
}

type EmbeddingResult struct {
	PagePath     string
	Title        string
	Score        float64
	ChunkContent string // matched chunk body text
	SectionTitle string // H2 heading for provenance display
}

// ensureIndexed guarantees the embedding index is loaded.
// Priority order:
//   1. Already indexed in memory → return immediately.
//   2. Valid cache file on disk → load it.
//   3. Neither → walk vault, chunk, embed, save cache.
func (es *EmbeddingStore) ensureIndexed(ctx context.Context) error {
	es.mu.Lock()
	if es.indexed {
		es.mu.Unlock()
		return nil
	}
	es.mu.Unlock()

	// Try loading from disk cache.
	if loaded := es.loadFromCache(ctx); loaded {
		es.mu.Lock()
		es.indexed = true
		es.mu.Unlock()
		es.logger.Info("embedding index loaded from cache",
			zap.Int("chunks", len(es.chunks)))
		return nil
	}

	// Build from scratch.
	if err := es.buildFromVault(ctx); err != nil {
		return err
	}

	// Persist for next start.
	if err := es.saveToCache(); err != nil {
		es.logger.Warn("failed to save embedding cache", zap.Error(err))
		// Non-fatal: index is still usable in memory.
	}

	es.mu.Lock()
	es.indexed = true
	es.mu.Unlock()
	es.logger.Info("embedding index built and cached",
		zap.Int("chunks", len(es.chunks)))
	return nil
}

// buildFromVault walks the vault, chunks all pages, and embeds every chunk.
func (es *EmbeddingStore) buildFromVault(ctx context.Context) error {
	es.logger.Info("building embedding index from vault...")

	var allChunks []vault.Chunk
	err := filepath.WalkDir(es.vaultDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
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
		base := filepath.Base(path)
		if base == "index.md" || base == "log.md" || base == "hot.md" || base == "AGENTS.md" {
			return nil
		}
		relPath, _ := filepath.Rel(es.vaultDir, path)

		page, err := es.vr.ReadPage(ctx, "personal", relPath)
		if err != nil {
			return nil
		}
		chunks := vault.ChunkPage(page)
		for i := range chunks {
			chunks[i].PagePath = relPath
		}
		allChunks = append(allChunks, chunks...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk vault: %w", err)
	}

	es.logger.Info("indexing chunks", zap.Int("count", len(allChunks)))

	var ecs []embeddedChunk
	for i, c := range allChunks {
		vec, err := es.embed(ctx, c.Content)
		if err != nil {
			es.logger.Warn("embed chunk failed, skipping",
				zap.String("title", c.PageTitle),
				zap.Int("chunk", i),
				zap.Error(err))
			continue
		}
		ecs = append(ecs, embeddedChunk{chunk: c, embedding: vec})
	}

	es.mu.Lock()
	es.chunks = ecs
	es.mu.Unlock()
	return nil
}

// ── Disk cache ──

// loadFromCache reads the persisted embedding cache and validates every entry
// against the current vault content. Returns true if the cache was usable.
func (es *EmbeddingStore) loadFromCache(ctx context.Context) bool {
	data, err := os.ReadFile(es.cachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			es.logger.Warn("read embedding cache failed", zap.Error(err))
		}
		return false
	}

	var cache embeddingCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		es.logger.Warn("parse embedding cache failed", zap.Error(err))
		return false
	}
	if cache.Version != embeddingCacheVersion {
		es.logger.Info("embedding cache version mismatch, rebuilding",
			zap.Int("cached", cache.Version),
			zap.Int("current", embeddingCacheVersion))
		return false
	}

	valid := 0
	invalid := 0
	var ecs []embeddedChunk

	for _, entry := range cache.Entries {
		// Verify the source file still exists and content has not changed.
		fullPath := filepath.Join(es.vaultDir, entry.PagePath)
		srcData, err := os.ReadFile(fullPath)
		if err != nil {
			invalid++
			continue
		}
		// Re-chunk the page to get the current chunk content, then compare hashes.
		page, err := vault.ParsePage(srcData)
		if err != nil {
			invalid++
			continue
		}
		chunks := vault.ChunkPage(page)
		found := false
		for _, c := range chunks {
			if c.ID == entry.ChunkID {
				if hashContent(c.Content) == entry.ContentHash {
					found = true
					ecs = append(ecs, embeddedChunk{
						chunk:     c,
						embedding: entry.Embedding,
					})
				}
				break
			}
		}
		if found {
			valid++
		} else {
			invalid++
		}
	}

	if valid == 0 {
		es.logger.Info("embedding cache stale, rebuilding")
		return false
	}

	es.mu.Lock()
	es.chunks = ecs
	es.mu.Unlock()
	es.logger.Info("embedding cache validated",
		zap.Int("valid", valid),
		zap.Int("invalid", invalid))
	return true
}

// saveToCache serialises the current in-memory index to disk.
func (es *EmbeddingStore) saveToCache() error {
	es.mu.RLock()
	defer es.mu.RUnlock()

	entries := make([]embeddingCacheEntry, 0, len(es.chunks))
	for _, ec := range es.chunks {
		entries = append(entries, embeddingCacheEntry{
			ChunkID:      ec.chunk.ID,
			PagePath:     ec.chunk.PagePath,
			PageTitle:    ec.chunk.PageTitle,
			SectionTitle: ec.chunk.SectionTitle,
			Content:      ec.chunk.Content,
			ContentHash:  hashContent(ec.chunk.Content),
			Embedding:    ec.embedding,
			Tags:         ec.chunk.Tags,
			Category:     ec.chunk.Category,
		})
	}

	cache := embeddingCacheFile{
		Version: embeddingCacheVersion,
		BuiltAt: time.Now().Unix(),
		Entries: entries,
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	if err := os.WriteFile(es.cachePath, data, 0644); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}

// hashContent returns a compact content hash (first 16 hex chars of SHA-256).
func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8]) // 8 bytes = 16 hex chars
}

// ── Inference ──

// embed calls the inference service to get an embedding vector.
func (es *EmbeddingStore) embed(ctx context.Context, text string) ([]float32, error) {
	req := map[string]interface{}{
		"model": "bge-m3",
		"input": text,
	}
	body, _ := json.Marshal(req)

	resp, err := es.infer.Embed(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("embed call: %w", err)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("embed parse: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return result.Data[0].Embedding, nil
}

// ── Cosine Similarity ──

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}
