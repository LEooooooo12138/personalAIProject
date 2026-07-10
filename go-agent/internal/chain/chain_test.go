package chain

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

)

// ── ChainState tests ──

func TestChainState_NewChainState(t *testing.T) {
	state := NewChainState("test query", "personal", nil)
	if state.Query != "test query" {
		t.Errorf("Query = %q, want %q", state.Query, "test query")
	}
	if state.Vault != "personal" {
		t.Errorf("Vault = %q, want %q", state.Vault, "personal")
	}
	if state.Data == nil {
		t.Error("Data map should be initialized")
	}
	if state.Metadata == nil {
		t.Error("Metadata map should be initialized even when nil is passed")
	}
}

func TestChainState_SetGet(t *testing.T) {
	state := NewChainState("q", "personal", nil)

	state.Set("key1", "value1")
	val, ok := state.Get("key1")
	if !ok {
		t.Error("Get should return true for existing key")
	}
	if val != "value1" {
		t.Errorf("Get = %q, want %q", val, "value1")
	}

	_, ok = state.Get("nonexistent")
	if ok {
		t.Error("Get should return false for nonexistent key")
	}
}

func TestChainState_GetString(t *testing.T) {
	state := NewChainState("q", "personal", nil)

	state.Set("str_key", "hello")
	val, ok := state.GetString("str_key")
	if !ok {
		t.Error("GetString should return true for string value")
	}
	if val != "hello" {
		t.Errorf("GetString = %q, want %q", val, "hello")
	}

	// Non-string value should return false.
	state.Set("int_key", 42)
	_, ok = state.GetString("int_key")
	if ok {
		t.Error("GetString should return false for non-string value")
	}
}

func TestChainState_AddSource(t *testing.T) {
	state := NewChainState("q", "personal", nil)

	src1 := VaultSource{Title: "Page A", Path: "concepts/a.md", Body: "body a"}
	src2 := VaultSource{Title: "Page B", Path: "concepts/b.md", Body: "body b"}
	src3 := VaultSource{Title: "Page A Dup", Path: "concepts/a.md", Body: "dup"}

	state.AddSource(src1)
	if len(state.Sources) != 1 {
		t.Fatalf("len(Sources) = %d, want 1", len(state.Sources))
	}

	state.AddSource(src2)
	if len(state.Sources) != 2 {
		t.Fatalf("len(Sources) = %d, want 2", len(state.Sources))
	}

	// Duplicate path should be skipped.
	state.AddSource(src3)
	if len(state.Sources) != 2 {
		t.Errorf("len(Sources) = %d, want 2 (dup skipped)", len(state.Sources))
	}
}

func TestChainState_HasSources(t *testing.T) {
	state := NewChainState("q", "personal", nil)
	if state.HasSources() {
		t.Error("HasSources should be false for empty state")
	}
	state.AddSource(VaultSource{Title: "Test", Path: "test.md"})
	if !state.HasSources() {
		t.Error("HasSources should be true after adding source")
	}
}

func TestChainState_AddDecision(t *testing.T) {
	state := NewChainState("q", "personal", nil)
	state.AddDecision("query-classify", "rag", "knowledge signal detected")
	if len(state.Decisions) != 1 {
		t.Fatalf("len(Decisions) = %d, want 1", len(state.Decisions))
	}
	d := state.Decisions[0]
	if d.Step != "query-classify" || d.Choice != "rag" {
		t.Errorf("Decision = %+v, want step=query-classify choice=rag", d)
	}
}

// ── Chain execution tests ──

func TestChainExecutor_BasicChain(t *testing.T) {
	logger := zap.NewNop()
	router := NewChainRouter()

	// A chain that sets FinalAnswer.
	chain := NewChain("test-chain", "test",
		NewFuncStep("step1", func(ctx context.Context, state *ChainState) error {
			state.Set("intermediate", "done")
			return nil
		}),
		NewFuncStep("step2", func(ctx context.Context, state *ChainState) error {
			val, _ := state.GetString("intermediate")
			state.FinalAnswer = "got: " + val
			return nil
		}),
	)
	router.Register("test-chain", chain)

	executor := NewChainExecutor(logger, router)
	state := NewChainState("hello", "personal", nil)
	result, err := executor.Run(context.Background(), "test-chain", state)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalAnswer != "got: done" {
		t.Errorf("FinalAnswer = %q, want %q", result.FinalAnswer, "got: done")
	}
	if len(result.Trace) != 2 {
		t.Errorf("Trace length = %d, want 2", len(result.Trace))
	}
}

