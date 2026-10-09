package channel

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// Manager owns all channels and routes messages between them and the agent core.
type Manager struct {
	channels map[string]Channel
	logger   *zap.Logger
	mu       sync.RWMutex
}

func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		channels: make(map[string]Channel),
		logger:   logger,
	}
}

func (m *Manager) Register(ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.ID()] = ch
	m.logger.Info("channel registered", zap.String("id", ch.ID()), zap.String("type", string(ch.Type())))
}

func (m *Manager) Get(id string) (Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[id]
	if !ok {
		return nil, fmt.Errorf("channel %q not found", id)
	}
	return ch, nil
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, ch := range m.channels {
		if err := ch.Start(ctx); err != nil {
			return fmt.Errorf("channel %s: start: %w", id, err)
		}
		m.logger.Info("channel started", zap.String("id", id))
	}
	return nil
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, ch := range m.channels {
		if err := ch.Stop(); err != nil {
			m.logger.Warn("channel stop error", zap.String("id", id), zap.Error(err))
		}
	}
}

// Run starts the unified message loop for all registered channels.
// Uses a fan-in pattern: one goroutine per channel forwards messages to a shared channel.
func (m *Manager) Run(ctx context.Context, handler func(msg Message)) {
	m.mu.RLock()
	var channels []Channel
	for _, ch := range m.channels {
		channels = append(channels, ch)
	}
	m.mu.RUnlock()

	if len(channels) == 0 {
		m.logger.Warn("no channels registered, message loop not started")
		return
	}

	merged := make(chan Message, 100)
	var wg sync.WaitGroup
	var handlers sync.WaitGroup
	// Register predecessors in receive order, before goroutines can race to run.
	type conversationKey struct{ channel, user string }
	tails := make(map[conversationKey]chan struct{})
	var queueMu sync.Mutex
	defer func() {
		wg.Wait()
		handlers.Wait()
	}()

	for _, ch := range channels {
		wg.Add(1)
		go func(c Channel) {
			defer wg.Done()
			inbound := c.Receive()
			for {
				var msg Message
				select {
				case <-ctx.Done():
					return
				case next, ok := <-inbound:
					if !ok {
						return
					}
					msg = next
				}
				select {
				case merged <- msg:
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}

	// Close merged channel when all fan-in goroutines are done
	go func() {
		wg.Wait()
		close(merged)
	}()

	m.logger.Info("unified message loop started", zap.Int("channels", len(channels)))

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("message loop stopping (context cancelled)")
			return
		case msg, ok := <-merged:
			if !ok {
				m.logger.Info("message loop stopping (all channels closed)")
				return
			}
			m.logger.Debug("message received",
				zap.String("channel", msg.ChannelID),
				zap.String("user", msg.UserID),
			)
			key := conversationKey{msg.ChannelID, msg.UserID}
			completed := make(chan struct{})
			queueMu.Lock()
			previous := tails[key]
			tails[key] = completed
			queueMu.Unlock()
			handlers.Add(1)
			go func(msg Message) {
				defer handlers.Done()
				defer func() {
					queueMu.Lock()
					if tails[key] == completed {
						delete(tails, key)
					}
					close(completed)
					queueMu.Unlock()
				}()
				if previous != nil {
					select {
					case <-previous:
					case <-ctx.Done():
						return
					}
				}
				if ctx.Err() != nil {
					return
				}
				handler(msg)
			}(msg)
		}
	}
}
