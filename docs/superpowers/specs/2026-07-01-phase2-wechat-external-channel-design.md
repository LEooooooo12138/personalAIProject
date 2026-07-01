# Phase 2 设计：微信接入 + 外部通道

> **依赖**: Phase 1 完成（知识库 + Go Agent 骨架 + LLM Gateway + Ollama）
> **目标**: 通过 QClaw 接入个人微信，实现双通道 Agent，外部输出经过完整过滤链

---

## 1. 微信接入方案

### 1.1 QClaw 路径（个人微信扫码）

```
用户个人微信
    │ 扫码 OAuth
    ▼
QClaw 网关 (security.guanjia.qq.com)
    │ WebSocket + AGP 协议
    ▼
Node.js Sidecar (Phase 2)
    │ HTTP POST /channels/qclaw/messages
    ▼
Go Agent Core（本地调度中心）→ 任务理解 / 隐私判断 / vault 检索 → LLM Gateway → Ollama 或云端 LLM
```

### 1.2 控制权归属（修订）

所有外部通道消息都必须先回到本地 Go Agent：

1. `Channel Adapter` 只负责收发消息
2. `Go Agent` 负责任务理解、隐私检查、vault/向量检索、输出策略
3. `Go Agent` 统一决定使用 `local / cloud / hybrid` 模型
4. `LLM Gateway` 是统一模型接口层，不是主业务调度层

这样可避免外部通道自行调用云端模型，保证本地控制中心完整。

### 1.3 QClaw 登录流程

```
1. Sidecar 调 QClaw API 获取 OAuth state
2. 生成微信扫码 URL → 用户扫码确认
3. 浏览器跳转 → 拿到 OAuth code
4. code 换 JWT Token + Channel Token + API Key
5. 设备绑定 — 微信服务号客服链接 → 用户在微信中打开
6. 微信中出现对话入口 → WebSocket 收发消息
```

### 1.4 Sidecar 架构

**Phase 2: Node.js Sidecar**
- 复用 `qclaw-wechat-client` (800⭐) TypeScript 实现
- Go Agent 通过 HTTP POST 和 sidecar 通信
- 协议更新时 `npm update` 即可

**Phase 3+: 迁移到 Go 原生**
- 在 Go 中重新实现 QClaw OAuth + WebSocket + AGP
- 消除 Node.js 依赖

### 1.5 Sidecar ↔ Go Agent 通信

```
POST /channels/qclaw/messages       ← sidecar 推消息给 Agent
POST /channels/qclaw/reply          ← Agent 回消息给 sidecar
GET  /channels/qclaw/stream         ← SSE 长连接，Agent 主动推消息
```

通用端点设计（未来扩展其他平台时复用）：
```
POST /channels/{platform}/messages
POST /channels/{platform}/reply
GET  /channels/{platform}/stream
```

---

## 2. 双模型策略

### 2.0 混合推理策略（修订）

Phase 2 的模型路由不是“通道自动猜测”，而是由本地 Agent 按任务特征统一判定：

- `local`：隐私敏感、短文本、本地知识库问答、低延迟任务
- `cloud`：复杂推理、长上下文、高质量生成、复杂多模态任务
- `hybrid`：本地做检索 / 脱敏 / 初筛，云端做最终生成

默认原则：

1. 所有外部请求先回 Agent
2. Agent 根据 metadata、隐私标签、任务复杂度决定模型
3. 云端调用统一经 LLM Gateway 发出
4. 不允许外部通道绕过 Agent 直接调用云端模型

### 2.1 本地模型配置

```
5080 16GB VRAM:

gemma4:12b (Q4_K_M, 7.2GB)    ← 常驻，对话 + 知识蒸馏
llava:7b (Q4, 4.5GB)          ← 按需加载，图片理解，~5-10秒冷启动
bge-m3 (~1GB)                  ← 常驻，embedding
```

### 2.2 模型路由规则

```go
func routeModel(req ChatRequest) string {
    if hasImages(req) {
        return "llava:7b"           // 图片 → 视觉模型
    }
    if req.Metadata["sensitive"] == "true" {
        return "gemma4:12b"         // 隐私敏感 → 本地
    }
    if req.Metadata["skill"] != "" {
        return "gemma4:12b"         // 知识库操作 → 本地
    }
    return "auto"                   // 自动路由
}
```

### 2.3 多媒体处理分流

| 类型 | 小文件 | 大文件/复杂 |
|------|--------|-----------|
| 图片 | llava:7b 本地 | 线上多模态 API (GPT-4o/Qwen-VL) |
| 语音 | Whisper CPU 本地转录 | Whisper CPU |
| 文件 | 文本提取本地处理 | 线上 API |

---

## 3. 会话管理

### 3.1 会话生命周期

```
会话开始 ← 用户发送第一条消息
    │
    │ 30 分钟超时 OR 20 轮对话
    │ 哪个先到都触发会话结束
    ▼
会话结束 → 触发记忆沉淀流程
```

### 3.2 并发处理

```
消息进入 → goroutine 池（上限 10 并发）
    │
    ├── 池满 → 排队等待（Go channel buffer）
    └── 池有空 → 分配 goroutine 处理
```

### 3.3 群聊策略

Phase 2 不做群聊，仅支持私聊。

---

## 4. 输出过滤链

### 4.0 输出控制策略（修订）

