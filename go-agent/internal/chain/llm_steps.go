package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/inference"
)

// LLMAnswerStep calls the LLM with retrieved context to answer a question.
type LLMAnswerStep struct {
	infer inference.Client; model string; temperature float64; logger *zap.Logger
}

func NewLLMAnswerStep(infer inference.Client, model string, temperature float64, logger *zap.Logger) *LLMAnswerStep {
	if model == "" { model = "gemma4:12b" }
	if temperature <= 0 { temperature = 0.7 }
	return &LLMAnswerStep{infer: infer, model: model, temperature: temperature, logger: logger}
}

func (s *LLMAnswerStep) Name() string { return "llm-answer" }

func (s *LLMAnswerStep) Run(ctx context.Context, state *ChainState) error {
	messages := buildChatMessages(state)
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": s.model, "messages": messages,
		"temperature": s.temperature, "max_tokens": 4096,
		"thinking": map[string]string{"type": "disabled"},
	})
	if err != nil { return fmt.Errorf("marshal request: %w", err) }
	if state.StreamMode && state.Stream != nil { return s.runStreaming(ctx, reqBody, state) }
	return s.runBuffered(ctx, reqBody, state)
}

func (s *LLMAnswerStep) runBuffered(_ context.Context, reqBody json.RawMessage, state *ChainState) error {
	llmCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	resp, err := s.infer.Chat(llmCtx, reqBody)
	if err != nil { state.Data["_llm_error"] = err.Error(); return fmt.Errorf("llm chat: %w", err) }
	var cr struct { Choices []struct { Message struct { Content string `json:"content"`; Reasoning string `json:"reasoning"` } `json:"message"` } `json:"choices"` }
	if err := json.Unmarshal(resp, &cr); err != nil { return fmt.Errorf("unmarshal: %w", err) }
	if len(cr.Choices) == 0 { return fmt.Errorf("empty response") }
	c := strings.TrimSpace(cr.Choices[0].Message.Content)
	r := strings.TrimSpace(cr.Choices[0].Message.Reasoning)
	if c == "" { c = r }
	state.FinalAnswer = c
	state.Data["_llm_raw_len"] = len(string(resp))
	return nil
}

func (s *LLMAnswerStep) runStreaming(ctx context.Context, reqBody json.RawMessage, state *ChainState) error {
	type streamer interface { ChatStream(ctx context.Context, body json.RawMessage) (<-chan inference.StreamChunk, error) }
	inf, ok := s.infer.(streamer)
	if !ok {
		if err := s.runBuffered(ctx, reqBody, state); err != nil { return err }
		s.emit(state, StreamToken{Text: state.FinalAnswer, Done: true})
		return nil
	}
	ch, err := inf.ChatStream(ctx, reqBody)
	if err != nil { return fmt.Errorf("llm stream: %w", err) }
	if state.HasSources() {
		srcs := make([]StreamSource, len(state.Sources))
		for i, src := range state.Sources { srcs[i] = StreamSource{Title: src.Title, Score: src.Score} }
		s.emit(state, StreamToken{Sources: srcs})
	}
	var acc strings.Builder
	for chunk := range ch {
		if chunk.Error != nil { s.emit(state, StreamToken{Error: chunk.Error.Error(), Done: true}); state.FinalAnswer = acc.String(); return nil }
		if chunk.Done { acc.Reset(); acc.WriteString(chunk.Text); state.FinalAnswer = acc.String(); s.emit(state, StreamToken{Done: true}); return nil }
		if chunk.Text != "" { acc.WriteString(chunk.Text); s.emit(state, StreamToken{Text: chunk.Text}) }
	}
	state.FinalAnswer = acc.String()
	return nil
}

func (s *LLMAnswerStep) emit(state *ChainState, token StreamToken) {
	select { case state.Stream <- token: default: s.logger.Warn("stream token dropped") }
}

func buildChatMessages(state *ChainState) []map[string]string {
	var msgs []map[string]string
	if sp, _ := state.GetString("system_prompt"); sp != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": sp})
	}
	if h, ok := state.Data["conversation_history"]; ok {
		if hm, ok2 := h.([]map[string]string); ok2 { msgs = append(msgs, hm...) }
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": state.Query})
	return msgs
}

type LLMSummarizeStep struct { infer inference.Client; model string; logger *zap.Logger }

func NewLLMSummarizeStep(infer inference.Client, model string, logger *zap.Logger) *LLMSummarizeStep {
	if model == "" { model = "gemma4:12b" }
	return &LLMSummarizeStep{infer: infer, model: model, logger: logger}
}

func (s *LLMSummarizeStep) Name() string { return "llm-summarize" }

