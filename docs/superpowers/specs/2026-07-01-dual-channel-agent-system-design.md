# 双通道 AI Agent 系统设计 v1.0

> **状态**: 设计中 (brainstorming) | **依赖**: obsidian-wiki 知识库
> **目标**: 构建基于 Go 核心 + Python 推理的双通道 AI Agent，接入企业微信和个人网站，部署于 k3s 集群。

---

## 1. 系统总览

```
┌──────────────────────────────────────────────────────────────────────┐
│                          k3s Cluster                                  │
│                                                                      │
│  ┌─────────────────────────┐    ┌──────────────────────────────────┐ │
│  │  Inference Service      │    │        Go Agent Core             │ │
│  │  (Python)               │    │                                  │ │
│  │                         │    │  ┌────────────┐ ┌────────────┐  │ │
│  │  Phase 1: Ollama        │◄──►│  │ Internal   │ │ External   │  │ │
│  │  Phase 2: vLLM + GPU    │    │  │ Channel    │ │ Channel    │  │ │
│  │  线上 API fallback      │    │  │            │ │            │  │ │
│  └─────────────────────────┘    │  │ Codex/CLI  │ │ WeChat WS  │  │ │
│                                  │  │ Full R/W   │ │ + Web Chat │  │ │
│  ┌─────────────────────────┐    │  └────────────┘ └─────┬──────┘  │ │
│  │  Qdrant / Weaviate      │    │                        │         │ │
│  │  向量检索 + 长期记忆     │    │                  ┌─────┴──────┐ │ │
│  └─────────────────────────┘    │                  │  Gateway   │ │ │
│                                  │                  │  WS + HTTP │ │ │
│  ┌─────────────────────────┐    │                  └─────┬──────┘ │ │
│  │  双 Vault                │    └────────────────────────│────────┘ │
│  │  ~/personal-vault/      │                              │          │
│  │  ~/agent-vault/         │                              │          │
│  └─────────────────────────┘                              │          │
│                                                   ┌───────┴──────┐  │
│                                                   │              │  │
│                                             企业微信 WebSocket  你的个人网站│
│                                             AI Bot 长连接      Web Chat 挂件│
└──────────────────────────────────────────────────────────────────────┘
```

### 1.1 数据流

```
用户微信 → 企业微信 WS → Go Agent (External Channel)
                              │
                              ├─ 1. 先做任务理解、隐私检查、vault/向量检索
                              ├─ 2. 由本地 Agent 统一决定 local / cloud / hybrid
                              ├─ 3. 经 LLM Gateway 调用本地 12B 或云端 LLM
                              ├─ 4. 写 agent-vault (wiki-update skill 逻辑)
                              ├─ 5. 写 personal-vault/_memory/ (对话摘要, 单向 flush)
                              └─ 6. 经输出策略 + 过滤链后回复微信 / Web Chat

Codex (Internal Channel) → Go Agent (Internal Channel)
                              │
                              ├─ 完整读写 personal-vault + agent-vault
                              ├─ wiki-ingest / wiki-query / wiki-lint 等操作
                              └─ 手动提升知识: personal → agent
```

### 1.2 技术栈

| 组件 | 语言 | 框架 | 部署 |
|------|------|------|------|
| **Agent Core** | Go | Gin + gorilla/websocket | k3s Deployment |
| **Inference Service** | Python | FastAPI + Ollama/vLLM | k3s Deployment (GPU node) |
| **向量库** | — | Qdrant (首选) 或 Weaviate | k3s StatefulSet |
| **双 Vault** | Markdown | obsidian-wiki 模式 | k3s hostPath / NFS PV |
| **Orchestration** | — | k3s | 3 节点 (节点角色待定) |

---

## 2. Go Agent Core 架构

### 2.1 核心模块