Phase 2 需要在过滤链之上增加策略层：

- 是否允许向当前通道输出
- 输出完整回复还是摘要回复
- 是否隐藏推理过程
- 是否允许引用知识库内容
- 是否允许调用云端模型
- 是否强制本地推理

过滤链负责“改内容”，策略层负责“控行为”。

### 4.1 过滤器完整版

```
Agent 原始输出
    │
    ├─ 1. PIIFilter         ← 正则：手机号/身份证/邮箱/地址
    ├─ 2. SensitiveFilter   ← 黑名单关键词 + visibility/pii/internal 标签内容
    ├─ 3. PersonalRefFilter ← 屏蔽 [[personal-vault/...]] 引用
    ├─ 4. PlatformFilter    ← WeChat 不支持的 Markdown 语法转义
    ├─ 5. LengthFilter      ← 截断超长输出
    └─ 6. AuditLogger       ← 记录过滤前后的输出到审计日志
    │
    ▼
最终发送到 External Channel
```

### 4.2 审计日志格式

```
vault-audit.log:
[TIMESTAMP] FILTER channel=qclaw user=xxx pii=true sensitive=false platform_adapt=true
            input_tokens=N output_tokens=N filtered_tokens=N
```

---

## 5. 记忆沉淀

### 5.0 记忆沉淀目标（修订）

Phase 2 的记忆沉淀不仅是为了“保存对话”，而是为了建立长期知识闭环：

- 沉淀前先分类：`decision` / `knowledge` / `follow-up`
- 每条记忆需带来源通道标识：`wechat` / `webchat` / `internal`
- 沉淀结果需可审计、可回收、可后续检索

### 5.1 触发条件

会话结束后（30 分钟超时或 20 轮），Agent 判断对话是否有知识价值：

**写入条件（满足任一）：**
- 包含明确的决策或结论
- 包含新发现、新知识
- 包含问题的解决方案
- 用户明确说「记住这个」

**不写入：**
- 纯闲聊/打招呼
- 重复已知信息
- 无结论的讨论

### 5.2 沉淀流程

```
会话结束
    │
    ├─ Step 1: Agent 判断是否有知识价值 → 无则跳过
    ├─ Step 2: 调 LLM Gateway 生成结构化摘要（带 frontmatter）
    ├─ Step 3: TF-IDF 去重 — 和 _memory/ 已有条目做相似度检查
    │          相似度 > 0.45 → 合并到已有条目
    │          相似度 ≤ 0.45 → 创建新条目
    └─ Step 4: 写入 personal-vault/_memory/YYYY-MM-DD_HHMM_<topic-slug>.md
```

### 5.3 摘要格式

```yaml
---
title: "对话摘要：关于 X 的讨论"
created: 2026-07-01T14:30:00+08:00
source: qclaw-session
tags: [topic1, topic2]
confidence: 0.8
---
### 主题
<一句话概括>

### 关键决策/发现
<核心结论>

### 待跟进
<未解决的问题>
```

---

## 6. Agent 人设

### 6.1 可配置 system prompt

```yaml
# agent.yaml
personality:
  enabled: false                # 默认无人设
  name: ""
  system_prompt: ""             # 自定义人设
  tone: "neutral"               # neutral / friendly / concise / humorous
```

**默认行为：** 中性 AI 助手，直接回答，不模拟任何人。
**启用后：** 通过 system prompt 定义名字、性格、语气。

---

## 7. 权限矩阵

| 操作 | Internal Channel | External Channel (QClaw) |
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

---

## 8. Phase 2 设计约束（修订）

- **单调度中心**：本地 Go Agent 是唯一调度中心
- **单模型出口**：云端模型调用统一经 LLM Gateway
- **双 vault 约束**：外部通道只能读 `agent-vault`，不能读 `personal-vault`
- **记忆约束**：对话沉淀需结构化，并区分通道来源
- **可替换约束**：微信通道只是 adapter，核心逻辑不能绑定 QClaw 实现细节

### Task 1: QClaw Sidecar 搭建
- 克隆 `qclaw-wechat-client`
- 实现 sidecar HTTP 端点
- QClaw OAuth 登录 + 设备绑定
- WebSocket 消息收发

### Task 2: Go Agent External Channel
- 实现 `/channels/{platform}/messages` 端点
- 实现 Channel Manager + QClaw adapter
- 接入 goroutine 池并发控制

### Task 3: 双模型路由
- Ollama 拉取 llava:7b
- 实现 model router（图片检测 + 隐私检测 + 自动路由）
- 验证 gemma4 常驻 + llava 按需切换

### Task 4: 输出过滤链
- PIIFilter / SensitiveFilter / PersonalRefFilter
- PlatformFilter (Markdown → 纯文本适配)
- LengthFilter + AuditLogger

### Task 5: 会话管理
- 会话状态机（开始/活跃/超时/结束）
- 30 分钟超时 + 20 轮上限
- 会话上下文内存管理

### Task 6: 记忆沉淀
- 知识价值判断逻辑
- LLM 生成结构化摘要
- TF-IDF 去重 + 合并
- 写入 _memory/

### Task 7: Agent 人设配置
- agent.yaml personality 字段
- system prompt 模板化

### Task 8: 端到端验证
- 微信扫码 → Agent 回复
- 图片/语音处理验证
- 过滤链验证
- 记忆沉淀验证
- 审计日志验证
