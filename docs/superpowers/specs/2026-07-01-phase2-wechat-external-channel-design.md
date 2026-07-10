# Phase 2 设计：企业微信接入 + 外部通道（v2.0）

> **依赖**: Phase 1 完成（知识库 + Go Agent 骨架 + LLM Gateway + Ollama）
> **目标**: 通过企业微信"客户联系"接入微信生态，实现双通道 Agent，外部输出经过完整过滤链
> **核心原则**: Channel 适配器模式 — 企业微信只是 Channel 接口的一个实现，可随时替换

---

## 0. 设计原则：为什么用适配器模式

旧方案用 QClaw + Node.js Sidecar，核心问题是：
- 引入了额外进程依赖（Node.js）
- QClaw AGP 协议与 Agent Core 紧耦合
- 替换通道需要改多个模块

新方案的核心思想：**Channel 接口是唯一的契约**。

```
┌─────────────────────────────────────────────────────┐
│                  Go Agent Core                       │
│                                                     │
│  Agent Router → LLM Gateway → Filter Chain          │
│       ↑              ↑              ↑                │
│       │              │              │                │
│  ┌────┴────┐   ┌─────┴─────┐  ┌────┴────┐          │
│  │Channel  │   │ Channel   │  │ Channel │  ...      │
│  │Interface│   │ Interface │  │ Interface│          │
│  └────┬────┘   └─────┬─────┘  └────┬────┘          │
│       │              │              │                │
└───────┼──────────────┼──────────────┼────────────────┘
        │              │              │
   ┌────▼────┐   ┌─────▼─────┐  ┌────▼────┐
   │  WeCom  │   │  WebChat  │  │ Telegram│
   │ Adapter │   │  Adapter  │  │ Adapter │
   └─────────┘   └───────────┘  └─────────┘
```

**Agent Core 对"微信"一无所知。** 它只知道：
- 有一个 `Channel` 接口
- 收到 `Message`，返回 `Response`
- 根据 `Channel.Type()` 判断权限

**替换企业微信 = 写一个新的 adapter 包 + 改配置文件。** 零核心代码改动。

---

## 1. Channel 接口（已有，需补充）

现有 `internal/channel/channel.go` 已定义核心接口，Phase 2 需要补充两个方法：

```go
// channel.go — 最终版 Channel 接口

type Channel interface {
    // ID returns the unique channel identifier (e.g. "wecom", "webchat").
    ID() string
    // Type returns whether this is an internal or external channel.
    Type() Type
    // Start begins listening for messages.
    Start(ctx context.Context) error
    // Stop gracefully shuts down the channel.
    Stop() error
    // Receive returns a read-only channel of inbound messages.
    // Phase 2 新增：Manager 通过此方法轮询各通道的入站消息。
    Receive() <-chan Message
    // Send delivers a response back through the channel.
    Send(msg Message, resp Response) error
}
```

**新增 `Receive()` 方法的原因**：
- 现有 `ExternalChannel` stub 已经有 `Receive()`，但没有在接口中声明
- Manager 需要统一轮询所有通道的入站消息，必须通过接口调用
- 这是保持适配器可替换的关键：任何 adapter 只要实现 `Receive()` 就能接入

---

## 2. 企业微信适配器设计

### 2.1 包结构

```
internal/channel/wecom/
├── adapter.go          # 实现 Channel 接口（入口）
├── callback.go         # HTTP 回调处理（接收微信推送的消息）
├── client.go           # 企业微信 API 客户端（发送消息）
├── auth.go             # Token 管理（corp_id + secret → access_token）
├── crypto.go           # 消息加解密（AES，企业微信回调要求）
├── contact.go          # 外部联系人白名单管理
├── message.go          # 消息格式转换（企业微信 XML ↔ channel.Message）
├── adapter_test.go     # 单元测试
└── config.go           # WeCom 专用配置结构
```

**封装边界**：
- `wecom/` 包内部可以 import `channel` 包（使用 `Message`、`Response` 等类型）
- `channel` 包不能 import `wecom/` 包（接口不知道实现者是谁）
- Agent Core（`core/`、`gateway/`、`inference/`、`filter/`、`memory/`）不能 import `wecom/`
- `wecom/` 包不能 import `core/`、`gateway/`、`inference/`、`filter/`、`memory/`

