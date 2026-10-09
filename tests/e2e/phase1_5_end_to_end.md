# Phase 1.5 端到端验证

> **目的**: 固化全链路手动测试步骤，确保任何人可按此文档复现。
> **调用链**: curl → Go Agent → Ollama（含 vault 上下文注入）
> **状态**: Python Inference Service 已被 Go 原生替代，步骤已简化。

---

## 前置条件

- [ ] Ollama 已安装并启动，gemma4:12b 模型已拉取
  ```powershell
  ollama --version
  ollama list | Select-String "gemma4:12b"
  ```
- [ ] Go Agent 已编译
  ```powershell
  cd go-agent
  go build -o agentd.exe ./cmd/agentd
  ```
- [ ] `config/agent.yaml` 配置正确
  ```powershell
  Get-Content go-agent\config\agent.yaml
  ```
- [ ] `.env` 文件已配置 `AGENT_INTERNAL_KEY`
  ```powershell
  Get-Content .env | Select-String "AGENT_INTERNAL_KEY"
  ```

---

## 步骤

### 1. 启动 Ollama

```powershell
ollama serve
```

**验证**:
```powershell
curl http://localhost:11434/api/tags
# 预期: JSON 返回，models 列表中包含 gemma4:12b
```

### 2. 启动 Go Agent

```powershell
cd go-agent
$env:AGENT_INTERNAL_KEY = "your-key-from-.env"
.\agentd.exe
```

**验证**:
```powershell
curl http://localhost:8080/health
# 预期: {"status":"ok"}
```

### 3. 发送测试请求

```powershell
curl -X POST http://localhost:8080/v1/chat/completions `
  -H "Content-Type: application/json" `
  -H "Authorization: Bearer your-key-from-.env" `
  -d '{"model":"auto","messages":[{"role":"user","content":"用中文解释什么是RAG"}]}'
```

### 4. 验证标准

- [ ] HTTP 返回 200
- [ ] `choices[0].message.content` 非空
- [ ] 回复包含 RAG 核心概念（检索增强生成 / retrieval augmented generation）
- [ ] 回复引用了 vault 中的种子内容（可通过回复中提及 wiki/concepts 内容验证）
- [ ] Agent 控制台日志显示完整的请求链：
  ```
  agent main loop starting → message received → chain step → response sent
  ```

### 5. 验证 vault 日志写入

```powershell
Get-Content C:\Users\Admin\vaults\agent\log.md -Tail 5
```
- [ ] 日志中有刚才的操作记录

---

## 常见问题排查

| 问题 | 原因 | 解决 |
|------|------|------|
| `connection refused :11434` | Ollama 未启动 | 重新运行 `ollama serve` |
| `model not found` | gemma4:12b 未拉取 | `ollama pull gemma4:12b` |
| `connection refused :8080` | Go Agent 未启动 | 检查 .env 中的 AGENT_INTERNAL_KEY、agent.yaml 路径 |
| 401 Unauthorized | API Key 不匹配 | 确认 .env 和 curl 中的 key 一致 |
| Agent 启动时 zlib 错误 | ollama serve 端口冲突 | 确保 Ollama 运行中且 Agent 未同时 serve |

---

## 环境变量速查

| 变量 | 用途 | 示例 |
|------|------|------|
| `AGENT_INTERNAL_KEY` | Agent 内部认证密钥 | `dev-key-123` |
| `OLLAMA_HOST` | Ollama 地址（默认 localhost:11434） | 通常无需设置 |