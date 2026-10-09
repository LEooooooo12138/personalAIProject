package memory

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
)

// ── 什么是 TF-IDF？ ──
//
// TF-IDF (Term Frequency - Inverse Document Frequency) 是一种经典的文本相似度算法。
// 它做了两件事：
//
// 1. TF（词频）：一个词在"这篇文档"里出现了多少次？
//    - 例如：对话摘要中"游戏"出现了 5 次，TF 值就高
//
// 2. IDF（逆文档频率）：这个词在"所有文档"中出现的频率的反比
//    - 例如："的"几乎所有文档都有，IDF 值很低，没什么区分度
//    - "RAG"只在少数文档出现，IDF 值很高，是个"特征词"
//
// 最终把每篇文档变成一个向量（一长串数字），用余弦相似度比较两个向量：
// - 0 = 完全不同
// - 1 = 完全相同
// - 我们设的阈值是 0.45，超过就认为"同一件事"

// ── Sedimentation Engine ──

// Sedimenter handles the memory sedimentation pipeline:
// 1. Judge knowledge value
// 2. Generate structured summary via LLM
// 3. TF-IDF dedup against existing _memory/ entries
// 4. Write to personal-vault/_memory/
//
// 记忆沉淀引擎 = 把对话变成笔记的流水线
type Sedimenter struct {
	mu                sync.Mutex
	personalVaultPath string // personal-vault 根目录
	logger            *zap.Logger
	llmSummarizer     LLMSummarizer // 调用 LLM 生成摘要的接口
}

// LLMSummarizer is the interface for generating structured summaries.
// 生成摘要的接口：对外隐藏了底层是 Ollama 还是其他 LLM
type LLMSummarizer interface {
	Summarize(ctx context.Context, conversation string) (*StructuredSummary, error)
}

// StructuredSummary is the output of the LLM summarization step.
// 结构化摘要 = 主题 + 关键决策 + 待跟进
type StructuredSummary struct {
	Title      string  // 一句话概括
	Decisions  string  // 关键结论/发现
	FollowUps  string  // 待跟进事项
	Confidence float64 // 信心度 0-1
}

// SedimentConfig holds parameters for the sedimentation pipeline.
type SedimentConfig struct {
	MemoryDir      string  // _memory/ 的完整路径
	DedupThreshold float64 // 去重阈值，默认 0.45
	MinMessages    int     // 最少消息数才触发沉淀，默认 3
}

// DefaultSedimentConfig returns sensible defaults.
func DefaultSedimentConfig(personalVaultPath string) SedimentConfig {
	return SedimentConfig{
		MemoryDir:      filepath.Join(personalVaultPath, "_memory"),
		DedupThreshold: 0.45,
		MinMessages:    3,
	}
}

// NewSedimenter creates a memory sedimentation engine.
func NewSedimenter(personalVaultPath string, summarizer LLMSummarizer, logger *zap.Logger) *Sedimenter {
	return &Sedimenter{
		personalVaultPath: personalVaultPath,
		logger:            logger,
		llmSummarizer:     summarizer,
	}
}

// ── Pipeline Steps ──

// Conversation represents a completed chat that may contain valuable knowledge.
// 一段已结束的对话，包含所有消息和元数据
type Conversation struct {
	Messages  []ConvMessage
	ChannelID string
	UserID    string
	StartedAt time.Time
	EndedAt   time.Time
}

// ConvMessage is a single message in a conversation.
type ConvMessage struct {
	Role    string
	Content string
}

// ── Step 1: Knowledge Value Judgment ──

