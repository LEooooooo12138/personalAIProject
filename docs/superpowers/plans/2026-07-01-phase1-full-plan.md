# Phase 1 完整实施计划：知识库 + Go Agent 骨架 + Ollama

> **范围**: 在台式机上完成双 vault 初始化（personal + agent）、Go Agent Core 搭建、Inference Service (Ollama) 集成，以及作为内部独立模块的 LLM Gateway 可用。
> **前提**: 台式机可用（U7-265K + 5080 16GB VRAM + 48GB RAM），已安装 Go、Python 3.10+、Docker。
> **完成标准**: personal-vault 与 agent-vault 均可初始化并被 Agent 读写；可通过 `curl POST /v1/chat/completions` 调用本地 `gemma4:12b`；Go Agent 内部的 LLM Gateway 能统一代理 Codex / Agent 内部 / 后续外部通道的 LLM 请求；系统预留本地 + 云端混合推理能力。

---

## Task 0: 环境准备

- [ ] **Step 0.1** 确认 Go ≥ 1.22
```bash
go version
```

- [ ] **Step 0.2** 确认 Docker + NVIDIA Container Toolkit
```bash
docker run --gpus all nvidia/cuda:12.0-base nvidia-smi
```

- [ ] **Step 0.3** 安装 Ollama
```bash
curl -fsSL https://ollama.com/install.sh | sh
ollama --version
```

- [ ] **Step 0.4** 创建项目目录
```bash
mkdir -p ~/ai-agent/{go-agent,inference-service,k3s-manifests}
```

---

## Task 1: obsidian-wiki Vault 初始化 (沿用已有计划)

- [ ] **Step 1.1** `pip3 install obsidian-wiki`
- [ ] **Step 1.2** 创建 `~/personal-vault/` 和 `~/agent-vault/` 双目录结构，并预置对齐后的目录规范（参考 raw-source 组织标准）
- [ ] **Step 1.3** 分别对 `~/personal-vault` 与 `~/agent-vault` 执行 `obsidian-wiki setup`，确认两个 vault 均可独立初始化
- [ ] **Step 1.4** 撰写两份 `AGENTS.md`，并在 agent-vault 的 AGENTS.md 中明确：该 vault 可被外部通道读取、不存放隐私内容、后续可作为对外知识底座
- [ ] **Step 1.5** 运行 wiki-setup，分别初始化两个 vault 的 index.md / log.md / hot.md / .manifest.json
- [ ] **Step 1.5.1** 明确 Agent 调度原则：所有请求统一回到本地 Go Agent，由 Agent 决定调用本地模型或云端 LLM；Phase 1 先落地最小路由桩，Phase 2 再接外部通道
- [ ] **Step 1.5.2** 明确 LLM Gateway 边界：Phase 1 将其作为 Go Agent 内部独立模块，不独立进程，但按可拆分接口设计；LLM Gateway 统一处理模型路由、敏感分流、后续多模态分流与云端调用

- [ ] **Step 1.6** ingest 种子内容（RAG、LLM Wiki、obsidian-wiki、Syncthing、双通道 Agent 架构）
- [ ] **Step 1.7** Git init 两个 vault + 首次 commit

---

## Task 2: Inference Service (Ollama 快速验证)

- [ ] **Step 2.1** 拉取本地模型
```bash
ollama pull gemma4:12b          # 5080 16GB VRAM 常驻文本模型
ollama pull bge-m3             # 多语言 embedding
```

- [ ] **Step 2.2** 验证模型可用
```bash
curl http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gemma4:12b","messages":[{"role":"user","content":"hello"}]}'
```

- [ ] **Step 2.3** 验证 embedding
```bash
curl http://localhost:11434/v1/embeddings \
  -d '{"model":"bge-m3","input":"retrieval augmented generation"}'
```

- [ ] **Step 2.4** 创建推理服务包装层 `inference-service/main.py`
  - FastAPI，暴露 OpenAI-compatible 端口 `:8000`
  - 转发 `/v1/chat/completions` → Ollama `:11434`
  - 转发 `/v1/embeddings` → Ollama `:11434`
  - `POST /v1/chat/completions` 支持 `"model": "auto"` 时路由到线上 API（先 placeholder）

```python
# inference-service/main.py 核心结构
from fastapi import FastAPI
import httpx

app = FastAPI()
OLLAMA = "http://localhost:11434"

@app.post("/v1/chat/completions")
async def chat_completions(req: ChatRequest):
    model = resolve_model(req)  # local / online / auto
    if model == "local":
        return await ollama_chat(req)
    else:
        return await online_api_chat(req)  # DeepSeek API placeholder

@app.post("/v1/embeddings")
async def embeddings(req: EmbedRequest):
    return await ollama_embed(req)

@app.get("/v1/models")
async def list_models():
    return {"data": [{"id": "qwen2.5:14b"}, {"id": "deepseek-chat"}]}
```