```
go-agent/
├── cmd/
│   └── agentd/                     # 主入口
│       └── main.go
├── internal/
│   ├── core/
│   │   ├── agent.go                # Agent 主循环
│   │   ├── session.go              # 会话管理 (内存 + Redis)
│   │   └── router.go               # 意图路由 (LLM 调用)
│   │
│   ├── channel/                    # 双通道抽象
│   │   ├── channel.go              # Channel 接口定义
│   │   ├── internal/
│   │   │   ├── internal.go         # Internal Channel (Codex/CLI)
│   │   │   └── handler.go         # CLI/HTTP handler
│   │   └── external/
│   │       ├── external.go         # External Channel 抽象
│   │       ├── wecom/
│   │       │   ├── client.go       # 企业微信 WS 客户端
│   │       │   ├── message.go      # 消息编解码
│   │       │   └── auth.go         # Token/签名管理
│   │       └── webchat/
│   │           ├── hub.go          # WebSocket Hub
│   │           └── handler.go      # HTTP → WS upgrade
│   │
│   ├── inference/
│   │   ├── client.go               # Inference Service gRPC/HTTP 客户端
│   │   ├── router.go               # 模型路由 (本地/线上)
│   │   └── stream.go               # 流式响应处理
│   │
│   ├── memory/
│   │   ├── vector.go               # Qdrant 客户端
│   │   ├── session_memory.go       # 短期会话记忆
│   │   ├── long_term.go            # 长期记忆 (personal-vault _memory/)
│   │   └── retrieval.go            # 知识检索 (agent-vault + Qdrant)
│   │
│   ├── vault/
│   │   ├── reader.go               # Vault 文件读取
│   │   ├── writer.go               # Vault 文件写入
│   │   ├── sync.go                 # personal → agent 提升
│   │   └── manifest.go             # .manifest.json 管理
│   │
│   ├── skill/                      # obsidian-wiki Skills 的 Go 实现
│   │   ├── ingest.go               # 源文件蒸馏
│   │   ├── query.go                # 知识检索
│   │   ├── lint.go                 # 健康检查
│   │   ├── capture.go              # 对话 → wiki
│   │   └── update.go               # 项目同步
│   │
│   └── gateway/
│       ├── ws.go                    # WebSocket 网关
│       ├── http.go                  # HTTP API
│       └── middleware.go            # 认证/限流/日志
│
├── pkg/                             # 可复用包
│   ├── config/                      # 配置管理 (Viper)
│   ├── logger/                      # 结构化日志 (Zap)
│   └── errors/                      # 错误类型
│
├── config/
│   └── agent.yaml                   # 默认配置
├── deploy/
│   ├── docker-compose.yml           # 本地开发
│   ├── k3s/                         # k3s 部署清单
│   │   ├── agent-deployment.yaml
│   │   ├── inference-deployment.yaml
│   │   ├── qdrant-statefulset.yaml
│   │   └── ingress.yaml
│   └── Dockerfile
├── go.mod
├── go.sum
└── Makefile
```

### 2.2 Channel 接口抽象

```go
// channel.go — 双通道统一接口

type Message struct {
    ID          string
    Channel     string            // "internal" | "external.wecom" | "external.webchat"
    UserID      string            // 发送者标识
    GroupID     string            // 群聊 ID (空 = 私聊)
    Content     string            // 文本内容
    Attachments []Attachment      // 图片/文件
    Metadata    map[string]string // 通道特有元数据
    Timestamp   time.Time
}

type Response struct {
    Content      string
    Attachments  []Attachment
    Stream       bool              // 是否流式输出
    Reasoning    string            // 思考过程 (可选对外展示)
}

type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop() error
    Receive() <-chan Message      // 入站消息流
    Send(msg Message, resp Response) error  // 出站回复
    CanAccessPrivate() bool       // 是否有权读 personal-vault
}
```

### 2.3 双通道权限隔离