```
import 方向：
  wecom/ ──→ channel/ ←── core/
                 ↑
                 │
           filter/ memory/
```

### 2.2 消息流

```
用户个人微信
    │ 发消息给你（企业微信联系人）
    ▼
企业微信服务器
    │ POST 回调（XML，AES 加密）
    ▼
wecom/callback.go
    │ 1. 验签（signature 校验）
    │ 2. 解密（AES-256-CBC）
    │ 3. 解析 XML → channel.Message
    │ 4. 检查白名单（contact.go）→ 非授权用户直接回复拒绝
    │ 5. 写入 Receive() channel
    ▼
Channel Manager
    │ 轮询所有 Channel 的 Receive()
    │ 取出 Message，交给 Agent Core
    ▼
Agent Router → LLM Gateway → Filter Chain
    │
    ▼
Channel Manager
    │ 调用对应 Channel 的 Send()
    ▼
wecom/client.go
    │ 1. 接收 channel.Response
    │ 2. 转换为企业微信消息格式
    │ 3. 调用企业微信 API 发送（POST /cgi-bin/externalcontact/send_msg）
    ▼
用户个人微信收到回复
```

### 2.3 Adapter 实现

```go
// wecom/adapter.go

package wecom

import (
    "context"
    "github.com/yuanleyao/ai-agent/internal/channel"
)

// Adapter implements channel.Channel for WeCom (企业微信).
// 它是 Channel 接口的一个具体实现，对 Agent Core 完全透明。
type Adapter struct {
    id       string
    cfg      Config
    callback *CallbackHandler   // 接收微信推送
    client   *APIClient         // 发送消息
    auth     *TokenManager      // access_token 管理
    contact  *ContactFilter     // 外部联系人白名单
    msgCh    chan channel.Message
    logger   *zap.Logger
}

func NewAdapter(cfg Config, logger *zap.Logger) (*Adapter, error) {
    auth := NewTokenManager(cfg.CorpID, cfg.CorpSecret, logger)
    client := NewAPIClient(auth, logger)
    contact := NewContactFilter(cfg.AllowedUsers, cfg.AutoApprove, logger)
    callback := NewCallbackHandler(cfg.Token, cfg.EncodingAESKey, contact, logger)

    return &Adapter{
        id:       "wecom",
        cfg:      cfg,
        callback: callback,
        client:   client,
        auth:     auth,
        contact:  contact,
        msgCh:    make(chan channel.Message, 100),
        logger:   logger,
    }, nil
}

func (a *Adapter) ID() string        { return a.id }
func (a *Adapter) Type() channel.Type { return channel.External }

func (a *Adapter) Start(ctx context.Context) error {
    // 1. 启动 token 自动刷新
    go a.auth.StartAutoRefresh(ctx)
    // 2. 启动 HTTP 回调服务器（接收微信推送）
    go a.callback.Start(ctx, a.cfg.ListenAddr, a.msgCh)
    a.logger.Info("wecom adapter started", zap.String("listen", a.cfg.ListenAddr))
    return nil
}

func (a *Adapter) Stop() error {
    return a.callback.Stop()
}

func (a *Adapter) Receive() <-chan channel.Message {
    return a.msgCh
}

func (a *Adapter) Send(msg channel.Message, resp channel.Response) error {
    // 将 channel.Response 转换为企业微信消息并发送
    return a.client.SendText(msg.UserID, resp.Content)
}
```

### 2.4 配置

