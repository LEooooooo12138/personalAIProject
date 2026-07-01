# Project Constitution: 全阶段架构约束与工程标准

> **定位**: 本文件为 personalAIProject 的最高约束文档。每一个子阶段（Phase 1.1 到 Phase 4.x）开工前必须读取，收工验收时必须对照自检。
> **原则**: 规范是为保障目标服务，不为规范而规范。如有冲突，以"两大终极需求"为准（本地文件操作 + 可复用 API）。

---

## 一、项目终极目标

在全部四个 Phase 完成后，系统必须满足：

1. **本地文件操作**：Agent 对指定目录具备读、写、复制、搜索能力，无需第三方云服务
2. **可复用 API 访问**：任何外部系统（Home Assistant、自定义脚本、Web 前端）通过同一个 OpenAI-compatible API 即可调用 Agent 的全部推理能力，不需要为每个场景单独开发接口

---

## 二、架构不变式 (Architecture Invariants)

以下约束在 Phase 1 到 Phase 4 全程有效。每引入新模块，必须自问：**是否违反了其中任何一条？**

### 2.1 控制权归约

| 编号 | 不变式 | 理由 | 违反示例 |
|------|--------|------|---------|
| **I-1** | 所有外部请求必须先回到本地 Go Agent，再由 Agent 统一调度 | 防止外部通道绕过隐私检查直接调云端模型 | WeChat sidecar 里直接调 DeepSeek API |
| **I-2** | LLM Gateway 是唯一的模型调用入口 | 统一鉴权、路由、日志、降级 | Inference Service 里加了独立的 API Key 认证逻辑 |
| **I-3** | Agent Router 是唯一的调度决策层 | 隐私检查、模型选择、fallback 策略集中管理 | External Channel handler 里写死"图片→llava" |

### 2.2 数据隔离

| 编号 | 不变式 | 理由 | 违反示例 |
|------|--------|------|---------|
| **I-4** | External Channel 永远不能读取 `personal-vault` | privacy boundary | WeChat 回复引用了 journal/ 里的日记内容 |
| **I-5** | External Channel 写入 `personal-vault` 仅限于 `_memory/` 目录，且必须通过 Memory Sedimentation 流程 | 防止外部输入污染隐私数据 | Web Chat 用户直接写了一篇 concepts/ 页面 |
| **I-6** | Agent 对外输出的知识必须来自 `agent-vault`，不能直接引用 `personal-vault` 的页面 | 知识边界清晰 | 对外 API 返回了 personal-vault 的 wiki link |

### 2.3 接口约定

| 编号 | 不变式 | 理由 | 违反示例 |
|------|--------|------|---------|
| **I-7** | 所有 LLM 调用入口统一为 `/v1/chat/completions`（OpenAI-compatible） | 调用方零适配成本 | Phase 4 新加了一个 `/smart-home/analyze` 专用端点绕过 Gateway |
| **I-8** | API 响应格式向前兼容：新 Phase 可以增加字段，不能删除或重命名已有字段 | 不破坏已有调用方 | Phase 2 把 `metadata` 重命名为 `context` |
| **I-9** | Channel 接口 (`internal/channel/channel.go` 定义的 `Channel` interface) 是所有通道的唯一抽象 | 将来换 WeChat → Telegram 只需写新 adapter | 在 Agent Router 里硬编码 QClaw 逻辑 |

---

## 三、代码规范

### 3.1 通用原则

| 原则 | 标准 | 检查方式 |
|------|------|---------|
| **单一职责** | 每个文件/模块只做一件事，文件名能准确描述其职责 | 如果一个文件超过 400 行，立即自问：能拆吗？ |
| **接口先行** | 模块间通信必须通过 Go `interface` 或 Python ABC，不允许直接 import 内部实现 | `internal/vault/reader.go` 被 `internal/router/` import 时必须通过 interface |
| **依赖方向** | `cmd/` → `internal/core/` → `internal/gateway/`, `internal/vault/`, `internal/inference/`；`internal/` 之间不允许循环依赖 | `go vet` + 人工检查 import graph |
| **配置集中** | 所有可变参数（端口、路径、超时、model name）必须在 `config/agent.yaml` 或环境变量中，禁止硬编码 | `rg "localhost:11434" internal/` 应该返回零结果（应从 config 读） |

### 3.2 Go 代码规范

```
go-agent/
├── cmd/              # 唯一入口，不包含业务逻辑
│   └── agentd/       # main.go 只做: 加载配置 → 初始化依赖 → 启动服务
├── internal/         # 所有业务逻辑，外部不可 import
│   ├── core/         # Agent 主循环、配置、路由决策——系统的"大脑"
│   ├── gateway/      # HTTP 接入层——只做协议转换，不做业务决策
│   ├── channel/      # 通信通道抽象——Phase 1 可以先只实现 internal
│   ├── inference/    # 推理客户端——对 Agent 其他模块暴露 interface，隐藏 Ollama/vLLM/Cloud 细节
│   ├── vault/        # Vault 读写——直接操作文件系统，不依赖 obsidian-wiki CLI
│   ├── memory/       # 长期记忆——Phase 2 启用
│   └── skill/        # Skill 实现——Phase 2 启用
├── pkg/              # 可复用工具库（config、logger、errors）
└── config/           # YAML 配置文件
```

