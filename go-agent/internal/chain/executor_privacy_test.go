package chain

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"strings"
	"testing"
)

// Removing the private-vault guard exposes inputs in execution diagnostics.
func TestExecutorPersonalQueryIsNotLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	router := NewChainRouter()
	router.Register("rag-answer", NewChain("rag-answer", "", NewFuncStep("answer", func(_ context.Context, s *ChainState) error { s.FinalAnswer = "fixture answer"; return nil })))
	result, err := NewChainExecutor(zap.New(core), router).Run(context.Background(), "rag-answer", NewChainState("PRIVATE_INPUT_ONLY_485", "personal", nil))
	if err != nil || result.Error != nil || result.FinalAnswer != "fixture answer" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if strings.Contains(fmt.Sprint(logs.All()), "PRIVATE_INPUT_ONLY_485") {
		t.Fatal("private input exposed in Info logs")
	}
	if logs.Len() == 0 {
		t.Fatal("execution diagnostics removed instead of private input")
	}
}

// Empty Vault is the existing pipeline's default personal Vault.
func TestExecutorDefaultPersonalQueryIsNotLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	router := NewChainRouter()
	router.Register("rag-answer", NewChain("rag-answer", "", NewFuncStep("answer", func(_ context.Context, s *ChainState) error { s.FinalAnswer = "fixture answer"; return nil })))
	_, err := NewChainExecutor(zap.New(core), router).Run(context.Background(), "rag-answer", NewChainState("DEFAULT_PRIVATE_INPUT_485", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(logs.All()), "DEFAULT_PRIVATE_INPUT_485") {
		t.Fatal("default personal input exposed in logs")
	}
}

// Console payloads, caller IDs and arbitrary step errors never belong in operational logs.
func TestConsoleChainLogsExcludePayload(t *testing.T) {
	const secretMarker = "PRIVATE_CONSOLE_PAYLOAD_157"
	for _, policy := range []string{"abort", "continue"} {
		t.Run(policy, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			router := NewChainRouter()
			c := NewChain("chat", "", NewFuncStep("answer", func(_ context.Context, s *ChainState) error { return fmt.Errorf("upstream body %s", secretMarker) }))
			c.OnError = policy
			router.Register("chat", c)
			state := NewChainState(secretMarker, "agent", map[string]string{"channel": "console", "request_id": secretMarker})
			result, err := NewChainExecutor(zap.New(core), router).Run(context.Background(), "chat", state)
			if err != nil || len(result.Trace) != 1 || !strings.Contains(result.Trace[0].Error, secretMarker) {
				t.Fatalf("original trace contract changed: result=%+v err=%v", result, err)
			}
			if strings.Contains(fmt.Sprint(logs.All()), secretMarker) {
				t.Fatal("payload leaked into logs")
			}
			if logs.Len() == 0 {
				t.Fatal("operational diagnostics removed")
			}
		})
	}
}

type privacyDecisionStep struct {
	decision *StepResult
	failure  error
}

func (*privacyDecisionStep) Name() string                           { return "classify" }
func (*privacyDecisionStep) Run(context.Context, *ChainState) error { return nil }
func (s *privacyDecisionStep) Decide(context.Context, *ChainState) (*StepResult, error) {
	return s.decision, s.failure
}

// Decisions can contain model-provided reasons and errors, so they require the same privacy boundary.
func TestConsoleChainLogsExcludePayloadDecisions(t *testing.T) {
	const marker = "PRIVATE_DECISION_PAYLOAD_157"
	for _, tc := range []struct {
		name     string
		decision *StepResult
		failure  error
	}{
		{"abort", &StepResult{Abort: true, Reason: marker}, nil},
		{"redirect", &StepResult{Next: "answer", Reason: marker}, nil},
		{"error", nil, fmt.Errorf("upstream error %s", marker)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			router := NewChainRouter()
			router.Register("chat", NewChain("chat", "", &privacyDecisionStep{decision: tc.decision, failure: tc.failure}))
			router.Register("answer", NewChain("answer", ""))
			_, err := NewChainExecutor(zap.New(core), router).Run(context.Background(), "chat", NewChainState("hello", "agent", map[string]string{"channel": "console"}))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprint(logs.All()), marker) {
				t.Fatal("decision payload leaked into logs")
			}
		})
	}
}
