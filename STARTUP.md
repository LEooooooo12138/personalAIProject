# Go Agent — 启动指南

## 前置条件

- **Go 1.23+**
- **Ollama** 已安装并运行（默认 `http://localhost:11434`）
- 所需模型已拉取：
  ```
  ollama pull gemma4:12b
  ollama pull bge-m3
  ollama pull llava:7b        # vision 模型，可选
  ```
- 确保 `agent.yaml` 中的 vault 路径存在：
  - `D:\vaults\personal` — 个人知识库（wiki 页面）
  - `D:\vaults\agent` — agent 内部状态（会话持久化、嵌入缓存）

## 启动

### 1. 确保 Ollama 在运行

```powershell
# 检查 Ollama 是否在运行
curl http://localhost:11434/api/tags
```

如果未运行，启动 Ollama 应用。

### 2. 构建并启动 agentd

```powershell
cd E:\personalAIProject\go-agent

# 构建
go build -o agentd.exe ./cmd/agentd

# 启动（默认读取 config/agent.yaml）
.\agentd.exe
```

### 3. 打开前端

浏览器访问：**http://localhost:8080**

右下角会看到聊天按钮，点击即可对话。

## 目录结构

```
E:\personalAIProject\go-agent\
├── agentd.exe          # 编译产物
├── config/
│   ├── agent.yaml      # 主配置
│   └── .env            # 环境变量（可选）
├── static/
│   ├── index.html      # 前端页面
│   └── chat-widget.js  # 聊天组件
├── internal/
│   ├── core/           # 会话管理、路由
│   ├── gateway/        # HTTP/WS 服务、RAG
│   ├── chain/          # LangChain 风格流水线
│   ├── memory/         # 记忆沉淀引擎
│   ├── vault/          # 知识库读写
│   └── inference/      # Ollama 客户端
└── cmd/agentd/main.go  # 入口
```

## 可选：Inference Service

`E:\personalAIProject\inference-service\` 是一个 FastAPI 推理服务包装，当前 Go agent 直连 Ollama，无需启动。

如需使用：
```powershell
cd E:\personalAIProject\inference-service
pip install -r requirements.txt
uvicorn main:app --host 0.0.0.0 --port 8081
```
