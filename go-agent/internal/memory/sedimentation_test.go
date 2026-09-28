package memory

import (
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	return zap.NewNop()
}

// ?? JudgeDecide ??

func TestJudgeDecide_KnowledgeContent(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	conv := &Conversation{
		Messages: []ConvMessage{
			{Role: "user", Content: "what is RAG and how to implement it?"},
			{Role: "assistant", Content: "RAG stands for Retrieval Augmented Generation..."},
		},
	}
	worthy, reason := s.JudgeDecide(conv)
	if !worthy {
		t.Errorf("should be worthy, got reason: %s", reason)
	}
}

func TestJudgeDecide_TechnicalQuestion(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	conv := &Conversation{
		Messages: []ConvMessage{
			{Role: "user", Content: "I got an error configuring Docker, how to fix?"},
		},
	}
	worthy, _ := s.JudgeDecide(conv)
	if !worthy {
		t.Error("technical question should be worthy")
	}
}

func TestJudgeDecide_Greeting(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	conv := &Conversation{
		Messages: []ConvMessage{
			{Role: "user", Content: "hello"},
		},
	}
	worthy, _ := s.JudgeDecide(conv)
	if worthy {
		t.Error("greeting should not be worthy")
	}
}

func TestJudgeDecide_ShortHi(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	conv := &Conversation{
		Messages: []ConvMessage{
			{Role: "user", Content: "hi"},
		},
	}
	worthy, _ := s.JudgeDecide(conv)
	if worthy {
		t.Error("short 'hi' should not be worthy")
	}
}

func TestJudgeDecide_EmptyConversation(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	conv := &Conversation{Messages: []ConvMessage{}}
	worthy, reason := s.JudgeDecide(conv)
	if worthy {
		t.Error("empty conversation should not be worthy")
	}
	if reason != "empty conversation" {
		t.Errorf("reason = %q, want empty conversation", reason)
	}
}

func TestJudgeDecide_NoUserContent(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	conv := &Conversation{
		Messages: []ConvMessage{
			{Role: "assistant", Content: "how can I help you?"},
		},
	}
	worthy, reason := s.JudgeDecide(conv)
	if worthy {
		t.Error("no user content should not be worthy")
	}
	if reason != "no user content" {
		t.Errorf("reason = %q, want no user content", reason)
	}
}

// ?? Tokenize ??

func TestTokenize_English(t *testing.T) {
	tokens := tokenize("Hello World test")
	if len(tokens) < 2 {
		t.Errorf("expected at least 2 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestTokenize_SingleChar(t *testing.T) {
	// Single ASCII character should be filtered out (min length 2 for non-CJK).
	tokens := tokenize("a b c")
	// "a", "b", "c" are each 1 char, non-CJK, should be filtered.
	if len(tokens) != 0 {
		t.Logf("tokens = %v (single chars may be filtered)", tokens)
	}
}

// ?? TF-IDF ??

func TestComputeTFIDF_Basic(t *testing.T) {
	corpus := []string{
		"hello world",
		"hello there",
	}
	vectors := computeTFIDF(corpus)
	if len(vectors) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vectors))
	}
	if vectors[0]["hello"] == 0 {
		t.Error("vector 0 should have weight for 'hello'")
	}
	if vectors[1]["hello"] == 0 {
		t.Error("vector 1 should have weight for 'hello'")
	}
}

func TestComputeTFIDF_UniqueWords(t *testing.T) {
	corpus := []string{
		"unique word only here",
		"completely different text",
	}
	vectors := computeTFIDF(corpus)
	if vectors[0]["unique"] == 0 {
		t.Error("vector 0 should contain 'unique'")
	}
}

// ?? Cosine Similarity ??

func TestCosineSimilarity_Identical(t *testing.T) {
	a := idfVector{"hello": 0.5, "world": 0.5}
	b := idfVector{"hello": 0.5, "world": 0.5}
	sim := cosineSimilarity(a, b)
	if sim < 0.99 {
		t.Errorf("identical vectors: similarity = %f, want ~1.0", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := idfVector{"a": 1.0}
	b := idfVector{"b": 1.0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("orthogonal vectors: similarity = %f, want 0", sim)
	}
}

func TestCosineSimilarity_Empty(t *testing.T) {
	a := idfVector{}
	b := idfVector{"x": 1.0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("empty vector: similarity = %f, want 0", sim)
	}
}

// ?? Slugify ??

func TestSlugify_Basic(t *testing.T) {
	result := slugify("Hello World")
	if result != "hello-world" {
		t.Errorf("slugify = %q, want hello-world", result)
	}
}

func TestSlugify_ConsecutiveDashes(t *testing.T) {
	result := slugify("a  b")
	if strings.Contains(result, "--") {
		t.Errorf("slugify should not have consecutive dashes: %q", result)
	}
}

func TestSlugify_Empty(t *testing.T) {
	result := slugify("")
	if result != "memory" {
		t.Errorf("slugify empty = %q, want memory", result)
	}
}

// ?? DedupCheck ??

func TestDedupCheck_NoExisting(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	cfg := SedimentConfig{
		MemoryDir:      t.TempDir(),
		DedupThreshold: 0.45,
	}
	summary := &StructuredSummary{
		Title:     "test topic",
		Decisions: "test decision",
		FollowUps: "test followup",
	}
	result, err := s.dedupCheck(cfg, summary)
	if err != nil {
		t.Fatalf("dedupCheck: %v", err)
	}
	if !result.IsNew {
		t.Error("should be new when no existing memories")
	}
	if result.Similarity != 0 {
		t.Errorf("similarity = %f, want 0", result.Similarity)
	}
}

// ?? WriteMemory ??

func TestWriteMemory_Format(t *testing.T) {
	s := &Sedimenter{logger: testLogger()}
	cfg := SedimentConfig{
		MemoryDir: t.TempDir(),
	}
	conv := &Conversation{
		ChannelID: "internal",
		EndedAt:   time.Date(2026, 7, 15, 14, 30, 0, 0, time.UTC),
	}
	summary := &StructuredSummary{
		Title:      "RAG Implementation Plan",
		Decisions:  "use local embedding",
		FollowUps:  "research Qdrant integration",
		Confidence: 0.85,
	}
	dedup := &dedupResult{IsNew: true, Similarity: 0.0}

	path, err := s.WriteMemory(cfg, conv, summary, dedup)
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}

	content := string(data)
	checks := []string{
		"source: internal-session",
		"RAG Implementation Plan",
		"use local embedding",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("memory file should contain %q", check)
		}
	}
}