```go
// Internal Channel (Codex/CLI)
//   CanAccessPrivate() → true
//   可读写 personal-vault + agent-vault
//   可执行任意 skill

// External Channel (WeCom/WebChat)
//   CanAccessPrivate() → false
//   仅读 agent-vault
//   输出需经 filter 层脱敏
//   对话摘要单向写入 personal-vault/_memory/ (非直接文件操作)
```

### 2.4 外部输出过滤器

```go
// 每次 External Channel 回复前经过 filter 链
type OutputFilter interface {
    Filter(ctx context.Context, msg Message, rawResponse string) (string, error)
}

// filter 链:
//   1. PIIFilter        — 去掉手机号/身份证/地址
//   2. SensitiveFilter   — 去掉 internal/pii 标签内容
//   3. PersonalRefFilter — 去掉 [[personal-vault/...]] 引用
//   4. LengthFilter      — 控制回复长度上限
//   5. MarkdownFilter    — WeChat 不支持的 Markdown 语法转义
```

---

## 3. Inference Service (Python)

### 3.1 架构

```
┌─────────────────────────────────────────┐
│          Inference Service               │
│          (FastAPI + uvicorn)             │
│                                          │
│  POST /v1/chat/completions               │
│  POST /v1/embeddings                     │
│  GET  /v1/models                         │
│  GET  /health                            │
│                                          │
│  ┌────────────────────────────────────┐  │
│  │        Model Router                │  │
│  │                                    │  │
│  │  request.model == "local"  ──►    │  │
│  │     Ollama / vLLM                  │  │
│  │                                    │  │
│  │  request.model == "online" ──►    │  │
│  │     OpenAI-compatible API          │  │
│  │     (DeepSeek / Qwen / ...)       │  │
│  │                                    │  │
│  │  request.model == "auto"  ──►     │  │
│  │     隐私敏感 → local               │  │
│  │     通用知识 → online              │  │
│  │     local 超时 → online fallback   │  │
│  └────────────────────────────────────┘  │
│                                          │
│  ┌────────────────────────────────────┐  │
│  │        Embedding Server            │  │
│  │        (BGE-small-zh / m3e)        │  │
│  └────────────────────────────────────┘  │
└─────────────────────────────────────────┘
```

### 3.2 演进路径

```
Phase 1: Ollama (HTTP API)
  ├── 模型: qwen2.5:14b / deepseek-r1:14b (INT4)
  ├── GPU: 5080 24GB VRAM → 14B INT4 绰绰有余
  ├── Embedding: ollama pull bge-m3
  └── 优点: 一条命令启动，零配置

Phase 2: vLLM (生产优化)
  ├── 前缀缓存 (prefix caching)
  ├── Continuous batching (更高吞吐)
  ├── Tensor 并行 (如需多 GPU)
  └── 触发条件: 并发用户 > 3, 或延迟敏感
```

### 3.3 API 约定

```python
# Inference Service 提供 OpenAI-compatible API
POST /v1/chat/completions
{
    "model": "auto",            # auto | local | online | qwen2.5:14b | deepseek-v3
    "messages": [...],
    "stream": true,
    "temperature": 0.7,
    "max_tokens": 2048,
    "metadata": {
        "channel": "qclaw",     # 通道来源 (用于路由和日志)
        "sensitive": false      # 是否涉及隐私 (true → 强制走 local)
    }
}
```

---

## 4. 双 Vault 集成

### 4.1 Vault 结构 (已设计, 此处引用)

| | `~/personal-vault/` | `~/agent-vault/` |
|---|---|---|
| **谁写** | 你 + Codex (Internal) | Go Agent (External) |
| **谁读** | 你 + Codex (Internal) | Go Agent (External) + 你 |
| **WeChat 对话存档** | `_memory/` (Agent 单向写入) | ❌ |
| **隐私内容 (journal/)** | ✅ | ❌ |
| **Agent 可用知识** | ❌ (除非手动提升) | ✅ |

### 4.2 知识提升流程