**耦合度检查清单**（每次代码变更后自检）：

- [ ] `cmd/agentd/main.go` 的行数是否 < 100？（不应膨胀为"上帝函数"）
- [ ] `internal/core/` 是否不 import 任何 `internal/gateway/`？（核心决策层不应依赖接入层）
- [ ] `internal/inference/` 是否通过 interface 暴露，调用方不知道底层是 Ollama 还是云 API？
- [ ] `internal/vault/` 是否直接 `os.ReadFile`，没有调用 `os/exec` 去跑 obsidian-wiki CLI？
- [ ] 有没有任何 `internal/` 包 import 了另一个 `internal/` 包的内部类型而非 interface？
- [ ] `go vet ./...` 和 `go test ./...` 是否全部通过？

**错误处理规范**：

```go
// 正确：层层返回，在边界处统一处理
func (r *Router) Route(ctx context.Context, msg Message) (*RouteDecision, error) {
    decision, err := r.analyze(ctx, msg)
    if err != nil {
        return nil, fmt.Errorf("router: analyze: %w", err)  // 包装但不吞掉
    }
    return decision, nil
}

// 错误：直接 log.Fatal 或 panic（只在 cmd/ 的 main 中可以）
// 错误：return nil, nil（调用方无法区分"空结果"和"错误"）
```

### 3.3 Python 代码规范

```
inference-service/
├── main.py              # FastAPI 应用组装
├── routers/             # 端点实现——每个文件一个端点组
│   ├── chat.py
│   ├── embeddings.py
│   └── models.py
├── ollama_client.py     # Ollama HTTP 适配——对 routers 暴露 async function，隐藏 httpx 细节
├── model_router.py      # 模型路由——纯函数，不依赖 FastAPI/网络
└── tests/               # pytest，与源码同目录结构
```

**耦合度检查清单**：

- [ ] `routers/` 中的函数是否只做参数校验 + 调 `ollama_client` + 格式化响应？
- [ ] `model_router.py` 是否是纯函数（输入 parameters → 输出 decision），不依赖任何全局状态？
- [ ] `ollama_client.py` 是否是唯一直接调用 `httpx` 的文件？
- [ ] pytest 是否全部通过？

### 3.4 跨语言边界规范

Go ↔ Python 之间只通过 HTTP 通信。 契约规则：

- 请求/响应格式为 JSON
- 所有端点必须返回标准的 OpenAI-compatible 格式
- 新增字段采用"只增不减"原则
- 端点 URL 命名使用 kebab-case，资源用复数：`/v1/chat/completions`（已定），`/internal/vault/status`

---

## 四、安全不变式

| 编号 | 不变式 | 适用阶段 |
|------|--------|---------|
| **S-1** | 密钥永不出现在代码或配置文件中。所有 secret 从环境变量读取 | Phase 1+ |
| **S-2** | `.env` 文件必须加入 `.gitignore`。`.env.example` 可以提交（仅 key 名） | Phase 1+ |
| **S-3** | 敏感内容（`metadata.sensitive=true`）强制路由到本地模型，不经过云端 API | Phase 2+ |
| **S-4** | External Channel 的所有输出必须经过 Output Filter Chain（PII → Sensitive → PersonalRef → Platform → Length） | Phase 2+ |
| **S-5** | 智能家居操作必须具备人工确认环节（`action_confirmation_required`），不允许 Agent 自主控制物理设备 | Phase 4+ |

---

## 五、测试标准

### 5.1 回归金字塔

详见 Phase 1 修订版设计文档 §0.3。核心铁律：

1. **契约即测试**：接口定义后立即写集成测试固化格式
2. **验证脚本化**：每个子阶段的验证点写成 `tests/integration/phaseX_Y_<name>.sh`
3. **新阶段 = 新测试 + 旧测试全跑**：Phase N 开发前必须跑通 Phase 1..N-1 的全量测试

### 5.2 每个子阶段的测试要求

| 测试层级 | 最低要求 | 工具 |
|---------|---------|------|
| 单元测试 | 每个 `internal/` 包的核心函数 ≥ 1 条 | Go: `testing` 标准库; Python: pytest |
| 集成测试 | 每个子阶段 ≥ 1 条 curl/脚本测试 | shell 脚本 (`tests/integration/`) |
| E2E | 每个大阶段 ≥ 1 条手动固化步骤 | Markdown 文档 (`tests/e2e/`) |

