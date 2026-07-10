package chain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ── Chain Executor ──
//
// The executor runs a chain step by step, collecting traces and handling errors.
// It supports:
//   - Sequential step execution with per-step timing
//   - Error handling: abort vs continue on failure
//   - DecisionStep branching: steps can redirect to a different chain
//   - Trace collection for debugging and auditing

// ChainExecutor runs chains with logging and tracing.
type ChainExecutor struct {
	logger *zap.Logger
	router *ChainRouter
}

// NewChainExecutor creates a chain executor with the given router.
func NewChainExecutor(logger *zap.Logger, router *ChainRouter) *ChainExecutor {
	return &ChainExecutor{
		logger: logger,
		router: router,
	}
}

// ExecuteResult holds the outcome of running a chain.
type ExecuteResult struct {
	ChainName string
	FinalAnswer string
	Error       error
	Trace       []StepTrace
}

// Run executes a chain by name. If the chain is not found, returns an error.
//
// During execution, if any step implements DecisionStep and returns a Next chain,
// execution switches to the named chain (which runs from its first step).
// This avoids infinite loops: a chain can switch at most once.
func (e *ChainExecutor) Run(ctx context.Context, chainName string, state *ChainState) (*ExecuteResult, error) {
	chain, err := e.router.Route(chainName)
	if err != nil {
		return nil, err
	}

	e.logger.Info("chain started",
		zap.String("chain", chain.Name),
		zap.String("query", truncate(state.Query, 80)),
	)

	result := e.runChain(ctx, chain, state)
	result.ChainName = chainName

	if result.Error != nil {
		e.logger.Warn("chain completed with error",
			zap.String("chain", chainName),
			zap.Error(result.Error),
		)
	} else {
		e.logger.Info("chain completed",
			zap.String("chain", chainName),
			zap.Int("steps", len(result.Trace)),
		)
	}

	return result, nil
}

// runChain executes a single chain. Returns the final result.
func (e *ChainExecutor) runChain(ctx context.Context, chain *Chain, state *ChainState) *ExecuteResult {
	result := &ExecuteResult{ChainName: chain.Name}

	for _, step := range chain.Steps {
		// Check context cancellation before each step.
		select {
		case <-ctx.Done():
			result.Error = ctx.Err()
			return result
		default:
		}

		stepName := step.Name()
		e.logger.Debug("step start", zap.String("step", stepName))

		start := time.Now()
		err := step.Run(ctx, state)
		elapsed := time.Since(start)

		trace := StepTrace{
			StepName: stepName,
			Duration: elapsed,
		}

		if err != nil {
			trace.Status = "error"
			trace.Error = err.Error()

			if chain.OnError == "continue" {
				e.logger.Warn("step error (continuing)",
					zap.String("step", stepName),
					zap.Error(err),
				)
				trace.Status = "skipped"
			} else {
				e.logger.Error("step error (aborting chain)",
					zap.String("step", stepName),
					zap.Error(err),
				)
				result.Error = fmt.Errorf("step %q: %w", stepName, err)
				result.Trace = append(result.Trace, trace)
				state.Trace = append(state.Trace, trace)
				return result
			}
		} else {
			trace.Status = "ok"
		}

		result.Trace = append(result.Trace, trace)
		state.Trace = append(state.Trace, trace)

		// Check if this step wants to redirect execution.
		if ds, ok := step.(DecisionStep); ok {
			decision, err := ds.Decide(ctx, state)
			if err != nil {
				e.logger.Warn("step decision error", zap.String("step", stepName), zap.Error(err))
				continue
			}
			if decision.Abort {
				e.logger.Info("chain aborted by step",
					zap.String("step", stepName),
					zap.String("reason", decision.Reason),
				)
				result.FinalAnswer = state.FinalAnswer
				return result
			}
			if decision.Next != "" {
				e.logger.Info("chain redirect",
					zap.String("from_step", stepName),
					zap.String("to_chain", decision.Next),
					zap.String("reason", decision.Reason),
				)
				nextChain, err := e.router.Route(decision.Next)
				if err != nil {
					result.Error = fmt.Errorf("redirect to unknown chain %q: %w", decision.Next, err)
					return result
				}
				return e.runChain(ctx, nextChain, state)
			}
		}
	}

	result.FinalAnswer = state.FinalAnswer
	return result
}

// NewChain creates a named chain with the given steps.
func NewChain(name, description string, steps ...Step) *Chain {
	return &Chain{
		Name:        name,
		Description: description,
		Steps:       steps,
		OnError:     "abort",
	}
}

// ── Branching Chain (Chain-of-Thought style) ──

// BranchingChain executes a classifier step first, then dispatches
// to one of several sub-chains based on the result.
type BranchingChain struct {
	Name       string
	Classifier DecisionStep
	Branches   map[string]*Chain   // class → chain
	Default    *Chain              // fallback if no branch matches
}

// ToChain converts the branching chain into a flat chain for the executor.
func (bc *BranchingChain) ToChain(router *ChainRouter) {
	for name, c := range bc.Branches {
		router.Register(name, c)
	}
	if bc.Default != nil {
		router.Register(bc.Name+"_default", bc.Default)
	}

	// Build the main chain: classifier → redirect decision.
	main := NewChain(bc.Name, "branching chain",
		bc.Classifier,
		NewFuncStep("chain-branch-dispatcher", func(ctx context.Context, state *ChainState) error {
			// The DecisionStep should have set Data["branch_target"].
			target, _ := state.GetString("branch_target")
			if target == "" {
				target = bc.Name + "_default"
			}
			state.Data["_redirect"] = target
			return nil
		}),
	)
	router.Register(bc.Name+"_entry", main)
}

// ── Helpers ──

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}