- [ ] **Step 2.5** 测试推理服务
```bash
python3 inference-service/main.py &
curl http://localhost:8000/v1/models
```

---

## Task 3: Go Agent 骨架（含内部 LLM Gateway 与路由桩）

- [ ] **Step 3.1** 初始化 Go 模块
```bash
cd ~/ai-agent/go-agent
go mod init github.com/yuanleyao/ai-agent
```

- [ ] **Step 3.2** 创建目录结构
```
go-agent/
├── cmd/agentd/main.go          # 主入口
├── internal/
│   ├── core/
│   │   ├── agent.go            # Agent 主循环
│   │   ├── config.go           # 配置加载
│   │   └── router.go           # 意图路由
│   ├── channel/
│   │   ├── channel.go          # Channel 接口
│   │   └── internal.go         # LLM Gateway (Internal)
│   ├── gateway/
│   │   ├── http.go             # HTTP 路由
│   │   └── middleware.go       # Auth / 日志
│   └── vault/
│       ├── reader.go           # vault 文件读取
│       └── writer.go           # vault 文件写入
├── config/
│   └── agent.yaml
├── go.mod
└── Makefile
```

- [ ] **Step 3.3** 实现配置加载 (`internal/core/config.go`)
  - 读取 `agent.yaml`，支持环境变量覆盖

- [ ] **Step 3.4** 实现 HTTP 服务器 (`cmd/agentd/main.go` + `internal/gateway/http.go`)
  - Gin 路由，端口 `:8080`
  - 路由表：`/v1/chat/completions`、`/v1/embeddings`、`/v1/models`、`/health`

- [ ] **Step 3.5** 实现 Channel 接口 (`internal/channel/channel.go`)
```go
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop() error
    Receive() <-chan Message
    Send(msg Message, resp Response) error
    CanAccessPrivate() bool
}
```

- [ ] **Step 3.6** 实现 Internal Channel (`internal/channel/internal.go`)
  - 作为内部请求入口，不直接承担主调度
  - 将请求统一转交 `internal/router`
  - 处理 HTTP 请求，解析 OpenAI-compatible 格式
  - 调用 Inference Service 转发

- [ ] **Step 3.7** 实现 Inference Client (`internal/inference/client.go`)
  - HTTP 客户端调用 Inference Service `:8000`
  - 支持流式 SSE 转发

- [ ] **Step 3.8** 实现 Vault Reader (`internal/vault/reader.go`)
  - 读取 `index.md`、`log.md`、hot.md`
  - 读取任意 wiki 页面 frontmatter (YAML 解析)
  - grep 搜索 wiki 页面关键词

- [ ] **Step 3.9** 实现 Vault Writer (`internal/vault/writer.go`)
  - 写入 `.manifest.json` 条目
  - 追加 `log.md` 条目
  - 写入 wiki 页面（Markdown 模板化）

- [ ] **Step 3.10** 实现 Agent Router (`internal/router/router.go`)
  - 所有请求统一回到本地 Agent
  - 负责意图识别、隐私检查、vault 上下文组装、模型选择
  - 输出 `RouteDecision{TargetModel, Mode, Reason}`

- [ ] **Step 3.11** 写 `agent.yaml` 默认配置
```yaml
server:
  port: 8080
inference:
  endpoint: "http://localhost:8000"
  models:
    local: "gemma4:12b"
    cloud: "cloud-default"

    sensitive_routing: true
    multimodal_hint_routing: true
    auto_routing: true
vaults:
  personal: ~/personal-vault
  agent: ~/agent-vault
```

- [ ] **Step 3.12** 编译并运行
```bash
go build -o agentd ./cmd/agentd
./agentd --config config/agent.yaml
```

---

## Task 4: Internal Channel (LLM Gateway) 端到端验证

- [ ] **Step 4.1** 启动全套服务
```bash
# Terminal 1: Ollama (自动随系统启动)
ollama serve

# Terminal 2: Inference Service
python3 inference-service/main.py

# Terminal 3: Go Agent
cd ~/ai-agent/go-agent && ./agentd
```

- [ ] **Step 4.2** 测试直连 LLM Gateway
```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"你好"}]}'
```

- [ ] **Step 4.3** 配置 Codex CLI 代理
```bash
export OPENAI_BASE_URL="http://localhost:8080/v1"
export OPENAI_API_KEY="internal-key-xxx"
codex
# 验证 Codex 调用走 Agent，Agent 路由到 Ollama
```

- [ ] **Step 4.4** 测试 vault 状态查询
```bash
curl http://localhost:8080/internal/vault/status
```

- [ ] **Step 4.5** 测试 wiki-query（通过 Agent 的 LLM Gateway）
```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "auto",
    "messages": [{"role": "user", "content": "用 wiki-query 查询 RAG 的定义"}],
    "metadata": {"skill": "wiki-query"}
  }'
