package channel

import (
	"context"
	"time"
)

// Types

// Type distinguishes internal (full access) from external (restricted) channels.
type Type string

const (
	Internal Type = "internal"
	External Type = "external"
)

// Message is a normalized inbound message from any channel.
type Message struct {
	ID        string
	ChannelID string
	UserID    string
	Content   string
	Metadata  map[string]string
	Timestamp time.Time
}

// Response is a normalized outbound response.
type Response struct {
	Content  string
	Metadata map[string]string
}

// Channel is the abstraction over all communication channels.
// Every channel (internal CLI, WeCom, WebChat) implements this interface.
// This is the only channel abstraction in the system (I-9 invariant).
type Channel interface {
	ID() string
	Type() Type
	Start(ctx context.Context) error
	Stop() error
	// Receive returns a read-only channel of inbound messages.
	// Phase 2: Manager polls this to get messages from all channels.
	Receive() <-chan Message
	Send(msg Message, resp Response) error
}

// Permission Matrix

func CanReadPersonalVault(chType Type) bool {
	return chType == Internal
}

func CanWritePersonalVault(chType Type) bool {
	return chType == Internal
}

func CanReadAgentVault(chType Type) bool {
	return true
}

func CanUseCloudModel(chType Type, sensitive bool) bool {
	if sensitive {
		return false
	}
	return true
}