```yaml
# config/agent.yaml（Phase 2 新增部分）

channels:
  internal:
    enabled: true

  # 企业微信适配器 — 只有 wecom 包关心这些配置
  wecom:
    enabled: true
    listen_addr: ":8081"              # 回调 HTTP 监听地址
    corp_id: "${WECOM_CORP_ID}"       # 企业 ID
    corp_secret: "${WECOM_CORP_SECRET}" # 应用 Secret
    agent_id: "${WECOM_AGENT_ID}"     # 应用 AgentId
    token: "${WECOM_CALLBACK_TOKEN}"  # 回调 Token（企业微信后台配置）
    encoding_aes_key: "${WECOM_AES_KEY}" # 回调 EncodingAESKey
    allowed_users:                    # 外部联系人白名单（external_userid）
      - "wmxxxxxxxx"
      - "wmyyyyyyyy"
    auto_approve: false               # 是否自动通过好友申请
```

**配置解析**：
- Agent Core 只读 `channels.wecom.enabled`
- 其余字段由 `wecom.NewAdapter()` 自行解析
- Agent Core 通过 `channel.Config` 接口（见 §2.7）获取通用配置

---

## 3. 外部联系人白名单（安全核心）

### 3.1 设计思路

企业微信的"客户联系"天然支持用户筛选：

```
企业微信后台                          Go Agent
    │                                   │
    │ 管理外部联系人                      │ contact.go
    │ (手动审批 / API 审批)               │ 维护白名单
    │                                   │
    ▼                                   ▼
微信用户申请添加                       白名单检查
    │                                   │
    ├── 已授权 → 正常对话                ├── 在白名单 → 转发给 Agent
    └── 未授权 → 不通过                  └── 不在白名单 → 自动回复拒绝
```

### 3.2 白名单实现

```go
// wecom/contact.go

type ContactFilter struct {
    allowedUsers map[string]bool  // external_userid 集合
    autoApprove  bool
    logger       *zap.Logger
    mu           sync.RWMutex
}

// IsAllowed checks if an external user is in the whitelist.
func (f *ContactFilter) IsAllowed(externalUserID string) bool {
    f.mu.RLock()
    defer f.mu.RUnlock()
    return f.allowedUsers[externalUserID]
}

// AddUser adds a user to the whitelist (called after approval).
func (f *ContactFilter) AddUser(externalUserID string) {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.allowedUsers[externalUserID] = true
}

// RemoveUser removes a user from the whitelist.
func (f *ContactFilter) RemoveUser(externalUserID string) {
    f.mu.Lock()
    defer f.mu.Unlock()
    delete(f.allowedUsers, externalUserID)
}
```

### 3.3 审批流（Phase 2 最小版）

```
微信用户扫描企业二维码 → 发送好友申请
    │
    ├── auto_approve: true  → 自动通过 + 加入白名单
    └── auto_approve: false → 你在企业微信 App 手动审批
                               审批通过后 → 通过 API 或配置加入白名单
```

Phase 2 最小版：手动审批 + 配置文件白名单。Phase 3+ 可扩展为 API 动态管理。

---

## 4. 企业微信 API 集成

### 4.1 Token 管理

```go
// wecom/auth.go

// TokenManager handles WeCom access_token lifecycle.
// 企业微信 access_token 有效期 7200 秒（2 小时），需要定期刷新。
type TokenManager struct {
    corpID     string
    corpSecret string
    token      string
    expiresAt  time.Time
    mu         sync.RWMutex
    logger     *zap.Logger
}

// GetToken returns a valid access_token, refreshing if needed.
func (m *TokenManager) GetToken(ctx context.Context) (string, error) {
    m.mu.RLock()
    if m.token != "" && time.Now().Before(m.expiresAt) {
        defer m.mu.RUnlock()
        return m.token, nil
    }
    m.mu.RUnlock()
    return m.refresh(ctx)
}

// refresh calls WeCom API to get a new access_token.
// GET https://qyapi.weixin.qq.com/cgi-bin/gettoken
//   ?corpid=ID&corpsecret=SECRET
func (m *TokenManager) refresh(ctx context.Context) (string, error) {
    // ... HTTP GET, parse JSON response, update token + expiresAt
}

// StartAutoRefresh refreshes the token every 100 minutes (before 120-min expiry).
func (m *TokenManager) StartAutoRefresh(ctx context.Context) {
    ticker := time.NewTicker(100 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if _, err := m.refresh(ctx); err != nil {
                m.logger.Error("token refresh failed", zap.Error(err))
            }
        }
    }
}
```