```
personal-vault/concepts/rag.md        agent-vault/concepts/rag.md
    │                                       ▲
    │  你 review → 脱敏 → 手动 cp             │
    └──────────────────────────────────────►│
    提升操作:
      1. 去掉 visibility/pii 标记
      2. 去掉 sources 中的敏感路径
      3. 打上 promoted: true frontmatter
      4. 更新 agent-vault 的 index.md / .manifest.json
```

### 4.3 WeChat 对话 → 长期记忆

```
WeChat 消息 → Go Agent 处理 → 回复
                  │
                  │ 每段对话结束后
                  │
                  ▼
         生成摘要 (≤200 字):
           - 主题
           - 关键决策
           - 待跟进
                  │
                  │ sync 脚本定时 flush
                  ▼
         personal-vault/_memory/
           2026-07-01_1430_contact-name.md
                  │
                  │ Agent 不可读此目录
                  ▼
          你定期 review → 有价值内容提升到 agent-vault
```

---

## 5. 外部微信接入（QClaw）

### 5.1 通道：QClaw 个人微信（修订）

外部通道仅负责消息收发，不负责模型决策。所有外部任务都先回到本地 Go Agent，再由 Agent 统一决定调用本地模型或云端 LLM。

### 5.2 当前接入路径：QClaw 个人微信

Phase 2 优先采用 QClaw 个人微信扫码路径，微信通道仅作为 adapter，不承担调度职责。

### 5.3 旧企业微信方案（保留参考，已被 QClaw 替代）

旧方案描述已迁移为参考信息，不再作为主线设计。

---

## 6. 个人网站 Web Chat 挂件

### 6.1 架构

```
用户浏览器
    │
    │ GET https://your-domain.com
    │ → 静态页面 (Hugo/Astro/Next.js)
    │
    │ WebSocket ws://agent:8080/ws/chat
    │ → Go Agent (External Channel webchat)
    │
    ▼
聊天挂件 (右下角气泡)
    ├── 流式输出 (逐字显示)
    ├── 思考过程 (可选展开)
    ├── Markdown 渲染
    └── 与 WeChat 共享同一 Agent 会话
```

### 6.2 嵌入方式

```html
<!-- 你的个人网站 HTML 底部 -->
<script src="https://your-domain.com/chat-widget.js"></script>
<script>
  ChatWidget.init({
    endpoint: "wss://agent.your-domain.com/ws/chat",
    theme: "dark",
    position: "bottom-right",
    greeting: "你好，我是 XXX 的 AI 助手。有什么可以帮你？"
  });
</script>
```

### 6.3 独立部署

```yaml
# k3s: webchat-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: webchat-widget
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: webchat
        image: nginx:alpine
        volumeMounts:
        - name: widget
          mountPath: /usr/share/nginx/html
      volumes:
      - name: widget
        configMap:
          name: webchat-widget-js
---
# chat-widget.js 由 Go template 或独立前端项目输出
# 内嵌 WebSocket 客户端, 连接 Agent 的 /ws/chat 端点
```

---

## 7. k3s 部署架构

### 7.1 节点规划 (待硬件到位后确定)

```
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│   台式机      │   │  Mac Mini    │   │    NAS       │
│   (GPU)      │   │  (24/7)      │   │  (存储)      │
├──────────────┤   ├──────────────┤   ├──────────────┤
│ 推理服务      │   │ Agent Core   │   │ Qdrant       │
│ (GPU node)   │   │ WebChat      │   │ Vault PV     │
│              │   │ Gateway      │   │ Git mirror   │
└──────────────┘   └──────────────┘   └──────────────┘
```

### 7.2 服务清单

| Service | 类型 | 副本 | 节点 | 端口 |
|---------|------|------|------|------|
| agent-core | Deployment | 2 | Mac Mini + 台式机 | 8080 |
| inference | Deployment | 1 | 台式机 (GPU) | 8000 |
| qdrant | StatefulSet | 1 | NAS | 6333 |
| vault-pv | hostPath PV | — | NAS | — |
| webchat-widget | Deployment | 1 | Mac Mini | 80 |
| ingress | nginx-ingress | — | Mac Mini | 443 |

