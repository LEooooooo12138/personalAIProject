# Phase 1 详细设计（修订版 v2.0）：知识库 + Agent 骨架 + 本地推理

> **状态**: 设计已确认
> **修订日期**: 2026-07-01
> **修订内容**: 基于全文档审视与 brainstorming 讨论，重构 Phase 1 范围为 6 个子阶段，明确 Windows 原生开发环境、测试策略、密钥管理、CI/CD 等工程基础设施

---

## 0. 全局工程决策

### 0.1 开发环境

| 项目 | 决策 |
|------|------|
| 操作系统 | **Windows 11（原生 PowerShell）**，不做 WSL2 |
| 包管理 | Go 1.22+, Python 3.10+, Ollama Windows 原生安装 |
| 容器化 | **Phase 3 再介入**。Phase 1-2 全部原生运行 |
| Vault 路径 | `C:\Users\Admin\vaults\personal` 和 `C:\Users\Admin\vaults\agent` |
| Docker | 仅 Phase 1.6 运行 Qdrant（如网络允许），否则推迟到 Phase 3 |

### 0.2 obsidian-wiki 定位

obsidian-wiki 仅作为**一次性引导工具 + 设计规范来源**，不在运行时依赖其 Python CLI：

- **Phase 1.1**: 用 obsidian-wiki 初始化 vault 结构、安装 Skills、创建种子页面
- **Phase 1.3+**: Go Agent 的 Vault 模块**直接读写 Markdown 文件系统**（解析 YAML frontmatter、grep 搜索、操作 .manifest.json），不调用 obsidian-wiki 的 Python CLI
- **35+ Skills 规范**: 将 wiki-ingest / wiki-query / wiki-lint 等 Skill 的语义转化为 Go 原生实现（`internal/vault/` 和 `internal/skill/`），而非包装 Python 子进程

### 0.3 测试总策略：回归金字塔

**核心原则："今天的验证点，就是明天的回归测试"**

```
        ┌─────────────────┐
        │ E2E             │  "用户视角"：每阶段至少 1 条，手动但步骤固化
        ├─────────────────┤
        │ 集成测试         │  "服务间契约"：A 调 B 的 HTTP 接口，脚本化可重跑
        ├─────────────────┤
        │ 单元测试         │  "纯逻辑"：Go testing / pytest，随代码提交
        └─────────────────┘
```

**三条铁律**:

1. **契约即测试**。LLM Gateway 的 `/v1/chat/completions` 接口定义后，立即写一条集成测试固化请求/响应格式。后续任何 Phase 改动都必须通过这条测试。
2. **验证脚本化**。每个 Phase 的验证点写成 `tests/integration/phaseX_Y_<name>.sh`，后续 Phase 改动时全量重跑。
3. **新阶段 = 新测试 + 旧测试全跑**。Phase N 开发前先跑 Phase 1..N-1 的全量集成测试。

### 0.4 密钥管理

Phase 1 仅管理一个 `INTERNAL_API_KEY`，但架构从第一天就预留扩展：

```
项目根目录/
├── .env.example       # 提交到 git，仅 key 名，value 为空
├── .env               # .gitignore，存真实值
├── config/agent.yaml  # 通过 ${VAR} 引用环境变量
```

```yaml
# config/agent.yaml
server:
  port: 8080
  internal_key: ${AGENT_INTERNAL_KEY}
```

不引入 HashiCorp Vault。Phase 2 加云端 API Key 时添加环境变量即可，不改代码。

### 0.5 CI/CD

GitHub Actions 最小门禁，Phase 1 只做两件事：

- Go test（`go test ./...`）
- Go build Linux 二进制（`GOOS=linux go build`，为 k3s 部署做准备）

```yaml
# .github/workflows/check.yml
name: Check
on: [push, pull_request]

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - name: Go test
        run: cd go-agent && go test ./...
      - name: Go build (linux)
        run: cd go-agent && GOOS=linux GOARCH=amd64 go build -o agentd ./cmd/agentd
```

### 0.6 观测性

- Go Agent: `go.uber.org/zap` 结构化日志
- Python Inference Service: `structlog`
- Phase 1 不引入 Prometheus / Grafana / OpenTelemetry

### 0.7 错误处理预留

