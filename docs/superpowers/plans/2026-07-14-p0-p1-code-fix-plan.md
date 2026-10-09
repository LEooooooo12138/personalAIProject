# P0/P1 代码级修改计划（依赖完整追踪版）

> 基准: code-review-and-gap-analysis-2026-07-14.md 第一节
> 范围: go-agent/ 目录下所有修改
> 原则: 每步 `go build ./... && go test ./...` 通过后才能进入下一步

---

## 依赖追踪总表（修改前）

### 多份重复实现清单

| 函数/常量 | 位置 | 是否导出 | 被谁调用 |
|----------|------|---------|---------|
| `const rrfK = 60` | `vault/retrieval.go:11` | N (包级) | `RRFMerge` |
| `const rrfK = 60` | `chain/vault_steps.go:327` | N (包级) | `rrfFuseSearchResults` |
| `const rrfK = 60` | `gateway/websocket.go:409` | N (包级) | `rrfFuse` |
| `RRFMerge(bm25, embed) []MergedResult` | `vault/retrieval.go:37` | **Y** | 无人调用(新写的) |
| `rrfFuseSearchResults(bm25, embed) []mergedHit` | `chain/vault_steps.go:329` | N | `VaultSearchStep.Run:93` |
| `rrfFuse(bm25, embed) []rrfEntry` | `gateway/websocket.go:419` | N | `retrieveVaultContext:335` |
| `FindBestChunk(bodyText, query) string` | `vault/retrieval.go:85` | **Y** | `VaultSearchStep.Run:109` |
| `findBestChunkByTokens(page, query) string` | `gateway/websocket.go:384` | N | `retrieveVaultContext:361` |
| `isInternalPage(page) bool` | `gateway/websocket.go:460` | N | `retrieveVaultContext:358` |
| `sessionToConversation(s) *memory.Conversation` | `core/agent.go:209` | N | `consumeSessionEnds:194` |
| `sessionToConv(s) *memory.Conversation` | `gateway/websocket.go:568` | N | 无人调用(死代码) |
| `extractResponseContent(raw) string` | `core/agent.go:168` | N | `handleMessage:125` (legacy分支) |
| `extractWSResponse(raw) string` | `gateway/websocket.go:535` | N | `handleChatLegacy:281` |
| `buildChatRequest(model, content) []byte` | `core/agent.go:157` | N | `handleMessage:115` (legacy分支) |
| `embedding.EmbeddingResult` (struct) | `vault/embedding.go` | Y | 多处 |
| `EmbeddingHit` (struct) | `chain/vault_steps.go:35` | Y | `EmbeddingSearcher` 接口 |

### 跨包 import 依赖（需要清理的）

| 文件 | import | 问题 |
|------|--------|------|
| `core/app.go:15` | `"github.com/yuanleyao/ai-agent/internal/channel/wecom"` | P0-3 core 不应该知道 wecom |
| `core/app.go:60-75` | 手动构造 `wecom.Config{}` | P0-3 通道专属配置泄露到 core |
| `gateway/websocket.go` | `import "github.com/yuanleyao/ai-agent/internal/memory"` | 仅为了 `sessionToConv` 返回类型，删除后不再需要 |
| `gateway/websocket.go` | `import "github.com/yuanleyao/ai-agent/internal/filter"` | 仅 legacy 路径用，删除后不再需要 |

---

## Step 1: 消除 chain.EmbeddingHit 与 vault.EmbeddingResult 的类型重复

**依赖**: 无前置步骤
**目标**: 统一为一个类型，为后续消除 RRF 重复铺路

### 1.1 修改 `chain/vault_steps.go`

```go
// 删除: type EmbeddingHit struct { ... } (第 35-41 行)
// 修改 EmbeddingSearcher 接口:
type EmbeddingSearcher interface {
    Search(ctx context.Context, query string, k int) ([]vault.EmbeddingResult, error)
}
```

### 1.2 修改 `chain/chains.go`

```go
// 修改 EmbeddingStoreAdapter:
type EmbeddingStoreAdapter struct {
    searchFn func(ctx context.Context, query string, k int) ([]vault.EmbeddingResult, error)
}

func NewEmbeddingStoreAdapter(
    searchFn func(ctx context.Context, query string, k int) ([]vault.EmbeddingResult, error),
) *EmbeddingStoreAdapter {
    return &EmbeddingStoreAdapter{searchFn: searchFn}
}
```

