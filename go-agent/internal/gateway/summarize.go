package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
)

// ── LLM Summarizer Implementation ──
//
// 这个文件实现了 memory.LLMSummarizer 接口。
// 它的作用：把一段对话发送给本地 gemma4:12b 模型，让它生成结构化摘要。
//
// 具体流程：
// 1. 构建一个 prompt（告诉 LLM "请总结这段对话"）
// 2. 调用 inference.Client.Chat() 发送给 Ollama
// 3. 解析 LLM 返回的 JSON 得到标题、关键决策、待跟进事项

// LLMSummarizer wraps the inference client to provide conversation summarization.
type LLMSummarizer struct {
	client inference.Client
	logger *zap.Logger
}

// NewLLMSummarizer creates a summarizer backed by the inference client.
func NewLLMSummarizer(client inference.Client, logger *zap.Logger) *LLMSummarizer {
	return &LLMSummarizer{client: client, logger: logger}
}

// systemPromptTemplate is the prompt that tells the LLM how to summarize.
const systemPromptTemplate = `你是一个对话摘要助手。请将以下对话总结为结构化 JSON。

要求：
1. title：一句话概括对话主题（不超过30字）
2. decisions：提取关键决策、发现或结论（如果没有就写"无"）
3. follow_ups：提取待跟进的事项或未解决的问题（如果没有就写"无"）

请严格按照 JSON 格式返回，不要添加任何其他内容：
{"title":"...", "decisions":"...", "follow_ups":"..."}`

// StructuredResponse is the JSON format we expect from the LLM.
type StructuredResponse struct {
	Title     string `json:"title"`
	Decisions string `json:"decisions"`
	FollowUps string `json:"follow_ups"`
}

// Summarize generates a structured summary of the conversation.
// 把一段对话文本发给 LLM，返回结构化摘要。
//
// 技术细节：
// - 使用 OpenAI-compatible API 格式
// - 设置 temperature=0.3（低温度 = 更确定性的输出，适合摘要任务）
// - 如果 LLM 返回的不是合法 JSON，尝试从文本中提取
func (s *LLMSummarizer) Summarize(ctx context.Context, conversation string) (*memory.StructuredSummary, error) {
	// 构建请求体（OpenAI-compatible 格式）
	reqBody := map[string]interface{}{
		"model": "gemma4:12b",
		"messages": []map[string]string{
			{"role": "system", "content": systemPromptTemplate},
			{"role": "user", "content": fmt.Sprintf("请总结以下对话：\n\n%s", conversation)},
		},
		"temperature": 0.3,
		"max_tokens":  512,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("summarize: marshal request: %w", err)
	}

	resp, err := s.client.Chat(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("summarize: call llm: %w", err)
	}

	// 解析 LLM 返回的 OpenAI-compatible 格式
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(resp, &chatResp); err != nil {
		return nil, fmt.Errorf("summarize: parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("summarize: empty response choices")
	}

	content := chatResp.Choices[0].Message.Content
	s.logger.Debug("llm summarizer response", zap.String("content", content))

	// 从 LLM 回复中提取 JSON
	sr, err := extractJSON(content)
	if err != nil {
		// 如果无法解析 JSON，返回一个降级摘要
		s.logger.Warn("summarize: json parse failed, using fallback", zap.Error(err))
		return &memory.StructuredSummary{
			Title:      truncate(conversation, 30),
			Decisions:  "（摘要生成失败，请手动整理）",
			FollowUps:  "",
			Confidence: 0.3,
		}, nil
	}

	return &memory.StructuredSummary{
		Title:      sr.Title,
		Decisions:  sr.Decisions,
		FollowUps:  sr.FollowUps,
		Confidence: 0.8,
	}, nil
}

// extractJSON tries to find and parse JSON from LLM output.
// LLM 有时会在 JSON 前后加一些解释文字，需要从中间提取。
func extractJSON(text string) (*StructuredResponse, error) {
	text = strings.TrimSpace(text)

	// 找到第一个 { 和最后一个 }
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON found in: %s", truncate(text, 100))
	}

	jsonStr := text[start : end+1]
	var sr StructuredResponse
	if err := json.Unmarshal([]byte(jsonStr), &sr); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &sr, nil
}

func truncate(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "..."
}
