package core

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
)

type Agent struct {
	cfg           *Config
	logger        *zap.Logger
	sessionMgr    *SessionManager
	sedimenter    *memory.Sedimenter
	filterChain   *filter.Chain
	modelRouter   *ModelRouter
	chMgr         *channel.Manager
	infer         inference.Client
	chainExecutor *chain.ChainExecutor
	chainRouter   *chain.ChainRouter
}

type AgentDeps struct {
	Config        *Config
	Logger        *zap.Logger
	SessionMgr    *SessionManager
	Sedimenter    *memory.Sedimenter
	FilterChain   *filter.Chain
	ModelRouter   *ModelRouter
	ChannelMgr    *channel.Manager
	Infer         inference.Client
	ChainExecutor *chain.ChainExecutor
	ChainRouter   *chain.ChainRouter
}

func NewAgent(deps AgentDeps) *Agent {
	return &Agent{
		cfg:           deps.Config,
		logger:        deps.Logger,
		sessionMgr:    deps.SessionMgr,
		sedimenter:    deps.Sedimenter,
		filterChain:   deps.FilterChain,
		modelRouter:   deps.ModelRouter,
		chMgr:         deps.ChannelMgr,
		infer:         deps.Infer,
		chainExecutor: deps.ChainExecutor,
		chainRouter:   deps.ChainRouter,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.logger.Info("agent main loop starting (phase2: full event loop)")
	a.sessionMgr.Start(ctx)
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		a.consumeSessionEnds(ctx)
	}()

	// Blocks until ctx is cancelled
	a.chMgr.Run(ctx, func(msg channel.Message) { a.handleMessageContext(ctx, msg) })
	cancel()
	<-consumerDone

	a.logger.Info("agent main loop stopping")
	a.sessionMgr.Stop()
	return ctx.Err()
}

func (a *Agent) handleMessage(msg channel.Message) {
	a.handleMessageContext(context.Background(), msg)
}

func (a *Agent) handleMessageContext(ctx context.Context, msg channel.Message) {
	ch, err := a.chMgr.Get(msg.ChannelID)
	if err != nil {
		a.logger.Error("channel lookup failed", zap.Error(err))
		return
	}
	a.logger.Info("handling message",
		zap.String("channel", msg.ChannelID),
		zap.String("user", msg.UserID),
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
		session, err = a.sessionMgr.GetOrCreate(msg.ChannelID, msg.UserID)
		if err != nil || !a.sessionMgr.AddMessage(session, Message{Role: "user", Content: msg.Content, Timestamp: msg.Timestamp}) {
			a.sendFailure(ch, msg)
			return
		}
	}

	metadata := msg.Metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}
	var responseText string
	if a.chainExecutor != nil && a.chainRouter != nil {
		vaultName := "personal"
		if ch.Type() == channel.External {
			vaultName = "agent"
		}
		state := chain.NewChainState(msg.Content, vaultName, metadata)
		clone := CloneSession(session)
		history := make([]map[string]string, 0, len(clone.Messages))
		for _, m := range clone.Messages[:len(clone.Messages)-1] {
			history = append(history, map[string]string{"role": m.Role, "content": m.Content})
		}
		state.Data["conversation_history"] = history
		ctx2, cancel2 := context.WithTimeout(ctx, a.cfg.Inference.Timeout)
		result, err := a.chainExecutor.Run(ctx2, "chat", state)
		cancel2()
		if err == nil && result != nil {
			err = result.Error
		}
		if err != nil || result == nil {
			a.logger.Error("chain error", zap.Error(err))
			a.sendFailure(ch, msg)
			return
		}
		responseText = result.FinalAnswer
		if responseText == "" {
			a.logger.Warn("chain produced empty response")
			a.sendFailure(ch, msg)
			return
		}
	} else {
		a.logger.Error("chain system not initialized, cannot handle message")
		a.sendFailure(ch, msg)
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

func (a *Agent) sendFailure(ch channel.Channel, msg channel.Message) {
	if err := ch.Send(msg, channel.Response{Content: "暂时无法生成回复，请稍后重试。", Metadata: map[string]string{"error": "generation_failed"}}); err != nil {
		a.logger.Warn("error response failed", zap.Error(err))
	}
}

func (a *Agent) consumeSessionEnds(ctx context.Context) {
	a.logger.Info("memory sedimentation consumer started")
	defer a.logger.Info("memory sedimentation consumer stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case session := <-a.sessionMgr.EndChan():
			if a.sedimenter == nil {
				a.sessionMgr.CompleteSession(session)
				continue
			}
			conv := sessionToConversation(session)
			cfg := memory.DefaultSedimentConfig(a.cfg.Vaults.Personal)
			if a.cfg.Memory.DedupThreshold > 0 {
				cfg.DedupThreshold = a.cfg.Memory.DedupThreshold
			}
			if a.cfg.Memory.MinMessages > 0 {
				cfg.MinMessages = a.cfg.Memory.MinMessages
			}
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
