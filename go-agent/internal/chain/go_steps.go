package chain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/vault"
)

// 鈹€鈹€ Go-Native Steps 鈹€鈹€
//
// These steps perform deterministic operations on the filesystem or in-memory.
// They do NOT call LLMs. They are fast, cheap, and never hallucinate.
//
// Each step reads from ChainState, does work, and writes results back.
// Steps are designed to be composable: a VaultSearchStep can feed into
// a VaultReadStep which feeds into an LLMAnswerStep.

// 鈹€鈹€ Vault Search Step 鈹€鈹€

// VaultSearchStep performs keyword + embedding search on the vault.
type VaultSearchStep struct {
	reader     vault.Reader
	embedStore EmbeddingSearcher
	maxResults int
	logger     *zap.Logger
	rrfK       int
}

// EmbeddingSearcher provides dense (semantic) search over embedded chunks.
type EmbeddingSearcher interface {
	Search(ctx context.Context, query string, k int) ([]vault.EmbeddingResult, error)
}

type VaultEmbeddingSearcher interface {
	SearchVault(ctx context.Context, vaultName, query string, k int) ([]vault.EmbeddingResult, error)
}

// NewVaultSearchStep creates a hybrid search step (BM25 + embedding).
func NewVaultSearchStep(reader vault.Reader, embedStore EmbeddingSearcher, maxResults int, logger *zap.Logger) *VaultSearchStep {
	if maxResults <= 0 {
		maxResults = 5
	}
	return &VaultSearchStep{
		reader:     reader,
		embedStore: embedStore,
		maxResults: maxResults,
		logger:     logger,
		rrfK:       60,
	}
}

func (s *VaultSearchStep) Name() string { return "vault-search" }

func (s *VaultSearchStep) Run(ctx context.Context, state *ChainState) error {
	vaultName := state.Vault
	if vaultName == "" {
		vaultName = "personal"
	}
	// Use explicit search_query from LLMDecideStep if available; fall back to user query.
	query := state.Query
	if sq, ok := state.GetString("search_query"); ok && sq != "" {
		query = sq
	}
	if query == "" {
		return nil
	}

	// BM25 sparse search.
	bm25Results, bm25Err := s.reader.Search(ctx, vaultName, query)
	if bm25Err != nil {
		s.logger.Warn("bm25 search error", zap.Error(bm25Err))
	}

	// Embedding dense search (if available).
	var embedResults []vault.EmbeddingResult
	denseAvailable := false
	if s.embedStore != nil {
		var results []vault.EmbeddingResult
		var err error
		if scoped, ok := s.embedStore.(VaultEmbeddingSearcher); ok {
			results, err = scoped.SearchVault(ctx, vaultName, query, s.maxResults*2)
			denseAvailable = err == nil
		} else if vaultName == "personal" {
			results, err = s.embedStore.Search(ctx, query, s.maxResults*2)
			denseAvailable = err == nil
		}
		if err != nil {
			s.logger.Warn("embedding search error", zap.Error(err))
		} else {
			embedResults = results
		}
	}
	if bm25Err != nil && !denseAvailable {
		return fmt.Errorf("vault search unavailable: %w", bm25Err)
	}

	// RRF fusion.
	retrieval := vault.NewRetrievalService(s.reader, nil, s.rrfK)
	merged := retrieval.RRFMerge(bm25Results, embedResults)

	// Add sources to state.
	for _, m := range merged {
		if len(state.Sources) >= s.maxResults {
			break
		}
		if m.Path == "" {
			continue
		}
		page, err := s.reader.ReadPage(ctx, vaultName, m.Path)
		if errors.Is(err, vault.ErrInvalidFrontmatter) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read search result %s: %w", m.Path, err)
		}
		if vault.IsInternalPage(page) {
			continue
		}
		body := m.Body
		// If no chunk body (e.g., BM25-only hit), read page from disk
		// and extract the best-matching chunk.
		if body == "" && m.Path != "" {
			if page.Body != "" {
				body = vault.FindBestChunk(page.Body, query)
			}
		}
		state.AddSource(VaultSource{
			Title:    m.Title,
			Path:     m.Path,
			Body:     body,
			Snippet:  m.Snippet,
			Score:    m.Score,
			Category: page.Category,
			Tags:     page.Tags,
		})
	}

	state.Data["bm25_count"] = len(bm25Results)
	state.Data["embed_count"] = len(embedResults)
	state.Data["merged_count"] = len(merged)
	state.Data["source_count"] = len(state.Sources)

	return nil
}

// 鈹€鈹€ Vault Read Step 鈹€鈹€

// VaultReadStep reads a specific vault page and adds it to sources.
type VaultReadStep struct {
	reader vault.Reader
	logger *zap.Logger
}

// NewVaultReadStep creates a step that reads a vault page by path.
func NewVaultReadStep(reader vault.Reader, logger *zap.Logger) *VaultReadStep {
	return &VaultReadStep{reader: reader, logger: logger}
}

func (s *VaultReadStep) Name() string { return "vault-read" }