### 1.3 修改 `core/app.go`

删除 `chain.EmbeddingHit` → `vault.EmbeddingResult` 的手动转换:
```go
// 旧: 手动转换 chain.EmbeddingHit → chain.EmbeddingHit
// 新: 直接返回 vault.EmbeddingResult
embedSearchAdapter := chain.NewEmbeddingStoreAdapter(
    func(ctx context.Context, query string, k int) ([]vault.EmbeddingResult, error) {
        return app.EmbedStore.Search(ctx, query, k)
    },
)
```

### 1.4 修改 `chain/vault_steps.go` — VaultSearchStep

```go
// 将所有 EmbeddingHit 引用替换为 vault.EmbeddingResult
// 修改后 VaultSearchStep.Run() 中的 embedResults 类型变为 []vault.EmbeddingResult
```

### 验证
```powershell
go build ./...
go test ./chain/...
```

---

## Step 2: 消除 RRF 三份重复实现

**依赖**: Step 1 完成
**目标**: `vault.RRFMerge` 成为唯一实现

### 2.1 修改 `vault/retrieval.go`

将 `const rrfK = 60` 改为可注入:
```go
type RetrievalService struct {
    reader     Reader
    embedStore *EmbeddingStore
    rrfK       int
}

func NewRetrievalService(reader Reader, embedStore *EmbeddingStore, rrfK int) *RetrievalService {
    return &RetrievalService{reader: reader, embedStore: embedStore, rrfK: rrfK}
}

// RRFMerge 改为方法
func (s *RetrievalService) RRFMerge(bm25 []SearchResult, embed []EmbeddingResult) []MergedResult {
    // 内部 s.rrfK 替换 const rrfK
}
```

### 2.2 修改 `chain/vault_steps.go`

```go
// 删除 const rrfK = 60 (第 327 行)
// 删除 rrfFuseSearchResults() 整个函数 (第 329-358 行)
// 删除 mergedHit struct (第 320-326 行)
// 删除 min() 函数 (第 360-365 行) — 如果只有这里用的话

// 修改 VaultSearchStep.Run() 第 93 行:
// 旧: merged := rrfFuseSearchResults(bm25Results, embedResults)
// 新:
var vaultEmbedResults []vault.EmbeddingResult
if embedResults != nil {
    vaultEmbedResults = embedResults // 类型已统一 (Step 1)
}
retrieval := vault.NewRetrievalService(s.reader, nil, 60)
merged := retrieval.RRFMerge(bm25Results, vaultEmbedResults)
```

### 2.3 修改 `gateway/websocket.go`

```go
// 删除 (在 Step 5 中一起删):
// const rrfK = 60 (第 409 行)
// type rrfEntry struct (第 411-417 行)
// func rrfFuse() (第 419-445 行)
// func findBestChunkByTokens() (第 384-403 行)
// func isInternalPage() (第 460-473 行)
// 这些在 Step 5 删除 legacy 路径时一起清除
```

### 验证
```powershell
go build ./...
go test ./...
```

---

## Step 3: 配置集中化

**依赖**: Step 2 完成
**目标**: 消除所有硬编码常量，收归 `agent.yaml`

### 3.1 修改 `config/agent.yaml`

```yaml
retrieval:
  rrf_k: 60
  top_k: 5
  max_chunk_chars: 800
  search_timeout: 30s

server:
  port: 8080
  internal_key: ${AGENT_INTERNAL_KEY}
  shutdown_timeout: 10s
  chain_timeout: 300s
  search_timeout: 30s
```

### 3.2 修改 `core/config.go`