### 7.3 存储

```yaml
# 双 Vault 存储
apiVersion: v1
kind: PersistentVolume
metadata:
  name: vaults-pv
spec:
  capacity:
    storage: 100Gi
  accessModes:
    - ReadWriteMany          # Agent 和推理服务都需读写
  nfs:
    server: nas.local
    path: /volume/vaults

---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: vaults-pvc
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 50Gi
```

---

## 8. 安全模型

### 8.1 权限矩阵

| 操作 | Internal Channel | External Channel |
|------|:---:|:---:|
| 读 personal-vault | ✅ | ❌ |
| 写 personal-vault | ✅ | `_memory/` 单向 |
| 读 agent-vault | ✅ | ✅ |
| 写 agent-vault | ✅ | ✅ (限定 skill) |
| 模型: local 12B | ✅ | ✅ |
| 模型: online API | ✅ | ✅ (非敏感) |
| 向量检索 | ✅ | ✅ (仅 agent-vault) |
| Shell 命令执行 | ✅ | ❌ |

### 8.2 外部输出过滤链

```
Agent 原始输出
    │
    ├─ PIIFilter            ← 正则匹配脱敏
    ├─ SensitiveFilter      ← 黑名单关键词/标签
    ├─ PlatformFilter       ← 平台格式适配 (WeChat vs Web)
    └─ LengthFilter         ← 截断超长输出
    │
    ▼
最终发送到 External Channel
```

### 8.3 隐私路由规则

```go
// inference/router.go
func RouteModel(msg Message, content string) string {
    if containsPersonalInfo(content) {
        return "local"     // 强制本地模型
    }
    if msg.Channel == "external" && isSensitiveTopic(content) {
        return "local"     // 敏感话题本地处理
    }
    return "auto"          // 通用话题优先线上 API
}
```

---

## 9. 实施阶段

### Phase 1: 知识库 + 基础 Agent (当前)
- obsidian-wiki 初始化 + 种子内容
- Go Agent 骨架 (单通道, Internal Only)
- Ollama 集成验证

### Phase 2: WeChat 接入
- 通过 QClaw 接入个人微信
- 所有外部消息先回本地 Go Agent，由 Agent 统一决定 local / cloud / hybrid
- External Channel 输出策略 + 过滤器
- 双 Vault 读写逻辑

### Phase 3: Web Chat + 容器化
- Web Chat 挂件 + 个人网站集成
- Docker Compose 本地验证
- k3s 集群部署
- 双 Vault 知识提升流程打通

### Phase 4: 智能家居 + 自动化
- (后续子项目, 另开 spec)

---

## 10. 配置文件示例

```yaml
# agent.yaml
server:
  port: 8080
  ws_path: /ws/chat

channels:
  internal:
    enabled: true
    port: 9090               # CLI/Codex 连接端口
  external:
    qclaw:
      enabled: false         # Phase 2 启用
      sidecar_url: "http://localhost:9090"
    webchat:
      enabled: false         # Phase 3 启用
      ws_path: /ws/chat
      cors_origins: ["https://your-domain.com"]

inference:
  endpoint: "http://inference-service:8000"
  models:
    local: "gemma4:12b"
    online: "deepseek-chat"
    auto_routing: true
  fallback_timeout: 30s

vaults:
  personal:
    path: /data/vaults/personal  # k3s PV 挂载
  agent:
    path: /data/vaults/agent

memory:
  qdrant:
    endpoint: "http://qdrant:6333"
    collection_agent: "agent_memory"
    collection_personal: "personal_memory"  # Internal Only
  session:
    max_rounds: 20              # 会话上下文保留轮数
    ttl: 24h

security:
  output_filter:
    enabled: true               # External Channel 强制启用
    pii_patterns: true
    sensitive_keywords: true
  privacy_routing:
    enabled: true               # 自动路由到本地模型
```