### 4.2 发送消息

```go
// wecom/client.go

// APIClient wraps WeCom REST API calls.
type APIClient struct {
    auth   *TokenManager
    logger *zap.Logger
    client *http.Client
}

// SendText sends a text message to an external contact.
// POST https://qyapi.weixin.qq.com/cgi-bin/externalcontact/message/send
func (c *APIClient) SendText(externalUserID, text string) error {
    token, err := c.auth.GetToken(context.Background())
    if err != nil {
        return fmt.Errorf("wecom: get token: %w", err)
    }

    payload := map[string]interface{}{
        "chat_type":           "single",
        "external_userid":     []string{externalUserID},
        "sender":              "",  // 使用配置的 sender
        "text":                map[string]string{"content": text},
        "msgtype":             "text",
    }
    // POST to WeCom API with token
    // ...
}

// SendMarkdown sends a markdown message (if supported by the receiving platform).
// 企业微信支持 Markdown 子集（仅在应用消息中）。
func (c *APIClient) SendMarkdown(externalUserID, markdown string) error {
    // 企业微信外部联系人消息不直接支持 Markdown
    // 需要通过 PlatformFilter 转为纯文本后再发送
    return c.SendText(externalUserID, markdown)
}
```

### 4.3 消息加解密

```go
// wecom/crypto.go

// WeComCallbackCrypto handles AES encryption/decryption for WeCom callbacks.
// 企业微信回调消息体是 AES-256-CBC 加密的 XML，需要解密后才能使用。
//
// 加密格式：
//   <Encrypt><![CDATA[base64_encoded_aes_ciphertext]]></Encrypt>
//
// 解密步骤：
//   1. Base64 解码
//   2. AES-256-CBC 解密（key = SHA256(encodingAESKey)）
//   3. 去 PKCS7 padding
//   4. 提取消息体（前 16 字节是随机数，4 字节是长度，后面是内容）
type WeComCallbackCrypto struct {
    token          string
    encodingAESKey string
    aesKey         []byte // 从 encodingAESKey 计算得出
}

func NewCrypto(token, encodingAESKey string) (*WeComCallbackCrypto, error) {
    // encodingAESKey 是 43 字符 Base64 编码，解码后得到 32 字节 AES key
    aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
    if err != nil {
        return nil, fmt.Errorf("wecom: invalid encoding_aes_key: %w", err)
    }
    return &WeComCallbackCrypto{
        token:          token,
        encodingAESKey: encodingAESKey,
        aesKey:         aesKey,
    }, nil
}

// VerifySignature checks the SHA1 signature of the callback request.
func (c *WeComCallbackCrypto) VerifySignature(signature, timestamp, nonce, encrypt string) bool {
    // SHA1(sort(token, timestamp, nonce, encrypt)) == signature
    // ...
}

// Decrypt decrypts the encrypted message body.
func (c *WeComCallbackCrypto) Decrypt(encryptedMsg string) ([]byte, error) {
    // AES-256-CBC decrypt with PKCS7 padding
    // ...
}
```

---

## 5. 回调 HTTP 处理

### 5.1 回调端点

```go
// wecom/callback.go

// CallbackHandler serves the HTTP endpoint that WeCom pushes messages to.
// 企业微信会向你配置的 URL 发送 POST 请求（加密 XML）。
type CallbackHandler struct {
    crypto  *WeComCallbackCrypto
    contact *ContactFilter
    server  *http.Server
    logger  *zap.Logger
}

// Start starts the HTTP server for WeCom callbacks.
func (h *CallbackHandler) Start(ctx context.Context, addr string, msgCh chan<- channel.Message) {
    mux := http.NewServeMux()
    // 企业微信验证 URL 有效性时用 GET（echostr 参数）
    mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet:
            h.handleVerification(w, r)
        case http.MethodPost:
            h.handleMessage(w, r, msgCh)
        }
    })
    h.server = &http.Server{Addr: addr, Handler: mux}
    go func() {
        <-ctx.Done()
        h.server.Shutdown(context.Background())
    }()
    h.server.ListenAndServe()
}

// handleVerification responds to WeCom's URL verification request.
// GET /callback?msg_signature=xxx&timestamp=xxx&nonce=xxx&echostr=xxx
func (h *CallbackHandler) handleVerification(w http.ResponseWriter, r *http.Request) {
    echostr := r.URL.Query().Get("echostr")
    // 验签后返回解密的 echostr
    // ...
}

// handleMessage processes an incoming message from WeCom.
// POST /callback?msg_signature=xxx&timestamp=xxx&nonce=xxx
func (h *CallbackHandler) handleMessage(w http.ResponseWriter, r *http.Request, msgCh chan<- channel.Message) {
    // 1. 读取请求体（XML）
    // 2. 提取 <Encrypt> 字段
    // 3. 验签
    // 4. 解密得到消息 XML
    // 5. 解析消息类型和内容
    // 6. 检查白名单
    // 7. 转为 channel.Message 写入 msgCh
    // 8. 返回 "success"（企业微信要求）
}
```

