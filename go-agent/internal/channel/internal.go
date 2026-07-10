package channel

import (
	"context"
	"sync"
	"time"

	"fmt"
)

// InternalChannel represents the full-access channel used by Codex CLI and curl.
// It processes HTTP requests directly, no persistent connection needed.
type InternalChannel struct {
	ctx    context.Context
	cancel context.CancelFunc
	msgCh  chan Message
	mu     sync.RWMutex
}

// NewInternalChannel creates the internal channel.
func NewInternalChannel() *InternalChannel {
	return &InternalChannel{
		msgCh: make(chan Message, 100),
	}
}

func (ch *InternalChannel) ID() string  { return "internal" }
func (ch *InternalChannel) Type() Type  { return Internal }

func (ch *InternalChannel) Start(ctx context.Context) error {
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	return nil
}

func (ch *InternalChannel) Stop() error {
	if ch.cancel != nil {
		ch.cancel()
	}
	return nil
}

// Receive returns the inbound message channel (not used for internal; HTTP handlers call Route directly).
func (ch *InternalChannel) Receive() <-chan Message {
	return ch.msgCh
}

// Send delivers a response back. For internal channel this is a no-op
// since responses are returned synchronously via HTTP.
func (ch *InternalChannel) Send(msg Message, resp Response) error {
	return nil
}

// NewMessage creates a normalized message with timestamp and UUID.
func NewMessage(channelID, userID, content string, metadata map[string]string) Message {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	return Message{
		ID:        fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond()%10000),
		ChannelID: channelID,
		UserID:    userID,
		Content:   content,
		Metadata:  metadata,
		Timestamp: time.Now(),
	}
}