```go
// 新增 RetrievalConfig
type RetrievalConfig struct {
    RRFK          int           `mapstructure:"rrf_k"`
    TopK          int           `mapstructure:"top_k"`
    MaxChunkChars int           `mapstructure:"max_chunk_chars"`
    SearchTimeout time.Duration `mapstructure:"search_timeout"`
}

// ServerConfig 新增字段
type ServerConfig struct {
    Port            int           `mapstructure:"port"`
    InternalKey     string        `mapstructure:"internal_key"`
    ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
    ChainTimeout    time.Duration `mapstructure:"chain_timeout"`
    SearchTimeout   time.Duration `mapstructure:"search_timeout"`
}

// Config 新增 Retrieval
type Config struct {
    // ...existing...
    Retrieval RetrievalConfig `mapstructure:"retrieval"`
}

// LoadConfig 新增默认值
if cfg.Retrieval.RRFK == 0          { cfg.Retrieval.RRFK = 60 }
if cfg.Retrieval.TopK == 0          { cfg.Retrieval.TopK = 5 }
if cfg.Retrieval.MaxChunkChars == 0 { cfg.Retrieval.MaxChunkChars = 800 }
if cfg.Retrieval.SearchTimeout == 0 { cfg.Retrieval.SearchTimeout = 30 * time.Second }
if cfg.Server.ShutdownTimeout == 0  { cfg.Server.ShutdownTimeout = 10 * time.Second }
if cfg.Server.ChainTimeout == 0     { cfg.Server.ChainTimeout = 300 * time.Second }
if cfg.Server.SearchTimeout == 0    { cfg.Server.SearchTimeout = 30 * time.Second }
```

### 3.3 替换硬编码 — gateway/http.go

```go
// 旧: 300*time.Second → 新: s.cfg.Server.ChainTimeout
// 旧: 30*time.Second  → 新: s.cfg.Server.SearchTimeout
// 旧: 10*time.Second  → 新: s.cfg.Server.ShutdownTimeout

// handleChatViaChain 中:
ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ChainTimeout)

// handleSessionSearch 中:
ctx, cancel := context.WithTimeout(c.Request.Context(), s.cfg.Server.SearchTimeout)

// Run() 中:
shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
```

### 3.4 替换硬编码 — gateway/websocket.go

```go
// handleChatWithChain 中:
// 旧: 300*time.Second → 新: w.cfg.Server.ChainTimeout
// handleChatLegacy 中 (Step 5 删除):
// 旧: 300*time.Second, 30*time.Second

// retrieveVaultContext 中 (Step 5 删除):
// 旧: 30*time.Second, if i >= 5, if len([]rune(body)) > 800
```

### 3.5 替换硬编码 — core/router.go

`Route()` 函数中 `"gemma4:12b"` 和 `"llava:7b"` 从外部传入，但 `Route()` 是 legacy 兼容函数。改为从 `NewModelRouter` 的字段读取：
```go
func Route(modelHint string) *RouteDecision {
    r := NewModelRouter("gemma4:12b", "llava:7b")  // 这两个值应从 config 来
    return r.Decide(nil, modelHint, nil)
}
// → 删除 Route() 函数，legacy 代码在 Step 6 清除后无调用者
```

### 3.6 替换硬编码 — core/app.go

```go
// ollamaEmbedder.Embed() 中的 "bge-m3"
// 旧: req := map[string]interface{}{"model": "bge-m3", "input": text}
// 新: 从 cfg.Inference.Models.Embedding 读取
```

### 验证
```powershell
go build ./...
go test ./...
```

---

## Step 4: core/app.go 去 wecom 依赖

**依赖**: Step 3 完成（config 结构已调整）
**目标**: `core` 包不 import `wecom` 包，不接触 wecom 配置字段

### 4.1 修改 `core/config.go`

```go
// 删除 WecomChannelConfig 中的通道专属字段，只保留 Enabled
type WecomChannelConfig struct {
    Enabled bool `mapstructure:"enabled"`
}
// 注意: 不删 ChannelsConfig.Wecom 字段本身，core 仍需知道 "wecom 通道已启用"
//       但不再知道 wecom 的 CorPID/Secret/Token 等内部字段
```

### 4.2 新增 `internal/channel/factory.go`

```go
package channel

import "go.uber.org/zap"

type ChannelFactory interface {
    ID() string
    Create(cfg map[string]interface{}, logger *zap.Logger) (Channel, error)
}

var registry = map[string]ChannelFactory{}

func RegisterFactory(f ChannelFactory) {
    registry[f.ID()] = f
}

func GetFactory(id string) (ChannelFactory, bool) {
    f, ok := registry[id]
    return f, ok
}
```

### 4.3 修改 `internal/channel/wecom/config.go`

