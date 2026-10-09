---
title: RAG Pipeline Specification
category: specs
tags: [rag, retrieval, chunking, embedding, bm25, rrf, vault]
created: 2026-07-08
updated: 2026-09-30
---

# RAG Pipeline Specification

This describes the Go chain implementation. Requirements and test evidence live
in [the plan](../../docs/superpowers/plans/2026-09-30-p1-p2-remediation.md) and
[the results](../../docs/p1-p2-remediation-results-2026-09-30.md).

## Routing and Vault boundaries

The `chat` chain asks a `TriggerEntityProvider` for entities in the request's
target Vault on every decision. `FileReader` implements the provider by reading
current Markdown metadata, reusing existing category and title rules. Adding,
editing, deleting, hiding, or repairing a page affects the next request, including
edits preserving file size and modification time. There is no additional trigger
cache, TTL, or file watcher.

Matching entities continue into retrieval; nonmatching queries use `direct`.
The explicit `wiki-query` skill selects `rag-answer`, bypassing entity matching
while retaining Vault selection and visibility checks. The existing trigger rule
sends queries of two or fewer characters to the direct path.

Gateway and channel adapters determine access scope before execution. Internal
REST chat uses the personal Vault; external channels use the agent Vault. Chain
state carries that selection into sparse and dense search. Provider failures
never fall back into another Vault. Bootstrap passes the same concrete
`FileReader` to retrieval and entity routing.

```text
request + authorized Vault
  -> current Vault entity decision (or explicit wiki-query)
  -> BM25 + scoped embedding search
  -> reciprocal rank fusion
  -> reread page and enforce metadata visibility
  -> preprocess body + assemble context
  -> model request, output filtering, response with sources
```

## Metadata and privacy

`ParsePage` returns `ErrInvalidFrontmatter` for malformed YAML, duplicate keys,
unclosed delimiters, and invalid types in supported metadata fields. Invalid
pages cannot supply search snippets, dense chunks, citations, trigger entities,
or model context. Sparse search rejects invalid candidates; dense indexing and
memory processing skip only the typed parse error and propagate operational
errors. Dense indexing excludes internal pages before embedding.

Ordinary Markdown without frontmatter remains valid. UTF-8 BOM, CRLF, and a
closing delimiter at EOF are supported. `ParsePage` does not invent a title;
path-aware read/search uses the filename if the title is absent. Filename display
fallback does not create an entity trigger. Unknown metadata keys remain valid.

`vault.IsInternalPage` excludes pages tagged `internal` or `visibility/internal`.
Categories `concept` and `concepts` require at least one public-use tag: `rag`,
`user-facing`, `knowledge-base`, or `game`. Retrieval rereads the page before
adding it to chain sources, so persisted search caches cannot bypass this rule.

Directory walks omit hidden and underscore-prefixed directories and existing
system files such as `index.md`, `log.md`, `hot.md`, and `AGENTS.md`. Root-scoped
filesystem access rejects traversal and symlink escapes.

## Sparse retrieval and cache integrity

BM25 uses `k1=1.5`, `b=0.75`, CJK bigram/ASCII tokenization, and numeric entity
recognition. Queries with up to two tokens require one matching token; longer
queries require two. It scores full documents, not semantic chunks.

The inverted index persists as `<vault>/.rag-cache/bm25.json`. Validation checks a
complete content-hashed Markdown manifest, detecting additions, deletions, and
same-size/same-mtime edits. Each query still reads files for this manifest; cached
token statistics avoid repeated tokenization and index assembly. Invalid/internal
pages retain manifest entries with zero indexed length and no postings, avoiding
a permanent mismatch and rebuild loop. Search also rereads candidate metadata.

## Dense retrieval and lifecycle

Each Vault has a separate `EmbeddingStore`, configured model, and
`<vault>/.rag-cache/embeddings.json`. Cache reuse checks version, model, Vault,
chunk identity, and content hash. Each query checks the full manifest. Changes
cause atomic rebuild publication with reuse of unchanged chunk vectors. Failed
or cancelled builds remain retryable.

Application startup warms both stores with a cancellable context (up to five
minutes per warmup). Search also indexes lazily when needed. A warmup failure
does not disable later retries. Cosine similarity must exceed `0.3`; sorted
results are limited before fusion. If dense search fails, sparse search can
still answer. If both fail, the chain reports retrieval failure.

Chunking uses H2 boundaries, a 1500-rune target, and 150-rune overlap from the
previous section. In long-page mode, sections below 100 runes are skipped while
their tail can bridge into the next section; short pages and the all-short-section
fallback preserve the body. Long sections split at paragraph boundaries.
A single very long paragraph still has no hard cap; H3 headings remain inside
their parent section. These remain documented limitations.

## Fusion, context, and citations

Reciprocal rank fusion combines ranks with `score(d) = sum(1 / (k + rank(d)))`.
Configuration defaults: `rrf_k=60`, `top_k=5`, and `max_chunk_chars=800`. The chain
applies current metadata visibility before preprocessing sources and assembling
system context. Sparse-only hits select a matching body chunk; dense hits provide
their matching chunk. Context accompanies original conversation messages.

REST exposes source `title`, Vault-relative `path`, and `score`. The WebSocket
handler buffers generation, filters and persists the complete answer, then sends
a final response; it currently emits no source/context frames. A nonempty answer
alone is insufficient retrieval evidence: tests observe the unique fixture fact in the
actual model request and the expected path in returned sources, while excluding
another Vault's facts.

## Implementation and verification map

| Responsibility | Implementation |
|---|---|
| Strict metadata, scoped I/O, current entities | `internal/vault/reader.go` |
| Visibility policy | `internal/vault/page_filter.go` |
| Content manifests, rooted filesystem access | `internal/vault/filesystem.go` |
| Sparse cache and tokenizer | `internal/vault/inverted.go`, `tokenizer.go` |
| Dense cache, scoped search, warmup | `internal/vault/embedding.go` |
| H2 chunks and overlap | `internal/vault/chunker.go` |
| Dynamic entity decision | `internal/chain/go_steps_decide.go` |
| Retrieval, filtering, context | `internal/chain/go_steps.go` |
| Model request and source streaming | `internal/chain/llm_steps.go` |
| Composition and wiring | `internal/chain/chains.go`, `internal/core/app.go` |

Offline Go tests use temporary Vaults and mock inference for chat and forced
retrieval, malformed/internal metadata, cache reload, and live file changes.
The live `tests/integration/phase1_5_full_chain.sh` requires `RAG_TEST_QUERY`,
`RAG_EXPECTED_SOURCE_PATH`, `RAG_EXPECTED_TEXT`, and the existing explicit internal
key. It JSON-encodes the query and requires both unique fact and source path in
the response. It does not populate real Vaults. Python fixtures intercept every
curl call; validation requires no running model, HA instance, or network.
