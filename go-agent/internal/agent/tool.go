package agent

import (
	"context"
	"fmt"
)

// ── Tool Interface ──
//
// A Tool is a capability the ReAct agent can invoke during reasoning.
// Each tool has a specification (name, description, parameter schema)
// that is injected into the LLM system prompt, and an Execute method
// that carries out the actual work.

// ToolInput describes a tool to the LLM (name, description, JSON Schema parameters).
type ToolInput struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Tool is a capability the agent can use.
type Tool interface {
	Spec() ToolInput
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// ── Registry ──

// ToolRegistry holds all available tools and provides lookup.
type ToolRegistry struct {
	tools map[string]Tool
	order []string // preserve registration order for prompt stability
}

// NewToolRegistry creates an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool. Duplicate names overwrite.
func (r *ToolRegistry) Register(t Tool) {
	name := t.Spec().Name
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

// Get looks up a tool by name.
func (r *ToolRegistry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("agent: unknown tool %q", name)
	}
	return t, nil
}

// Specs returns tool specifications in registration order (for prompt generation).
func (r *ToolRegistry) Specs() []ToolInput {
	specs := make([]ToolInput, 0, len(r.order))
	for _, name := range r.order {
		specs = append(specs, r.tools[name].Spec())
	}
	return specs
}

// Names returns tool names for error messages.
func (r *ToolRegistry) Names() []string {
	names := make([]string, len(r.order))
	copy(names, r.order)
	return names
}

// ── Dependency injection types ──
//
// Tools receive their dependencies as function values so that the agent
// package has zero import dependencies on gateway, vault, or inference.

// VaultHit is one search result returned by the vault search function.
type VaultHit struct {
	Path         string
	Title        string
	Snippet      string
	ChunkContent string // matched chunk body (from embedding search)
	SectionTitle string // H2 heading
	Score        float64
}

// VaultPage is a page read from the vault.
type VaultPage struct {
	Title string
	Body  string
}

// SearchVaultFunc searches the knowledge base and returns ranked results.
type SearchVaultFunc func(ctx context.Context, query string) ([]VaultHit, error)

// ReadPageFunc reads a wiki page by its relative path.
type ReadPageFunc func(ctx context.Context, path string) (*VaultPage, error)