```go
package wecom

import (
    "fmt"
    "github.com/yuanleyao/ai-agent/internal/channel"
    "go.uber.org/zap"
)

func init() {
    channel.RegisterFactory(&Factory{})
}

type Factory struct{}

func (f *Factory) ID() string { return "wecom" }

func (f *Factory) Create(cfg map[string]interface{}, logger *zap.Logger) (channel.Channel, error) {
    wcCfg := Config{}
    if v, ok := cfg["listen_addr"].(string); ok      { wcCfg.ListenAddr = v }
    if v, ok := cfg["corp_id"].(string); ok           { wcCfg.CorpID = v }
    if v, ok := cfg["corp_secret"].(string); ok       { wcCfg.CorpSecret = v }
    if v, ok := cfg["agent_id"].(string); ok          { wcCfg.AgentID = v }
    if v, ok := cfg["token"].(string); ok             { wcCfg.Token = v }
    if v, ok := cfg["encoding_aes_key"].(string); ok  { wcCfg.EncodingAESKey = v }
    if v, ok := cfg["auto_approve"].(bool); ok        { wcCfg.AutoApprove = v }
    // allowed_users 是 []interface{} → []string
    if v, ok := cfg["allowed_users"].([]interface{}); ok {
        for _, u := range v {
            if s, ok := u.(string); ok {
                wcCfg.AllowedUsers = append(wcCfg.AllowedUsers, s)
            }
        }
    }
    if wcCfg.CorpID == "" {
        return nil, fmt.Errorf("wecom: corp_id is required")
    }
    return NewAdapter(wcCfg, logger)
}
```

### 4.4 修改 `config/agent.yaml`

```yaml
channels:
  wecom:
    enabled: true
    config:                          # ← 通道专属配置嵌套在 config 下
      listen_addr: ":8081"
      corp_id: "${WECOM_CORP_ID}"
      corp_secret: "${WECOM_CORP_SECRET}"
      agent_id: "${WECOM_AGENT_ID}"
      token: "${WECOM_CALLBACK_TOKEN}"
      encoding_aes_key: "${WECOM_AES_KEY}"
      allowed_users:
        - "wmxxxxxxxx"
      auto_approve: false
```

### 4.5 修改 `core/config.go`

```go
// WecomChannelConfig 改为通用结构
type WecomChannelConfig struct {
    Enabled bool                   `mapstructure:"enabled"`
    Config  map[string]interface{} `mapstructure:"config"`  // ← 通道自解析
}
```

### 4.6 修改 `core/app.go`

**删除 import**:
```go
// 删除: "github.com/yuanleyao/ai-agent/internal/channel/wecom"
```

**替换 Bootstrap 中的 wecom 初始化** (原第 15 行 import + 第 60-75 行初始化):
```go
// 旧:
if cfg.Channels.Wecom.Enabled {
    wcCfg := wecom.Config{
        Enabled:        cfg.Channels.Wecom.Enabled,
        ListenAddr:     cfg.Channels.Wecom.ListenAddr,
        CorpID:         cfg.Channels.Wecom.CorpID,
        ...
    }
    wcAdapter, err := wecom.NewAdapter(wcCfg, logger)
    ...
}

// 新:
if cfg.Channels.Wecom.Enabled {
    factory, ok := channel.GetFactory("wecom")
    if !ok {
        logger.Fatal("wecom channel enabled but factory not registered (import missing?)")
    }
    wcAdapter, err := factory.Create(cfg.Channels.Wecom.Config, logger)
    if err != nil {
        logger.Fatal("wecom adapter", zap.Error(err))
    }
    app.ChMgr.Register(wcAdapter)
}
```

### 4.7 修改 `cmd/agentd/main.go`

添加空白导入触发 wecom 自注册:
```go
import (
    // ...existing...
    _ "github.com/yuanleyao/ai-agent/internal/channel/wecom"
)
```

### 验证
```powershell
go build ./...
# 确认 core 包不再 import wecom:
go list -f '{{.Imports}}' ./internal/core/ | Select-String "wecom"
# 应无输出
```

---

## Step 5: 删除 gateway legacy 路径 + 去调度化

**依赖**: Step 1-4 全部完成
**目标**: gateway 不再做 vault search / RRF / routing。chain 成为唯一路径。

### 5.1 修改 `gateway/http.go` — 删除 legacy chat

删除函数 (第 286-330 行附近):
```go
// 删除: func (s *Server) handleChatLegacyHTTP(...)
```

修改 `handleChatWithChain` (第 197-215 行):
```go
// 旧:
if s.chainRouter != nil && s.chainExecutor != nil && query != "" {
    s.handleChatViaChain(c, body, req, query)
    return
}
s.handleChatLegacyHTTP(c, body, req, query)

// 新:
if s.chainRouter == nil || s.chainExecutor == nil {
    c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chain system not initialized"})
    return
}
s.handleChatViaChain(c, body, req, query)
```

