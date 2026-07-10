package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
)

type Agent struct {
	cfg         *Config
	logger      *zap.Logger
	sessionMgr  *SessionManager
	sedimenter  *memory.Sedimenter
	filterChain *filter.Chain
	router      *Router
	chMgr       *channel.Manager
	infer       inference.Client
}

type AgentDeps struct {
	Config      *Config
	Logger      *zap.Logger
	SessionMgr  *SessionManager
	Sedimenter  *memory.Sedimenter
	FilterChain *filter.Chain
	Router      *Router
	ChannelMgr  *channel.Manager
	Infer       inference.Client
}

func NewAgent(deps AgentDeps) *Agent {
	return &Agent{
		cfg:         deps.Config,
		logger:      deps.Logger,
		sessionMgr:  deps.SessionMgr,
		sedimenter:  deps.Sedimenter,
		filterChain: deps.FilterChain,
		router:      deps.Router,
		chMgr:       deps.ChannelMgr,
		infer:       deps.Infer,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("agent main loop starting (phase2: full event loop)")
	a.sessionMgr.Start(ctx)
	go a.consumeSessionEnds(ctx)

	// Blocks until ctx is cancelled
	a.chMgr.Run(ctx, a.handleMessage)

	a.logger.Info("agent main loop stopping")
	a.sessionMgr.Stop()
	return ctx.Err()
}

func (a *Agent) handleMessage(msg channel.Message) {
	a.logger.Info("handling message",
		zap.String("channel", msg.ChannelID),
		zap.String("user", msg.UserID),
		zap.String("content", truncate(msg.Content, 100)),
	)

	session, err := a.sessionMgr.GetOrCreate(msg.ChannelID, msg.UserID)
	if err != nil {
		a.logger.Error("session create failed", zap.Error(err))
		return
	}

	if ok := a.sessionMgr.AddMessage(session, Message{
		Role:      "user",
		Content:   msg.Content,
		Timestamp: msg.Timestamp,
	}); !ok {
		a.logger.Warn("session round limit reached", zap.String("session", session.ID))
		a.sessionMgr.EndSession(session)
		return
	}

	metadata := msg.Metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}
	decision := a.router.Decide(nil, "", metadata)

	reqBody := buildChatRequest(decision.TargetModel, msg.Content)
	inferCtx, cancel := context.WithTimeout(context.Background(), a.cfg.Inference.Timeout)
	defer cancel()

	rawResp, err := a.infer.Chat(inferCtx, json.RawMessage(reqBody))
	if err != nil {
		a.logger.Error("llm error", zap.Error(err))
		return
	}

	responseText, err := extractResponseContent(rawResp)
	if err != nil {
		a.logger.Error("parse response failed", zap.Error(err))
		return
	}

	ch, err := a.chMgr.Get(msg.ChannelID)
	if err != nil {
		a.logger.Error("channel lookup failed", zap.Error(err))
		return
	}

	if ch.Type() == channel.External {
		filtered, _ := a.filterChain.Apply(responseText, metadata)
		responseText = filtered
	}

	if err := ch.Send(msg, channel.Response{Content: responseText}); err != nil {
		a.logger.Error("send failed", zap.Error(err))
		return
	}

	if ok := a.sessionMgr.AddMessage(session, Message{
		Role:      "assistant",
		Content:   responseText,
		Timestamp: time.Now(),
	}); !ok {
		a.sessionMgr.EndSession(session)
	}
}

func buildChatRequest(model, content string) []byte {
	req := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": content},
		},
	}
	data, _ := json.Marshal(req)
	return data
}

func extractResponseContent(raw json.RawMessage) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return resp.Choices[0].Message.Content, nil
}

func (a *Agent) consumeSessionEnds(ctx context.Context) {
	a.logger.Info("memory sedimentation consumer started")
	defer a.logger.Info("memory sedimentation consumer stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case session := <-a.sessionMgr.EndChan():
			conv := sessionToConversation(session)
			cfg := memory.DefaultSedimentConfig(a.cfg.Vaults.Personal)
			result := a.sedimenter.Process(ctx, cfg, conv)
			if result.Error != "" {
				a.logger.Warn("sedimentation had errors", zap.String("error", result.Error))
			}
			a.sessionMgr.CompleteSession(session)
			a.logger.Info("session processing complete",
				zap.String("session_id", session.ID),
				zap.Bool("memory_written", result.Worthy && result.FilePath != ""),
			)
		}
	}
}

func sessionToConversation(s *Session) *memory.Conversation {
	clone := CloneSession(s)
	messages := make([]memory.ConvMessage, len(clone.Messages))
	for i, m := range clone.Messages {
		messages[i] = memory.ConvMessage{Role: m.Role, Content: m.Content}
	}
	return &memory.Conversation{
		Messages:  messages,
		ChannelID: clone.ChannelID,
		UserID:    clone.UserID,
		StartedAt: clone.StartedAt,
		EndedAt:   clone.LastActiveAt,
	}
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
