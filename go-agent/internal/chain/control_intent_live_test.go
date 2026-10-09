package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
)

// Opt in explicitly. This harness uses only synthetic fixtures and the local
// inference gateway; no HA client or control service is reachable from it.
func TestHAIntentLiveEvaluation(t *testing.T) {
	if os.Getenv("HA_INTENT_LIVE") != "1" {
		t.Skip("set HA_INTENT_LIVE=1 for local synthetic-model evaluation")
	}
	const endpoint = "http://127.0.0.1:11434"
	model := os.Getenv("HA_INTENT_LIVE_MODEL")
	if model == "" {
		model = "gemma4:12b"
	}
	output := filepath.Join("..", "..", "..", "docs", "console-ha-model-evaluation-2026-10-09.json")
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "ha-intents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []haLiveFixture `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	report := haLiveReport{Mode: "real_local_model_synthetic_only", Model: model, PromptVersion: HAIntentPromptVersion, SchemaVersion: HAIntentSchemaVersion, Temperature: 0, ReasoningEffort: "none", MaxTokens: 512, PrimaryReference: "https://docs.ollama.com/api/openai-compatibility", StartedAt: time.Now().UTC(), RequestedCases: len(fixture.Cases), PerCaseTimeoutSeconds: 60, TotalBudgetSeconds: 600, HAWrites: 0, Compatibility: "not_tested", Status: "running"}
	save := func() {
		report.FinishedAt = time.Now().UTC()
		report.Summary = haLiveMetrics(report.Cases)
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	defer save()
	if os.Getenv("HA_INTENT_LIVE_RESUME") == "1" {
		saved, readErr := os.ReadFile(output)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var previous haLiveReport
		if json.Unmarshal(saved, &previous) != nil || previous.Model != model || previous.PromptVersion != HAIntentPromptVersion || previous.SchemaVersion != HAIntentSchemaVersion || len(previous.Smoke) != 3 || len(previous.Cases) != 0 {
			t.Fatal("resume requires matching saved three-smoke report and no evaluated cases")
		}
		report.Smoke = previous.Smoke
		report.StartedAt = previous.StartedAt
	}
	digest, err := haLiveModelDigest(endpoint, model)
	if err != nil {
		report.Status = "model_unavailable"
		report.Compatibility = "model_inventory_unavailable"
		save()
		t.Fatalf("local model inventory unavailable (details omitted); report=%s", output)
	}
	report.ModelDigest = digest
	budget, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	run := func(tc haLiveFixture) haLiveCase {
		client := &haLiveClient{Client: inference.NewOllamaClient(endpoint, 60*time.Second)}
		ctx, cancel := context.WithTimeout(budget, 60*time.Second)
		defer cancel()
		started := time.Now()
		observed, err := NewHAIntentParser(client, model).Parse(ctx, tc.Query, tc.Candidates)
		result := haLiveCase{ID: tc.ID, Category: tc.Category, Query: tc.Query, CandidateCount: len(haVisibleTargets(tc.Candidates)), Expected: tc.Expected, Observed: observed, LatencyMS: float64(time.Since(started).Microseconds()) / 1000, RequestAccepted: client.Accepted, SchemaShaped: client.SchemaShaped, ModelIntent: client.ModelIntent, FinishReason: client.FinishReason, ContentBytes: client.ContentBytes, RejectionStage: client.RejectionStage}
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			result.ErrorCode = "timeout"
		case errors.Is(err, context.Canceled):
			result.ErrorCode = "cancelled"
		case errors.Is(err, ErrHAIntentUnavailable):
			result.ErrorCode = "model_unavailable"
		case err != nil:
			result.ErrorCode = "invalid_or_ambiguous"
		}
		result.Exact = result.ErrorCode == "" && observed.Kind == tc.Expected.Kind && observed.EntityID == tc.Expected.EntityID && observed.Action == tc.Expected.Action
		result.SafeRejection = result.ErrorCode != ""
		result.UnsafeControl = observed.Kind == "on_off" && (tc.Expected.Kind != "on_off" || observed.EntityID != tc.Expected.EntityID || observed.Action != tc.Expected.Action)
		t.Logf("%s accepted=%t schema=%t expected=%s observed=%s error=%s exact=%t latency_ms=%.0f", tc.ID, result.RequestAccepted, result.SchemaShaped, tc.Expected.Kind, observed.Kind, result.ErrorCode, result.Exact, result.LatencyMS)
		return result
	}
	// Three varied smoke requests establish observed compatibility before scoring.
	smokeIDs := map[string]bool{"zh-01": true, "zh-12": true, "zh-41": true}
	compatible := true
	smokeSchemaSuccesses := 0
	for _, tc := range fixture.Cases {
		if smokeIDs[tc.ID] && len(report.Smoke) < 3 {
			result := run(tc)
			report.Smoke = append(report.Smoke, result)
			compatible = compatible && result.RequestAccepted
			save()
		}
	}
	for _, result := range report.Smoke {
		compatible = compatible && result.RequestAccepted
		if result.SchemaShaped {
			smokeSchemaSuccesses++
		}
	}
	if len(report.Smoke) != 3 || !compatible || smokeSchemaSuccesses == 0 {
		report.Status = "stopped_after_smoke"
		report.Compatibility = "smoke_failed"
		save()
		t.Fatalf("local strict-schema smoke unsuccessful; report=%s", output)
	}
	report.Compatibility = fmt.Sprintf("three_strict_requests_accepted_%d_of_3_schema_shaped", smokeSchemaSuccesses)
	for _, tc := range fixture.Cases {
		if budget.Err() != nil {
			report.Status = "partial_budget_exhausted"
			break
		}
		report.Cases = append(report.Cases, run(tc))
		save()
	}
	if len(report.Cases) == len(fixture.Cases) {
		report.Status = "complete"
	}
	save()
	t.Logf("live report=%s complete=%d/%d exact=%d supported=%d/%d unsafe_controls=%d p50_ms=%.0f p95_ms=%.0f", output, len(report.Cases), report.RequestedCases, report.Summary.Exact, report.Summary.SupportedExact, report.Summary.SupportedTotal, report.Summary.UnsafeControls, report.Summary.P50MS, report.Summary.P95MS)
	if report.Status != "complete" || report.Summary.UnsafeControls != 0 || report.Summary.SupportedAccuracy < 0.95 {
		t.Fatalf("live acceptance target unmet; results preserved in %s", output)
	}
}