### 5.2 公网可达

企业微信回调需要公网可达的 URL。两种方案：

| 方案 | 适用场景 | 复杂度 |
|------|---------|--------|
| **Cloudflare Tunnel** | 有域名，无公网 IP | 低，一个 `cloudflared` 命令 |
| **frp** | 无域名，有公网 IP 的 VPS | 中，需要 VPS |
| **ngrok** | 开发测试 | 低，但免费版有限制 |

推荐 Phase 2 开发阶段用 ngrok 测试，生产环境用 Cloudflare Tunnel。

---

## 6. Agent Core 消息循环（Channel Manager 升级）

### 6.1 统一消息循环

Phase 2 需要升级 Channel Manager，加入统一的消息轮询：

```go
// channel/manager.go — Phase 2 升级

// Run starts the unified message loop for all registered channels.
// 这是整个外部通道的核心驱动：轮询所有 Channel 的 Receive()，
// 取出消息后交给 handler 处理。
func (m *Manager) Run(ctx context.Context, handler func(msg Message)) {
    // 使用 reflect 或者维护一个 []<-chan Message 切片
    // 用 select 轮询所有通道
    var cases []reflect.SelectCase
    for _, ch := range m.channels {
        cases = append(cases, reflect.SelectCase{
            Dir:  reflect.SelectRecv,
            Chan: reflect.ValueOf(ch.Receive()),
        })
    }

    for {
        chosen, value, ok := reflect.Select(cases)
        if !ok {
            continue // channel closed
        }
        msg := value.Interface().(Message)
        m.logger.Debug("message received",
            zap.String("channel", msg.ChannelID),
            zap.String("user", msg.UserID),
        )
        go handler(msg) // goroutine pool 处理
    }
}
```

### 6.2 Agent 主循环集成

```go
// core/agent.go — Phase 2 升级

func (a *Agent) Run(ctx context.Context) {
    a.channelMgr.Run(ctx, func(msg channel.Message) {
        // 1. 会话管理：查找或创建会话
        session := a.sessionMgr.GetOrCreate(msg.UserID, msg.ChannelID)

        // 2. Agent Router：意图识别 + vault 上下文组装
        decision := a.router.Route(msg, session)

        // 3. LLM Gateway：调用本地或云端模型
        rawResponse, err := a.llmGateway.Chat(ctx, decision, msg)
        if err != nil {
            a.logger.Error("llm error", zap.Error(err))
            return
        }

        // 4. 输出过滤链（仅 External 通道）
        ch, _ := a.channelMgr.Get(msg.ChannelID)
        if ch.Type() == channel.External {
            rawResponse, _ = a.filterChain.Apply(rawResponse, msg.Metadata)
        }

        // 5. 发送回复
        ch.Send(msg, channel.Response{Content: rawResponse})

        // 6. 更新会话
        session.AddRound(msg.Content, rawResponse)

        // 7. 会话超时检查 → 触发记忆沉淀
        if session.ShouldEnd() {
            a.memorySedimenter.Process(ctx, session.ToConversation())
        }
    })
}
```

---

## 7. 目录结构变更

