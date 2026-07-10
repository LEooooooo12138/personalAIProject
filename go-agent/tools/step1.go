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

    // Find the insertion point: right before "// ── RRF Fusion for chain types ──"
    anchor := "// ── RRF Fusion for chain types ──"
    idx := strings.Index(text, anchor)
    if idx < 0 {
        fmt.Println("ERROR: anchor not found")
        os.Exit(1)
    }

    newStep := `// ── LLM Decide-and-Answer Step ──
//
// This step replaces QueryClassifyStep. Instead of using keyword matching,
// it asks the LLM itself to decide whether the query needs knowledge-base
// retrieval. The LLM either answers directly or outputs [SEARCH: query].
//
// DecisionStep behaviour:
//   - "direct" → Abort=true, FinalAnswer is already set
//   - "search" → Next="", continue to VaultSearchStep → ContextAssembly → Answer

// LLMDecideAndAnswerStep is the entry point of the chat chain.
// It sends the user query to the LLM with a system prompt that explains
// the SEARCH tool. The LLM decides in a single call.
type LLMDecideAndAnswerStep struct {
    infer  inference.Client
    model  string
    logger *zap.Logger
}

// NewLLMDecideAndAnswerStep creates the decide-and-answer step.
func NewLLMDecideAndAnswerStep(infer inference.Client, model string, logger *zap.Logger) *LLMDecideAndAnswerStep {
    if model == "" {
        model = "gemma4:12b"
    }
    return &LLMDecideAndAnswerStep{infer: infer, model: model, logger: logger}
}

func (s *LLMDecideAndAnswerStep) Name() string { return "llm-decide" }

func (s *LLMDecideAndAnswerStep) Run(ctx context.Context, state *ChainState) error {
    query := strings.TrimSpace(state.Query)

    // Fast-path: very short queries (≤2 runes) that aren't knowledge-seeking.
    if len([]rune(query)) <= 2 {
        state.FinalAnswer = "你好，有什么可以帮你的？"
        state.Data["llm_decision"] = "direct"
        return nil
    }

    // Build system prompt that explains the SEARCH tool.
    systemPrompt := `你是一个智能助手，可以访问用户的个人知识库（Obsidian Wiki）。

规则：
1. 闲聊、问候、自我介绍（"你是谁"、"你能做什么"）、常识、通用知识 → 直接回答，不要搜索。
2. 涉及用户个人信息的问题（具体人物、项目经历、技术文档、个人历史、工作经历、教育背景、游戏收藏、个人技能等）→ 只回复一行：[SEARCH: 搜索关键词]
3. 如果搜索后仍然没有相关信息 → 诚实告知用户。
4. 回答时直接给出内容，不要提及"知识库"、"搜索工具"、"[SEARCH:]"标记等内部机制。
5. 使用中文回答。`

    // Build messages: system + conversation history + current query.
    messages := []map[string]string{
        {"role": "system", "content": systemPrompt},
    }

    // Inject conversation history if available.
    if history, ok := state.Data["conversation_history"]; ok {
        if histMsgs, ok2 := history.([]map[string]string); ok2 {
            messages = append(messages, histMsgs...)
        }
    }

    messages = append(messages, map[string]string{
        "role": "user", "content": query,
    })

    reqBody, err := json.Marshal(map[string]interface{}{
        "model":       s.model,
        "messages":    messages,
        "temperature": 0.7,
        "max_tokens":  1024,
        "thinking":    map[string]string{"type": "disabled"},
    })
    if err != nil {
        return fmt.Errorf("marshal decide request: %w", err)
    }

    llmCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
    defer cancel()

    resp, err := s.infer.Chat(llmCtx, reqBody)
    if err != nil {
        // On error, fall back to direct answer so the user isn't blocked.
        s.logger.Warn("llm decide call failed, falling back to direct", zap.Error(err))
        state.FinalAnswer = "抱歉，我暂时无法处理这个请求。"
        state.Data["llm_decision"] = "direct"
        state.Data["_llm_error"] = err.Error()
        return nil
    }

    var chatResp struct {
        Choices []struct {
            Message struct {
                Content string ` + "`" + `json:"content"` + "`" + `
            } ` + "`" + `json:"message"` + "`" + `
        } ` + "`" + `json:"choices"` + "`" + `
    }
    if err := json.Unmarshal(resp, &chatResp); err != nil {
        state.FinalAnswer = "抱歉，我暂时无法处理这个请求。"
        state.Data["llm_decision"] = "direct"
        return nil
    }
    if len(chatResp.Choices) == 0 {
        state.FinalAnswer = "抱歉，我暂时无法回答这个问题。"
        state.Data["llm_decision"] = "direct"
        return nil
    }

    content := strings.TrimSpace(chatResp.Choices[0].Message.Content)

    // Check for SEARCH directive.
    searchMarker := "[SEARCH:"
    if strings.HasPrefix(content, searchMarker) {
        // Extract search query: [SEARCH: 关键词] → "关键词"
        end := strings.Index(content, "]")
        searchQuery := content
        if end > 0 {
            searchQuery = strings.TrimSpace(content[len(searchMarker):end])
        } else {
            searchQuery = strings.TrimPrefix(content, searchMarker)
            searchQuery = strings.TrimSpace(searchQuery)
        }
        if searchQuery == "" {
            searchQuery = query
        }
        state.Data["llm_decision"] = "search"
        state.Data["search_query"] = searchQuery
        s.logger.Debug("llm decided to search",
            zap.String("query", query),
            zap.String("search_query", searchQuery),
        )
        return nil
    }

    // LLM answered directly.
    state.FinalAnswer = content
    state.Data["llm_decision"] = "direct"
    s.logger.Debug("llm answered directly",
        zap.String("query", query),
        zap.Int("answer_len", len(content)),
    )
    return nil
}

// Decide implements DecisionStep.
func (s *LLMDecideAndAnswerStep) Decide(ctx context.Context, state *ChainState) (*StepResult, error) {
    decision, _ := state.GetString("llm_decision")
    switch decision {
    case "direct":
        return &StepResult{Abort: true, Reason: "llm answered directly"}, nil
    case "search":
        return &StepResult{Next: "", Reason: "llm requested knowledge base search"}, nil
    default:
        // Shouldn't happen, but be safe.
        return &StepResult{Abort: true, Reason: "unknown decision, aborting"}, nil
    }
}

// Ensure LLMDecideAndAnswerStep implements DecisionStep.
var _ DecisionStep = (*LLMDecideAndAnswerStep)(nil)

`

    text = text[:idx] + newStep + text[idx:]

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("Step 1 done: LLMDecideAndAnswerStep added")
}