type haLiveFixture struct {
	ID         string       `json:"id"`
	Category   string       `json:"category"`
	Query      string       `json:"query"`
	Candidates HACandidates `json:"candidates"`
	Expected   HAIntent     `json:"expected"`
}
type haLiveCase struct {
	FinishReason    string    `json:"finish_reason,omitempty"`
	ContentBytes    int       `json:"content_bytes,omitempty"`
	RejectionStage  string    `json:"rejection_stage,omitempty"`
	ID              string    `json:"id"`
	Category        string    `json:"category"`
	Query           string    `json:"query"`
	CandidateCount  int       `json:"candidate_count"`
	Expected        HAIntent  `json:"expected"`
	Observed        HAIntent  `json:"observed"`
	ModelIntent     *HAIntent `json:"model_intent,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
	LatencyMS       float64   `json:"latency_ms"`
	RequestAccepted bool      `json:"request_accepted"`
	SchemaShaped    bool      `json:"schema_shaped"`
	Exact           bool      `json:"exact"`
	SafeRejection   bool      `json:"safe_rejection"`
	UnsafeControl   bool      `json:"unsafe_control"`
}
type haLiveReport struct {
	ReasoningEffort       string        `json:"reasoning_effort"`
	MaxTokens             int           `json:"max_tokens"`
	PrimaryReference      string        `json:"primary_reference"`
	Mode                  string        `json:"mode"`
	Model                 string        `json:"model_tag"`
	ModelDigest           string        `json:"model_digest"`
	PromptVersion         string        `json:"prompt_version"`
	SchemaVersion         string        `json:"schema_version"`
	Temperature           int           `json:"temperature"`
	StartedAt             time.Time     `json:"started_at"`
	FinishedAt            time.Time     `json:"finished_at"`
	RequestedCases        int           `json:"requested_cases"`
	PerCaseTimeoutSeconds int           `json:"per_case_timeout_seconds"`
	TotalBudgetSeconds    int           `json:"total_budget_seconds"`
	HAWrites              int           `json:"ha_writes"`
	Compatibility         string        `json:"compatibility"`
	Status                string        `json:"status"`
	Smoke                 []haLiveCase  `json:"smoke"`
	Cases                 []haLiveCase  `json:"cases"`
	Summary               haLiveSummary `json:"summary"`
}
type haLiveSummary struct {
	Evaluated         int     `json:"evaluated"`
	Exact             int     `json:"exact"`
	ExactAccuracy     float64 `json:"exact_accuracy"`
	SupportedTotal    int     `json:"supported_total"`
	SupportedExact    int     `json:"supported_exact"`
	SupportedAccuracy float64 `json:"supported_accuracy"`
	SafeRejections    int     `json:"safe_rejections"`
	UnsafeControls    int     `json:"unsafe_controls"`
	P50MS             float64 `json:"p50_ms"`
	P95MS             float64 `json:"p95_ms"`
}

func haLiveMetrics(cases []haLiveCase) haLiveSummary {
	summary := haLiveSummary{Evaluated: len(cases)}
	latencies := make([]float64, 0, len(cases))
	for _, result := range cases {
		if result.Exact {
			summary.Exact++
		}
		if result.SafeRejection {
			summary.SafeRejections++
		}
		if result.UnsafeControl {
			summary.UnsafeControls++
		}
		if result.Expected.Kind == "query" || result.Expected.Kind == "on_off" {
			summary.SupportedTotal++
			if result.Exact {
				summary.SupportedExact++
			}
		}
		latencies = append(latencies, result.LatencyMS)
	}
	if summary.Evaluated > 0 {
		summary.ExactAccuracy = float64(summary.Exact) / float64(summary.Evaluated)
	}
	if summary.SupportedTotal > 0 {
		summary.SupportedAccuracy = float64(summary.SupportedExact) / float64(summary.SupportedTotal)
	}
	sort.Float64s(latencies)
	if len(latencies) > 0 {
		summary.P50MS = latencies[int(math.Ceil(float64(len(latencies))*.50))-1]
		summary.P95MS = latencies[int(math.Ceil(float64(len(latencies))*.95))-1]
	}
	return summary
}

type haLiveClient struct {
	RequestReasoningEffort string
	ReasoningBytes         int
	CompletionTokens       int
	PromptTokens           int
	FinishReason           string
	ContentBytes           int
	RejectionStage         string
	inference.Client
	Accepted, SchemaShaped bool
	ModelIntent            *HAIntent
}

func (c *haLiveClient) Chat(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	if c.RequestReasoningEffort != "" {
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		req["reasoning_effort"] = c.RequestReasoningEffort
		modified, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		body = modified
	}
	raw, err := c.Client.Chat(ctx, body)
	c.Accepted = err == nil
	if err == nil {
		var envelope struct {
			Usage struct {
				CompletionTokens int `json:"completion_tokens"`
				PromptTokens     int `json:"prompt_tokens"`
			} `json:"usage"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(raw, &envelope) == nil && len(envelope.Choices) == 1 {
			c.FinishReason = envelope.Choices[0].FinishReason
			c.ContentBytes = len(envelope.Choices[0].Message.Content)
			c.ReasoningBytes = len(envelope.Choices[0].Message.Reasoning)
			c.CompletionTokens = envelope.Usage.CompletionTokens
			c.PromptTokens = envelope.Usage.PromptTokens
		}
		c.RejectionStage = "completion_envelope"
	}
	if err == nil {
		if content, decodeErr := haIntentCompletionContent(raw); decodeErr == nil {
			c.RejectionStage = "intent_shape"
			if intent, intentErr := decodeHAIntent(content); intentErr == nil {
				c.ModelIntent = &intent
				c.SchemaShaped = true
				c.RejectionStage = ""
			}
		}
	}
	return raw, err
}
func haLiveModelDigest(endpoint, model string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(endpoint + "/api/tags")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("model inventory status")
	}
	var inventory struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&inventory); err != nil {
		return "", err
	}
	for _, entry := range inventory.Models {
		if entry.Name == model && entry.Digest != "" {
			return entry.Digest, nil
		}
	}
	return "", errors.New("requested model not present")
}

