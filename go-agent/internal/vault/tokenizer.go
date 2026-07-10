package vault

import (
	"fmt"
	"strings"
	"unicode"
)

// ── Query Tokenizer ──
//
// Splits a Chinese-majority search query into meaningful tokens
// using bigram segmentation + numeric entity preservation + stop-word filtering.
//
// Strategy:
//   1. Extract numeric/date entities ("2020年", "8080端口") as single tokens
//   2. CJK text → sliding bigram (two-char windows), which naturally captures
//      Chinese word boundaries without requiring a dictionary
//   3. Filter stop-words (的, 是, 在, the, is, etc.)
//   4. Drop single-char tokens (they match everywhere, producing noise)

// Chinese stop-words — function words that appear in every document.
var cjkStopWords = map[string]bool{
	"的": true, "了": true, "是": true, "在": true, "我": true,
	"有": true, "和": true, "就": true, "不": true, "人": true,
	"都": true, "一": true, "个": true, "上": true, "也": true,
	"很": true, "到": true, "说": true, "要": true, "去": true,
	"你": true, "会": true, "着": true, "看": true, "好": true,
	"这": true, "那": true, "些": true, "他": true, "她": true,
	"它": true, "们": true, "对": true, "与": true, "从": true,
	"但": true, "或": true, "被": true, "把": true, "让": true,
	"向": true, "给": true, "为": true, "以": true, "能": true,
	"可": true, "将": true, "还": true, "又": true, "再": true,
	"没": true, "得": true, "地": true, "之": true, "其": true,
	"么": true, "吗": true, "呢": true, "吧": true, "啊": true,
	"哦": true, "嗯": true, "哈": true, "嘛": true, "呀": true,
}

// English stop-words
var enStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "can": true, "could": true,
	"should": true, "may": true, "might": true, "shall": true, "must": true,
	"i": true, "me": true, "my": true, "we": true, "our": true,
	"you": true, "your": true, "he": true, "him": true, "his": true,
	"she": true, "her": true, "it": true, "its": true, "they": true,
	"them": true, "their": true, "this": true, "that": true, "these": true,
	"those": true, "to": true, "of": true, "in": true, "for": true,
	"on": true, "with": true, "at": true, "by": true, "from": true,
	"and": true, "or": true, "not": true, "but": true, "if": true,
	"so": true, "as": true, "than": true, "too": true, "very": true,
	"what": true, "which": true, "who": true, "whom": true, "when": true,
	"where": true, "how": true, "all": true, "each": true, "every": true,
}

func isStopWord(tok string) bool {
	return cjkStopWords[tok] || enStopWords[strings.ToLower(tok)]
}

// tokenizeQuery splits a query into meaningful search tokens.
//
// Examples:
//
//	"2020年姚远乐在干什么" → ["2020年", "姚远", "远乐", "干什么"]
//	"姚远乐的技术栈是什么" → ["姚远", "远乐", "技术", "术栈"]
//	"what is my tech stack" → ["tech", "stack"]
func tokenizeQuery(query string) []string {
	// Step 1: Extract numeric/date entities and replace with placeholders.
	entities, cleaned := extractNumericEntities(strings.ToLower(query))

	// Step 2: Split into segments: CJK runs vs ASCII runs.
	segments := splitIntoSegments(cleaned)

	// Step 3: CJK segments → bigrams; ASCII segments → whitespace tokens.
	var tokens []string
	for _, seg := range segments {
		if seg.isCJK {
			tokens = append(tokens, bigramTokens(seg.text)...)
		} else {
			tokens = append(tokens, asciiTokens(seg.text)...)
		}
	}

	// Step 4: Restore numeric entities.
	for i, tok := range tokens {
		if restored, ok := entities[tok]; ok {
			tokens[i] = restored
		}
	}

	// Step 5: Filter stop-words, short tokens, and deduplicate.
	seen := make(map[string]bool)
	var result []string
	for _, tok := range tokens {
		if len([]rune(tok)) < 2 {
			continue
		}
		if isStopWord(tok) {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		result = append(result, tok)
	}

	return result
}

// ── Numeric entity extraction ──

// extractNumericEntities finds patterns like "2020年", "8080端口", "3.5分"
// and replaces them with placeholders to preserve them as single tokens.
func extractNumericEntities(text string) (map[string]string, string) {
	entities := make(map[string]string)

	// Pattern: digits + optional CJK suffix (年,月,日,时,分,秒,岁,个,次,倍,层,楼,号,端口,元,块,万字,小时,分钟)
	// Simple rune-based scan for digit sequences followed by common measure words.
	runes := []rune(text)
	var cleaned []rune
	i := 0
	for i < len(runes) {
		if isDigit(runes[i]) {
			start := i
			for i < len(runes) && isDigit(runes[i]) {
				i++
			}
			// Check for common CJK suffix (1-2 chars).
			suffixLen := 0
			if i < len(runes) && isCJKMeasure(runes[i]) {
				suffixLen = 1
				if i+1 < len(runes) && isCJKMeasure(runes[i+1]) {
					suffixLen = 2
				}
			}
			entity := string(runes[start : i+suffixLen])
			placeholder := fmt.Sprintf("__ENT%d__", len(entities))
			entities[placeholder] = entity
			cleaned = append(cleaned, []rune(placeholder)...)
			i += suffixLen
		} else {
			cleaned = append(cleaned, runes[i])
			i++
		}
	}
	return entities, string(cleaned)
}

func isDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= '０' && r <= '９')
}