func (s *VaultReadStep) Run(ctx context.Context, state *ChainState) error {
	vaultName := state.Vault
	if vaultName == "" {
		vaultName = "personal"
	}

	// Read sources that only have paths (from a previous search step).
	// Use index-based loop: range copies the struct, so src.Body = ... would be lost.
	for i := range state.Sources {
		if state.Sources[i].Body != "" {
			continue // already has content
		}
		if state.Sources[i].Path == "" {
			continue
		}
		page, err := s.reader.ReadPage(ctx, vaultName, state.Sources[i].Path)
		if err != nil {
			s.logger.Warn("vault read failed", zap.String("path", state.Sources[i].Path), zap.Error(err))
			continue
		}
		state.Sources[i].Body = page.Body
		state.Sources[i].Tags = page.Tags
		state.Sources[i].Category = page.Category
	}
	return nil
}

// 鈹€鈹€ Vault Index Step 鈹€鈹€

// VaultIndexStep reads and parses the vault index.md.
type VaultIndexStep struct {
	reader vault.Reader
	logger *zap.Logger
}

// NewVaultIndexStep creates a step that reads the vault index.
func NewVaultIndexStep(reader vault.Reader, logger *zap.Logger) *VaultIndexStep {
	return &VaultIndexStep{reader: reader, logger: logger}
}

func (s *VaultIndexStep) Name() string { return "vault-index" }

func (s *VaultIndexStep) Run(ctx context.Context, state *ChainState) error {
	vaultName := state.Vault
	if vaultName == "" {
		vaultName = "personal"
	}

	entries, err := s.reader.ReadIndex(ctx, vaultName)
	if err != nil {
		return fmt.Errorf("read index: %w", err)
	}

	state.Data["index_entries"] = entries
	state.Data["index_count"] = len(entries)
	return nil
}

// 鈹€鈹€ Page Preprocess Step 鈹€鈹€

// PagePreprocessStep cleans and normalizes retrieved page bodies.
// Removes YAML frontmatter, wiki links, and truncates long content.
type PagePreprocessStep struct {
	maxBodyLen int
}

// NewPagePreprocessStep creates a page cleaning step.
func NewPagePreprocessStep(maxBodyLen int) *PagePreprocessStep {
	if maxBodyLen <= 0 {
		maxBodyLen = 800
	}
	return &PagePreprocessStep{maxBodyLen: maxBodyLen}
}

func (s *PagePreprocessStep) Name() string { return "page-preprocess" }

func (s *PagePreprocessStep) Run(ctx context.Context, state *ChainState) error {
	for i := range state.Sources {
		body := state.Sources[i].Body

		// Truncate very long bodies.
		if len([]rune(body)) > s.maxBodyLen {
			body = string([]rune(body)[:s.maxBodyLen]) + "..."
		}

		// Clean wiki internal links: [[link]] 鈫?link.
		body = cleanWikiLinks(body)

		state.Sources[i].Body = body
	}
	return nil
}

// cleanWikiLinks converts [[page|alias]] and [[page]] to the alias or page name.
func cleanWikiLinks(text string) string {
	// [[鏄剧ず鏂囧瓧]] 鈫?鏄剧ず鏂囧瓧
	// [[椤甸潰鍚峕] 鈫?椤甸潰鍚?
	result := text
	for {
		start := strings.Index(result, "[[")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "]]")
		if end < 0 {
			break
		}
		inner := result[start+2 : start+end]
		// Check for alias syntax: [[page|alias]]
		if pipe := strings.Index(inner, "|"); pipe >= 0 {
			inner = inner[pipe+1:]
		}
		result = result[:start] + inner + result[start+end+2:]
	}
	return result
}

// 鈹€鈹€ Context Assembly Step 鈹€鈹€

// ContextAssemblyStep builds a system prompt from retrieved sources.
// This is the RAG "assemble" step: retrieved docs 鈫?structured prompt.
type ContextAssemblyStep struct {
	maxSources int
}

// NewContextAssemblyStep creates a context assembly step.
func NewContextAssemblyStep(maxSources int) *ContextAssemblyStep {
	if maxSources <= 0 {
		maxSources = 5
	}
	return &ContextAssemblyStep{maxSources: maxSources}
}

func (s *ContextAssemblyStep) Name() string { return "context-assembly" }

func (s *ContextAssemblyStep) Run(ctx context.Context, state *ChainState) error {
	if len(state.Sources) == 0 {
		state.Data["system_prompt"] = ""
		return nil
	}

	var sb strings.Builder
	sb.WriteString("浣犲彲浠ュ弬鑰冧互涓嬬煡璇嗗簱鍐呭鏉ュ洖绛旈棶棰?\n\n")

	count := 0
	var included []VaultSource
	for _, src := range state.Sources {
		if count >= s.maxSources {
			break
		}
		if src.Body == "" || vault.IsInternalPage(&vault.Page{Tags: src.Tags, Category: src.Category}) {
			continue
		}
		included = append(included, src)
		sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", src.Title, src.Body))
		count++
	}

	state.Data["system_prompt"] = sb.String()
	state.Data["context_source_titles"] = sourceTitles(included)
	return nil
}

func sourceTitles(sources []VaultSource) string {
	titles := make([]string, len(sources))
	for i, s := range sources {
		titles[i] = s.Title
	}
	return strings.Join(titles, ", ")
}

// 鈹€鈹€ Query Classification Step 鈹€鈹€

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