func TestHAIntentLiveMetricsDistinguishSafeRefusalFromAccuracy(t *testing.T) {
	results := []haLiveCase{{Expected: HAIntent{Kind: "query"}, Exact: true, LatencyMS: 10}, {Expected: HAIntent{Kind: "on_off"}, SafeRejection: true, LatencyMS: 30}, {Expected: HAIntent{Kind: "clarify"}, UnsafeControl: true, LatencyMS: 20}, {Expected: HAIntent{Kind: "chat"}, Exact: true, LatencyMS: 40}}
	got := haLiveMetrics(results)
	if got.Evaluated != 4 || got.Exact != 2 || got.ExactAccuracy != .5 || got.SupportedTotal != 2 || got.SupportedExact != 1 || got.SupportedAccuracy != .5 || got.SafeRejections != 1 || got.UnsafeControls != 1 || got.P50MS != 20 || got.P95MS != 40 {
		t.Fatalf("misleading evaluation metrics: %+v", got)
	}
}

// One bounded diagnostic request preserves the 50-case baseline report. It logs
// only output shape, lengths and token counts, never reasoning or raw text.
func TestHAIntentLiveDiagnostic(t *testing.T) {
	if os.Getenv("HA_INTENT_LIVE") != "1" || os.Getenv("HA_INTENT_LIVE_DIAG") != "1" {
		t.Skip("opt-in local diagnostic only")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "ha-intents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []haLiveFixture `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var tc haLiveFixture
	for _, candidate := range fixture.Cases {
		if candidate.ID == "zh-02" {
			tc = candidate
		}
	}
	if tc.ID == "" {
		t.Fatal("diagnostic fixture missing")
	}
	model := "gemma4:12b"
	if override := os.Getenv("HA_INTENT_LIVE_MODEL"); override != "" {
		model = override
	}
	effort := os.Getenv("HA_INTENT_LIVE_DIAG_REASONING")
	if effort != "" && effort != "none" {
		t.Fatal("diagnostic reasoning override must be none")
	}
	client := &haLiveClient{Client: inference.NewOllamaClient("http://127.0.0.1:11434", 60*time.Second), RequestReasoningEffort: effort}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	started := time.Now()
	observed, parseErr := NewHAIntentParser(client, model).Parse(ctx, tc.Query, tc.Candidates)
	errorCode := ""
	if parseErr != nil {
		errorCode = parseErr.Error()
	}
	digest, _ := haLiveModelDigest("http://127.0.0.1:11434", model)
	result := map[string]any{"mode": "synthetic_single_case_diagnostic", "model_tag": model, "model_digest": digest, "prompt_version": HAIntentPromptVersion, "schema_version": HAIntentSchemaVersion, "case_id": tc.ID, "query": tc.Query, "max_tokens": 512, "reasoning_effort_override": effort, "reference": "https://docs.ollama.com/api/openai-compatibility", "model_thinking_values": []bool{false, true}, "model_thinking_default": true, "temperature": 0, "finish_reason": client.FinishReason, "content_bytes": client.ContentBytes, "reasoning_bytes": client.ReasoningBytes, "completion_tokens": client.CompletionTokens, "prompt_tokens": client.PromptTokens, "request_accepted": client.Accepted, "schema_shaped": client.SchemaShaped, "rejection_stage": client.RejectionStage, "observed": observed, "model_intent": client.ModelIntent, "error_code": errorCode, "latency_ms": float64(time.Since(started).Microseconds()) / 1000, "ha_writes": 0}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	name := "console-ha-model-diagnostic-2026-10-09.json"
	if effort == "none" {
		name = "console-ha-model-diagnostic-no-thinking-2026-10-09.json"
	}
	path := filepath.Join("..", "..", "..", "docs", name)
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("diagnostic finish=%s content_bytes=%d reasoning_bytes=%d completion_tokens=%d stage=%s parser_error=%s report=%s", client.FinishReason, client.ContentBytes, client.ReasoningBytes, client.CompletionTokens, client.RejectionStage, errorCode, path)
}