func isCJKMeasure(r rune) bool {
	switch r {
	case '年', '月', '日', '时', '分', '秒', '岁', '个', '次',
		'倍', '层', '楼', '号', '元', '块', '万', '亿', '千', '百':
		return true
	}
	return false
}

// ── Segmentation ──

type segment struct {
	text  string
	isCJK bool
}

func splitIntoSegments(text string) []segment {
	var segs []segment
	runes := []rune(text)
	if len(runes) == 0 {
		return segs
	}

	start := 0
	currentIsCJK := isCJKRune(runes[0])
	for i := 1; i < len(runes); i++ {
		r := runes[i]
		cjk := isCJKRune(r)
		// Transition boundary: skip punctuation that sits between CJK and ASCII.
		if cjk != currentIsCJK && !unicode.IsPunct(r) && r != ' ' && r != '_' {
			segs = append(segs, segment{text: string(runes[start:i]), isCJK: currentIsCJK})
			start = i
			currentIsCJK = cjk
		}
	}
	segs = append(segs, segment{text: string(runes[start:]), isCJK: currentIsCJK})
	return segs
}

func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0xF900 && r <= 0xFAFF) // CJK Compatibility
}

// ── Tokenizers ──

// bigramTokens splits a CJK string into sliding 2-character windows.
//
//	"姚远乐" → ["姚远", "远乐"]
//	"技术栈" → ["技术", "术栈"]
func bigramTokens(text string) []string {
	runes := []rune(text)
	if len(runes) < 2 {
		return []string{text}
	}
	var tokens []string
	for i := 0; i < len(runes)-1; i++ {
		tokens = append(tokens, string(runes[i:i+2]))
	}
	return tokens
}

// asciiTokens splits ASCII text by whitespace and punctuation.
func asciiTokens(text string) []string {
	// Replace punctuation with spaces, then split.
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) && r != '_' && r != '-' {
			return ' '
		}
		return r
	}, text)
	parts := strings.Fields(cleaned)
	var result []string
	for _, p := range parts {
		if len(p) >= 2 {
			result = append(result, p)
		}
	}
	return result
}


// tokenize splits document text into tokens for indexing.
// Uses the same bigram approach as tokenizeQuery but keeps all tokens
// (including stop-words) for accurate BM25 statistics.
func tokenize(text string) []string {
	entities, cleaned := extractNumericEntities(text)
	segments := splitIntoSegments(cleaned)

	var tokens []string
	for _, seg := range segments {
		if seg.isCJK {
			for _, t := range bigramTokens(seg.text) {
				tokens = append(tokens, t)
			}
		} else {
			for _, t := range asciiTokens(seg.text) {
				tokens = append(tokens, t)
			}
		}
	}
	for i, tok := range tokens {
		if restored, ok := entities[tok]; ok {
			tokens[i] = restored
		}
	}
	return tokens
}