Phase 2 在 Phase 1 基础上新增以下文件：

```
go-agent/
├── internal/
│   ├── channel/
│   │   ├── channel.go              # Channel 接口（补充 Receive()）
│   │   ├── manager.go              # Channel Manager（升级消息循环）
│   │   ├── internal.go             # Internal Channel（已有）
│   │   ├── external.go             # External Channel stub → 删除，由 wecom/ 替代
│   │   └── wecom/                  # ← 新增：企业微信适配器（自包含）
│   │       ├── adapter.go          # Channel 接口实现
│   │       ├── callback.go         # HTTP 回调处理
│   │       ├── client.go           # API 客户端（发消息）
│   │       ├── auth.go             # Token 管理
│   │       ├── crypto.go           # 消息加解密
│   │       ├── contact.go          # 外部联系人白名单
│   │       ├── message.go          # XML ↔ channel.Message 转换
│   │       ├── config.go           # WeCom 配置结构
│   │       └── adapter_test.go     # 单元测试
│   │
│   ├── core/
│   │   ├── agent.go                # 主循环（升级：接入 Channel Manager）
│   │   ├── router.go               # 意图路由（Phase 2 增强）
│   │   └── session.go              # 会话管理（已有，Phase 2 增强）
│   │
│   ├── filter/
│   │   └── filters.go              # 过滤链（已有，修正 PlatformFilter）
│   │
│   ├── memory/
│   │   └── sedimentation.go        # 记忆沉淀（已有，更新 source 标识）
│   │
│   └── gateway/
│       └── http.go                 # HTTP 路由（新增 wecom 回调路由挂载点）
│
├── config/
│   └── agent.yaml                  # 新增 wecom 配置节
│
└── tests/
    ├── integration/
    │   └── phase2_1_wecom_callback.sh
    └── e2e/
        └── phase2_e2e_wechat_reply.md
```

---

## 8. 双模型策略（不变，修正引用）

Phase 2 的模型路由逻辑不变，仅将 "qclaw" 引用替换为 "wecom"：

```go
func routeModel(req ChatRequest) string {
    if hasImages(req) {
        return "llava:7b"
    }
    if req.Metadata["sensitive"] == "true" {
        return "gemma4:12b"
    }
    if req.Metadata["skill"] != "" {
        return "gemma4:12b"
    }
    return "auto"
}
```

---

## 9. 输出过滤链（修正 PlatformFilter）

### 9.1 现有 PlatformFilter 修正

```go
// filter/filters.go — 修正

func (f *PlatformFilter) Apply(text string, metadata map[string]string) (string, bool, string) {
    platform := ""
    if metadata != nil {
        platform = metadata["platform"]
    }

    switch platform {
    case "wecom", "wechat":     // ← 修正：qclaw → wecom
        return adaptForWeChat(text)
    case "webchat":
        return text, false, ""
    default:
        return text, false, ""
    }
}
```

### 9.2 审计日志修正

```
vault-audit.log:
[TIMESTAMP] FILTER channel=wecom user=xxx pii=true sensitive=false platform_adapt=true
            input_tokens=N output_tokens=N filtered_tokens=N
```

---

## 10. 记忆沉淀（修正 source 标识）

### 10.1 摘要格式修正

```yaml
---
title: "对话摘要：关于 X 的讨论"
created: 2026-07-01T14:30:00+08:00
source: wecom-session          # ← 修正：qclaw-session → wecom-session
tags: [topic1, topic2]
confidence: 0.8
---
```

### 10.2 记忆来源标识

每条记忆需带来源通道标识，用于后续追溯：
- `wecom-session`：企业微信外部联系人对话
- `internal-session`：Codex/CLI 内部对话
- `webchat-session`：Web Chat 对话（Phase 3）

---

## 11. 权限矩阵（修正）

