---
title: RAG Pipeline Specification
category: specs
tags: [rag, retrieval, chunking, embedding, bm25, rrf, vault]
created: 2026-07-08
updated: 2026-07-08
---

# RAG Pipeline Specification

## 1. Architecture Overview

The Retrieval-Augmented Generation pipeline enriches every chat message with
relevant context from the Obsidian wiki vault before sending the prompt to the LLM.

```
User message
    │
    ▼
┌─────────────────────────────────────────────────────┐
│                  retrieveVaultContext()               │
│                                                       │
│   ┌──────────────┐    ┌──────────────────┐           │
│   │ BM25 Search  │    │ Embedding Search │           │
│   │  (sparse)    │    │    (dense)       │           │
│   │  vault/bm25  │    │  gateway/rag     │           │
│   └──────┬───────┘    └────────┬─────────┘           │
│          │                     │                      │
│          └─────────┬───────────┘                      │
│                    ▼                                  │
│          ┌─────────────────┐                          │
│          │   RRF Fusion    │                          │
│          │    (k=60)       │                          │
│          └────────┬────────┘                          │
│                   ▼                                   │
│          ┌─────────────────┐                          │
│          │ Metadata Filter │                          │
│          │ (internal skip) │                          │
│          └────────┬────────┘                          │
│                   ▼                                   │
│          ┌─────────────────┐                          │
│          │  Top-5 Pages    │                          │
│          │  Read Full Body │                          │
│          └────────┬────────┘                          │
│                   ▼                                   │
│           vaultSource[]  →  system prompt injection  │
└─────────────────────────────────────────────────────┘
    │
    ▼
LLM Chat (with history + vault context as system message)
```

### Component Map

| Component | File | Role |
|-----------|------|------|
| Tokenizer | `vault/tokenizer.go` | Bigram + numeric entity tokenization |
| BM25 Search | `vault/bm25.go` | Sparse keyword retrieval |
| Chunker | `vault/chunker.go` | Page → semantic chunks |
| Embedding Store | `gateway/rag.go` | Dense semantic search via bge-m3 |
| RRF Fusion | `gateway/websocket.go` | Merges sparse + dense results |
| Metadata Filter | `gateway/websocket.go` | Excludes internal pages |
| Context Injection | `gateway/websocket.go` | System prompt prepending |
| WebSocket Handler | `gateway/websocket.go` | Orchestrates the full pipeline |

---

## 2. Chunking Strategy

### 2.1 Algorithm: Heading-Boundary Segmentation

Pages are split at `## ` (H2) heading boundaries. The rationale:
- H2 headings typically mark semantic topic shifts within a page.
- Each chunk carries its section title as metadata (`SectionTitle`), which helps
  the LLM understand the provenance of retrieved content.
- The chunk ID is `"{PageTitle}#section-{N}"`, providing human-readable provenance.

### 2.2 Parameters

| Parameter | Value | Meaning |
|-----------|-------|---------|
| `chunkMaxLen` | 1500 chars | Pages shorter than this stay as single chunk |
| `chunkMinLen` | 100 chars | Sections shorter than this are dropped (noise) |

### 2.3 Splitting Rules

1. **Short page** (≤1500 chars): One chunk, entire body.
2. **Long page, with H2s**: Split at each `## heading`. If any section exceeds 1500
   chars, it is further split at paragraph boundaries (double newline).
3. **Long page, no H2s**: Split at paragraph boundaries, with 1500-char window.

### 2.4 Chunk Metadata

Each `Chunk` carries:

| Field | Source | Purpose |
|-------|--------|---------|
| `ID` | `"{PageTitle}#section-{N}"` | Unique identifier |
| `PagePath` | Relative vault path | Trace back to source file |
| `PageTitle` | YAML frontmatter `title` | Display and provenance |
| `SectionTitle` | H2 heading text | Semantic context |
| `Content` | Chunk body | Embedding input / retrieval payload |
| `Tags` | YAML frontmatter `tags` | Metadata filtering |
| `Category` | YAML frontmatter `category` | Metadata filtering |

### 2.5 Chunking Is Performed

- **On embedding index build** — once per startup (lazy, on first query).
- BM25 search operates on **full pages** (unchunked), reading files directly.
  This is by design: BM25 needs term-frequency statistics over the whole document.

### 2.6 Current Limitations