Agent Router 接口预留 `fallback` 策略字段，Phase 1 仅实现 `fail`：

```go
type RouteDecision struct {
    TargetModel  string            // "gemma4:12b"
    Fallback     string            // "fail" | "retry_local_only" | "retry_local_then_cloud"
    Reason       string            // 路由决策原因
    ConfirmRequired bool           // Phase 4 智能家居预留：是否需要人工确认
}
```

### 0.8 两大终极需求

整个项目必须满足：

1. **本地文件操作**：最低做到读取和复制。Vault Reader/Writer 为 Phase 1 核心交付
2. **可复用 API**：LLM Gateway `/v1/chat/completions` 从第一天就考虑外部调用方（Home Assistant、其他自动化工具），Phase 4 不"另起炉灶"

---

## 子阶段总览

| 子阶段 | 交付物 | 核心验证 |
|--------|--------|---------|
| 1.1 | 双 Vault 初始化 + 种子内容 + Git | `/wiki-query RAG定义` 返回种子内容 |
| 1.2 | Ollama + gemma4:12b + bge-m3 | `curl POST :11434/v1/chat/completions` 有效 |
| 1.3 | Go Agent 骨架 + LLM Gateway + Vault Reader | `curl :8080/health` + Agent 代理 Ollama |
| 1.4 | Python Inference Service | `curl POST :8000/v1/chat/completions` |
| 1.5 | 端到端全链路 | Codex via Agent → Ollama，含 vault 上下文 |
| 1.6 | Qdrant + Syncthing（可推迟到 Phase 2 前） | Qdrant collection 创建 + Syncthing 识别 |

---

## Phase 1.1: 知识库引导

### 目标

在 Windows 上完成 obsidian-wiki 框架安装、双 Vault 初始化、AGENTS.md 个性化配置、种子内容 ingest，以及 Git 版本管理。

### 前置条件

- Python 3.10+ 已安装且 `python --version` 正常
- pip 可用
- Git 已安装

### Vault 路径

| Vault | 路径 |
|-------|------|
| personal-vault | `C:\Users\Admin\vaults\personal` |
| agent-vault | `C:\Users\Admin\vaults\agent` |

### 步骤

- [ ] **Step 1.1.1**: 安装 obsidian-wiki
  ```powershell
  pip install obsidian-wiki
  obsidian-wiki --version
  obsidian-wiki list
  ```

- [ ] **Step 1.1.2**: 创建双 vault 目录结构
  ```powershell
  $personal = "C:\Users\Admin\vaults\personal"
  $agent = "C:\Users\Admin\vaults\agent"
  New-Item -ItemType Directory -Force -Path "$personal\concepts","$personal\entities","$personal\skills","$personal\references","$personal\synthesis","$personal\journal","$personal\projects","$personal\_raw","$personal\_meta"
  New-Item -ItemType Directory -Force -Path "$agent\concepts","$agent\entities","$agent\skills","$agent\references","$agent\synthesis","$agent\journal","$agent\projects","$agent\_raw","$agent\_meta"
  ```

- [ ] **Step 1.1.3**: 分别对两个 vault 执行 `obsidian-wiki setup`
  ```powershell
  obsidian-wiki setup --vault C:\Users\Admin\vaults\personal
  obsidian-wiki setup --vault C:\Users\Admin\vaults\agent
  ```

- [ ] **Step 1.1.4**: 撰写 AGENTS.md
  - personal-vault: 技术 + 个人双轨，中文为主，隐私内容标记
  - agent-vault: 明确标注"本 vault 可被外部通道读取，不存放隐私内容"

- [ ] **Step 1.1.5**: 运行 wiki-setup，生成 index.md / log.md / hot.md / .manifest.json / taxonomy

- [ ] **Step 1.1.6**: 运行 wiki-ingest，导入种子内容（至少 4 条：RAG、LLM Wiki 模式、obsidian-wiki 框架、双通道 Agent 架构）

- [ ] **Step 1.1.7**: Git init + 首次提交
  ```powershell
  cd C:\Users\Admin\vaults\personal
  git init; git add -A; git commit -m "init: personal-vault bootstrap"
  cd C:\Users\Admin\vaults\agent
  git init; git add -A; git commit -m "init: agent-vault bootstrap"
  ```

