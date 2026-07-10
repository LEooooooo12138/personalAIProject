package chain

import (
	"context"
	"fmt"

	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
)

// Pre-built Chain Library
//
// These are ready-to-use chain compositions. Each function constructs
// a complete chain from steps and registers it in the router.

// BuildRAGChatChain creates the main chat chain with deterministic entity-based routing.
func BuildRAGChatChain(
	vr vault.Reader,
	infer inference.Client,
	model string,
	embedStore EmbeddingSearcher,
	triggerEntities []string,
	logger *zap.Logger,
) (*ChainRouter, error) {

	router := NewChainRouter()

	mainChain := NewChain(
		"chat",
		"entity-trigger decision + RAG pipeline",
		NewEntityTriggerDecideStep(triggerEntities, logger),
		NewVaultSearchStep(vr, embedStore, 5, logger),
		NewPagePreprocessStep(1000),
		NewContextAssemblyStep(5),
		NewLLMAnswerStep(infer, model, 0.7, logger),
	)
	router.Register("chat", mainChain)

	directChain := NewChain(
		"direct",
		"direct LLM answer (no knowledge base)",
		NewLLMSimpleAnswerStep(infer, model, logger),
	)
	router.Register("direct", directChain)

	ragChain := NewChain(
		"rag-answer",
		"force search + LLM answer",
		NewVaultSearchStep(vr, embedStore, 5, logger),
		NewPagePreprocessStep(1000),
		NewContextAssemblyStep(5),
		NewLLMAnswerStep(infer, model, 0.7, logger),
	)
	router.Register("rag-answer", ragChain)

	return router, nil
}

func BuildWikiIngestChain(
	vr vault.Reader,
	vw vault.Writer,
	infer inference.Client,
	model string,
	personalPath string,
	agentPath string,
	logger *zap.Logger,
) (*ChainRouter, error) {

	router := NewChainRouter()

	ingestChain := NewChain(
		"wiki-ingest",
		"wiki ingest: distill -> write -> index",
		NewLLMIngestStep(infer, model, logger),
		NewVaultWriteStep(vw, logger),
		NewIndexUpdateStep(vw, vr, personalPath, agentPath, logger),
		NewFuncStep("ingest-finalize", func(ctx context.Context, state *ChainState) error {
			path, _ := state.GetString("written_path")
			title, _ := state.GetString("written_title")
			cat, _ := state.GetString("written_category")
			if title != "" {
				state.FinalAnswer = fmt.Sprintf("Done: %s/%s (%s)", cat, title, path)
			} else {
				wikiOutput, _ := state.GetString("wiki_output")
				state.FinalAnswer = wikiOutput
			}
			return nil
		}),
	)
	router.Register("wiki-ingest", ingestChain)

	return router, nil
}

func BuildSummarizeChain(
	infer inference.Client,
	model string,
	logger *zap.Logger,
) (*ChainRouter, error) {

	router := NewChainRouter()

	summarizeChain := NewChain(
		"summarize",
		"memory summarization via LLM",
		NewLLMSummarizeStep(infer, model, logger),
	)
	router.Register("summarize", summarizeChain)

	return router, nil
}

func BuildSynthesizeChain(
	vr vault.Reader,
	infer inference.Client,
	model string,
	embedStore EmbeddingSearcher,
	logger *zap.Logger,
) (*ChainRouter, error) {

	router := NewChainRouter()

	synthesizeChain := NewChain(
		"synthesize",
		"cross-concept synthesis: search -> LLM analysis",
		NewVaultSearchStep(vr, embedStore, 10, logger),
		NewPagePreprocessStep(1500),
		NewLLMSynthesizeStep(infer, model, logger),
	)
	router.Register("synthesize", synthesizeChain)

	return router, nil
}

func BuildCrossLinkChain(
	vr vault.Reader,
	infer inference.Client,
	model string,
	embedStore EmbeddingSearcher,
	logger *zap.Logger,
) (*ChainRouter, error) {

	router := NewChainRouter()

	crossLinkChain := NewChain(
		"cross-link",
		"cross-link discovery: search -> LLM analysis",
		NewVaultSearchStep(vr, embedStore, 8, logger),
		NewPagePreprocessStep(1000),
		NewLLMCrossLinkStep(infer, model, logger),
	)
	router.Register("cross-link", crossLinkChain)

	return router, nil
}

type ChainDeps struct {
	VaultReader     vault.Reader
	VaultWriter     vault.Writer
	Infer           inference.Client
	Model           string
	EmbedStore      EmbeddingSearcher
	PersonalPath    string
	AgentPath       string
	Logger          *zap.Logger
	TriggerEntities []string
}

func BuildAllChains(deps ChainDeps) (*ChainRouter, error) {
	router := NewChainRouter()

	chatRouter, err := BuildRAGChatChain(
		deps.VaultReader, deps.Infer, deps.Model,
		deps.EmbedStore, deps.TriggerEntities, deps.Logger,
	)
	if err != nil {
		return nil, err
	}
	mergeRouters(router, chatRouter)

	ingestRouter, err := BuildWikiIngestChain(
		deps.VaultReader, deps.VaultWriter, deps.Infer, deps.Model,
		deps.PersonalPath, deps.AgentPath, deps.Logger,
	)
	if err != nil {
		return nil, err
	}
	mergeRouters(router, ingestRouter)

	sumRouter, err := BuildSummarizeChain(deps.Infer, deps.Model, deps.Logger)
	if err != nil {
		return nil, err
	}
	mergeRouters(router, sumRouter)

	synthRouter, err := BuildSynthesizeChain(
		deps.VaultReader, deps.Infer, deps.Model,
		deps.EmbedStore, deps.Logger,
	)
	if err != nil {
		return nil, err
	}
	mergeRouters(router, synthRouter)

	clRouter, err := BuildCrossLinkChain(
		deps.VaultReader, deps.Infer, deps.Model,
		deps.EmbedStore, deps.Logger,
	)
	if err != nil {
		return nil, err
	}
	mergeRouters(router, clRouter)

	return router, nil
}

func mergeRouters(dst, src *ChainRouter) {
	for name, chain := range src.routes {
		dst.Register(name, chain)
	}
}

type EmbeddingStoreAdapter struct {
	searchFn func(ctx context.Context, query string, k int) ([]EmbeddingHit, error)
}

func NewEmbeddingStoreAdapter(
	searchFn func(ctx context.Context, query string, k int) ([]EmbeddingHit, error),
) *EmbeddingStoreAdapter {
	return &EmbeddingStoreAdapter{searchFn: searchFn}
}

func (a *EmbeddingStoreAdapter) Search(ctx context.Context, query string, k int) ([]EmbeddingHit, error) {
	if a.searchFn == nil {
		return nil, nil
	}
	return a.searchFn(ctx, query, k)
}