### 5.3 回归检查流程（每个子阶段收工时执行）

```powershell
# 1. 单元测试
go test ./...
pytest tests/ -v

# 2. 当前阶段集成测试
bash tests/integration/phase1_X_<name>.sh

# 3. 所有历史阶段集成测试（防止退化）
bash tests/integration/run_all.sh

# 4. CI 状态检查
# 确认 GitHub Actions 最新一次 push 的 check 通过
```

---

## 六、四阶段演进路线（全局视图）

```
Phase 1: 知识库 + Agent 骨架 + 本地推理          [当前]
  ├── 1.1 双 Vault 引导
  ├── 1.2 Ollama 推理底座
  ├── 1.3 Go Agent 骨架 + LLM Gateway
  ├── 1.4 Python Inference Service
  ├── 1.5 端到端全链路
  └── 1.6 Qdrant + Syncthing（可推迟）

Phase 2: 微信接入 + 外部通道                      [依赖 Phase 1]
  ├── QClaw 个人微信接入
  ├── External Channel 实现
  ├── 双模型路由（local/cloud/hybrid）
  ├── Output Filter Chain
  ├── Session Management + Memory Sedimentation
  └── Agent 人设配置

Phase 3: Web Chat + 容器化 + k3s                  [依赖 Phase 2]
  ├── Web Chat 挂件 (chat-widget.js)
  ├── Docker Compose 全服务编排
  ├──多架构镜像 (amd64 + arm64)
  ├── Helm Chart (k3s 部署)
  └── 跨环境可复现验证

Phase 4: 智能家居 + 自动化                         [依赖 Phase 3]
  ├── Home Assistant 部署与集成
  ├── 三平台设备接入 (涂鸦/天猫精灵/小米)
  ├── Go Agent HA Client
  ├── 规则分析引擎（建议模式）
  └── 自动化规则生命周期管理
```

---

## 七、子阶段开工/收工检查清单

以下清单在每个子阶段开工和收工时必须逐条核对。

### 开工检查（BreakGround Check）

- [ ] 是否已阅读本 Constitution 文件？（特别是 §二 架构不变式）
- [ ] 当前子阶段的目标和交付物是否明确？
- [ ] 前置依赖的子阶段是否已完成？
- [ ] 本次变更会影响哪些模块？（列出 affected packages）
- [ ] 是否会影响已有的 API 契约？（如有，必须更新契约测试）
- [ ] 是否需要新增环境变量/密钥？（如有，先更新 `.env.example` 和 `.env`）
- [ ] 当前阶段完工时测试脚本是否已预先定义文件名和路径？

### 收工检查（CheckOut Check）

- [ ] 所有单元测试通过？(`go test ./...` + `pytest`)
- [ ] 当前阶段的集成测试脚本已编写并可重跑？
- [ ] 所有历史阶段的集成测试未退化？(`bash tests/integration/run_all.sh`)
- [ ] 代码变更是否违反了任何一条架构不变式（§二）？
- [ ] 代码变更是否引入了新的耦合违规（§三）？
- [ ] 是否有硬编码的配置值（端口、路径、密钥、model name）？
- [ ] `.env.example` 是否已更新（如有新增 secret）？
- [ ] 是否已有适当的错误处理和日志输出？
- [ ] `go build` + `go vet` 零警告？
- [ ] Git commit message 是否清晰描述了变更内容？

---

## 八、关键术语表

| 术语 | 定义 | 首次出现 |
|------|------|---------|
| **personal-vault** | 用户私有的知识库，包含技术笔记、日记、隐私内容。External Channel 不可访问 | Phase 1 |
| **agent-vault** | Agent 对外输出的知识库。External Channel 可读，内容来自 personal-vault 的脱敏提升 | Phase 1 |
| **LLM Gateway** | Agent 内部统一的模型调用接口，OpenAI-compatible。所有推理请求唯一入口 | Phase 1 |
| **Agent Router** | 调度决策层：隐私检查、模型选择、fallback 策略 | Phase 1 |
| **Internal Channel** | Codex CLI / curl 直接调用。完整读写权限 | Phase 1 |
| **External Channel** | 微信 / WebChat 等对外通道。受限权限 | Phase 2 |
| **Output Filter Chain** | External Channel 输出必经的过滤器链（PII → Sensitive → PersonalRef → Platform → Length） | Phase 2 |
| **Memory Sedimentation** | 对话结束后生成结构化摘要 → TF-IDF 去重 → 写入 personal-vault/_memory/ | Phase 2 |
| **Knowledge Promotion** | 人工审核后，将 personal-vault 中的知识脱敏提升到 agent-vault | Phase 2 |

---

> **本文件最后更新**: 2026-07-01
> **下次审查**: Phase 1.3 收工时（验证是否所有标准在实际代码中可执行）