```

---

## Task 5: Syncthing 预埋

- [ ] **Step 5.1** `brew install syncthing && syncthing &`
- [ ] **Step 5.2** 在 Syncthing Web UI 添加 `~/personal-vault` 和 `~/agent-vault` 两个文件夹
- [ ] **Step 5.3** 确认 Syncthing 识别两个 vault（Unshared 状态）

---

## Task 6: Qdrant 本地验证

- [ ] **Step 6.1** Docker 启动 Qdrant
```bash
docker run -d -p 6333:6333 -v ~/ai-agent/qdrant-storage:/qdrant/storage qdrant/qdrant
```

- [ ] **Step 6.2** 创建 collection
```bash
curl -X PUT http://localhost:6333/collections/agent_memory \
  -H "Content-Type: application/json" \
  -d '{"vectors": {"size": 1024, "distance": "Cosine"}}'
```

- [ ] **Step 6.3** 验证嵌入 + 写入
```bash
# 用 embedding API 生成向量
curl http://localhost:8000/v1/embeddings \
  -d '{"model":"bge-m3","input":"RAG 是一种..."}' \
  | python3 -c "import json,sys; v=json.load(sys.stdin)['data'][0]['embedding']; print(v[:5])"
```

- [ ] **Step 6.4** Agent 集成 Qdrant reader（`internal/vault/vector.go`）
  - 接入 Qdrant client，支持语义搜索

---

## Phase 1 完成标准

- **控制权归属**：所有外部任务先回到本地 Agent，再由 Agent 统一决定 `local / cloud / hybrid`
- **Gateway 定位**：LLM Gateway 是 Agent 内部的统一模型接口层，不是主业务调度层
- **推理目标**：Phase 1 先保证本地 `gemma4:12b` 可用，同时预留云端复杂任务出口
- **Vault 目标**：Phase 1 必须同时建立 `personal-vault` 与 `agent-vault`，不能只初始化一个
- **模块边界**：Internal Channel 负责接入、Agent Router 负责决策、LLM Gateway 负责统一模型调用、Inference Service 负责执行适配
- **最小闭环**：Phase 1 结束时应能完成一次“请求进入 → Agent Router 决策 → 本地或云端桩调用 → 结果返回”的完整链路

- [ ] 双 vault 目录结构就绪，AGENTS.md 生效
- [ ] 种子内容已 ingest (≥5 条概念页)
- [ ] Git 版本管理运行中
- [ ] Ollama 本地 `gemma4:12b` 模型可用
- [ ] Inference Service `:8000` 正常转发
- [ ] Go Agent `:8080` LLM Gateway 可用
- [ ] Codex CLI 通过 Agent 代理 LLM 调用正常
- [ ] Qdrant 本地运行，可写入和检索向量
- [ ] Syncthing 单节点预埋


## Phase 1 详细设计说明

### A. 控制流（Phase 1 最小闭环）

```
请求进入
  -> Internal Channel
    -> Agent Router
       -> 读取 vault / 上下文
       -> 判定 local / cloud / hybrid
    -> LLM Gateway
       -> 统一格式
       -> 调用 Inference Service 或云端桩
    -> 返回 Internal Channel
       -> 输出结果
```

### B. 模块边界

- **Internal Channel**：只负责协议接入，不负责业务决策
- **Agent Router**：Phase 1 的核心决策层
- **LLM Gateway**：统一模型接口层，不是调度层
- **Inference Service**：只负责模型执行适配，不负责策略

### C. Phase 1 必须新增的细节任务

- [ ] 新增 `internal/router/router.go`
- [ ] 新增 `internal/gateway/llm_gateway.go`
- [ ] 新增 `internal/inference/client.go`
- [ ] 新增 `internal/inference/cloud_stub.go`
- [ ] 新增 `internal/vault/reader.go`
- [ ] 新增 `internal/vault/writer.go`
- [ ] 新增 `internal/memory/dedup.go`（Phase 1 只做桩）
- [ ] 新增 `config/routing.yaml`

### D. Phase 1 最小路由规则

1. `metadata.sensitive=true` → `local`
2. 明确 `model=local` → `local`
3. 明确 `model=cloud` → `cloud`
4. 明确 `model=auto` → 先检查本地能力，复杂任务再转 `cloud`
5. 多模态提示 → Phase 1 先记为 `multimodal_hint`，路由到云端桩

### E. Phase 1 验收标准（增强版）

- [ ] personal-vault 初始化成功
- [ ] agent-vault 初始化成功
- [ ] Go Agent 可启动
- [ ] Internal Channel 可请求
- [ ] Agent Router 可输出路由决策
- [ ] LLM Gateway 可统一转发
- [ ] 本地 `gemma4:12b` 可调用
- [ ] embedding 可调用
- [ ] 云端 LLM 桩可达
- [ ] vault 可读写至少一条记录
