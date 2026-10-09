package chain

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// EntityTriggerDecideStep is a deterministic, rule-based routing step.
// It does NOT call the LLM. Instead, it checks whether the user's query
// contains any entity name from the selected vault, scanned for each request,
// including person names and project names.
//
// Decision behaviour:
//   "search" -> continue chain to VaultSearchStep -> LLMAnswerStep (RAG path)
//   "direct" -> redirect to "direct" chain (LLM-only, no knowledge base)
//
// Key design principle:
//   Only explicit mention of a known entity triggers RAG.
//   "Who are you", "Hello", "What is X" -> all go to direct path.
//   "Yaoyuanle XXXX" -> triggers RAG because "Yaoyuanle" is a known entity.

type TriggerEntityProvider interface {
	TriggerEntities(context.Context, string) ([]string, error)
}

type EntityTriggerDecideStep struct {
	provider TriggerEntityProvider
	logger   *zap.Logger
}

// NewEntityTriggerDecideStep creates a rule-based routing step.
// provider supplies current entities for the target vault.
func NewEntityTriggerDecideStep(provider TriggerEntityProvider, logger *zap.Logger) *EntityTriggerDecideStep {
	return &EntityTriggerDecideStep{
		provider: provider,
		logger:   logger,
	}
}

func (s *EntityTriggerDecideStep) Name() string { return "entity-trigger-decide" }

// Run checks whether any trigger entity appears in the query.
// Sets llm_decision = "search" or "direct" and optionally sets search_query.
func (s *EntityTriggerDecideStep) Run(ctx context.Context, state *ChainState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.provider == nil {
		return fmt.Errorf("entity trigger provider is required")
	}
	entities, err := s.provider.TriggerEntities(ctx, state.Vault)
	if err != nil {
		return fmt.Errorf("load trigger entities: %w", err)
	}
	query := strings.TrimSpace(state.Query)

	// Extremely short queries -> direct.
	if len([]rune(query)) <= 2 {
		state.Data["llm_decision"] = "direct"
		state.Data["trigger_reason"] = "query too short"
		return nil
	}

	// Check each entity against the query.
	for _, entity := range entities {
		if entity == "" {
			continue
		}
		if strings.Contains(query, entity) {
			state.Data["llm_decision"] = "search"
			state.Data["search_query"] = query
			state.Data["trigger_entity"] = entity
			state.Data["trigger_reason"] = "matched entity: " + entity

			s.logger.Debug("entity trigger matched",
				zap.String("entity", entity),
			)
			return nil
		}
	}

	// No entity matched -> direct path.
	state.Data["llm_decision"] = "direct"
	state.Data["trigger_reason"] = "no entity match"
	return nil
}

// Decide returns the routing decision based on llm_decision.
//
//   "search" -> Next="" (continue current chain: VaultSearchStep -> ...)
//   "direct" -> Next="direct" (redirect to DirectAnswerStep chain)
func (s *EntityTriggerDecideStep) Decide(ctx context.Context, state *ChainState) (*StepResult, error) {
	decision, _ := state.GetString("llm_decision")
	switch decision {
	case "search":
		return &StepResult{Next: "", Reason: "entity matched, continue RAG chain"}, nil
	case "direct":
		return &StepResult{Next: "direct", Reason: "no entity match, redirect to direct answer"}, nil
	default:
		// Safety: unknown -> direct.
		return &StepResult{Next: "direct", Reason: "unknown decision, fallback to direct"}, nil
	}
}

var _ DecisionStep = (*EntityTriggerDecideStep)(nil)