---

## 11. 更新说明 (v1.1)

> 以下内容来自对 §2 Channel 接口和 Skill Executor 的讨论修订。

### 11.1 Skill Executor: Go 原生 + LLM 混合

Go Agent 不从零重写所有 obsidian-wiki Skills，也不完全依赖 Codex CLI 子进程。
按**是否需要 LLM 判断力**切分：

**Go 原生 (毫秒级，无 token 成本)：**
- 读 `index.md` / `log.md` / `hot.md`
- 解析 wiki 页面 YAML frontmatter
- 更新 `.manifest.json`
- 追加 `log.md` 条目
- `grep` 搜索关键词
- Qdrant 向量检索
- `wiki-lint`（遍历文件系统检查断链、frontmatter 完整性）
- Obsidian wiki 标准 wiki-update skill 的查询逻辑

**通过 LLM Gateway 调模型 (秒级，需要 token)：**
- `wiki-ingest`（读源文件、蒸馏、改写 wiki 页面）
- `wiki-capture`（对话改写为陈述知识）
- `cross-linker`（语义理解补链接）
- `wiki-dedup`（内容相似度判断）
- `wiki-synthesize`（多概念交叉分析）

**Go = 管道，LLM = 大脑。** Go 负责所有可写出确定性规则的操作；LLM 只在需要阅读理解、判断、写作时介入。

### 11.2 Internal Channel = LLM Gateway

Internal Channel 不只是 Codex CLI 的入口，而是**整个系统的 LLM 访问层**。
所有 LLM 调用——来自你、Codex CLI、Agent 内部 skill——走同一个 OpenAI-compatible API：

| 端点 | 方法 | 用途 |
|------|------|------|
| `/v1/chat/completions` | POST | LLM 对话（OpenAI-compatible） |
| `/v1/embeddings` | POST | 向量嵌入 |
| `/v1/models` | GET | 列出可用模型 |
| `/internal/vault/status` | GET | Vault 状态 |
| `/internal/vault/promote` | POST | 知识提升 personal → agent |
| `/internal/config` | PUT | 配置热更新 |
| `/internal/skills` | GET | 可用 skill 列表 |

**调用方：**
- `curl` 直接调用（你手动调试）
- Codex CLI（通过 `OPENAI_BASE_URL=http://agent:8080/v1`）
- Agent 内部 Skill Executor（goSkills 自动，llmSkills 通过此网关）
- External Channel（WeChat/WebChat 收到用户消息后调度）

**Codex CLI 接入方式：**
```bash
export OPENAI_BASE_URL="http://localhost:8080/v1"
export OPENAI_API_KEY="$INTERNAL_API_KEY"
codex
# Codex 所有 LLM 调用自动走 Agent，Agent 按 metadata 自动路由到本地/线上模型
```

### 11.3 obsidian-wiki 三阶段生命周期

| 阶段 | 执行者 | 操作 | 通道 |
|------|--------|------|------|
| **建立** (Phase 1) | Codex 手动 + LLM | wiki-setup, wiki-ingest 种子内容 | Codex CLI → LLM Gateway |
| **持续更新** (Phase 2) | Go Agent 自动 | 对话摘要写入 _memory/，自动 ingest agent-vault | External Channel → Skill Executor → LLM Gateway |
| **定期维护** (Phase 3) | Go Agent cron | wiki-lint (Go), cross-linker (LLM), synthesize (LLM) | Skill Executor → LLM Gateway |

### 11.4 双 Gateway 架构图 (最终版)

