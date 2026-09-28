package channel

import "go.uber.org/zap"

// ChannelFactory creates a Channel from its own configuration.
// Each channel adapter (wecom, etc.) implements this and
// self-registers via RegisterFactory in its init() function.
type ChannelFactory interface {
	ID() string
	Create(cfg map[string]interface{}, logger *zap.Logger) (Channel, error)
}

var factories = map[string]ChannelFactory{}

// RegisterFactory registers a channel factory.
func RegisterFactory(f ChannelFactory) {
	factories[f.ID()] = f
}

// GetFactory returns a registered factory by ID.
func GetFactory(id string) (ChannelFactory, bool) {
	f, ok := factories[id]
	return f, ok
}
