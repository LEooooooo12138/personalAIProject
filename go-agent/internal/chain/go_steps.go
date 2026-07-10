package chain

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/vault"
)

// ── Go-Native Steps ──
//
// These steps perform deterministic operations on the filesystem or in-memory.
// They do NOT call LLMs. They are fast, cheap, and never hallucinate.
//
// Each step reads from ChainState, does work, and writes results back.
// Steps are designed to be composable: a VaultSearchStep can feed into
// a VaultReadStep which feeds into an LLMAnswerStep.

// ── Vault Search Step ──

// VaultSearchStep performs keyword + embedding search on the vault.
type VaultSearchStep struct {
	reader     vault.Reader
	embedStore EmbeddingSearcher
	maxResults int
	logger     *zap.Logger
}

// EmbeddingSearcher provides dense (semantic) search over embedded chunks.
type EmbeddingSearcher interface {
	Search(ctx context.Context, query string, k int) ([]EmbeddingHit, error)
}

// EmbeddingHit is a single dense search result.
type EmbeddingHit struct {
	PagePath     string
	Title        string
	Score        float64
	ChunkContent string
	SectionTitle string
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
	var embedResults []EmbeddingHit
	if s.embedStore != nil {
		results, err := s.embedStore.Search(ctx, query, s.maxResults*2)
		if err != nil {
			s.logger.Warn("embedding search error", zap.Error(err))
		} else {
			embedResults = results
		}
	}

	// RRF fusion.
	merged := rrfFuseSearchResults(bm25Results, embedResults)

	// Add sources to state.
	for i, m := range merged {
		if i >= s.maxResults {
			break
		}
		body := m.body
		// If no chunk body (e.g., BM25-only hit), read page from disk
		// and extract the best-matching chunk.
		if body == "" && m.path != "" {
			page, err := s.reader.ReadPage(ctx, vaultName, m.path)
			if err != nil {
				s.logger.Warn("vault read page for chunk fallback failed",
					zap.String("path", m.path), zap.Error(err))
			} else if page.Body != "" {
				body = findBestChunkByTokens(page.Body, query)
			}
		}
		state.AddSource(VaultSource{
			Title:    m.title,
			Path:     m.path,
			Body:     body,
			Snippet:  m.snippet,
			Score:    m.score,
			Category: m.category,
		})
	}

	state.Data["bm25_count"] = len(bm25Results)
	state.Data["embed_count"] = len(embedResults)
	state.Data["merged_count"] = len(merged)
	state.Data["source_count"] = len(state.Sources)

	return nil
}

// ── Vault Read Step ──

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

// ── Vault Index Step ──

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

// ── Page Preprocess Step ──

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

		// Clean wiki internal links: [[link]] → link.
		body = cleanWikiLinks(body)

		state.Sources[i].Body = body
	}
	return nil
}

// cleanWikiLinks converts [[page|alias]] and [[page]] to the alias or page name.
func cleanWikiLinks(text string) string {
	// [[显示文字]] → 显示文字
	// [[页面名]] → 页面名
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

// ── Context Assembly Step ──

// ContextAssemblyStep builds a system prompt from retrieved sources.
// This is the RAG "assemble" step: retrieved docs → structured prompt.
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
	sb.WriteString("你可以参考以下知识库内容来回答问题:\n\n")

	count := 0
	for _, src := range state.Sources {
		if count >= s.maxSources {
			break
		}
		if src.Body == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", src.Title, src.Body))
		count++
	}

	state.Data["system_prompt"] = sb.String()
	state.Data["context_source_titles"] = sourceTitles(state.Sources[:min(count, len(state.Sources))])
	return nil
}

func sourceTitles(sources []VaultSource) string {
	titles := make([]string, len(sources))
	for i, s := range sources {
		titles[i] = s.Title
	}
	return strings.Join(titles, ", ")
}

// ── Query Classification Step ──

// ── RRF Fusion for chain types ──

type mergedHit struct {
	path     string
	title    string
	body     string
	snippet  string
	score    float64
	category string
}

const rrfK = 60


// findBestChunkByTokens splits raw body text using vault chunking and returns
// the chunk with the best bigram overlap against the query.
// Falls back to the full body if chunking produces nothing.
func findBestChunkByTokens(bodyText, query string) string {
	if len([]rune(bodyText)) <= 800 {
		return bodyText
	}
	// Create a minimal Page for the vault chunker.
	page := &vault.Page{Body: bodyText}
	chunks := vault.ChunkPage(page)
	if len(chunks) == 0 {
		return bodyText
	}
	queryRunes := []rune(strings.ToLower(query))
	bestScore := 0
	bestContent := bodyText[:min(800, len([]rune(bodyText)))]
	for _, c := range chunks {
		contentRunes := []rune(strings.ToLower(c.Content))
		score := 0
		for i := 0; i < len(queryRunes)-1; i++ {
			window := string(queryRunes[i : i+2])
			if strings.Contains(string(contentRunes), window) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestContent = c.Content
		}
	}
	return bestContent
}

func rrfFuseSearchResults(bm25 []vault.SearchResult, embed []EmbeddingHit) []mergedHit {
	scores := make(map[string]float64)
	titles := make(map[string]string)
	bodies := make(map[string]string)
	snippets := make(map[string]string)

	for i, r := range bm25 {
		scores[r.Path] += 1.0 / float64(rrfK+i+1)
		titles[r.Path] = r.Title
		snippets[r.Path] = r.Snippet
	}
	for i, r := range embed {
		scores[r.PagePath] += 1.0 / float64(rrfK+i+1)
		if titles[r.PagePath] == "" {
			titles[r.PagePath] = r.Title
		}
		if r.ChunkContent != "" {
			bodies[r.PagePath] = r.ChunkContent
			snippets[r.PagePath] = r.ChunkContent
		}
	}

	var results []mergedHit
	for path, score := range scores {
		results = append(results, mergedHit{
			path:    path,
			title:   titles[path],
			body:    bodies[path],
			snippet: snippets[path],
			score:   score,
		})
	}

	// Sort descending.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	return results
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