### 验证标准

```powershell
# 验证目录结构
Get-ChildItem C:\Users\Admin\vaults\personal -Directory | Select-Object Name
Get-ChildItem C:\Users\Admin\vaults\agent -Directory | Select-Object Name

# 验证 Skills 安装
Get-ChildItem "$env:USERPROFILE\.codex\skills\" | Where-Object { $_.Name -like "*wiki*" }

# 验证 obsidian-wiki 状态
obsidian-wiki info

# 验证种子内容（通过 LLM）
# 在 Codex 中发送: /wiki-query RAG的核心原理是什么
```

### 集成测试脚本

`tests/integration/phase1_1_vault_bootstrap.sh`:
```bash
# 验证两个 vault 目录结构就绪
# 验证 AGENTS.md 存在
# 验证 .manifest.json 可解析
# 验证 seeds 数量 >= 4
```

---

## Phase 1.2: 本地推理底座（Ollama）

### 目标

在 Windows 上安装 Ollama，拉取 gemma4:12b（文本）和 bge-m3（embedding），验证本地推理可用。

### 前置条件

- Phase 1.1 完成
- RTX 5080 16GB VRAM

### 步骤

- [ ] **Step 1.2.1**: 下载并安装 Ollama for Windows
  - 从 https://ollama.com/download/windows 下载 `.exe`
  - 安装后验证: `ollama --version`

- [ ] **Step 1.2.2**: 拉取模型
  ```powershell
  ollama pull gemma4:12b    # 16GB VRAM 常驻文本模型
  ollama pull bge-m3        # 多语言 embedding
  ```

- [ ] **Step 1.2.3**: 验证 chat
  ```powershell
  $body = @{
      model = "gemma4:12b"
      messages = @(
          @{ role = "user"; content = "你好，请用一句话介绍你自己" }
      )
  } | ConvertTo-Json -Depth 3

  Invoke-RestMethod -Uri "http://localhost:11434/v1/chat/completions" `
      -Method Post `
      -ContentType "application/json" `
      -Body $body
  ```

- [ ] **Step 1.2.4**: 验证 embedding
  ```powershell
  $body = @{
      model = "bge-m3"
      input = "retrieval augmented generation"
  } | ConvertTo-Json -Depth 2

  Invoke-RestMethod -Uri "http://localhost:11434/v1/embeddings" `
      -Method Post `
      -ContentType "application/json" `
      -Body $body
  ```

### 验证标准

- gemma4:12b 返回合理的中文回复
- bge-m3 返回 1024 维向量
- 推理延迟在可接受范围（首个 token < 5s）

### 集成测试脚本

`tests/integration/phase1_2_ollama.sh`:
```bash
# 验证 gemma4:12b chat 返回 200 + choices[0].message.content 非空
# 验证 bge-m3 embedding 返回 200 + data[0].embedding 长度 = 1024
```

---

## Phase 1.3: Go Agent 骨架

### 目标

初始化 Go 模块，搭建 Agent 核心骨架（HTTP 服务 + LLM Gateway 代理 + Vault Reader），验证本地调用链：curl → Agent → Ollama。

### 前置条件

- Phase 1.2 完成（Ollama 可用）
- Go 1.22+ 已安装

### 目录结构

```
C:\Users\Admin\ai-agent\go-agent\
├── cmd\
│   └── agentd\
│       └── main.go                 # 主入口
├── internal\
│   ├── core\
│   │   ├── agent.go                # Agent 主循环（stub）
│   │   ├── config.go               # 配置加载（Viper + 环境变量）
│   │   └── router.go               # 意图路由（stub，返回固定决策）
│   ├── gateway\
│   │   ├── http.go                 # HTTP 路由（Gin）
│   │   └── middleware.go           # Auth 中间件（INTERNAL_API_KEY）
│   ├── inference\
│   │   └── client.go               # Ollama HTTP 客户端
│   └── vault\
│       ├── reader.go               # Vault 文件读取（Markdown + YAML frontmatter）
│       └── writer.go               # Vault 文件写入（stub）
├── config\
│   └── agent.yaml                  # 默认配置
├── go.mod
├── go.sum
└── Makefile
```

### LLM Gateway 端口矩阵