// JudgeDecide evaluates whether a conversation contains knowledge worth saving.
// 判断这段话"值不值得记"。
//
// 判断标准（从设计文档 §5.1）：
// - 包含明确的决策或结论 → 值得记
// - 包含新发现、新知识 → 值得记
// - 包含问题的解决方案 → 值得记
// - 纯闲聊/打招呼 → 不记
// - 重复已知信息 → 不记
// - 无结论的讨论 → 不记
func (s *Sedimenter) JudgeDecide(conv *Conversation) (bool, string) {
	if len(conv.Messages) == 0 {
		return false, "empty conversation"
	}

	// 合并所有用户消息内容
	var userContent strings.Builder
	for _, m := range conv.Messages {
		if m.Role == "user" {
			userContent.WriteString(m.Content)
			userContent.WriteString(" ")
		}
	}
	text := strings.TrimSpace(userContent.String())

	if text == "" {
		return false, "no user content"
	}

	// 检测闲聊信号（简短问候、无实质内容）
	greetings := []string{"你好", "hello", "hi", "在吗", "早", "晚安", "谢谢", "thanks", "ok", "好的"}
	textLower := strings.ToLower(text)
	textLen := len([]rune(text))
	if textLen < 10 {
		for _, g := range greetings {
			if strings.Contains(textLower, g) {
				return false, fmt.Sprintf("greeting only: %q", g)
			}
		}
	}

	// 检测知识信号（包含问题、技术术语、决策相关词汇）
	knowledgeSignals := []string{
		"?", "？", "怎么", "如何", "什么是", "为什么",
		"配置", "config", "设置", "安装", "install",
		"错误", "error", "问题", "bug",
		"模块", "module", "架构", "architecture",
		"设计方案", "design", "实现", "implement",
		"api", "接口", "数据库", "database",
		"总结", "summary", "记住", "remember",
		"知识点", "knowledge", "决策", "decision",
	}
	signalCount := 0
	for _, sig := range knowledgeSignals {
		if strings.Contains(textLower, sig) {
			signalCount++
		}
	}

	if signalCount >= 1 {
		return true, fmt.Sprintf("knowledge signals detected (%d)", signalCount)
	}

	return false, "no knowledge signals detected"
}

// ── Step 2: Generate Summary ──

// GenerateSummary creates a structured summary of the conversation via LLM.
// 调用 LLM 把对话变成结构化摘要。
//
// 流程：把对话拼接成一段文本 → 发给 LLM → 解析返回的 JSON → 得到摘要
func (s *Sedimenter) GenerateSummary(ctx context.Context, conv *Conversation) (*StructuredSummary, error) {
	if s.llmSummarizer == nil {
		return nil, fmt.Errorf("memory: no LLM summarizer configured")
	}

	// 拼接对话文本
	var convText strings.Builder
	for _, m := range conv.Messages {
		role := "用户"
		if m.Role == "assistant" {
			role = "AI"
		}
		convText.WriteString(fmt.Sprintf("%s: %s\n", role, m.Content))
	}

	summary, err := s.llmSummarizer.Summarize(ctx, convText.String())
	if err != nil {
		return nil, fmt.Errorf("memory: summarize: %w", err)
	}

	return summary, nil
}

// ── Step 3: TF-IDF Dedup ──

// dedupResult is the outcome of comparing a new summary against existing memories.
type dedupResult struct {
	Similarity float64 // 最高相似度
	MatchPath  string  // 最相似的已有文件路径
	IsNew      bool    // 是否为新知识
}

