package chain

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingInference struct{ requests []string }

func (c *recordingInference) Chat(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	c.requests = append(c.requests, string(body))
	return json.RawMessage(`{"choices":[{"message":{"role":"assistant","content":"fixture answer"},"finish_reason":"stop"}]}`), nil
}
func (*recordingInference) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"data":[{"embedding":[1,0]}]}`), nil
}
func (*recordingInference) ListModels(context.Context) ([]inference.ModelInfo, error) {
	return nil, nil
}
func putPage(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidMetadataNeverReachesModel(t *testing.T) {
	personal, agent := t.TempDir(), t.TempDir()
	reader := vault.NewFileReader(personal, agent)
	client := &recordingInference{}
	ctx := context.Background()
	putPage(t, personal, "Orion.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion PRIVATE_OTHER_VAULT")
	putPage(t, agent, "public.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion PUBLIC_UNIQUE_FACT")
	for name, meta := range map[string]string{"bad-tags.md": "tags: internal", "bad-date.md": "tags: [internal]\ncreated: [wrong]", "duplicate.md": "tags: [internal]\ntags: []", "unclosed.md": "tags: [internal]"} {
		data := "---\ntitle: Orion\n" + meta + "\n---\nOrion MALFORMED_PRIVATE_FACT"
		if name == "unclosed.md" {
			data = "---\ntags: [internal]\nOrion MALFORMED_PRIVATE_FACT"
		}
		putPage(t, agent, name, data)
	}
	store := vault.NewScopedEmbeddingStore(client, reader, agent, "agent", "fixture", zap.NewNop())
	dense := NewVaultEmbeddingStoreAdapter(func(ctx context.Context, name, query string, k int) ([]vault.EmbeddingResult, error) {
		return store.Search(ctx, query, k)
	})
	router, err := BuildAllChains(ChainDeps{VaultReader: reader, Infer: client, TriggerEntityProvider: reader, EmbedStore: dense, Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"chat", "rag-answer"} {
		state := NewChainState("Orion", "agent", nil)
		result, err := NewChainExecutor(zap.NewNop(), router).Run(ctx, route, state)
		if err != nil || result.Error != nil {
			t.Fatalf("chain failed: %v %+v", err, result)
		}
		if len(state.Sources) != 1 || state.Sources[0].Path != "public.md" || !strings.Contains(state.Sources[0].Body, "PUBLIC_UNIQUE_FACT") {
			t.Fatalf("wrong sources: %+v", state.Sources)
		}
		request := client.requests[len(client.requests)-1]
		if !strings.Contains(request, "PUBLIC_UNIQUE_FACT") || strings.Contains(request, "PRIVATE_FACT") || strings.Contains(request, "PRIVATE_OTHER_VAULT") {
			t.Fatalf("wrong model context: %s", request)
		}
	}
	// A stale dense hit is never trusted when current disk metadata is invalid.
	putPage(t, agent, "public.md", "---\ntags: internal\n---\nOrion MALFORMED_PRIVATE_FACT")
	stale := NewVaultEmbeddingStoreAdapter(func(context.Context, string, string, int) ([]vault.EmbeddingResult, error) {
		return []vault.EmbeddingResult{{PagePath: "public.md", Title: "Orion", ChunkContent: "STALE_PRIVATE_FACT", Score: 1}}, nil
	})
	state := NewChainState("Orion", "agent", nil)
	if err := NewVaultSearchStep(reader, stale, 5, zap.NewNop()).Run(ctx, state); err != nil {
		t.Fatal(err)
	}
	if len(state.Sources) != 0 {
		t.Fatalf("stale invalid source survived: %+v", state.Sources)
	}
}
func TestEntityChainUsesCurrentTargetVault(t *testing.T) {
	personal, agent := t.TempDir(), t.TempDir()
	reader := vault.NewFileReader(personal, agent)
	client := &recordingInference{}
	putPage(t, personal, "Secret.md", "---\ntitle: Secret\ncategory: entity\n---\nPERSONAL_UNIQUE_FACT")
	router, err := BuildAllChains(ChainDeps{VaultReader: reader, Infer: client, TriggerEntityProvider: reader, Logger: zap.NewNop()})
	if err != nil {
		t.Fatal(err)
	}
	check := func(query, decision, fact string) {
		t.Helper()
		state := NewChainState(query, "agent", nil)
		result, err := NewChainExecutor(zap.NewNop(), router).Run(context.Background(), "chat", state)
		if err != nil || result.Error != nil {
			t.Fatalf("chain: %v %+v", err, result)
		}
		got, _ := state.GetString("llm_decision")
		if got != decision {
			t.Fatalf("%s: route %s want %s", query, got, decision)
		}
		request := client.requests[len(client.requests)-1]
		if strings.Contains(request, "PERSONAL_UNIQUE_FACT") {
			t.Fatal("personal content leaked")
		}
		if fact != "" && (!strings.Contains(request, fact) || len(state.Sources) != 1) {
			t.Fatalf("fact/source absent: %s %+v", request, state.Sources)
		}
		if decision == "direct" && len(state.Sources) != 0 {
			t.Fatal("direct route has sources")
		}
	}
	check("Secret", "direct", "")
	putPage(t, agent, "Orion.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion AGENT_UNIQUE_FACT")
	check("Orion", "search", "AGENT_UNIQUE_FACT")
	putPage(t, agent, "Orion.md", "---\ntitle: Lyrae\ncategory: entity\n---\nLyrae RENAMED_UNIQUE_FACT")
	check("Orion", "direct", "")
	check("Lyrae", "search", "RENAMED_UNIQUE_FACT")
	if err := os.Remove(filepath.Join(agent, "Orion.md")); err != nil {
		t.Fatal(err)
	}
	check("Lyrae", "direct", "")
	for _, meta := range []string{"tags: [internal]", "tags: internal"} {
		putPage(t, agent, "Orion.md", "---\ntitle: Orion\ncategory: entity\n"+meta+"\n---\nOrion HIDDEN_FACT")
		check("Orion", "direct", "")
	}
	putPage(t, agent, "Orion.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion REPAIRED_UNIQUE_FACT")
	check("Orion", "search", "REPAIRED_UNIQUE_FACT")
	if err := os.RemoveAll(personal); err != nil {
		t.Fatal(err)
	}
	check("Orion", "search", "REPAIRED_UNIQUE_FACT")
}
func TestContextAssemblyRejectsInternalSources(t *testing.T) {
	state := NewChainState("query", "agent", nil)
	state.Sources = []VaultSource{{Title: "secret", Body: "INTERNAL_FACT", Tags: []string{"internal"}}, {Title: "public", Body: "PUBLIC_FACT"}}
	if err := NewContextAssemblyStep(5).Run(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	prompt, _ := state.GetString("system_prompt")
	if strings.Contains(prompt, "INTERNAL_FACT") || !strings.Contains(prompt, "PUBLIC_FACT") {
		t.Fatalf("invalid final context: %s", prompt)
	}
}

type readErrorReader struct {
	*vault.FileReader
	failure error
}

func (r readErrorReader) ReadPage(context.Context, string, string) (*vault.Page, error) {
	return nil, r.failure
}
func TestVaultSearchPropagatesPageReadFailures(t *testing.T) {
	root := t.TempDir()
	putPage(t, root, "public.md", "needle")
	for _, failure := range []error{os.ErrPermission, context.Canceled} {
		reader := readErrorReader{vault.NewFileReader(root, root), failure}
		if err := NewVaultSearchStep(reader, nil, 5, zap.NewNop()).Run(context.Background(), NewChainState("needle", "agent", nil)); !errors.Is(err, failure) {
			t.Fatalf("page read failure swallowed: %v want %v", err, failure)
		}
	}
}