### 5.2 修改 `gateway/websocket.go` — 删除全部 legacy 路径

**删除以下所有函数/常量/类型:**

| 行号 | 内容 | 原因 |
|------|------|------|
| 171 | `w.handleChatLegacy(content, session)` | 调用点 |
| 261-300 | `func (w *wsConn) handleChatLegacy(...)` | legacy handler |
| 335 | `merged := rrfFuse(bm25Results, embedResults)` | 调用点 |
| 325-375 | `func (w *wsConn) retrieveVaultContext(...)` | 重复 RRF 检索 |
| 384-403 | `func findBestChunkByTokens(...)` | 重复 vault.FindBestChunk |
| 409 | `const rrfK = 60` | 重复 const |
| 411-417 | `type rrfEntry struct` | 仅 legacy 使用 |
| 419-445 | `func rrfFuse(...)` | 重复 RRFMerge |
| 460-473 | `func isInternalPage(...)` | 移入 vault 包 |
| 281 | `responseText, err := extractWSResponse(rawResp)` | 调用点 |
| 535-548 | `func extractWSResponse(...)` | 重复 extractResponseContent |
| 498-515 | `func buildWSRequestWithContext(...)` | legacy 上下文组装 |
| 480-496 | `func buildHistoryFromSession(...)` | 仅 legacy 使用 |
| 517-522 | `type wsMessage struct` | 仅 legacy 使用 |
| 524-526 | `func setModel(...)` | 仅 legacy 使用 |
| 568-579 | `func sessionToConv(...)` | 重复 sessionToConversation |

**删除不再需要的 import:**
```go
// 删除 (仅 legacy 路径使用):
// "github.com/yuanleyao/ai-agent/internal/memory"
// "github.com/yuanleyao/ai-agent/internal/filter"
```

**修改 `handleChat` (第 145 行附近):**
```go
// 旧:
if w.chainExecutor != nil && w.chainRouter != nil {
    w.handleChatWithChain(content, session)
    return
}
w.handleChatLegacy(content, session)

// 新:
if w.chainExecutor == nil || w.chainRouter == nil {
    w.writeJSON(serverMessage{Type: "error", Message: "agent not available"})
    return
}
w.handleChatWithChain(content, session)
```

### 5.3 修改 `core/agent.go` — 删除 legacy 分支

删除 `handleMessage` 中的 else 分支:
```go
// 删除 (第 112-130 行附近):
// } else {
//     decision := a.modelRouter.Decide(nil, "", metadata)
//     reqBody := buildChatRequest(decision.TargetModel, msg.Content)
//     ...
//     responseText, err = extractResponseContent(rawResp)
// ...
```

改为 chain 不可用时提前返回:
```go
func (a *Agent) handleMessage(msg channel.Message) {
    // ...session 管理...
    if a.chainExecutor == nil || a.chainRouter == nil {
        a.logger.Error("chain system not initialized, cannot handle message")
        return
    }
    // ...走 chain 路径...
}
```

删除不再需要的函数:
```go
// 删除: func buildChatRequest() (第 157-166 行)
// 删除: func extractResponseContent() (第 168-179 行)
// → 但 extractResponseContent 先保留，Step 6 导出给其他地方用
```

### 5.4 将 `isInternalPage` 移到 `vault` 包

新增 `vault/page_filter.go`:
```go
package vault

func IsInternalPage(page *Page) bool {
    for _, tag := range page.Tags {
        if tag == "visibility/internal" || tag == "internal" {
            return true
        }
    }
    if page.Category == "concept" || page.Category == "concepts" {
        for _, tag := range page.Tags {
            if tag == "rag" || tag == "user-facing" || tag == "knowledge-base" || tag == "game" {
                return false
            }
        }
        return true
    }
    return false
}
```

### 验证
```powershell
go build ./...
go test ./...
```

---

## Step 6: 消除剩余重复函数

**依赖**: Step 5 完成
**目标**: 所有功能只有一份实现

### 6.1 导出 `core.SessionToConversation`

修改 `core/agent.go`:
```go
// 旧: func sessionToConversation(s *Session) *memory.Conversation {
// 新: func SessionToConversation(s *Session) *memory.Conversation {
```

