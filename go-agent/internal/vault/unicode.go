package vault

// ── Shared Unicode / Tokenization helpers ──
//
// These functions are used across multiple packages (vault, memory, chain, gateway).
// Centralizing them here eliminates duplicate implementations.

// IsCJK reports whether r is a CJK (Chinese/Japanese/Korean) character.
func IsCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x20000 && r <= 0x2A6DF) // CJK Extension B
}

// IsAlphaNum reports whether r is an alphanumeric character, underscore, or hyphen.
func IsAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}

// IsStopWord reports whether token is a stop word (Chinese or English).
func IsStopWord(token string) bool {
	return cjkStopWords[token] || enStopWords[token]
}

// StopWordMaps returns the combined stop-word map for external consumers.
func StopWordMaps() (cjk map[string]bool, en map[string]bool) {
	return cjkStopWords, enStopWords
}
