# Phase 3 设计：Web Chat + 容器化 + k3s

> **依赖**: Phase 1 + Phase 2 完成
> **目标**: Web Chat 前端挂件、Docker Compose 本地验证、k3s Helm Chart 部署清单、跨环境可复现

---

## 1. Web Chat 挂件

### 1.1 架构

```
个人网站 HTML
    │
    │ <script src="chat-widget.js"></script>
    ▼
chat-widget.js
    │ WebSocket ws://agent:8080/channels/webchat/ws
    │
    ▼
Go Agent → LLM Gateway → Ollama/线上 API
```

### 1.2 技术要求

- 纯 JS，零框架依赖（不用 React/Vue）
- 单文件 `chat-widget.js`，可直接嵌入任何网站
- 流式输出（逐字显示）
- 思考过程可选展开
- 简易 Markdown 渲染
- 深色/浅色主题可配置
- 右下角气泡样式

### 1.3 嵌入方式

```html
<script src="https://your-domain.com/chat-widget.js"></script>
<script>
  ChatWidget.init({
    endpoint: "wss://agent.your-domain.com/channels/webchat/ws",
    theme: "dark",
    position: "bottom-right",
    greeting: "你好，有什么可以帮你？"
  });
</script>
```

---

## 2. 容器化策略

### 2.1 容器划分

```
┌──────────────────────────────────────────────────────────────────┐
│                     Docker Compose                               │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │   agent       │  │  inference   │  │  sidecar     │          │
│  │  (Go)         │  │  (Python)    │  │  (Node.js)   │          │
│  │  :8080        │  │  :8000       │  │  :9090       │          │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘          │
│         │                  │                  │                  │
│  ┌──────┴──────────────────┴──────────────────┴───────┐         │
│  │                Docker Network                       │         │
│  └──────────────────────────┬──────────────────────────┘         │
│                              │                                   │
│  ┌──────────────────────────┴──────────────────────────┐         │
│  │                    qdrant                             │         │
│  │                    :6333                              │         │
│  └──────────────────────────────────────────────────────┘         │
│                                                                  │
│  Volumes:                                                         │
│  ├── vault-personal (NFS/host)                                   │
│  ├── vault-agent (NFS/host)                                      │
│  ├── qdrant-storage                                              │
│  └── model-storage (Ollama models)                               │
└──────────────────────────────────────────────────────────────────┘
```

### 2.2 跨环境可复现

**Agent 系统（完全可移植，任何人可用）：**
- `docker-compose.yml` + `.env.example` + Helm Chart
- 一条 `docker-compose up` 或 `helm install` 即可启动
- Go Agent 和 Sidecar 提供多架构镜像 (amd64 + arm64)

**Inference Service（环境依赖，自动检测）：**
- 安装脚本自动检测 GPU 环境：
  - 有 NVIDIA GPU → 拉取 GPU 镜像 + Ollama + 模型自动下载
  - 无 GPU → 使用 CPU 模式，提示性能限制
  - Apple Silicon → 使用 Metal 加速
- Inference 端点通过 `.env` 配置，可指向本地或远程

**知识库数据（用户私有，不打包）：**
- vault 目录由用户自行提供
- `.env` 中指定 `VAULT_PERSONAL_PATH` 和 `VAULT_AGENT_PATH`
- 首次运行自动创建空 vault 结构

### 2.3 安装脚本

```bash
# 一键安装
curl -fsSL https://your-repo/install.sh | bash

# 脚本自动执行：
# 1. 检测系统架构 (x86_64 / arm64)
# 2. 检测 GPU 环境 (NVIDIA / Apple Metal / CPU)
# 3. 生成 .env 文件
# 4. docker-compose pull 拉取对应架构镜像
# 5. docker-compose up -d
# 6. 验证所有服务健康
```

---

## 3. k3s 部署清单

### 3.1 Helm Chart 结构