// dedupCheck compares the new summary against all existing _memory/ entries.
// 把新的摘要和已有的所有记忆做对比，找最相似的。
//
// 算法流程：
// 1. 读取所有 _memory/ 目录下的 .md 文件
// 2. 为每篇文档计算 TF-IDF 向量
// 3. 计算新摘要与每篇文档的余弦相似度
// 4. 如果最高相似度 > 阈值（0.45），认为"已经记过了"
// 5. 否则，创建新条目
func (s *Sedimenter) dedupCheck(cfg SedimentConfig, summary *StructuredSummary) (*dedupResult, error) {
	// 读取已有记忆文件
	existing, err := s.loadExistingMemories(cfg.MemoryDir)
	if err != nil {
		return nil, fmt.Errorf("dedup: load: %w", err)
	}

	if len(existing) == 0 {
		return &dedupResult{Similarity: 0, IsNew: true}, nil
	}

	// 构建语料库（现有文档 + 新摘要）
	corpus := make([]string, len(existing)+1)
	for i, doc := range existing {
		corpus[i] = doc.content
	}
	newDoc := summary.Title + " " + summary.Decisions + " " + summary.FollowUps
	corpus[len(existing)] = newDoc

	// 计算所有文档的 TF-IDF 向量
	vectors := computeTFIDF(corpus)

	// 新摘要的向量是最后一个
	newVec := vectors[len(vectors)-1]

	// 计算与每个已有文档的余弦相似度
	bestSim := 0.0
	bestIdx := -1
	for i := 0; i < len(existing); i++ {
		sim := cosineSimilarity(newVec, vectors[i])
		if sim > bestSim {
			bestSim = sim
			bestIdx = i
		}
	}

	if bestSim >= cfg.DedupThreshold && bestIdx >= 0 {
		return &dedupResult{
			Similarity: bestSim,
			MatchPath:  existing[bestIdx].path,
			IsNew:      false,
		}, nil
	}

	return &dedupResult{Similarity: bestSim, IsNew: true}, nil
}

// loadExistingMemories reads all memory files from the _memory/ directory.
type storedMemory struct{ path, content string }

func (s *Sedimenter) openMemoryDirectory(memoryDir string, create bool) (*os.Root, error) {
	if s.personalVaultPath == "" {
		return os.OpenRoot(memoryDir)
	}
	rel, err := filepath.Rel(s.personalVaultPath, memoryDir)
	if err != nil || !filepath.IsLocal(rel) || (rel != "_memory" && !strings.HasPrefix(rel, "_memory"+string(filepath.Separator))) {
		return nil, fmt.Errorf("memory directory must be inside personal-vault/_memory")
	}
	root, err := os.OpenRoot(s.personalVaultPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if create {
		if err := root.MkdirAll(rel, 0700); err != nil {
			return nil, err
		}
	}
	return root.OpenRoot(rel)
}

func (s *Sedimenter) loadExistingMemories(memoryDir string) ([]storedMemory, error) {
	dir, err := s.openMemoryDirectory(memoryDir, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var docs []storedMemory
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := dir.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		page, err := vault.ParsePage(data)
		if errors.Is(err, vault.ErrInvalidFrontmatter) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var lines []string
		for _, line := range strings.Split(page.Body, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				lines = append(lines, line)
			}
		}
		docs = append(docs, storedMemory{path: filepath.Join(memoryDir, entry.Name()), content: strings.Join(lines, " ")})
	}
	return docs, nil
}

// ── Step 4: Write to _memory/ ──

// WriteMemory creates a new memory markdown file or merges with an existing one.
// 把摘要写入 personal-vault/_memory/ 目录。
//
// 文件格式（从设计文档 §5.3）：
//
//	---
//	title: "对话摘要：关于 X 的讨论"
//	created: 2026-07-01T14:30:00+08:00
//	source: qclaw-session
//	tags: [topic1, topic2]
//	confidence: 0.8
//	---
//	### 主题
//	<一句话概括>
//
//	### 关键决策/发现
//	<核心结论>
//
//	### 待跟进
//	<未解决的问题>
func (s *Sedimenter) WriteMemory(cfg SedimentConfig, conv *Conversation, summary *StructuredSummary, dedup *dedupResult) (string, error) {
	if !dedup.IsNew {
		return dedup.MatchPath, nil
	}
	dir, err := s.openMemoryDirectory(cfg.MemoryDir, true)
	if err != nil {
		return "", err
	}
	defer dir.Close()

	// 生成文件名：日期-时间-主题
	slug := slugify(summary.Title)
	if len([]rune(slug)) > 50 {
		slug = string([]rune(slug)[:50])
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s_%s_%x.md", conv.EndedAt.Format("2006-01-02_150405"), slug, nonce)
	filePath := filepath.Join(cfg.MemoryDir, filename)

	// 生成 frontmatter
	source := conv.ChannelID + "-session"
	confidence := summary.Confidence
	if confidence == 0 {
		confidence = 0.7 // 默认信心度
	}

	content := fmt.Sprintf(`---
title: %s
created: %s
source: %s
tags: []
confidence: %.1f
---

### 主题
%s

### 关键决策/发现
%s

### 待跟进
%s
`, strconv.Quote("对话摘要："+summary.Title),
		conv.EndedAt.Format(time.RFC3339),
		source,
		confidence,
		summary.Title,
		summary.Decisions,
		summary.FollowUps,
	)

	f, err := dir.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(content)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write memory file: %w", err)
	}

	s.logger.Info("memory written",
		zap.String("path", filePath),
		zap.Bool("is_new", dedup.IsNew),
		zap.Float64("max_similarity", dedup.Similarity),
	)

	return filePath, nil
}