- **No overlap between chunks.** A sentence spanning a heading boundary is split
  mid-thought. Production RAG systems typically use 10-20% overlap to prevent
  boundary artifacts.
- **No chunk-size cap within sections.** A very long section without paragraph
  breaks will produce a single giant chunk. Should add a hard cap at ~2000 chars
  with overlapping sliding window.
- **H2-only splitting.** H3+ headings are ignored. Content under an H3 is merged
  into the H2 section body.

---

## 3. Tokenization

### 3.1 Query Tokenizer (`tokenizeQuery`)

Used for BM25 search queries. Pipeline:

1. **Numeric entity extraction**: `"2020年"` → placeholder `__ENT0__`; `"8080端口"` → `__ENT1__`
   - Recognizes digits + CJK measure words (年,月,日,时,分,秒,岁,个,次,倍,层,楼,号,端,口,元,块,万字,小时,分钟)
2. **Segmentation**: Split into CJK runs vs ASCII runs.
3. **Bigram**: CJK segments → sliding 2-char windows (`"姚远乐"` → `["姚远","远乐"]`)
4. **ASCII tokenization**: Whitespace splitting, punctuation removal, min 2 chars.
5. **Entity restoration**: Replace placeholders with original entities.
6. **Stop-word filtering**: Remove 50+ CJK stop-words (的,是,在,我...) + 50+ English stop-words.
7. **Deduplication**: Each token appears once.

### 3.2 Document Tokenizer (`tokenize`)

Used for BM25 document indexing. Same as query tokenizer but **keeps all tokens**
(including stop-words) for accurate TF-IDF statistics. Stop-word filtering would
distort BM25's document-length normalization.

### 3.3 Tokenizer Limitations

- **No dictionary-based segmentation** for Chinese. Bigram is a coarse approximation;
  a word like `"人工智能"` becomes `["人工","工智","智能"]`, diluting the signal.
  A jieba-like dictionary tokenizer would improve precision for common compound words.
- **Punctuation handling is inconsistent.** Some CJK punctuation passes through
  the CJK/ASCII boundary detection, creating meaningless bigrams.
- **Numeric entity regex is rune-based, not regex-based**, which misses patterns
  like `"3.14分"` (decimal points).

---

## 4. BM25 Search (Sparse Retrieval)

### 4.1 Algorithm: Okapi BM25

Standard Okapi BM25 with parameters:

| Parameter | Value | Meaning |
|-----------|-------|---------|
| k1 | 1.5 | Term frequency saturation |
| b | 0.75 | Document length normalization |

### 4.2 Two-Pass Algorithm

**Pass 1 — Corpus Scan**
- Walk vault directory, skip dot-prefixed dirs, underscore-prefixed dirs, system files.
- For each `.md` file: read, lowercase, tokenize, compute per-doc term frequencies.
- Track: docFreq (how many docs contain each term), total corpus length.
- **Early filtering**: A document must contain at least `minMatchCount(tokens)`
  distinct query tokens. For queries with ≤2 tokens, minMatchCount=1; for 3+ tokens, minMatchCount=2.

**Pass 2 — Scoring**
- Compute IDF per term: `log((N - n + 0.5) / (n + 0.5) + 1.0)`
- Score each doc: `Σ IDF * (f*(k1+1)) / (f + k1*(1-b + b*dl/avgdl))`
- Sort descending. Only scores > 0 are included.

### 4.3 Performance Characteristics

- **O(N × T)** where N = vault page count, T = distinct tokens in each page.
- **Reads every `.md` file on every query.** No caching, no pre-built index.
  For a vault with 50 pages × 5KB each, this is 250KB of I/O per query — fast enough.
  For 10,000 pages, unacceptable.
- **Ideal for vaults with < 200 pages.** Beyond that, needs an inverted-index cache.

### 4.4 Results Returned As

```go
[]SearchResult{
    Path:    "entities/姚远乐.md",
    Title:   "姚远乐",       // derived from filename, NOT frontmatter title
    Snippet: "..."           // 80-char window around first token match
}
```

### 4.5 Known Issue: Title Resolution

In `reader.go:Search()`, the title is `strings.TrimSuffix(filepath.Base(br.Path), ".md")`
— it uses the **filename** as the title, not the YAML frontmatter `title` field.
If the filename is `resume-2020.md`, the "title" reported to the user is `resume-2020`.

**Fix**: Read the frontmatter title or use `page.Title` after parsing.