| 端点 | 方法 | Phase 1.3 | Phase 2+ |
|------|------|-----------|----------|
| `/health` | GET | 返回 {"status":"ok"} | 同 |
| `/v1/chat/completions` | POST | 代理到 Ollama | 智能路由 local/cloud |
| `/v1/embeddings` | POST | 代理到 Ollama | 同 |
| `/v1/models` | GET | 返回可用模型列表 | 含云端模型 |
| `/internal/vault/status` | GET | 返回 vault 统计 | 同 |
| `/internal/skills` | GET | stub（空列表） | 返回可用 skills |

### 步骤

- [ ] **Step 1.3.1**: 初始化 Go 模块
  ```powershell
  cd C:\Users\Admin\ai-agent\go-agent
  go mod init github.com/yuanleyao/ai-agent
  ```

- [ ] **Step 1.3.2**: 实现配置加载 (`internal/core/config.go`)
  - 读取 `config/agent.yaml`
  - 支持 `${VAR}` 环境变量展开
  - Phase 1 核心配置:
    ```yaml
    server:
      port: 8080
      internal_key: ${AGENT_INTERNAL_KEY}
    inference:
      endpoint: "http://localhost:11434"
      models:
        local: "gemma4:12b"
    vaults:
      personal: "C:\\Users\\Admin\\vaults\\personal"
      agent: "C:\\Users\\Admin\\vaults\\agent"
    ```

- [ ] **Step 1.3.3**: 实现 HTTP 服务 + Auth 中间件
  - Gin 路由，`:8080`
  - 中间件验证 `Authorization: Bearer ${AGENT_INTERNAL_KEY}`（仅 `/v1/*` 端点；`/health` 放行）

- [ ] **Step 1.3.4**: 实现 Ollama HTTP 客户端 (`internal/inference/client.go`)
  - POST 到 `http://localhost:11434/v1/chat/completions`
  - 透传请求体 + 返回体

- [ ] **Step 1.3.5**: 实现 Vault Reader (`internal/vault/reader.go`)
  - 读取 `index.md`、`log.md`、`hot.md`
  - 解析任意 wiki 页面的 YAML frontmatter
  - grep 搜索关键字

- [ ] **Step 1.3.6**: 实现 `/v1/chat/completions`
  - 收到请求 → 透传至 Ollama → 返回结果
  - 不在此阶段做路由决策（Phase 1.5 再说）

- [ ] **Step 1.3.7**: 编写 `agent.yaml` 和 `.env.example`

- [ ] **Step 1.3.8**: 编译并运行
  ```powershell
  go build -o agentd.exe .\cmd\agentd
  $env:AGENT_INTERNAL_KEY = "dev-key-123"
  .\agentd.exe
  ```

- [ ] **Step 1.3.9**: 添加 Go 单元测试
  - `internal/core/config_test.go`: 配置加载 + 环境变量展开
  - `internal/vault/reader_test.go`: frontmatter 解析

### 验证标准

```powershell
# 1. 健康检查
curl http://localhost:8080/health
# 预期: {"status":"ok"}

# 2. 无认证被拒
curl -X POST http://localhost:8080/v1/chat/completions `
  -H "Content-Type: application/json" `
  -d '{"model":"gemma4:12b","messages":[{"role":"user","content":"hi"}]}'
# 预期: 401 Unauthorized

# 3. 有认证成功代理
curl -X POST http://localhost:8080/v1/chat/completions `
  -H "Content-Type: application/json" `
  -H "Authorization: Bearer dev-key-123" `
  -d '{"model":"gemma4:12b","messages":[{"role":"user","content":"你好"}]}'
# 预期: 200，返回 LLM 回复

# 4. Vault 状态
curl http://localhost:8080/internal/vault/status `
  -H "Authorization: Bearer dev-key-123"
# 预期: 返回两个 vault 的目录统计