func (s *LLMSummarizeStep) Run(ctx context.Context, state *ChainState) error {
	c := state.Query
	if v, ok := state.GetString("summarize_content"); ok && v != "" { c = v }
	prompt := fmt.Sprintf("Please summarize the following content as structured JSON in Chinese:\n\n{\n  \"title\": \"one-sentence summary\",\n  \"decisions\": \"key conclusions (or 'none')\",\n  \"follow_ups\": \"follow-up items (or 'none')\",\n  \"confidence\": 0.0-1.0\n}\n\nContent:\n%s", c)
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": s.model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.3, "max_tokens": 1024,
		"thinking": map[string]string{"type": "disabled"},
	})
	if err != nil { return fmt.Errorf("marshal: %w", err) }
	resp, err := s.infer.Chat(ctx, reqBody)
	if err != nil { return fmt.Errorf("llm summarize: %w", err) }
	var cr struct { Choices []struct { Message struct { Content string `json:"content"` } `json:"message"` } `json:"choices"` }
	if err := json.Unmarshal(resp, &cr); err != nil { return fmt.Errorf("unmarshal: %w", err) }
	if len(cr.Choices) == 0 { return fmt.Errorf("empty response") }
	raw := cr.Choices[0].Message.Content
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var sum StructuredSummary
	if err := json.Unmarshal([]byte(raw), &sum); err != nil {
		sum = StructuredSummary{Title: truncate(raw, 100), Decisions: "see original", FollowUps: "none", Confidence: 0.5}
	}
	state.Summary = &sum
	state.Data["summary_title"] = sum.Title
	state.Data["summary_decisions"] = sum.Decisions
	state.Data["summary_followups"] = sum.FollowUps
	return nil
}

type LLMIngestStep struct { infer inference.Client; model string; logger *zap.Logger }

func NewLLMIngestStep(infer inference.Client, model string, logger *zap.Logger) *LLMIngestStep {
	if model == "" { model = "gemma4:12b" }
	return &LLMIngestStep{infer: infer, model: model, logger: logger}
}

func (s *LLMIngestStep) Name() string { return "llm-ingest" }

func (s *LLMIngestStep) Run(ctx context.Context, state *ChainState) error {
	rc, _ := state.GetString("raw_content")
	if rc == "" {
		for _, src := range state.Sources {
			if src.Body != "" { rc += fmt.Sprintf("### %s\n%s\n\n", src.Title, src.Body) }
		}
	}
	if rc == "" { rc = state.Query }
	prompt := fmt.Sprintf("You are a knowledge management expert. Analyze the following content and extract key concepts, entities, and relationships. Output in Markdown format.\n\nRequirements:\n- Start with YAML frontmatter (title, tags, category, created)\n- Use ## headings\n- Bold key concepts\n- Define terms on first use\n- Preserve important code examples and configs\n\nContent:\n%s", rc)
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": s.model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.4, "max_tokens": 4096,
		"thinking": map[string]string{"type": "disabled"},
	})
	if err != nil { return fmt.Errorf("marshal: %w", err) }
	resp, err := s.infer.Chat(ctx, reqBody)
	if err != nil { return fmt.Errorf("llm ingest: %w", err) }
	var cr struct { Choices []struct { Message struct { Content string `json:"content"` } `json:"message"` } `json:"choices"` }
	if err := json.Unmarshal(resp, &cr); err != nil { return fmt.Errorf("unmarshal: %w", err) }
	if len(cr.Choices) == 0 { return fmt.Errorf("empty response") }
	state.Data["wiki_output"] = cr.Choices[0].Message.Content
	return nil
}

type LLMSynthesizeStep struct { infer inference.Client; model string; logger *zap.Logger }

func NewLLMSynthesizeStep(infer inference.Client, model string, logger *zap.Logger) *LLMSynthesizeStep {
	if model == "" { model = "gemma4:12b" }
	return &LLMSynthesizeStep{infer: infer, model: model, logger: logger}
}

func (s *LLMSynthesizeStep) Name() string { return "llm-synthesize" }

