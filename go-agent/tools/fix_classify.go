package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\internal\chain\go_steps.go`
    data, _ := os.ReadFile(path)
    text := string(data)

    // Find and replace the QueryClassifyStep.Run function
    funcStart := "func (s *QueryClassifyStep) Run(ctx context.Context, state *ChainState) error {"
    funcEnd := "\n// Decide implements DecisionStep"

    startIdx := strings.Index(text, funcStart)
    endIdx := strings.Index(text, funcEnd)
    if startIdx < 0 || endIdx < 0 {
        fmt.Println("ERROR: boundaries not found")
        os.Exit(1)
    }

    // New implementation with expanded patterns and smarter defaults
    newFunc := `func (s *QueryClassifyStep) Run(ctx context.Context, state *ChainState) error {
	query := strings.ToLower(state.Query)

	// ── Tier 1: Wiki operations ──
	wikiOps := []string{"ingest", "wiki-ingest", "wiki-capture", "wiki-lint",
		"wiki-dedup", "cross-link", "synthesize", "wiki-update", "wiki-query",
		"\u66f4\u65b0\u77e5\u8bc6\u5e93", "\u5bfc\u5165", "\u6444\u53d6", "\u84b8\u998f", "\u53bb\u91cd", "\u4ea4\u53c9\u94fe\u63a5", "\u5408\u6210"}
	for _, op := range wikiOps {
		if strings.Contains(query, op) {
			state.Data["query_type"] = "wiki_op"
			return nil
		}
	}

	// ── Tier 2: Simple / chitchat / meta ──
	// These clearly do NOT need knowledge-base retrieval.
	simplePatterns := []string{
		// Greetings & farewells
		"\u4f60\u597d", "hello", "hi ", "hey", "\u65e9\u4e0a\u597d", "\u665a\u4e0a\u597d", "\u5348\u5b89",
		"\u518d\u89c1", "bye", "goodbye", "\u665a\u5b89",
		"\u8c22\u8c22", "thanks", "thank you",
		// Self-identity / meta questions
		"\u4f60\u662f\u8c01", "\u4f60\u53eb\u4ec0\u4e48", "\u4f60\u662f\u4ec0\u4e48",
		"\u4f60\u80fd\u505a\u4ec0\u4e48", "\u4f60\u7684\u80fd\u529b", "\u4f60\u6709\u4ec0\u4e48\u529f\u80fd",
		"\u4f60\u662fai", "\u4f60\u662f\u4eba\u5de5\u667a\u80fd",
		"\u4ecb\u7ecd\u4e00\u4e0b\u4f60\u81ea\u5df1", "\u4f60\u662f\u8c01\u5f00\u53d1\u7684",
		"\u4f60\u7528\u7684\u662f\u4ec0\u4e48\u6a21\u578b", "\u4f60\u7684\u77e5\u8bc6\u622a\u6b62\u65e5\u671f",
		// Casual chitchat
		"\u4f60\u597d\u5417", "how are you", "\u8c22\u8c22\u4f60",
		"\u8bb2\u4e2a\u7b11\u8bdd", "\u804a\u804a\u5929", "\u4f60\u559c\u6b22\u4ec0\u4e48",
		// Direct tasks that don't need retrieval
		"\u7ffb\u8bd1", "translate", "\u603b\u7ed3\u4e00\u4e0b", "summarize",
		"\u5199\u4e00\u7bc7", "\u5199\u4e2a", "\u751f\u6210\u4e00\u6bb5",
		"\u8ba1\u7b97", "calculate",
		"\u89e3\u91ca\u4e00\u4e0b", "explain",
		// Single-word / very short queries unlikely to need retrieval
	}
	for _, pat := range simplePatterns {
		if strings.Contains(query, pat) {
			state.Data["query_type"] = "simple"
			return nil
		}
	}

	// ── Tier 3: Heuristic check ──
	// Very short queries (<= 2 CJK characters or <= 3 words) are likely chitchat.
	runes := []rune(query)
	if len(runes) <= 3 {
		state.Data["query_type"] = "simple"
		return nil
	}

	// ── Tier 4: Knowledge signals ──
	// Queries with explicit knowledge-seeking language should use RAG.
	knowledgeSignals := []string{
		"\u4ec0\u4e48\u662f", "\u8c01\u662f", "\u4ecb\u7ecd", "\u5982\u4f55",
		"\u600e\u4e48", "\u4e3a\u4ec0\u4e48", "\u539f\u7406",
		"\u8be6\u7ec6", "\u5177\u4f53", "\u8bf4\u660e",
		"\u6587\u6863", "\u8d44\u6599", "\u5185\u5bb9",
		"\u67e5\u8be2", "\u641c\u7d22", "\u627e\u4e00\u4e0b",
		"what is", "who is", "how to", "how does", "explain",
		"\u77e5\u9053\u5417", "\u4e86\u89e3\u5417",
	}
	for _, sig := range knowledgeSignals {
		if strings.Contains(query, sig) {
			state.Data["query_type"] = "rag"
			return nil
		}
	}

	// ── Default: no strong signal either way → try without RAG first ──
	// Most casual conversation doesn't need knowledge retrieval.
	state.Data["query_type"] = "simple"
	return nil
}
`

    text = text[:startIdx] + newFunc + text[endIdx:]

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("OK: QueryClassifyStep rewritten with expanded classification")
}