### 6.2 删除 `gateway/websocket.go:sessionToConv`

(已在 Step 5 删除)

### 6.3 如果 extractResponseContent 仍有调用者（检查后决定）

在 Step 5 删除 agent.go 的 legacy 分支后，`extractResponseContent` 不再被 agent.go 调用。检查 gateway 是否调用 → 已在 Step 5 删除 `extractWSResponse`。所以两个都可以删除。

如果将来 chain 的 LLM steps 需要解析 OpenAI 响应，在 chain 包中重新实现，不放在 core。

### 验证
```powershell
go build ./...
go test ./...
```

---

## Step 7: 拆分 gateway/websocket.go

**依赖**: Step 5-6 完成（文件已大幅缩小）
**目标**: 每个文件 <400 行，职责单一

### 7.1 拆分方案

当前 `gateway/websocket.go` 在 Step 5 删除 legacy 代码后，剩余内容:

| 内容 | 行数(估) | 目标文件 |
|------|---------|---------|
| `upgrader` 变量 | 5 | 保留在 websocket.go |
| `wsConn` struct + `wsConn` 方法 | ~250 | `ws_conn.go` |
| `clientMessage`, `serverMessage`, `vaultSource` 类型 | ~20 | `ws_types.go` |
| `handleWebSocket()` 工厂函数 | ~15 | 保留在 websocket.go |
| `newSessionID()` | ~5 | `ws_conn.go` |
| RRF 融合 + chunk 选择 (已删除) | 0 | — |
| `handleChatLegacy` (已删除) | 0 | — |
| `retrieveVaultContext` (已删除) | 0 | — |
| `buildHistoryFromSession` (已删除) | 0 | — |
| `buildWSRequestWithContext` (已删除) | 0 | — |
| `extractWSResponse` (已删除) | 0 | — |
| `sessionToConv` (已删除) | 0 | — |

### 7.2 新建 `gateway/ws_types.go`

```go
package gateway

import (
    "sync"
    "github.com/gorilla/websocket"
    "go.uber.org/zap"
    "github.com/yuanleyao/ai-agent/internal/chain"
    "github.com/yuanleyao/ai-agent/internal/core"
)

type clientMessage struct {
    SessionID string `json:"session_id"`
    Content   string `json:"content"`
}

type serverMessage struct {
    Type      string `json:"type"`
    Content   string `json:"content,omitempty"`
    Message   string `json:"message,omitempty"`
    SessionID string `json:"session_id,omitempty"`
}

type vaultSource struct {
    Title string
    Body  string
}
```

### 7.3 新建 `gateway/ws_conn.go`

```go
package gateway

// wsConn — WebSocket 连接封装
// 职责: 连接生命周期管理 + 消息序列化 + 委托 chain executor
type wsConn struct {
    conn          *websocket.Conn
    logger        *zap.Logger
    mu            sync.Mutex

    chainExecutor *chain.ChainExecutor
    chainRouter   *chain.ChainRouter
    sessionMgr    *core.SessionManager
    filter        *filter.Chain

    sessionID string
    session   *core.Session
}

// loop() — 消息读取循环
// handleChat() — 入站消息 → session 管理 → 委托 handleChatWithChain
// handleChatWithChain() — 调用 chain executor（流式）
// writeJSON() — 线程安全的 JSON 写入
// newSessionID() — 生成 session ID
```

### 7.4 `gateway/websocket.go` 保留内容

只保留:
```go
package gateway

var upgrader = websocket.Upgrader{...}

func handleWebSocket(logger, cfg, chainExecutor, chainRouter, sessionMgr, filter) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        conn, _ := upgrader.Upgrade(w, r, nil)
        wsc := &wsConn{
            conn: conn, logger: logger,
            chainExecutor: chainExecutor, chainRouter: chainRouter,
            sessionMgr: sessionMgr, filter: filter,
        }
        wsc.loop()
    }
}
```

### 验证
```powershell
go build ./...
go test ./...
# 确认每个 gateway 文件 < 400 行
Get-ChildItem gateway/*.go | ForEach-Object { $lines = (Get-Content $_.FullName).Count; "$($_.Name): $lines lines" }
```

---

## Step 8: 全量编译 + 测试 + 冒烟

**依赖**: Step 1-7 全部完成

### 8.1 编译
```powershell
cd go-agent
go build ./...
go vet ./...
```

### 8.2 测试
```powershell
go test ./...
```

