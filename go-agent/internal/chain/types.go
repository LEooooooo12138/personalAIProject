// ── Chain / Pipeline System ──
//
// 灵感来自 LangChain 的 Chain 组合模式，用 Go interface + 泛型思想实现。
//
// 核心概念：
//
//   Step    = 最小的可组合操作单元（一个 Go 原生操作或一次 LLM 调用）
//   Chain   = 一组 Step 的顺序执行，通过 ChainState 传递中间状态
//   Branch  = 条件分支：根据当前 ChainState 选择下一个要执行的 Chain
//
// 设计决策（与 LangChain 的对照）：
//
//   LangChain          │  本实现
//   ──────────────────│──────────────────────────
//   Runnable           │  Step interface
//   Chain              │  SequentialChain
//   RunnableLambda     │  GoFuncStep（任何 func 都能包装为 Step）
//   ChatModel          │  LLMStep
//   Retriever          │  VaultSearchStep
//   Document           │  VaultSource（已有）
//   memory             │  ChainState（轻量级，不引入外部状态）
//
// 为什么不用 channel 传递数据？
//   Chain 是同步执行的（一个步骤完成才进入下一个），不需要 channel。

package chain

import (
	"context"
	"fmt"
	"time"
)

// ── Core Types ──

// Step is the smallest composable operation unit.
// Each step reads from ChainState, does work, and writes results back.
type Step interface {
	// Name returns a human-readable step identifier for logging.
	Name() string

	// Run executes the step logic. The step may modify state in place.
	// Returns an error if the step fails; the chain executor decides
	// whether to abort or continue.
	Run(ctx context.Context, state *ChainState) error
}

// ChainState carries data through the chain execution pipeline.
// Think of it as a typed bag of values that steps accumulate into.
type ChainState struct {
	// ── Inputs (set before chain execution) ──

	// Query is the user's original request text.
	Query string

	// Vault is the target vault name ("personal" or "agent").
	Vault string

	// Metadata carries channel info, skill name, etc.
	Metadata map[string]string

	// ── Outputs (accumulated during chain execution) ──

	// FinalAnswer holds the chain's output result.
	FinalAnswer string

	// Sources contains retrieved documents with provenance.
	Sources []VaultSource

	// Decisions records key routing/classification decisions for audit.
	Decisions []Decision

	// Summary holds a structured summary (used by memory sedimentation).
	Summary *StructuredSummary

	// ── Intermediate state (freely readable/writable by steps) ──

	// Data is a generic key-value store for step-to-step communication.
	Data map[string]interface{}

	// ── Diagnostics ──

	// Trace records timing and status for each executed step.
	Trace []StepTrace

	// ── Streaming ──

	// Stream is an output channel for streaming responses.
	// When set (non-nil), steps that support streaming write StreamTokens
	// to this channel. The caller reads from it in a separate goroutine.
	//
	// Usage pattern:
	//   ch := make(chan StreamToken, 50)
	//   state.Stream = ch
	//   go executor.Run(ctx, "chat", state)
	//   for token := range ch { handle(token) }
	Stream chan<- StreamToken

	// StreamMode enables streaming output. When true, LLM steps that support
	// streaming will use SSE token-by-token output instead of buffered mode.
	// This is separate from Stream channel: Stream is the transport, StreamMode
	// is the opt-in flag. Both must be set for streaming to activate.
	StreamMode bool
}

// VaultSource is a retrieved document with its content and metadata.
type VaultSource struct {
	Title    string
	Path     string
	Body     string
	Snippet  string // a short match context for display
	Score    float64
	Category string // entities, concepts, skills, etc.
	Tags     []string
}

// Decision records a key routing or classification choice.
type Decision struct {
	Step   string // which step made this decision
	Choice string // what was chosen
	Reason string // why
}

// StructuredSummary is a structured memory entry.
type StructuredSummary struct {
	Title      string
	Decisions  string
	FollowUps  string
	Confidence float64
}

// StepTrace records timing and status for a single step execution.
type StepTrace struct {
	StepName string
	Duration time.Duration
	Status   string // "ok", "skipped", "error"
	Error    string
}

// ── Streaming ──