// ── Full Pipeline ──

// SedimentationResult summarizes what happened during the sedimentation run.
type SedimentationResult struct {
	Worthy     bool               // 是否有值得记录的知识
	Reason     string             // 判断理由
	Summary    *StructuredSummary // 生成的摘要（如果 worthy = true）
	FilePath   string             // 写入的文件路径
	IsNew      bool               // 是否为新知识（非去重合并）
	Similarity float64            // 与已有记忆的最高相似度
	Error      string             // 如果某步失败，这里记录错误
}

// Process runs the full memory sedimentation pipeline on a completed conversation.
// 运行完整的记忆沉淀流水线：判断 → 摘要 → 去重 → 写入
func (s *Sedimenter) Process(ctx context.Context, cfg SedimentConfig, conv *Conversation) *SedimentationResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := &SedimentationResult{}
	if conv == nil || len(conv.Messages) < cfg.MinMessages {
		result.Reason = "not enough messages"
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}

	// Step 1: Judge knowledge value.
	worthy, reason := s.JudgeDecide(conv)
	if !worthy {
		result.Worthy = false
		result.Reason = reason
		s.logger.Debug("sedimentation: skipped",
			zap.String("reason", reason),
		)
		return result
	}
	result.Worthy = true
	result.Reason = reason

	// Step 2: Generate structured summary via LLM.
	summary, err := s.GenerateSummary(ctx, conv)
	if err != nil {
		result.Error = fmt.Sprintf("summarization failed: %v", err)
		s.logger.Error("sedimentation: summary failed", zap.Error(err))
		return result
	}
	result.Summary = summary
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}

	// Step 3: TF-IDF dedup.
	dedup, err := s.dedupCheck(cfg, summary)
	if err != nil {
		result.Error = fmt.Sprintf("dedup failed: %v", err)
		s.logger.Error("sedimentation: dedup failed", zap.Error(err))
		return result
	}
	result.IsNew = dedup.IsNew
	result.Similarity = dedup.Similarity
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}

	// Step 4: Write to _memory/.
	filePath, err := s.WriteMemory(cfg, conv, summary, dedup)
	if err != nil {
		result.Error = fmt.Sprintf("write failed: %v", err)
		s.logger.Error("sedimentation: write failed", zap.Error(err))
		return result
	}
	result.FilePath = filePath

	s.logger.Info("sedimentation complete",
		zap.String("file", filePath),
		zap.Bool("is_new", dedup.IsNew),
	)
	return result
}

// ── TF-IDF Implementation ──
//
// 以下是 TF-IDF 的 Go 实现。不依赖任何外部 NLP 库，
// 全部使用 Go 标准库完成：分词 → 统计词频 → 计算向量 → 余弦相似度

// tfidfVector maps term → tfidf score for one document.
type idfVector map[string]float64

