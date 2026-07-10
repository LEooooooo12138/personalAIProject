package wecom

import (
	"sync"

	"go.uber.org/zap"
)

type ContactFilter struct {
	allowedUsers map[string]bool
	autoApprove  bool
	logger       *zap.Logger
	mu           sync.RWMutex
}

func NewContactFilter(allowedUsers []string, autoApprove bool, logger *zap.Logger) *ContactFilter {
	users := make(map[string]bool)
	for _, u := range allowedUsers {
		users[u] = true
	}
	return &ContactFilter{
		allowedUsers: users,
		autoApprove:  autoApprove,
		logger:       logger,
	}
}

func (f *ContactFilter) IsAllowed(externalUserID string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.allowedUsers[externalUserID]
}

func (f *ContactFilter) AddUser(externalUserID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowedUsers[externalUserID] = true
}

func (f *ContactFilter) RemoveUser(externalUserID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.allowedUsers, externalUserID)
}
