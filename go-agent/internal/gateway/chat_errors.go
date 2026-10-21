package gateway

import (
	"errors"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/inference"
)

var errAssistantSettingsUnavailable = errors.New("assistant settings unavailable")

func chatErrorCode(err error, fallback string) string {
	if errors.Is(err, chain.ErrContextBudgetExceeded) {
		return "context_budget_exceeded"
	}
	if errors.Is(err, errAssistantSettingsUnavailable) {
		return "assistant_settings_unavailable"
	}
	if code := inference.ErrorCode(err); code != "" {
		return code
	}
	if code := controlChatErrorClass(err); code != "unknown" {
		return code
	}
	return fallback
}

func controlFailureOutcome(err error) string {
	// Classifier/settings failures precede device processing. Other errors may
	// originate after proposal persistence, so retain reconciliation semantics.
	if errors.Is(err, chain.ErrHAIntentUnavailable) || errors.Is(err, chain.ErrHAIntentInvalid) || errors.Is(err, errAssistantSettingsUnavailable) {
		return "failed"
	}
	return "unknown"
}
