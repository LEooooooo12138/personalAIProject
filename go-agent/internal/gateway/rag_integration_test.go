package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
)

type ragHTTPInference struct{ request json.RawMessage }

func (c *ragHTTPInference) Chat(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	c.request = append(json.RawMessage(nil), request...)
	return json.RawMessage(`{"choices":[{"message":{"role":"assistant","content":"FIXTURE_FACT_7391"}}]}`), nil
}
func (*ragHTTPInference) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"data":[{"embedding":[1,0]}]}`), nil
}
func (*ragHTTPInference) ListModels(context.Context) ([]inference.ModelInfo, error) { return nil, nil }

// Exercise public HTTP routing into real chains, including the wiki-query override.
// A fabricated nonempty model response cannot satisfy the request-context checks.
func TestHTTPRAGChatAndWikiQueryIncludeFactAndSource(t *testing.T) {
	personal, agent := t.TempDir(), t.TempDir()
	for _, page := range []struct{ root, name, text string }{
		{personal, "Orion.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion retrieval fixture FIXTURE_FACT_7391"},
		{personal, "bad.md", "---\ntags: internal\n---\nOrion retrieval fixture MALFORMED_SECRET"},
		{agent, "Orion.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion retrieval fixture OTHER_VAULT_FACT"},
	} {
		if err := os.WriteFile(filepath.Join(page.root, page.name), []byte(page.text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader := vault.NewFileReader(personal, agent)
	client := &ragHTTPInference{}
	logger := zap.NewNop()
	router, err := chain.BuildAllChains(chain.ChainDeps{VaultReader: reader, TriggerEntityProvider: reader, Infer: client, Model: "fixture", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:    &core.Config{Server: core.ServerConfig{InternalKey: "fixture-key", ChainTimeout: time.Second}},
		logger: logger, filterChain: filter.NewChain(), router: core.NewModelRouter("fixture", "fixture"),
		chainRouter: router, chainExecutor: chain.NewChainExecutor(logger, router),
	}
	s.setupRoutes()
	for _, tc := range []struct{ name, query, skill string }{
		{"entity chat", "Orion retrieval fixture", ""},
		// No entity name: only the explicit skill may reach retrieval here.
		{"forced wiki-query", "retrieval fixture", "wiki-query"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"model": "auto", "messages": []map[string]string{{"role": "user", "content": tc.query}}, "metadata": map[string]string{"skill": tc.skill}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer fixture-key")
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			s.engine.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var modelRequest struct {
				Messages []struct{ Role, Content string }
			}
			if err := json.Unmarshal(client.request, &modelRequest); err != nil {
				t.Fatal(err)
			}
			var contextText string
			for _, message := range modelRequest.Messages {
				if message.Role == "system" {
					contextText += message.Content
				}
			}
			if !strings.Contains(contextText, "FIXTURE_FACT_7391") || strings.Contains(contextText, "OTHER_VAULT_FACT") || strings.Contains(contextText, "MALFORMED_SECRET") {
				t.Fatalf("incorrect model context: %s", contextText)
			}
			var result struct {
				Sources []struct{ Title, Path string }
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Sources) != 1 || result.Sources[0].Path != "Orion.md" || result.Sources[0].Title != "Orion" {
				t.Fatalf("incorrect HTTP sources: %+v", result.Sources)
			}
		})
	}
}