// StreamToken is a single streaming output chunk sent through ChainState.Stream.
// When ChainState.Stream is set (non-nil), steps that support streaming write
// tokens to this channel instead of buffering the full result.
type StreamToken struct {
	// Text is the incremental token content (may be empty for control tokens).
	Text string `json:"text,omitempty"`

	// Done signals the end of the stream. The final accumulated text is in Text.
	Done bool `json:"done,omitempty"`

	// Error is set when streaming fails. Only valid with Done=true.
	Error string `json:"error,omitempty"`

	// Sources contains the retrieved vault pages. Sent once, before the first
	// text token, so the UI can display citation info early.
	Sources []StreamSource `json:"sources,omitempty"`
}

// StreamSource is a lightweight source reference for streaming responses.
type StreamSource struct {
	Title string  `json:"title"`
	Score float64 `json:"score,omitempty"`
}

// ── Step Result (for conditional branching) ──

// StepResult is returned by steps that need to influence chain flow.
// Most steps return nil and modify state in place; this is used for
// steps that make routing decisions.
type StepResult struct {
	// Next specifies which chain to execute next. Empty = continue current chain.
	Next string

	// Abort signals the chain executor to stop execution.
	Abort bool

	// Reason explains the routing decision.
	Reason string
}

// ── Decision Step interface ──

// DecisionStep is a Step that also returns a routing decision.
// Used for steps that may redirect the chain (e.g., "is this a wiki query?").
type DecisionStep interface {
	Step
	Decide(ctx context.Context, state *ChainState) (*StepResult, error)
}

// ── Chain ──

// Chain is a named, ordered list of steps that execute sequentially.
type Chain struct {
	Name        string
	Description string
	Steps       []Step

	// onError determines behavior when a step fails.
	// "abort" (default): stop the chain immediately.
	// "continue": log the error and continue to the next step.
	OnError string
}

// ── Router ──

// ChainRouter maps intent labels to chains. This is how the agent
// decides which chain to run for a given user request.
type ChainRouter struct {
	routes map[string]*Chain
}

// NewChainRouter creates an empty chain router.
func NewChainRouter() *ChainRouter {
	return &ChainRouter{routes: make(map[string]*Chain)}
}

// Register maps a name to a chain.
func (r *ChainRouter) Register(name string, c *Chain) {
	r.routes[name] = c
}

// Route returns the chain for a given name.
func (r *ChainRouter) Route(name string) (*Chain, error) {
	c, ok := r.routes[name]
	if !ok {
		return nil, fmt.Errorf("chain: no route for %q", name)
	}
	return c, nil
}

// ── Convenience: wrap a function as a Step ──

// FuncStep wraps a plain function as a Step.
type FuncStep struct {
	name string
	fn   func(ctx context.Context, state *ChainState) error
}

func (s *FuncStep) Name() string                                         { return s.name }
func (s *FuncStep) Run(ctx context.Context, state *ChainState) error     { return s.fn(ctx, state) }

// NewFuncStep creates a Step from a function.
func NewFuncStep(name string, fn func(ctx context.Context, state *ChainState) error) Step {
	return &FuncStep{name: name, fn: fn}
}

// ── State helpers ──

// NewChainState creates a ChainState with initialized fields.
func NewChainState(query, vault string, metadata map[string]string) *ChainState {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	return &ChainState{
		Query:    query,
		Vault:    vault,
		Metadata: metadata,
		Data:     make(map[string]interface{}),
	}
}

// Set stores a value in the generic data map.
func (s *ChainState) Set(key string, val interface{}) {
	s.Data[key] = val
}

// Get retrieves a value from the generic data map.
func (s *ChainState) Get(key string) (interface{}, bool) {
	val, ok := s.Data[key]
	return val, ok
}

// GetString retrieves a string value from the generic data map.
func (s *ChainState) GetString(key string) (string, bool) {
	val, ok := s.Data[key]
	if !ok {
		return "", false
	}
	sval, ok2 := val.(string)
	return sval, ok2
}

// AddSource appends a vault source with dedup by path.
func (s *ChainState) AddSource(src VaultSource) {
	// Simple dedup: skip if same path already exists.
	for _, existing := range s.Sources {
		if existing.Path == src.Path {
			return
		}
	}
	s.Sources = append(s.Sources, src)
}

// AddDecision records a routing decision.
func (s *ChainState) AddDecision(step, choice, reason string) {
	s.Decisions = append(s.Decisions, Decision{
		Step:   step,
		Choice: choice,
		Reason: reason,
	})
}

// HasSources returns true if any sources were found.
func (s *ChainState) HasSources() bool {
	return len(s.Sources) > 0
}