| 操作 | Internal Channel | External Channel (WeCom) |
|------|:---:|:---:|
| 读 personal-vault | ✅ | ❌ |
| 写 personal-vault | ✅ | `_memory/` 仅通过记忆沉淀流程 |
| 读 agent-vault | ✅ | ✅ |
| 写 agent-vault | ✅ | ✅ (限定 skill) |
| 模型: local | ✅ | ✅ |
| 模型: online | ✅ | ✅ (非敏感) |
| 向量检索 | ✅ | ✅ (仅 agent-vault) |
| Shell 命令 | ✅ | ❌ |
| 审计日志 | 可选 | 强制 |
| 白名单检查 | ❌ | ✅ (必须通过) |

---

## 12. 实施任务

### Task 1: Channel 接口补充
- 在 `channel.go` 中添加 `Receive()` 方法
- 更新 `InternalChannel` 实现 `Receive()`
- 删除 `external.go` stub
- 更新 `manager.go` 添加统一消息循环 `Run()` 方法
- 单元测试

### Task 2: WeCom 适配器实现
- 创建 `internal/channel/wecom/` 包
- 实现 `crypto.go`（AES 加解密 + 签名验证）
- 实现 `auth.go`（Token 管理 + 自动刷新）
- 实现 `callback.go`（HTTP 回调处理）
- 实现 `client.go`（API 客户端发消息）
- 实现 `contact.go`（白名单过滤）
- 实现 `message.go`（XML ↔ Message 转换）
- 实现 `adapter.go`（Channel 接口实现）
- 单元测试

### Task 3: Agent 主循环集成
- 更新 `agent.go` 接入 Channel Manager 消息循环
- 更新配置加载支持 `channels.wecom` 配置节
- 更新 `.env.example` 添加 WeCom 环境变量

### Task 4: 过滤链修正
- 修正 PlatformFilter 中的 "qclaw" → "wecom"
- 修正审计日志中的 channel 标识
- 确认过滤链对所有 External 通道通用

### Task 5: 记忆沉淀修正
- 修正 source 标识：qclaw-session → wecom-session
- 确认沉淀流程对所有 External 通道通用

### Task 6: 公网穿透配置
- 安装 Cloudflare Tunnel 或 ngrok
- 配置回调 URL（如 `https://your-domain.com/callback`）
- 在企业微信后台配置回调 URL + Token + EncodingAESKey

### Task 7: 企业微信后台配置
- 登录企业微信管理后台
- 创建应用（或使用已有的）
- 配置"客户联系"权限
- 配置回调 URL
- 测试 URL 验证通过

### Task 8: 端到端验证
- 微信用户添加企业微信为联系人
- 微信用户发送消息
- Agent 收到消息 → 调用 LLM → 过滤 → 回复
- 验证白名单拒绝未授权用户
- 验证记忆沉淀写入 _memory/
- 验证审计日志

---

## 13. 与旧方案的差异对照

| 维度 | 旧方案 (QClaw) | 新方案 (WeCom) |
|------|---------------|---------------|
| 微信接入 | QClaw 个人微信扫码 | 企业微信"客户联系" |
| 协议 | AGP (WebSocket) | 企业微信回调 (HTTP POST) + REST API |
| 外部依赖 | Node.js sidecar | 无（Go 原生） |
| 认证 | OAuth + JWT | CorpID + CorpSecret → access_token |
| 消息加密 | AGP 协议内置 | AES-256-CBC（企业微信标准） |
| 用户筛选 | 无（扫码即用） | 白名单 + 企业微信审批 |
| 替换成本 | 改 sidecar + 协议 + 适配器 | 新写一个 adapter 包 + 改配置 |
| Agent Core 改动 | 需要理解 AGP 协议 | 零改动（只认 Channel 接口） |

---

## 14. 设计约束

- **接口隔离**：Agent Core 只认 `Channel` 接口，不 import `wecom/` 包
- **配置分离**：WeCom 专用配置在 `wecom.Config` 中，Agent Core 只关心 `enabled: true/false`
- **白名单前置**：未授权用户的消息在 adapter 层就被拒绝，不进入 Agent Core
- **过滤链通用**：所有 External 通道共用同一个 `filter.Chain`，不针对特定平台
- **记忆标识**：所有记忆沉淀带 `source` 字段，可追溯来源通道
- **可替换**：未来替换为企业微信以外的方案，只需写新的 adapter 包 + 改配置