// computeTFIDF calculates TF-IDF vectors for a corpus of documents.
// 输入：一组文档（每篇是一个字符串）
// 输出：每篇文档的 TF-IDF 向量（词 → 权重）
func computeTFIDF(corpus []string) []idfVector {
	if len(corpus) == 0 {
		return nil
	}

	// 对每篇文档分词
	docTokens := make([][]string, len(corpus))
	allTerms := make(map[string]bool)

	for i, doc := range corpus {
		tokens := tokenize(doc)
		docTokens[i] = tokens
		for _, t := range tokens {
			allTerms[t] = true
		}
	}

	numDocs := float64(len(corpus))
	vectors := make([]idfVector, len(corpus))

	// 对每个词计算 IDF
	for term := range allTerms {
		// 计算包含这个词的文档数
		docFreq := 0
		for _, tokens := range docTokens {
			for _, t := range tokens {
				if t == term {
					docFreq++
					break
				}
			}
		}

		// IDF = log(总文档数 / 包含该词的文档数) + 1（平滑）
		idf := math.Log(numDocs/float64(docFreq)) + 1.0

		// 为每篇文档计算该词的 TF-IDF
		for i, tokens := range docTokens {
			tf := termFrequency(tokens, term)
			if tf > 0 {
				if vectors[i] == nil {
					vectors[i] = make(idfVector)
				}
				vectors[i][term] = tf * idf
			}
		}
	}

	return vectors
}

// tokenize splits text into lowercase tokens (simple whitespace + CJK char split).
// 分词：中文按字分，英文按空格和标点分
func tokenize(text string) []string {
	// 转为小写
	text = strings.ToLower(text)

	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if vault.IsCJK(r) {
			// 中文字符：先保存当前英文 token（如果有），再单独作为一个 token
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(r))
		} else if vault.IsAlphaNum(r) {
			current.WriteRune(r)
		} else {
			// 空格/标点：保存当前 token
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	// 过滤太短的 token 和停用词
	var filtered []string
	for _, t := range tokens {
		if len(t) < 2 && !vault.IsCJK([]rune(t)[0]) {
			continue
		}
		if vault.IsStopWord(t) {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// termFrequency returns TF = count of term / total tokens.
func termFrequency(tokens []string, term string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	count := 0
	for _, t := range tokens {
		if t == term {
			count++
		}
	}
	return float64(count) / float64(len(tokens))
}

// cosineSimilarity computes cosine similarity between two TF-IDF vectors.
// 余弦相似度 = (A · B) / (|A| × |B|)
// 两个向量越相似，值越接近 1；越不相关，值越接近 0。
func cosineSimilarity(a, b idfVector) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	// 点积 A · B
	dotProduct := 0.0
	for term, weightA := range a {
		if weightB, ok := b[term]; ok {
			dotProduct += weightA * weightB
		}
	}

	// 模长 |A|
	magA := 0.0
	for _, w := range a {
		magA += w * w
	}
	magA = math.Sqrt(magA)

	// 模长 |B|
	magB := 0.0
	for _, w := range b {
		magB += w * w
	}
	magB = math.Sqrt(magB)

	if magA == 0 || magB == 0 {
		return 0
	}

	return dotProduct / (magA * magB)
}

// ── Helpers ──

// isStopWord filters common words that carry little semantic meaning.

// slugify converts a title into a filesystem-safe slug.
func slugify(title string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(title) {
		if vault.IsAlphaNum(r) || vault.IsCJK(r) {
			result.WriteRune(r)
		} else if r == ' ' || r == '-' {
			result.WriteRune('-')
		}
	}
	slug := result.String()
	// Remove consecutive dashes.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "memory"
	}
	return slug
}

// 欧几里得向量大小助手
func magnitude(vec idfVector) float64 {
	sum := 0.0
	for _, v := range vec {
		sum += v * v
	}
	return math.Sqrt(sum)
}

// Debug helpers (used in tests)
func _sortTerms(vec idfVector) []string {
	terms := make([]string, 0, len(vec))
	for t := range vec {
		terms = append(terms, t)
	}
	sort.Strings(terms)
	return terms
}