func TestChainExecutor_StepErrorAborts(t *testing.T) {
	logger := zap.NewNop()
	router := NewChainRouter()

	chain := NewChain("error-chain", "test",
		NewFuncStep("step1", func(ctx context.Context, state *ChainState) error {
			return context.DeadlineExceeded // simulate failure
		}),
		NewFuncStep("step2", func(ctx context.Context, state *ChainState) error {
			state.FinalAnswer = "should not reach"
			return nil
		}),
	)
	router.Register("error-chain", chain)

	executor := NewChainExecutor(logger, router)
	state := NewChainState("q", "personal", nil)
	result, err := executor.Run(context.Background(), "error-chain", state)

	if err != nil {
		t.Fatalf("Run should not return error for step failure: %v", err)
	}
	if result.Error == nil {
		t.Error("result.Error should not be nil when step fails")
	}
	if result.FinalAnswer != "" {
		t.Errorf("FinalAnswer should be empty after abort, got %q", result.FinalAnswer)
	}
	if len(result.Trace) != 1 {
		t.Errorf("Trace length = %d, want 1 (only step1 executed)", len(result.Trace))
	}
}

func TestChainExecutor_ContinueOnError(t *testing.T) {
	logger := zap.NewNop()
	router := NewChainRouter()

	chain := NewChain("continue-chain", "test")
	chain.OnError = "continue"
	chain.Steps = []Step{
		NewFuncStep("step1", func(ctx context.Context, state *ChainState) error {
			return context.DeadlineExceeded
		}),
		NewFuncStep("step2", func(ctx context.Context, state *ChainState) error {
			state.FinalAnswer = "recovered"
			return nil
		}),
	}
	router.Register("continue-chain", chain)

	executor := NewChainExecutor(logger, router)
	state := NewChainState("q", "personal", nil)
	result, err := executor.Run(context.Background(), "continue-chain", state)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalAnswer != "recovered" {
		t.Errorf("FinalAnswer = %q, want %q", result.FinalAnswer, "recovered")
	}
	if len(result.Trace) != 2 {
		t.Errorf("Trace length = %d, want 2", len(result.Trace))
	}
	if result.Trace[0].Status != "skipped" {
		t.Errorf("Trace[0].Status = %q, want %q", result.Trace[0].Status, "skipped")
	}
}

func TestChainExecutor_UnknownChain(t *testing.T) {
	logger := zap.NewNop()
	router := NewChainRouter()
	executor := NewChainExecutor(logger, router)
	state := NewChainState("q", "personal", nil)
	_, err := executor.Run(context.Background(), "nonexistent", state)
	if err == nil {
		t.Error("expected error for unknown chain")
	}
}

func TestChainExecutor_ContextCancellation(t *testing.T) {
	logger := zap.NewNop()
	router := NewChainRouter()

	chain := NewChain("slow-chain", "test",
		NewFuncStep("step1", func(ctx context.Context, state *ChainState) error {
			time.Sleep(100 * time.Millisecond) // slow enough to be cancelled
			return nil
		}),
	)
	router.Register("slow-chain", chain)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	executor := NewChainExecutor(logger, router)
	state := NewChainState("q", "personal", nil)
	result, err := executor.Run(ctx, "slow-chain", state)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error == nil {
		t.Error("result.Error should be set when context is cancelled")
	}
}

// ── DecisionStep tests ──

func TestChainExecutor_DecisionStepRedirect(t *testing.T) {
	logger := zap.NewNop()
	router := NewChainRouter()

	// Target chain.
	targetChain := NewChain("target", "target",
		NewFuncStep("target-step", func(ctx context.Context, state *ChainState) error {
			state.FinalAnswer = "redirected-here"
			return nil
		}),
	)
	router.Register("target", targetChain)

	// Main chain with a DecisionStep that redirects.
	mainChain := NewChain("main", "main",
		&testDecisionStep{next: "target", reason: "test redirect"},
	)
	router.Register("main", mainChain)

	executor := NewChainExecutor(logger, router)
	state := NewChainState("q", "personal", nil)
	result, err := executor.Run(context.Background(), "main", state)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalAnswer != "redirected-here" {
		t.Errorf("FinalAnswer = %q, want %q", result.FinalAnswer, "redirected-here")
	}
}

type testDecisionStep struct {
	next   string
	reason string
}

func (s *testDecisionStep) Name() string { return "test-decision" }
func (s *testDecisionStep) Run(ctx context.Context, state *ChainState) error {
	state.Set("decision_made", "true")
	return nil
}
func (s *testDecisionStep) Decide(ctx context.Context, state *ChainState) (*StepResult, error) {
	return &StepResult{Next: s.next, Reason: s.reason}, nil
}


// ── Ensure interfaces are satisfied ──

func TestLLMDecideStep_ImplementsDecisionStep(t *testing.T) {
	var _ DecisionStep = NewLLMDecideAndAnswerStep(nil, "", nil)
}


