package vault

import "strings"

// ── Retrieval Service ──
//
// Provides unified retrieval combining BM25 keyword search and dense embedding search.
// The RRF (Reciprocal Rank Fusion) algorithm merges ranked lists from both backends.
// This eliminates duplicate implementations in gateway/websocket.go and chain/go_steps.go.

// RetrievalService performs hybrid search over vault content.
type RetrievalService struct {
	reader     Reader
	embedStore *EmbeddingStore
	rrfK       int
}

// NewRetrievalService creates a hybrid retrieval service.
func NewRetrievalService(reader Reader, embedStore *EmbeddingStore, rrfK int) *RetrievalService {
	if rrfK <= 0 {
		rrfK = 60
	}
	return &RetrievalService{reader: reader, embedStore: embedStore, rrfK: rrfK}
}

// MergedResult is a fused search result with content.
type MergedResult struct {
	Path         string
	Title        string
	Body         string
	Snippet      string
	Score        float64
	ChunkContent string
	SectionTitle string
	Category     string
}

// RRFMerge fuses BM25 and embedding results using Reciprocal Rank Fusion.
// Both input lists are assumed to be sorted by descending relevance.
func (s *RetrievalService) RRFMerge(bm25 []SearchResult, embed []EmbeddingResult) []MergedResult {
	scores := make(map[string]float64)
	titles := make(map[string]string)
	bodies := make(map[string]string)
	snippets := make(map[string]string)
	sections := make(map[string]string)
	seenDense := make(map[string]bool)

	for i, r := range bm25 {
		scores[r.Path] += 1.0 / float64(s.rrfK+i+1)
		titles[r.Path] = r.Title
		snippets[r.Path] = r.Snippet
	}
	for i, r := range embed {
		if r.PagePath == "" || seenDense[r.PagePath] {
			continue
		}
		seenDense[r.PagePath] = true
		scores[r.PagePath] += 1.0 / float64(s.rrfK+i+1)
		if titles[r.PagePath] == "" {
			titles[r.PagePath] = r.Title
		}
		if r.ChunkContent != "" {
			bodies[r.PagePath] = r.ChunkContent
			snippets[r.PagePath] = r.ChunkContent
			sections[r.PagePath] = r.SectionTitle
		}
	}

	var results []MergedResult
	for path, score := range scores {
		results = append(results, MergedResult{
			Path:         path,
			Title:        titles[path],
			Body:         bodies[path],
			Snippet:      snippets[path],
			Score:        score,
			ChunkContent: bodies[path],
			SectionTitle: sections[path],
		})
	}

	// Sort descending by score.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	return results
}

// FindBestChunk selects the chunk from bodyText with the best bigram overlap against query.
// Falls back to the full body if chunking produces nothing or the body is short enough.
func FindBestChunk(bodyText, query string) string {
	bodyLen := len([]rune(bodyText))
	if bodyLen <= 800 {
		return bodyText
	}
	page := &Page{Body: bodyText}
	chunks := ChunkPage(page)
	if len(chunks) == 0 {
		if bodyLen > 800 {
			return string([]rune(bodyText)[:800]) + "..."
		}
		return bodyText
	}
	queryLower := strings.ToLower(query)
	queryRunes := []rune(queryLower)
	bestScore := 0
	bestContent := string([]rune(bodyText)[:800]) + "..."

	for _, c := range chunks {
		contentLower := strings.ToLower(c.Content)
		contentRunes := []rune(contentLower)
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