```
┌───────────────────────────────────────────────────────────────┐
│                        Go Agent Core                           │
│                                                               │
│  ┌─────────────────────────────┐  ┌─────────────────────────┐ │
│  │   LLM Gateway (Internal)    │  │ Message Gateway (External)│ │
│  │   OpenAI-compatible :8080   │  │ WeCom WS + WebChat WS   │ │
│  │                             │  │                          │ │
│  │  POST /v1/chat/completions  │  │ 入站: 用户消息           │ │
│  │  POST /v1/embeddings        │  │ 出站: Agent 回复         │ │
│  │  GET  /v1/models            │  │       (经 Output Filter) │ │
│  │                             │  └────────────┬─────────────┘ │
│  │  调用方:                     │               │              │
│  │  • curl / 你                │               │              │
│  │  • Codex CLI               │               │              │
│  │  • Agent Skill Executor     │               │              │
│  │  • External Channel ────────┼───────────────┘              │
│  │                             │                              │
│  │  ┌──────────────────────┐   │  ┌─────────────────────────┐ │
│  │  │    Model Router      │   │  │   Skill Executor        │ │
│  │  │  sensitive → local   │   │  │  goSkills ← Go 原生     │ │
│  │  │  general   → online  │   │  │  llmSkills ← LLM Gateway│ │
│  │  │  skill     → auto    │   │  └─────────────────────────┘ │
│  │  └──────────┬───────────┘   │                              │
│  └─────────────│───────────────┘                              │
│                │                                              │
│  ┌─────────────▼───────────────┐                              │
│  │   Inference Service         │                              │
│  │   Ollama ↔ vLLM ↔ 线上 API  │                              │
│  └─────────────────────────────┘                              │
└───────────────────────────────────────────────────────────────┘
```

> **用户审阅说明**: 此更新反映 v1.1 的两个澄清决策（§11.1 Skill 混合实现、§11.2 Internal Channel = LLM Gateway）。§1-§10 保持不变。

---

## 12. 修订说明（2026-07-01）

- 主文档已将外部通道控制权统一到本地 Go Agent
- 外部通道不再直接决定是否调用云端模型
- Phase 2 微信接入路径统一为 QClaw 个人微信
- 本地模型参考更新为 `gemma4:12b`
- 配置示例中的 `channels.wecom` 已替换为 `channels.qclaw`

---

## 13. 设计文档索引 (v2.0)

本系统包含四阶段，每阶段有独立设计文档：

| Phase | 设计文档 | 核心内容 |
|-------|---------|---------|
| **Phase 1** | `2026-07-01-phase1-full-plan.md` | 知识库初始化 + Go Agent 骨架 + Ollama |
| **Phase 2** | `2026-07-01-phase2-wechat-external-channel-design.md` | QClaw 个人微信 + 双模型路由 + 过滤链 + 记忆沉淀 |
| **Phase 3** | `2026-07-01-phase3-webchat-containerization-k3s-design.md` | Web Chat 挂件 + Docker Compose + Helm Chart |
| **Phase 4** | `2026-07-01-phase4-smart-home-automation-design.md` | Home Assistant 接入 + 数据分析 + 自动化建议引擎 |

### 关键设计决策汇总

| 决策 | 选择 | 理由 |
|------|------|------|
| 微信接入 | QClaw 个人微信扫码 | Marvis 同款体验，开源逆向客户端可用 |
| 通道实现 | Phase 2 Node.js sidecar → Phase 3+ Go 原生 | 先复用社区维护，后消除依赖 |
| 本地模型 | gemma4:12b 常驻 + llava:7b 按需切换 | 16GB VRAM 方案 B，单模型常驻最省显存 |
| 记忆沉淀 | LLM 生成摘要 + TF-IDF 去重 | 结构化质量高，去重避免膨胀 |
| 知识库 | 双 vault (personal + agent) | 隐私隔离，Agent 只能读 agent-vault |
| 容器化 | Docker Compose → Helm Chart | 先验证后部署，跨环境可复现 |
| 智能家居 | Home Assistant 统一层 | 涂鸦/天猫精灵/小米三平台一个 API 管 |
| 自动化 | 建议模式（人工确认） | 安全可控，用户保持最终决策权 |