---

## 5. Embedding Search (Dense Retrieval)

### 5.1 Model: bge-m3 (via Ollama)

- BGE-M3 is BAAI's multilingual embedding model supporting dense, sparse, and
  ColBERT-style retrieval.
- Called via Ollama's `/api/embed` endpoint.
- Returns a `[]float32` embedding vector.

### 5.2 Index Lifecycle

```
Agent Start
    │
    ▼
EmbeddingStore.indexed = false, chunks = nil
    │
    ▼ (first query arrives)
ensureIndexed()
    │
    ├── Walk vault: read all .md files, chunk each
    ├── For each chunk: call Ollama /api/embed
    ├── Store in memory: []embeddedChunk
    └── Set indexed = true
    │
    ▼
All subsequent queries: cosine similarity against cached embeddings
```

### 5.3 Cosine Similarity Search

- Query is embedded with the same `bge-m3` model.
- Cosine similarity: `dot(a,b) / (|a| × |b|)`
- **Minimum relevance threshold: 0.3** — embeddings below this are discarded.
- Results sorted descending, limited to `k` (usually 10, passed to RRF).

### 5.4 Performance

- **Index build**: walks entire vault + embeds every chunk. With Ollama on localhost,
  ~2-3 seconds for a 10-page vault.
- **Query**: one Ollama embed call (~100ms) + cosine similarity scan (~1ms for 500 chunks).
- **Memory**: Each embedding is 1024 × float32 = 4KB. 500 chunks = 2MB. Negligible.

### 5.5 Known Issues

1. **No persistence.** Embeddings are in-memory only. Restart = full rebuild.
2. **No incremental updates.** New pages added after index build are invisible.
3. **No cache warming at startup.** First user query after restart incurs 2-3s latency.
4. **Single embedding size assumption.** If a different model with different dimensions
   is used, everything breaks silently.

---

## 6. RRF (Reciprocal Rank Fusion)

### 6.1 Algorithm

Merges BM25 (sparse) and embedding (dense) result lists into a single ranked list.

```
score(d) = Σ 1/(k + rank_i(d))
```

Where `k = 60` (standard value from the literature).

### 6.2 Process

1. BM25 results assigned ranks 1, 2, 3... by position in sorted result list.
2. Embedding results assigned ranks 1, 2, 3... by position.
3. Each document's RRF score = sum of `1/(60 + rank)` from both lists.
4. Sort descending by RRF score.

### 6.3 Rationale

RRF is the de facto standard for hybrid search fusion because:
- **No score normalization needed.** BM25 scores and cosine similarities have
  different scales; RRF is scale-free.
- **Rewards consensus.** A document that appears in both lists (even at low rank)
  gets a higher score than a document that appears only in one list at high rank.
- **Simple and deterministic.** No tunable weights to maintain.

### 6.4 Top-N Truncation

After RRF fusion, results are:
1. Filtered by `isInternalPage()` (visibility/internal tags, concepts/ category)
2. Truncated to top 5
3. Full page body read from disk (truncated to 800 chars for prompt injection)

---

## 7. Metadata Filtering

### 7.1 Internal Page Exclusion

`isInternalPage()` in `gateway/websocket.go` excludes from chat results:

- Pages tagged `visibility/internal` or `internal`
- Pages in `concept` or `concepts` category, **unless** they carry at least one
  user-facing tag: `rag`, `user-facing`, `knowledge-base`, or `game`

### 7.2 System File Exclusion

BM25 and embedding search skip:
- `index.md`, `log.md`, `hot.md` (vault scaffolding)
- `.manifest.json`, `AGENTS.md` (tooling files)
- Any directory starting with `.` or `_`

---

## 8. Context Injection

### 8.1 System Prompt Construction

In `buildWSRequestWithContext()`:

```go
system_prompt = "你可以参考以下知识库内容来回答问题:\n\n" +
                "--- {page.Title} ---\n{page.Body[:800]}...\n\n" + ...
```

The vault context is prepended as the **first system message**, before the
conversation history. This positions it as authoritative reference material.

### 8.2 Context Window Budgeting

- Each retrieved page body is truncated to **800 chars**.
- With 5 sources: ~4000 chars for vault context.
- With 20 rounds of conversation history: ~4000 chars for chat context.
- Total: ~8000 chars ≈ 2000 tokens, well within any model's context window.