# 5. Go test
go test ./...
# 预期: 全部通过
```

### 集成测试脚本

`tests/integration/phase1_3_agent_gateway.sh`:
```bash
# 验证 /health
# 验证 无认证 → 401
# 验证 正确认证 → 200 且消息带回
# 验证 /internal/vault/status 返回有效 JSON
# 验证 go test 全部通过
```

---

## Phase 1.4: Python Inference Service

### 目标

搭建 FastAPI 推理服务 `:8000`，支持 OpenAI-compatible 端点，底层代理到 Ollama `:11434`，并预留 model router stub。

### 前置条件

- Phase 1.3 完成（Go Agent 可用）
- Python 3.10+ + pip

### 目录结构

```
C:\Users\Admin\ai-agent\inference-service\
├── main.py                          # FastAPI 应用
├── routers\
│   ├── chat.py                      # /v1/chat/completions
│   ├── embeddings.py                # /v1/embeddings
│   └── models.py                    # /v1/models
├── ollama_client.py                 # Ollama HTTP 客户端
├── model_router.py                  # 模型路由（stub：全部→local）
├── requirements.txt
└── tests\
    └── test_chat.py                  # pytest
```

### 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | `{"status":"ok"}` |
| `/v1/chat/completions` | POST | 路由到 Ollama（Phase 1.4 全部 local） |
| `/v1/embeddings` | POST | 代理到 Ollama bge-m3 |
| `/v1/models` | GET | `{"data":[{"id":"gemma4:12b"}]}` |

### 步骤

- [ ] **Step 1.4.1**: 初始化项目
  ```powershell
  pip install fastapi uvicorn httpx pytest structlog
  ```

- [ ] **Step 1.4.2**: 实现 `ollama_client.py`（异步 HTTP 客户端）

- [ ] **Step 1.4.3**: 实现 `model_router.py`
  - Phase 1.4: 全部路由到 `local`
  - 接口预留 `route(model, metadata) → {target, fallback}`

- [ ] **Step 1.4.4**: 实现三个端点

- [ ] **Step 1.4.5**: 编写 pytest 单元测试

- [ ] **Step 1.4.6**: 启动验证
  ```powershell
  uvicorn main:app --port 8000
  ```

- [ ] **Step 1.4.7**: Go Agent 重新指向 Inference Service
  ```yaml
  # config/agent.yaml 更新
  inference:
    endpoint: "http://localhost:8000"    # 之前是 11434
  ```

### 验证标准

```powershell
# 1. 健康检查
curl http://localhost:8000/health

# 2. Chat 端点
curl -X POST http://localhost:8000/v1/chat/completions `
  -H "Content-Type: application/json" `
  -d '{"model":"auto","messages":[{"role":"user","content":"你好"}]}'

# 3. Embedding 端点
curl -X POST http://localhost:8000/v1/embeddings `
  -H "Content-Type: application/json" `
  -d '{"model":"bge-m3","input":"test"}'

# 4. pytest
pytest tests/ -v
```

### 集成测试脚本

`tests/integration/phase1_4_inference_service.sh`

---

## Phase 1.5: 端到端串联

### 目标

完成全链路验证：Codex CLI → Go Agent → Inference Service → Ollama，并加入 vault 上下文读取。

### 调用链

```
curl / Codex CLI
  → Go Agent :8080 (LLM Gateway)
    → Agent Router（读取 vault 上下文 + 路由决策）
    → Inference Service :8000
      → Ollama :11434 (gemma4:12b)
    ← 结果返回
```

### 步骤

- [ ] **Step 1.5.1**: 实现 Agent Router 最小逻辑
  - 读取请求中的 `metadata.sensitive` 字段
  - 组装 vault 上下文（从 `index.md` 中提取相关条目）
  - 路由到 Inference Service

- [ ] **Step 1.5.2**: 实现 Vault Writer 最小逻辑
  - 追加 `log.md` 条目
  - 更新 `.manifest.json`

- [ ] **Step 1.5.3**: Codex CLI 接入
  ```powershell
  $env:OPENAI_BASE_URL = "http://localhost:8080/v1"
  $env:OPENAI_API_KEY = "dev-key-123"
  codex
  # Codex 所有 LLM 调用自动走 Agent → Inference Service → Ollama
  ```

