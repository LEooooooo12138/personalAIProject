package vault

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

type EmbedClient interface {
	Embed(context.Context, json.RawMessage) (json.RawMessage, error)
}

const (
	embeddingCacheVersion  = 2
	embeddingMinScore      = 0.3
	embeddingCacheDir      = ".rag-cache"
	embeddingCacheFileName = "embeddings.json"
)

type embeddingCacheEntry struct {
	Chunk       Chunk     `json:"chunk"`
	ContentHash string    `json:"content_hash"`
	Embedding   []float32 `json:"embedding"`
}
type embeddingCacheFile struct {
	Version int                   `json:"version"`
	Model   string                `json:"model"`
	Vault   string                `json:"vault"`
	Entries []embeddingCacheEntry `json:"entries"`
}
type embeddedChunk struct {
	chunk     Chunk
	embedding []float32
}

type EmbeddingStore struct {
	mu        sync.RWMutex
	chunks    []embeddedChunk
	manifest  map[string]string
	indexed   bool
	infer     EmbedClient
	vr        Reader
	vaultDir  string
	vaultName string
	model     string
	logger    *zap.Logger
}

// NewEmbeddingStore preserves the original personal-vault constructor.
func NewEmbeddingStore(infer EmbedClient, vr Reader, vaultDir string, logger *zap.Logger) *EmbeddingStore {
	return NewScopedEmbeddingStore(infer, vr, vaultDir, "personal", "bge-m3", logger)
}
func NewScopedEmbeddingStore(infer EmbedClient, vr Reader, vaultDir, vaultName, model string, logger *zap.Logger) *EmbeddingStore {
	if model == "" {
		model = "bge-m3"
	}
	return &EmbeddingStore{infer: infer, vr: vr, vaultDir: vaultDir, vaultName: vaultName, model: model, logger: logger}
}
func (es *EmbeddingStore) Warmup() {
	es.WarmupContext(context.Background())
}

// WarmupContext lets the application cancel and join startup indexing on exit.
func (es *EmbeddingStore) WarmupContext(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if err := es.ensureIndexed(ctx); err != nil {
		es.logger.Warn("embedding warmup failed", zap.Error(err))
	}
}

type EmbeddingResult struct {
	PagePath     string
	Title        string
	Score        float64
	ChunkContent string
	SectionTitle string
}

func (es *EmbeddingStore) Search(ctx context.Context, query string, k int) ([]EmbeddingResult, error) {
	if k <= 0 {
		return nil, nil
	}
	if err := es.ensureIndexed(ctx); err != nil {
		return nil, err
	}
	vec, err := es.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	es.mu.RLock()
	defer es.mu.RUnlock()
	var results []EmbeddingResult
	for _, ec := range es.chunks {
		score := CosineSimilarity32(vec, ec.embedding)
		if score > embeddingMinScore {
			results = append(results, EmbeddingResult{PagePath: ec.chunk.PagePath, Title: ec.chunk.PageTitle, Score: float64(score), ChunkContent: ec.chunk.Content, SectionTitle: ec.chunk.SectionTitle})
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

// The full manifest is checked on each search. Rebuild publication is atomic;
// unchanged chunks reuse vectors, failed/cancelled builds remain retryable.
func (es *EmbeddingStore) ensureIndexed(ctx context.Context) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if es.vaultName != "personal" && es.vaultName != "agent" {
		return fmt.Errorf("unknown embedding vault %q", es.vaultName)
	}
	manifest, err := markdownFiles(es.vaultDir, systemFiles)
	if err != nil {
		return err
	}
	if es.indexed && maps.Equal(manifest, es.manifest) {
		return nil
	}
	reusable := make(map[string][]float32)
	for _, ec := range es.chunks {
		reusable[chunkKey(ec.chunk)] = ec.embedding
	}
	if !es.indexed {
		var cache embeddingCacheFile
		data, err := readRootFile(es.vaultDir, filepath.Join(embeddingCacheDir, embeddingCacheFileName))
		if err == nil && json.Unmarshal(data, &cache) == nil && cache.Version == embeddingCacheVersion && cache.Model == es.model && cache.Vault == es.vaultName {
			for _, entry := range cache.Entries {
				if len(entry.Embedding) > 0 && entry.ContentHash == hashContent(entry.Chunk.Content) {
					reusable[chunkKey(entry.Chunk)] = entry.Embedding
				}
			}
		}
	}
	paths := make([]string, 0, len(manifest))
	for path := range manifest {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var rebuilt []embeddedChunk
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := es.vr.ReadPage(ctx, es.vaultName, path)
		if err != nil {
			return err
		}
		for _, chunk := range ChunkPage(page) {
			chunk.PagePath = path
			vec := reusable[chunkKey(chunk)]
			if len(vec) == 0 {
				vec, err = es.embed(ctx, chunk.Content)
				if err != nil {
					return fmt.Errorf("index %s: %w", path, err)
				}
			}
			rebuilt = append(rebuilt, embeddedChunk{chunk: chunk, embedding: vec})
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	es.chunks = rebuilt
	es.manifest = manifest
	es.indexed = true
	cache := embeddingCacheFile{Version: embeddingCacheVersion, Model: es.model, Vault: es.vaultName}
	for _, ec := range rebuilt {
		cache.Entries = append(cache.Entries, embeddingCacheEntry{Chunk: ec.chunk, ContentHash: hashContent(ec.chunk.Content), Embedding: ec.embedding})
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	if err := writeRootFile(es.vaultDir, filepath.Join(embeddingCacheDir, embeddingCacheFileName), data, 0600); err != nil {
		es.logger.Warn("embedding cache save failed", zap.Error(err))
	}
	return nil
}
func chunkKey(c Chunk) string     { return c.PagePath + "\x00" + c.ID + "\x00" + hashContent(c.Content) }
func hashContent(s string) string { h := sha256.Sum256([]byte(s)); return fmt.Sprintf("%x", h[:]) }
func (es *EmbeddingStore) embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]interface{}{"model": es.model, "input": text})
	resp, err := es.infer.Embed(ctx, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return result.Data[0].Embedding, nil
}