### 8.3 Visible Citations

Before sending the AI response, a `context` message is sent to the client:
```
{"type": "context", "content": "检索到相关页面: 姚远乐, AI全栈开发, ..."}
```
This gives the user visibility into which sources were retrieved.

---

## 9. Index Lifecycle & Cache Invalidation

### 9.1 Current State

| Aspect | Status |
|--------|--------|
| BM25 index | **None** — reads all files per query |
| Embedding index | **In-memory, lazy** — built on first query after start |
| Persistence | **None** — lost on restart |
| Re-index trigger | **Manual restart only** |
| Vault change detection | **None** — new/changed pages invisible until restart |

### 9.2 Recommended Improvements

| Priority | Change | Rationale |
|----------|--------|-----------|
| P0 | Persist embeddings to disk | Avoid 2-3s cold start per restart |
| P0 | Build embedding index at startup | No first-query penalty |
| P1 | File watcher for vault changes | Auto-reindex on page add/edit/delete |
| P1 | BM25 inverted index cache | Eliminate per-query file reads |
| P2 | Embedding cache TTL + checksum | Invalidate stale embeddings |
| P2 | Incremental index update | Re-embed only changed pages |

### 9.3 Recommended Architecture for P0-P1

```
Agent Start
    │
    ├── Load embedding index from disk (JSON or binary blob)
    │   └── Checksum each chunk against source file mtime+size
    │       ├── Match → use cached embedding
    │       └── Mismatch → re-embed that chunk
    │
    ├── Start fsnotify watcher on vault directory
    │   ├── .md CREATE → chunk + embed + append to index
    │   ├── .md MODIFY → re-chunk + re-embed + replace in index
    │   └── .md DELETE → remove from index
    │
    └── Build BM25 inverted index (token → []{docID, tf})
        └── Persist alongside embedding cache
```

---

## 10. Session Integration

### 10.1 WebSocket Chat Flow

The full flow in `wsConn.handleChat()`:

1. **RAG retrieval** — `retrieveVaultContext(query)` → `[]vaultSource`
2. **Session management** — get or create session, add user message
3. **Context injection** — vault sources become system prompt prefix
4. **Model routing** — `router.Decide()` chooses model
5. **Inference** — Ollama `/api/chat` with full prompt
6. **Filter** — output filtering pipeline
7. **Save** — add assistant message to session
8. **Citations** — send `context` message with source titles

### 10.2 Session Memory Sedimentation

When a session ends (idle timeout or round limit), `SessionManager` sends it to
`EndChan` → memory sedimentation module → LLM summarization → vault `_memory/` write.

This is **separate from RAG** — it's about saving chat history as knowledge, not
about retrieving knowledge for chat.

---

## 11. Debugging & Observability

### 11.1 Log Points

```
[INFO] building embedding index for vault chunks...
[INFO] indexing chunks    count=42
[INFO] embedding index ready    chunks=42
[DEBUG] rag retrieval    bm25_results=3    embed_results=7    merged=6    sources=3
```

### 11.2 Common Failure Modes

| Symptom | Likely Cause | Check |
|---------|-------------|-------|
| `untitled` in source list | Page has no YAML `title` field | Frontmatter validation |
| Empty RAG results | Tokenizer dropped all query tokens | Tokenizer output log |
| BM25 finds 0 results | `minMatchCount` too strict for query | Query token count |
| Embedding finds 0 results | Cosine threshold 0.3 too high | Raw cosine scores |
| "concepts/" pages leaking | Missing `visibility/internal` tag | Tag audit |
| Wrong content in context | Chunk boundary split | Section heading boundaries |

---

## 12. File Index

| File | Lines | Purpose |
|------|-------|---------|
| `internal/vault/tokenizer.go` | ~200 | Bigram tokenizer + numeric entity extraction |
| `internal/vault/bm25.go` | ~180 | Okapi BM25 two-pass scorer |
| `internal/vault/chunker.go` | ~130 | H2-boundary page segmentation |
| `internal/vault/reader.go` | ~270 | Vault I/O, Search orchestration, Page parsing |
| `internal/gateway/rag.go` | ~200 | EmbeddingStore + cosine search |
| `internal/gateway/websocket.go` | ~320 | WebSocket handler + RRF + context injection |
| `internal/gateway/http.go` | ~120 | Route registration |
| `internal/core/session.go` | ~280 | Session state machine |
