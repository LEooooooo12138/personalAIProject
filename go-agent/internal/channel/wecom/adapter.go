package wecom

import (
	"context"

	"github.com/yuanleyao/ai-agent/internal/channel"
	"go.uber.org/zap"
)

type Adapter struct {
	id       string
	cfg      Config
	callback *CallbackHandler
	client   *APIClient
	auth     *TokenManager
	contact  *ContactFilter
	msgCh    chan channel.Message
	logger   *zap.Logger
}

func NewAdapter(cfg Config, logger *zap.Logger) (*Adapter, error) {
	crypto, err := NewCrypto(cfg.Token, cfg.EncodingAESKey, cfg.CorpID, logger)
	if err != nil {
		return nil, err
	}

	auth := NewTokenManager(cfg.CorpID, cfg.CorpSecret, logger)
	client := NewAPIClient(auth, cfg.AgentID, logger)
	contact := NewContactFilter(cfg.AllowedUsers, cfg.AutoApprove, logger)
	callback := NewCallbackHandler(crypto, contact, logger)

	return &Adapter{
		id:       "wecom",
		cfg:      cfg,
		callback: callback,
		client:   client,
		auth:     auth,
		contact:  contact,
		msgCh:    make(chan channel.Message, 100),
		logger:   logger,
	}, nil
}

func (a *Adapter) ID() string        { return a.id }
func (a *Adapter) Type() channel.Type { return channel.External }

func (a *Adapter) Start(ctx context.Context) error {
	go a.auth.StartAutoRefresh(ctx)
	go a.callback.Start(ctx, a.cfg.ListenAddr, a.msgCh)
	a.logger.Info("wecom adapter started", zap.String("listen", a.cfg.ListenAddr))
	return nil
}

func (a *Adapter) Stop() error {
	return a.callback.Stop()
}

func (a *Adapter) Receive() <-chan channel.Message {
	return a.msgCh
}

func (a *Adapter) Send(msg channel.Message, resp channel.Response) error {
	return a.client.SendText(msg.UserID, resp.Content)
}