```
charts/ai-agent/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── agent-deployment.yaml
│   ├── agent-service.yaml
│   ├── agent-configmap.yaml
│   ├── agent-secret.yaml
│   ├── inference-deployment.yaml
│   ├── inference-service.yaml
│   ├── sidecar-deployment.yaml
│   ├── sidecar-service.yaml
│   ├── qdrant-statefulset.yaml
│   ├── qdrant-service.yaml
│   ├── qdrant-pvc.yaml
│   ├── vault-pv.yaml
│   ├── vault-pvc.yaml
│   ├── ingress.yaml
│   ├── networkpolicy.yaml
│   ├── hpa.yaml
│   └── NOTES.txt
└── tests/
    └── test-connection.yaml
```

### 3.2 values.yaml 核心配置

```yaml
# values.yaml
agent:
  replicaCount: 1
  image:
    repository: your-registry/ai-agent
    tag: latest
    pullPolicy: IfNotPresent
  service:
    type: ClusterIP
    port: 8080
  resources:
    requests:
      cpu: 500m
      memory: 512Mi
    limits:
      cpu: 2000m
      memory: 2Gi
  healthCheck:
    path: /health
    port: 8080
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0

inference:
  replicaCount: 1
  image:
    repository: your-registry/inference-service
    tag: latest
  nodeSelector:
    gpu: "true"                    # 仅调度到 GPU 节点
  resources:
    limits:
      nvidia.com/gpu: 1
      memory: 16Gi

sidecar:
  replicaCount: 1
  image:
    repository: your-registry/qclaw-sidecar
    tag: latest

qdrant:
  replicaCount: 1
  storage:
    size: 10Gi
    storageClass: local-path

vault:
  personal:
    existingClaim: ""              # 使用已有的 PVC
  agent:
    existingClaim: ""

ingress:
  enabled: true
  className: nginx
  host: agent.your-domain.com
  tls:
    enabled: true
    secretName: agent-tls

networkPolicy:
  enabled: true
  # agent 可访问 inference + qdrant
  # sidecar 可访问 agent
  # 外部只能访问 agent + ingress

hpa:
  enabled: false                   # 默认关闭，按需启用
  minReplicas: 1
  maxReplicas: 3
  targetCPUUtilization: 80
```

### 3.3 部署命令

```bash
# 本地 Docker Compose 验证
docker-compose up -d

# k3s 部署
helm install ai-agent ./charts/ai-agent \
  --namespace ai-agent --create-namespace \
  --set vault.personal.existingClaim=vault-personal-pvc \
  --set vault.agent.existingClaim=vault-agent-pvc

# 升级
helm upgrade ai-agent ./charts/ai-agent -n ai-agent

# 多节点扩展
kubectl scale deployment ai-agent --replicas=2 -n ai-agent
```

---

## 4. 实施阶段

### Task 1: Web Chat 挂件
- 编写 chat-widget.js (WebSocket 客户端 + 气泡 UI + 流式渲染)
- Go Agent 实现 `/channels/webchat/ws` 端点
- 本地验证：浏览器打开 → 对话 → 流式回复

### Task 2: Docker Compose
- 编写 docker-compose.yml (四个服务)
- 编写各服务 Dockerfile
- 编写 .env.example
- 本地验证：`docker-compose up` → 全部服务健康

### Task 3: GPU 环境检测脚本
- 编写 install.sh
- 自动检测 NVIDIA / Apple Metal / CPU
- 自动生成 .env
- 自动拉取镜像和模型

### Task 4: 多架构镜像
- Dockerfile 支持 amd64 + arm64
- CI 构建多架构镜像 (GitHub Actions / Docker buildx)
- 推送到镜像仓库

### Task 5: Helm Chart
- 编写完整 Helm Chart 结构
- values.yaml 参数化所有配置
- NetworkPolicy 网络隔离
- HPA 自动扩缩容（默认关闭）
- 健康检查 + RollingUpdate

### Task 6: 端到端验证
- Docker Compose 本地全链路验证
- Helm Chart 在 k3s 单节点验证
- 跨架构镜像在另一台机器验证

---

## 5. 完成标准

- [ ] Web Chat 挂件可在浏览器中对话
- [ ] `docker-compose up` 一条命令启动全部服务
- [ ] `install.sh` 自动检测 GPU 并配置
- [ ] 多架构镜像 (amd64 + arm64) 可用
- [ ] `helm install` 一条命令部署到 k3s
- [ ] 其他人拿到 repo + `docker-compose up` 能跑通（需配置 .env）
