package core

import (
	"bytes"
	"encoding/json"
)

// ── Decision type ──

// RouteDecision is the output of the model router.
// It tells the system: which model to use, what to do if it fails,
// and why this decision was made (for audit/debugging).
type RouteDecision struct {
	TargetModel     string // e.g. "gemma4:12b", "llava:7b", "deepseek-chat"
	Backend         string // "local" or "cloud"
	Fallback        string // "fail" | "retry_local_only" | "retry_local_then_cloud"
	Reason          string // human-readable explanation
	ConfirmRequired bool   // Phase 4+: smart-home guard
}

// ── Router ──

// Router is the centralized model routing engine.
// It examines each request and decides which AI model should handle it.
// All routing logic lives here — nothing is hardcoded in gateway or channels.
type Router struct {
	defaultLocal string // e.g. "gemma4:12b"
	visionLocal  string // e.g. "llava:7b"
}

// NewRouter creates a model router with the given local model names.
func NewRouter(defaultLocal, visionLocal string) *Router {
	return &Router{
		defaultLocal: defaultLocal,
		visionLocal:  visionLocal,
	}
}

// Decide examines a raw chat request body (JSON) and model hint,
// then returns the routing decision.
//
// The method inspects the request for:
//   - Images (base64 or URL) → routes to vision model
//   - metadata.sensitive=true → forces local
//   - Explicit model name → uses it directly
//   - "auto" → delegates to default local model
//   - "cloud" → routes to cloud (stub for Phase 2.8)
func (r *Router) Decide(body []byte, modelHint string, metadata map[string]string) *RouteDecision {
	// 1. If the user explicitly named a model, use it.
	if modelHint != "" && modelHint != "auto" && modelHint != "cloud" {
		return &RouteDecision{
			TargetModel: modelHint,
			Backend:     "local",
			Fallback:    "fail",
			Reason:      "explicit model hint",
		}
	}

	// 2. Cloud routing (Phase 2.8 stub).
	if modelHint == "cloud" {
		return &RouteDecision{
			TargetModel: "deepseek-chat",
			Backend:     "cloud",
			Fallback:    "fail",
			Reason:      "explicit cloud hint (stub)",
		}
	}

	// 3. Image detection: if the request contains an image, route to vision model.
	if hasImage(body) {
		return &RouteDecision{
			TargetModel: r.visionLocal,
			Backend:     "local",
			Fallback:    "fail",
			Reason:      "image detected → vision model",
		}
	}

	// 4. Sensitive content must stay local (S-3 invariant).
	if metadata != nil && metadata["sensitive"] == "true" {
		return &RouteDecision{
			TargetModel: r.defaultLocal,
			Backend:     "local",
			Fallback:    "fail",
			Reason:      "sensitive content → local only",
		}
	}

	// 5. Skill-based operations always use local model.
	if metadata != nil && metadata["skill"] != "" {
		return &RouteDecision{
			TargetModel: r.defaultLocal,
			Backend:     "local",
			Fallback:    "fail",
			Reason:      "skill operation → local",
		}
	}

	// 6. Default: "auto" → local model.
	return &RouteDecision{
		TargetModel: r.defaultLocal,
		Backend:     "local",
		Fallback:    "fail",
		Reason:      "auto → default local model",
	}
}

// ── Image detection ──

// hasImage checks whether the request body contains image data.
// It looks for two patterns common in OpenAI-compatible vision requests:
//   - "image_url": a URL to an image
//   - "data:image/": base64-encoded image data
//
// This uses byte-level scanning (no JSON parsing) for speed.
// A request with images is typically much larger than text-only,
// so checking raw bytes avoids allocating full parse trees.
func hasImage(body []byte) bool {
	// Quick pre-check: if the body is small, it can't contain a base64 image.
	if len(body) < 100 {
		return false
	}
	return bytes.Contains(body, []byte(`"image_url"`)) ||
		bytes.Contains(body, []byte(`data:image/`))
}

// ── Legacy compatibility ──

// Route is kept for backward compatibility with Phase 1 code.
// New code should use router.Decide() instead.
func Route(modelHint string) *RouteDecision {
	r := NewRouter("gemma4:12b", "llava:7b")
	return r.Decide(nil, modelHint, nil)
}

// ── Request types (for use by gateway) ──

// ChatRequest is the OpenAI-compatible chat request structure.
// Used by the gateway to extract routing metadata.
type ChatRequest struct {
	Model    string            `json:"model"`
	Messages []ChatMessage     `json:"messages"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ChatMessage is a single message in a chat conversation.
type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or multimodal array
}