- [ ] **Step 1.5.4**: wiki-query 闭环
  ```powershell
  # 通过 Agent 的 LLM Gateway 触发 wiki-query
  curl -X POST http://localhost:8080/v1/chat/completions `
    -H "Content-Type: application/json" `
    -H "Authorization: Bearer dev-key-123" `
    -d '{"model":"auto","messages":[{"role":"user","content":"使用 wiki-query 查询 RAG 的定义"}],"metadata":{"skill":"wiki-query"}}'
  ```

### 验证标准

- Codex 通过 Agent 完成一次 LLM 问答（非空回复）
- Agent 日志中记录完整的请求链：Codex → Agent → Inference → Ollama
- Agent 能读取 vault 中的种子内容并作为上下文注入
- 完成后 log.md 中有操作记录

### E2E 测试（手动固化）

`tests/e2e/phase1_5_end_to_end.md`:
```markdown
1. 启动 Ollama（确保 gemma4:12b 已加载）
2. 启动 Inference Service: uvicorn main:app --port 8000
3. 启动 Go Agent: .\agentd.exe
4. 设置 Codex 环境变量: OPENAI_BASE_URL + OPENAI_API_KEY
5. 发送: "用中文解释什么是 RAG"
6. 验证: 回复包含 RAG 的核心概念，提及 obsidian-wiki vault 中的种子内容
```

---

## Phase 1.6: Qdrant + Syncthing（可推迟）

### 目标

Qdrant 本地运行 + Syncthing 单节点预埋。**此阶段可在 Phase 2 开发前夕再做**，不影响 Phase 1.5 的闭环。

### 步骤

- [ ] Qdrant: Docker 运行（如网络允许），否则推迟到 Phase 3
- [ ] Syncthing: Windows 原生安装，添加两个 vault 文件夹
- [ ] 验证: Qdrant collection 创建成功；Syncthing 识别 vault 文件夹

### 推迟条件

如 Docker 网络不通或 Qdrant 镜像拉取失败，Qdrant 推迟到 Phase 3（k3s 部署时）。Syncthing 无网络依赖，可在 Phase 2 同步需求出现时再安装。

---

## Phase 1 完成标准

- [ ] 双 vault 目录就绪，种子内容 ≥ 4 条概念页，git 管理
- [ ] Ollama gemma4:12b + bge-m3 本地可用
- [ ] Go Agent `:8080` 启停正常，`/health` 返回 OK
- [ ] LLM Gateway `/v1/chat/completions` 有认证可用
- [ ] Python Inference Service `:8000` 正常代理
- [ ] Codex CLI 通过 Agent 调用本地模型完成一次问答
- [ ] Agent 可读取 vault 种子内容作为上下文
- [ ] 所有单元测试 + 集成测试脚本全部通过
- [ ] GitHub Actions CI 通过（go test + go build linux）
- [ ] `tests/` 目录下每个子阶段有对应的回归测试脚本

---

## 附录 A: 测试目录结构

```
C:\Users\Admin\ai-agent\tests\
├── unit\
│   └── (随 go test / pytest 运行)
├── integration\
│   ├── phase1_1_vault_bootstrap.sh
│   ├── phase1_2_ollama.sh
│   ├── phase1_3_agent_gateway.sh
│   ├── phase1_4_inference_service.sh
│   └── phase1_5_full_chain.sh
└── e2e\
    └── phase1_5_end_to_end.md
```

## 附录 B: 环境变量一览（.env.example）

```ini
# Go Agent
AGENT_INTERNAL_KEY=

# Ollama（默认 localhost:11434，无需额外配置）

# Phase 2+ 预留
CLOUD_LLM_API_KEY=
CLOUD_LLM_BASE_URL=
QCLAW_TOKEN=
HA_ACCESS_TOKEN=
```

## 附录 C: 与原 Phase 1 文档的差异说明

| 变更项 | 原文档 | 修订版 |
|--------|--------|--------|
| 开发环境 | macOS/Linux 命令 | Windows/PowerShell |
| obsidian-wiki 角色 | 运行时依赖 | 引导工具 + 规范来源 |
| 容器化 | Phase 1 使用 Docker/Qdrant/Syncthing | Phase 3 再用 |
| 测试 | 无 | 回归金字塔 + 每阶段固化脚本 |
| 密钥管理 | 无 | .env + 环境变量 |
| CI/CD | 无 | GitHub Actions 最小门禁 |
| 观测性 | 无 | zap + structlog |
| Phase 拆分 | 9 个 Task 线性 | 6 个子阶段，每阶段独立可验证 |
| Go Agent 目录 | 复杂（含 channel/external/webchat） | 精简至 Phase 1 需要的模块 |