func (s *LLMSynthesizeStep) Run(ctx context.Context, state *ChainState) error {
	if len(state.Sources) < 2 { state.FinalAnswer = "synthesis needs at least 2 sources"; return nil }
	var st strings.Builder
	for _, src := range state.Sources { st.WriteString(fmt.Sprintf("### %s\n%s\n\n", src.Title, src.Body)) }
	prompt := fmt.Sprintf("You are a knowledge analyst. Below are related but distinct knowledge points. Synthesize them:\n\n1. **Common theme**: what core concept do they share?\n2. **Differences**: what important distinctions or perspectives?\n3. **Cross-insight**: what can only be discovered by reading all together?\n4. **To explore**: what directions deserve deeper investigation?\n\nContent:\n%s", st.String())
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": s.model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.6, "max_tokens": 4096,
		"thinking": map[string]string{"type": "disabled"},
	})
	if err != nil { return fmt.Errorf("marshal: %w", err) }
	resp, err := s.infer.Chat(ctx, reqBody)
	if err != nil { return fmt.Errorf("llm synthesize: %w", err) }
	var cr struct { Choices []struct { Message struct { Content string `json:"content"` } `json:"message"` } `json:"choices"` }
	if err := json.Unmarshal(resp, &cr); err != nil { return fmt.Errorf("unmarshal: %w", err) }
	if len(cr.Choices) == 0 { return fmt.Errorf("empty response") }
	state.FinalAnswer = cr.Choices[0].Message.Content
	return nil
}

type LLMSimpleAnswerStep struct { infer inference.Client; model string; logger *zap.Logger }

func NewLLMSimpleAnswerStep(infer inference.Client, model string, logger *zap.Logger) *LLMSimpleAnswerStep {
	if model == "" { model = "gemma4:12b" }
	return &LLMSimpleAnswerStep{infer: infer, model: model, logger: logger}
}

func (s *LLMSimpleAnswerStep) Name() string { return "llm-simple-answer" }

func (s *LLMSimpleAnswerStep) Run(ctx context.Context, state *ChainState) error {
	messages := buildChatMessagesWithoutSystemPrompt(state)
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": s.model, "messages": messages,
		"temperature": 0.7, "max_tokens": 1024,
		"thinking": map[string]string{"type": "disabled"},
	})
	if err != nil { return fmt.Errorf("marshal: %w", err) }
	resp, err := s.infer.Chat(ctx, reqBody)
	if err != nil { return fmt.Errorf("llm simple: %w", err) }
	var cr struct { Choices []struct { Message struct { Content string `json:"content"` } `json:"message"` } `json:"choices"` }
	if err := json.Unmarshal(resp, &cr); err != nil { return fmt.Errorf("unmarshal: %w", err) }
	if len(cr.Choices) == 0 { return fmt.Errorf("empty response") }
	state.FinalAnswer = cr.Choices[0].Message.Content
	return nil
}

type LLMCrossLinkStep struct { infer inference.Client; model string; logger *zap.Logger }

func NewLLMCrossLinkStep(infer inference.Client, model string, logger *zap.Logger) *LLMCrossLinkStep {
	if model == "" { model = "gemma4:12b" }
	return &LLMCrossLinkStep{infer: infer, model: model, logger: logger}
}

func (s *LLMCrossLinkStep) Name() string { return "llm-cross-link" }

func (s *LLMCrossLinkStep) Run(ctx context.Context, state *ChainState) error {
	if len(state.Sources) < 2 { state.FinalAnswer = "cross-link needs at least 2 pages"; return nil }
	var pt strings.Builder
	for i, src := range state.Sources { pt.WriteString(fmt.Sprintf("Page %d: %s\n%s\n\n", i+1, src.Title, truncate(src.Body, 500))) }
	prompt := fmt.Sprintf("Analyze the following wiki pages and find cross-reference relationships that should but don't yet exist.\n\nFor each pair, explain:\n1. What is the relationship? (conceptual dependency, complement, contrast, practice/theory)\n2. Which section should add a [[link]]?\n3. Connection strength (1-5)\n\nPages:\n%s", pt.String())
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": s.model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0.3, "max_tokens": 4096,
		"thinking": map[string]string{"type": "disabled"},
	})
	if err != nil { return fmt.Errorf("marshal: %w", err) }
	resp, err := s.infer.Chat(ctx, reqBody)
	if err != nil { return fmt.Errorf("llm cross-link: %w", err) }
	var cr struct { Choices []struct { Message struct { Content string `json:"content"` } `json:"message"` } `json:"choices"` }
	if err := json.Unmarshal(resp, &cr); err != nil { return fmt.Errorf("unmarshal: %w", err) }
	if len(cr.Choices) == 0 { return fmt.Errorf("empty response") }
	state.FinalAnswer = cr.Choices[0].Message.Content
	return nil
}

func buildChatMessagesWithoutSystemPrompt(state *ChainState) []map[string]string {
	var msgs []map[string]string
	if h, ok := state.Data["conversation_history"]; ok {
		if hm, ok2 := h.([]map[string]string); ok2 { msgs = append(msgs, hm...) }
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": state.Query})
	return msgs
}