### 8.3 冒烟验证
```powershell
# 启动
.\agentd.exe

# HTTP chat (chain-only)
curl -X POST http://localhost:8080/v1/chat/completions `
  -H "Authorization: Bearer my_local_api_key" `
  -H "Content-Type: application/json" `
  -d '{"model":"auto","messages":[{"role":"user","content":"你好"}]}'

# 应返回 200 + chain 路径的完整响应
```

---

## 修改文件清单

| 文件 | 操作 | 变化量 |
|------|------|--------|
| `config/agent.yaml` | 修改 | 新增 retrieval + server timeout 节 |
| `core/config.go` | 修改 | 新增 RetrievalConfig; ServerConfig 加字段; WecomChannelConfig 精简 |
| `cmd/agentd/main.go` | 修改 | 添加 `_ "..." wecom` 空白 import |
| `core/app.go` | 修改 | 删除 wecom import; 替换初始化逻辑; 简化 EmbeddingStoreAdapter |
| `core/agent.go` | 修改 | 删除 legacy 分支; 删除 buildChatRequest/extractResponseContent; 导出 SessionToConversation |
| `core/router.go` | 修改 | 删除 Route() legacy 函数 |
| `channel/factory.go` | **新建** | ChannelFactory 接口 + 注册表 |
| `channel/wecom/config.go` | 修改 | 新增 init() + Factory + Create() |
| `vault/retrieval.go` | 修改 | rrfK 注入化; RRFMerge 改方法 |
| `vault/page_filter.go` | **新建** | IsInternalPage() |
| `chain/vault_steps.go` | 修改 | 删除 EmbeddingHit; 删除 rrfFuseSearchResults/mergedHit/rrfK/min; EmbeddingSearcher 改用 vault.EmbeddingResult |
| `chain/chains.go` | 修改 | EmbeddingStoreAdapter 改用 vault.EmbeddingResult |
| `gateway/http.go` | 修改 | 删除 handleChatLegacyHTTP; 替换硬编码 timeout |
| `gateway/websocket.go` | **大幅缩减** | 删除全部 legacy 函数/types/const; 只保留 handleWebSocket + upgrader |
| `gateway/ws_types.go` | **新建** | clientMessage, serverMessage, vaultSource |
| `gateway/ws_conn.go` | **新建** | wsConn struct + 方法 |

---

## 不变文件（确认无修改）

| 文件 | 原因 |
|------|------|
| `channel/channel.go` | Channel 接口 + 权限矩阵不变 |
| `channel/manager.go` | 实现正确，无需修改 |
| `channel/internal.go` | 实现正确，无需修改 |
| `channel/wecom/adapter.go` | Adapter 实现不变，仅构造方式从 core 变为 factory |
| `channel/wecom/callback.go` | 无变更 |
| `channel/wecom/client.go` | 无变更 |
| `channel/wecom/auth.go` | 无变更 |
| `channel/wecom/crypto.go` | 无变更 |
| `channel/wecom/contact.go` | 无变更 |
| `filter/filters.go` | 实现正确，无需修改 |
| `inference/client.go` | Client 接口 + OllamaClient 实现正确 |
| `vault/reader.go` | Reader/Writer 接口不变 |
| `vault/writer.go` | 不变 |
| `vault/embedding.go` | EmbeddingStore 不变 |
| `vault/chunker.go` | 不变 |
| `vault/bm25.go` | 不变 |
| `vault/vector.go` | 不变 |
| `vault/inverted.go` | 不变 |
| `vault/tokenizer.go` | 不变 |
| `vault/unicode.go` | 不变 |
| `vault/math.go` | 不变 |
| `memory/sedimentation.go` | 不变 |
| `memory/summarizer.go` | 不变 |
| `core/session.go` | 不变 (仅新增 HistoryAsMaps 可选) |
| `core/session_store.go` | 不变 |
| `gateway/middleware.go` | 不变 |
| `gateway/rag.go` | 不变 |
| `chain/executor.go` | 不变 |
| `chain/go_steps.go` | 不变 |
| `chain/go_steps_decide.go` | 不变 |
| `chain/llm_steps.go` | 不变 |
| `chain/types.go` | 不变 |
| `chain/chain_test.go` | 可能需更新 (EmbeddingHit → EmbeddingResult) |
| `gateway/chat_test.go` | 可能需更新 (删除 legacy handler) |
