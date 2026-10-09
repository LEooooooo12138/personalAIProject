# 设计文档审核 — 逐项缺失与受影响分析（Part 2）

> 审核日期：2026-07-14
> 审核范围：Phase 1 修订版设计 + Phase 2 设计 + Phase 3 设计 + Phase 4 设计 对照实际代码库
> 基准：`docs/superpowers/specs/` 下全部设计文档

---

## 一、对照方法论

以下逐项以"设计承诺了什么 → 实际代码库里有什么 → 差距是什么"的三段式审核。

| 符号 | 含义 |
|:---:|------|
| ✅ | 已实现且符合设计 |
| ⚠️ | 部分实现，有缺口 |
| ❌ | 完全缺失 |
| ➖ | 按设计主动推迟，不算缺口 |

---

## 二、Phase 1 逐项审核

### 2.1 Phase 1.1：知识库引导

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.1.1 | `tests/integration/phase1_1_vault_bootstrap.sh` | 文件不存在 | ❌ | **受影响端**：Phase 1.1 无回归测试。设计文档附录 A 明确要求此脚本。脚本需验证：双 vault 目录结构就绪、AGENTS.md 存在、.manifest.json 可解析、种子内容 ≥ 4 条。**缺失原因**：vault bootstrap 是手动执行的，当时未写验证脚本。**恢复路径**：编写脚本，逻辑从 Phase 1.1 设计的"验证标准"节直接翻译 |
| 1.1.2 | 双 vault 目录结构 | 实际存在 `C:\Users\Admin\vaults\personal` 和 `agent` | ✅ | 目录结构符合设计 |
| 1.1.3 | Git 管理 | vault 已 git init | ✅ | 设计要求的版本管理已到位 |

### 2.2 Phase 1.2：Ollama 推理底座

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.2.1 | `tests/integration/phase1_2_ollama.sh` | 存在（19行有效脚本） | ✅ | 验证 gemma4:12b chat 返回内容 + bge-m3 embedding 返回 1024 维向量 |
| 1.2.2 | Ollama 模型 | gemma4:12b + bge-m3 已拉取 | ✅ | |

### 2.3 Phase 1.3：Go Agent 骨架

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.3.1 | `tests/integration/phase1_3_agent_gateway.sh` | 存在 | ✅ | 验证 /health、401 拒绝、正确认证返回、/internal/vault/status |
| 1.3.2 | `internal/core/config_test.go` | 存在（2602 bytes） | ✅ | 配置加载 + 环境变量展开已测 |
| 1.3.3 | `internal/vault/reader_test.go` | 存在（3014 bytes） | ✅ | frontmatter 解析已测 |
| 1.3.4 | `internal/gateway/chat_test.go` | 存在但仅 551 bytes | ⚠️ | 覆盖太薄，几乎只有骨架 |

### 2.4 Phase 1.4：Python Inference Service

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.4.1 | `tests/integration/phase1_4_inference_service.sh` | 存在 | ✅ | |
| 1.4.2 | Python Inference Service 仍在运行？ | CONSTITUTION §八 注记"Python Inference Service 已被 Go 原生 Ollama 客户端替代" | ✅ | **设计演进**：Phase 1.4 的 Python 推理服务已被 Go 原生实现替代。这是合理的架构简化——少一个进程依赖，测试复杂度降低。不影响功能。 |

### 2.5 Phase 1.5：端到端串联

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.5.1 | `tests/integration/phase1_5_full_chain.sh` | 存在 | ✅ | |
| 1.5.2 | `tests/e2e/phase1_5_end_to_end.md` | **目录存在但文件缺失** | ❌ | **受影响端**：无文档化的手动 E2E 测试步骤。设计明确要求"用户视角的测试步骤固化"。路径 `tests/e2e/` 已创建但空。**恢复路径**：按设计文档中 E2E 测试段落（6 步验证流程）写成 markdown |
| 1.5.3 | Codex CLI 通过 Agent 调用本地模型 | 已通过 chain 系统实现 | ✅ | |

### 2.6 Phase 1.6：Qdrant + Syncthing

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.6.1 | Qdrant 向量数据库 | 代码库中零 Qdrant 引用 | ➖ | 设计明确标记为"可推迟到 Phase 2 前"。当前推迟是合法的。**但需注意**：Phase 4 的长期趋势数据存储设计中提及 Qdrant 做强依赖，Phase 4 启动前必须就绪 |
| 1.6.2 | Syncthing 文件同步 | 未安装 | ➖ | 同样被设计标记为可推迟，单机开发不需要 |

### 2.7 全局基础设施（Phase 1 设计 §0 节）

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 1.7.1 | `.github/workflows/check.yml` | 存在，且超设计规格（加了 go vet） | ✅ | 设计要求 `go test` + `go build linux`。实际实现还加了 `go vet`，这是质量提升 |
| 1.7.2 | `tests/integration/run_all.sh` | **不存在** | ❌ | **受影响端**：CONSTITUTION §5.1 铁律 3——"Phase N 开发前必须跑通 Phase 1..N-1 全量测试"。没有 run_all.sh 意味着每次都要手动逐个跑。**恢复路径**：写一个简单的 bash 脚本，按顺序调用 phase1_1..phase1_5 等脚本，收集退出码 |
| 1.7.3 | `config/agent.yaml` | 存在且结构完整 | ✅ | |
| 1.7.4 | `.env.example` | 存在 | ✅ | 含 AGENT_INTERNAL_KEY、Phase 2+ 预留变量 |

---

## 三、Phase 2 逐项审核

### 3.1 Channel 接口 + Manager

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.1 | Channel 接口含 `Receive() <-chan Message` | 已实现 | ✅ | `channel.go` line 43 |
| 2.2 | Manager 含 `Run()` 统一消息循环 | 已实现，fan-in 模式 | ✅ | `manager.go` line 79-131，一个 goroutine per channel → merged channel |
| 2.3 | `InternalChannel` 实现 `Receive()` | 已实现 | ✅ | `internal.go` |
| 2.4 | 删除 `external.go` stub | 已删除 | ✅ | 外部通道完全由 wecom adapter 替代 |
| 2.5 | `channel.Factory` 注册机制 | 已实现 | ✅ | `factory.go` + wecom `config.go` 的 `init()` 自注册 |

### 3.2 WeCom 适配器（按设计文档 §2 包结构逐个核对）

| 设计文件 | 实际文件 | 判定 | 详细说明 |
|---------|---------|:---:|---------|
| `adapter.go` | ✅ 存在（1577 bytes） | ✅ | 实现 Channel 接口，含 Start/Stop/Receive/Send |
| `callback.go` | ✅ 存在（5606 bytes） | ✅ | HTTP 回调处理 + URL 验证 + 消息接收。且内联了 XML 解析（原设计放在 message.go） |
| `client.go` | ✅ 存在（1868 bytes） | ✅ | API 客户端，SendText 实现 |
| `auth.go` | ✅ 存在（2232 bytes） | ✅ | Token 管理 + 自动刷新 |
| `crypto.go` | ✅ 存在（3071 bytes） | ✅ | AES 加解密 + 签名验证 |
| `contact.go` | ✅ 存在（893 bytes） | ✅ | 白名单过滤，含 IsAllowed/AddUser/RemoveUser |
| `config.go` | ✅ 存在（1591 bytes） | ✅ | WeCom 专用配置 + init() 自注册工厂 |
| **`message.go`** | **不存在** | ❌ | **受影响端**：设计文档 §2 包结构明确列出 `message.go` 负责"消息格式转换（企业微信 XML ↔ channel.Message）"。当前这个转换逻辑内联在 `callback.go` 的 `handleMessage` 函数中（`WeComMessage` struct + 手动字段赋值）。功能是工作的，但不满足单一职责原则。**建议**：提取到独立的 `message.go`，让 callback.go 只做 HTTP 处理 |
| **`adapter_test.go`** | **不存在** | ❌ | **受影响端**：wecom 包零单元测试。加密解密、签名验证、消息格式转换、白名单过滤——这些都是纯逻辑且有安全敏感性，缺少测试是高风险的。**恢复路径**：至少覆盖 crypto（加解密往返）+ contact（白名单增删查）+ message（XML ↔ Message 转换） |

### 3.3 Cloud 模型路由

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.6 | Cloud 模型路由实现（Phase 2.8） | **仍是 stub** | ❌ | **受影响端**：`core/router.go` 中 `modelHint == "cloud"` 分支返回 `TargetModel: "deepseek-chat"` + `Backend: "cloud"`，但没有任何实际 HTTP 调用到 DeepSeek API。**这是一个影响功能的缺口**：用户指定 cloud 模型时会得到一个 stub 字符串而非真实推理结果。**恢复路径**：在 `inference/client.go` 中增加 CloudClient 实现，或扩展 InferenceClient 支持云端 endpoint 配置 |

### 3.4 输出过滤链

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.7 | PlatformFilter 修正 `qclaw → wecom` | 已修正 | ✅ | `filters.go` 中 switch case 使用 `"wecom", "wechat"`，无 qclaw 残留 |
| 2.8 | 审计日志 channel 标识 | 设计提及但未在代码中独立实现审计 | ⚠️ | 审计日志功能在代码库中未找到独立模块。当前依赖 zap 结构化日志 |

### 3.5 记忆沉淀 source 标识

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.9 | source 标识：`wecom-session` 等 | 需验证具体格式 | ⚠️ | memory 模块存在但需要检查实际 source 字段值 |

### 3.6 Phase 2 测试脚本

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.10 | `tests/integration/phase2_1_wecom_callback.sh` | 不存在 | ❌ | **受影响端**：Phase 2 零集成测试。无法自动化验证 wecom 回调流程、白名单过滤、加密解密。后续 Phase 改动时无法保证 Phase 2 不退 |
| 2.11 | `tests/e2e/phase2_e2e_wechat_reply.md` | 不存在 | ❌ | **受影响端**：无文档化的微信端到端测试步骤。虽然需要真实微信环境，但步骤应该固化 |

### 3.7 WeChat 端到端验证

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.12 | 企业微信后台配置 | 未验证 | ❌ | 需要公网穿透（ngrok/Cloudflare Tunnel）+ 企业微信后台回调 URL 配置 + 实际消息收发。**这是 Phase 2 的核心验证点**，当前未完成意味着 wecom adapter 虽然代码存在但从未在真实环境中跑通过 |

### 3.8 wecom 自动审批

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 2.13 | `auto_approve` 配置 | config.go 中存在字段 | ⚠️ | config 结构存在但实际审批流未实现（设计 Phase 2 最小版用手动审批） |

---

## 四、Phase 3 逐项审核

### 4.1 Web Chat 挂件

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 3.1 | `chat-widget.js`（零框架依赖，单文件） | ✅ 存在，526 行 | ✅ | 符合设计：纯 JS、WebSocket 客户端、气泡 UI、流式渲染、深色/浅色主题、右下角气泡样式 |
| 3.2 | Go Agent `/channels/webchat/ws` 端点 | 需验证 gateway 中 WebSocket 路由 | ⚠️ | gateway 中有 WebSocket 支持，但需确认是否专为 webchat 提供端点 |

### 4.2 Docker 容器化

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 3.3 | `docker-compose.yml` | **不存在** | ❌ | **受影响端**：Phase 4 设计明确要求 "Docker Compose 新增 HA 服务"（Task 1）。没有 docker-compose.yml 做基础，Phase 4 无法开始。当前所有服务（agent, inference, qdrant）都是裸进程运行，没有容器网络。**恢复路径**：编写包含 agent + qdrant + 可选 inference 的 docker-compose.yml |
| 3.4 | Dockerfile（Go Agent） | 不存在 | ❌ | **受影响端**：没有容器镜像，无法做 docker-compose 编排，也无法推到 k3s |
| 3.5 | Dockerfile（Inference Service） | 不存在 | ❌ | Python 推理服务虽已被 Go 替代，但仍需考虑是否为其准备容器化 |

### 4.3 Helm Chart / k3s

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 3.6 | `charts/ai-agent/` 完整目录 | **不存在** | ❌ | **受影响端**：设计文档 §3 列出了 15 个模板文件。一个都不存在。k3s 部署完全不可行 |
| 3.7 | `values.yaml` 参数化配置 | 不存在 | ❌ | |
| 3.8 | NetworkPolicy | 不存在 | ❌ | 属于生产安全，可推迟 |
| 3.9 | HPA | 不存在 | ❌ | 属于生产优化，可推迟 |

### 4.4 跨环境可重现

| # | 设计要求 | 实际状态 | 判定 | 详细说明 |
|---|---------|---------|:---:|---------|
| 3.10 | `install.sh` 一键安装脚本 | 不存在 | ❌ | 设计 §2.3 详细描述了安装脚本的 6 步流程（检测架构→检测 GPU→生成 .env→拉取镜像→启动→验证）。都不存在 |
| 3.11 | GPU 环境自动检测 | 不存在 | ❌ | 在 install.sh 中实现 |
| 3.12 | 多架构 Docker 镜像（amd64 + arm64） | 不存在 | ❌ | 依赖 Dockerfile + CI buildx |

---

## 五、Phase 4 完整细节

（以下为设计文档 `2026-07-01-phase4-smart-home-automation-design.md` 的完整翻译和结构化呈现，当前全部未实现。）

### 5.1 总体架构

```
涂鸦设备 ──Tuya Open API──┐
天猫精灵 ──MoloBot─────────┤
小米设备 ──MIOT────────────┤
                           ▼
                    Home Assistant
                    (Docker 容器)
                      REST API + WebSocket 事件总线
                           │
                           ▼
                    Go Agent HA Client
                    (新增 internal/smarthome/)
                      ├── client.go     (HA REST 客户端)
                      ├── analyzer.go   (规则分析引擎)
                      ├── rules.go      (规则生命周期管理)
                      └── storage.go    (Agent 侧长期数据存储)
                           │
                           ▼
                    WeChat 通知 → 用户确认 → 规则创建 → HA 写入
```

### 5.2 Task 1: Home Assistant 部署

**前置依赖**：Phase 3 的 docker-compose.yml 必须已存在。

**步骤**：
1. 在 `docker-compose.yml` 新增 `home-assistant` 服务
2. 安装 HACS → 安装涂鸦集成 (`tuya-home-assistant`)
3. 安装小米集成 (`ha_xiaomi_home`)
4. 安装 MoloBot（天猫精灵）
5. 配置 Long-Lived Access Token
6. 验证三平台设备在 HA 仪表板中可见可操作

**交付物**：
- `docker-compose.yml` 更新（含 HA 服务定义）
- `config/ha/configuration.yaml`（HA 配置参考）
- 集成测试脚本：验证 HA API 可访问 + 设备列表非空

**当前状态**：❌ 全部未开始

### 5.3 Task 2: Go Agent HA Client

**目标**：实现 `internal/smarthome/` 包，封装 HA REST API 调用。

**接口定义**：

```go
// internal/smarthome/client.go

type HomeAssistantClient struct {
    BaseURL string   // http://homeassistant:8123
    Token   string   // HA Long-Lived Access Token
}

// 设备状态
func (c *HomeAssistantClient) GetStates() ([]EntityState, error)

// 历史数据（时间范围查询）
func (c *HomeAssistantClient) GetHistory(entityID string, start, end time.Time) ([]HistoryEntry, error)

// 设备控制
func (c *HomeAssistantClient) CallService(domain, service string, data map[string]interface{}) error

// 创建自动化规则
func (c *HomeAssistantClient) CreateAutomation(automation AutomationConfig) error

// 重载自动化配置
func (c *HomeAssistantClient) ReloadAutomations() error

// WebSocket 事件监听（用于实时触发）
func (c *HomeAssistantClient) SubscribeEvents(ctx context.Context, handler EventHandler) error
```

**关键类型**：

```go
type EntityState struct {
    EntityID    string                 // "light.living_room"
    State       string                 // "on" / "off" / "25.5"
    Attributes  map[string]interface{} // brightness, color_temp, etc.
    LastChanged time.Time
}

type HistoryEntry struct {
    EntityID  string
    State     string
    Timestamp time.Time
}

type AutomationConfig struct {
    Alias       string
    Description string
    Trigger     map[string]interface{}
    Condition   []map[string]interface{}
    Action      []map[string]interface{}
}
```

**集成点**：
- `core/config.go` 新增 `SmartHomeConfig`
- `core/app.go` Bootstrap 初始化 `HomeAssistantClient`

**当前状态**：❌ `internal/smarthome/` 目录不存在

### 5.4 Task 3: Agent 侧长期数据存储

**背景**：HA 只保留 7 天历史，Agent 需要更长期数据做趋势分析。

**设计方案**：
- 定时任务（cron）：每小时从 HA 拉取设备历史 → 存入 Agent 侧
- 存储格式：结构化 JSON Lines 文件 `agent-vault/smart-home/history/{entity_id}/{YYYY-MM}.jsonl`
- 或使用独立的 SQLite DB（如果数据量大）

```go
func (a *Agent) startHistoryCollector(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Hour)
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                a.collectDeviceHistory(ctx)
            }
        }
    }()
}
```

**当前状态**：❌ 未实现

### 5.5 Task 4: 规则分析引擎

**目标**：从历史数据中发现用户行为模式，生成自动化建议。

**分析维度**：

| 维度 | 检测内容 | 示例 |
|------|---------|------|
| 时间模式 | 每天/每周固定时间的设备操作 | "过去 14 天，93% 的日子在 18:00-19:00 客厅灯被打开" |
| 关联模式 | 设备之间的因果链 | "开门传感器触发后 30 秒内，走廊灯有 85% 概率被打开" |
| 异常模式 | 偏离正常范围的传感器数据 | "温度传感器在凌晨 3 点异常飙升 5°C" |
| 节能机会 | 无人时设备仍开启 | "客厅灯每天平均有 2.3 小时在无人状态下亮着" |

**建议生成流程**：
1. 分析引擎扫描历史数据
2. 发现候选模式 → 计算置信度（≥ 0.7 才推送）
3. 去重：检查是否已有类似规则
4. 生成 RuleSuggestion 结构体
5. 通过 WeChat 推送给用户

**置信度计算因素**：命中率（天数/总天数）、时间一致性（标准差）、数据量（样本是否充足，至少 7 天）

**当前状态**：❌ 未实现

### 5.6 Task 5: 规则建议交互流程

```
┌─────────┐     ┌──────────┐     ┌─────────┐     ┌──────────┐
│  Agent  │     │  WeChat  │     │  用户   │     │    HA    │
└────┬────┘     └────┬─────┘     └────┬────┘     └────┬─────┘
     │               │               │               │
     │ 发现模式       │               │               │
     │ 生成建议       │               │               │
     │──────────────>│               │               │
     │ 推送建议卡片    │               │               │
     │               │──────────────>│               │
     │               │  显示建议      │               │
     │               │<──────────────│               │
     │               │  确认/修改/忽略 │               │
     │<──────────────│               │               │
     │ 用户决策       │               │               │
     │               │               │               │
     │ (如确认)      │               │               │
     │ 生成 HA YAML  │               │               │
     │──────────────────────────────────────────────>│
     │ POST /api/services/automation/reload          │
     │<──────────────────────────────────────────────│
     │               │               │               │
     │ 规则文档写入   │               │               │
     │ agent-vault/  │               │               │
     │ smart-home/   │               │               │
     │ rules/        │               │               │
     │               │               │               │
     │──────────────>│               │               │
     │ 确认创建成功   │               │               │
```

**WeChat 通知格式**：
```
【自动化建议 #001】

触发条件: 每天 18:00
执行动作: 打开客厅灯
置信度: 85%
数据来源: 过去 14 天，命中率 93%

回复 "确认" 创建规则
回复 "修改" 自定义调整
回复 "忽略" 放弃此建议
```

**当前状态**：❌ 未实现

### 5.7 Task 6: Agent 人设集成

**System Prompt 补充**（在 personality 配置中）：
```yaml
personality:
  smart_home_prompt: |
    你是用户的智能家居管家。你可以：
    1. 查询家中设备状态（灯、空调、传感器等）
    2. 根据历史数据建议自动化规则
    3. 帮助用户管理已有的自动化

    约束：
    - 所有设备操作前必须获得用户确认
    - 建议语气：温和建议，不强迫
    - 当用户询问"家里怎么样"时，主动报告异常设备状态
```

**设备-房间映射**（配置驱动）：
```yaml
smart_home:
  room_mapping:
    living_room: "客厅"
    bedroom: "卧室"
    kitchen: "厨房"
  device_labels:
    light.living_room: "客厅主灯"
    sensor.temperature_living_room: "客厅温度传感器"
```

**当前状态**：❌ 未实现

### 5.8 Task 7: 端到端验证清单

- [ ] 三平台设备（涂鸦/天猫精灵/小米至少各 1 个）在 HA 中可见
- [ ] `GET /api/states` 返回设备列表
- [ ] Agent 能通过 HA Client 查询任意设备实时状态
- [ ] Agent 能通过 HA Client 查询历史数据
- [ ] Agent 侧长期数据存储正常工作（运行 24h+ 验证）
- [ ] 规则分析引擎能发现至少一个使用模式
- [ ] WeChat 通知 → 确认 → HA 规则创建 全流程跑通
- [ ] 创建的规则在 HA 中生效（触发条件满足时自动执行）
- [ ] agent-vault/smart-home/rules/ 中有完整规则文档

### 5.9 Phase 4 对现有系统的侵入汇总

| 现有模块 | 变更类型 | 具体内容 |
|---------|---------|---------|
| `core/config.go` | 新增 | `SmartHomeConfig` 结构体 |
| `core/app.go` | 新增 | `smarthome.HomeAssistantClient` 初始化 |
| `core/agent.go` | 修改 | 新增 HA 事件监听 goroutine + 定时历史采集 |
| `core/router.go` | 启用 | `RouteDecision.ConfirmRequired` 字段启用 |
| `config/agent.yaml` | 新增 | `smart_home:` 配置节 |
| `.env.example` | 新增 | `HA_ACCESS_TOKEN` |
| `docker-compose.yml` | 新增 | `home-assistant` 服务 |
| `internal/smarthome/` | **新增** | client.go, analyzer.go, rules.go, storage.go |
| `agent-vault/smart-home/` | **新增** | rules/, reports/, devices/ 目录 |

---

## 六、分类汇总

### 6.1 影响正常功能的缺失项（Phase 4 前必须完成）

| 优先级 | # | 缺失项 | 所属 Phase | 影响说明 |
|:---:|---|--------|-----------|---------|
| **P0** | 1 | **Cloud 模型路由仍是 stub** | Phase 2 | 用户指定 cloud 模型时无实际推理，返回空壳字符串。影响 LLM Gateway 核心功能 |
| **P0** | 2 | **Docker Compose 缺失** | Phase 3 | Phase 4 Task 1 直接依赖于此。没有 docker-compose 意味着 HA 无法与 Agent 在同一网络运行 |
| **P0** | 3 | **Go 核心模块零单元测试** | Phase 1 | CONSTITUTION §5.2 明确要求每个 internal 包 ≥1 条测试。9 个模块当前覆盖为 0 |
| **P1** | 4 | **Phase 1.1 vault bootstrap 测试脚本** | 1.1 | 无回归保护。改 Phase 1 代码时无法验证 vault 基础结构不被破坏 |
| **P1** | 5 | **Phase 1.5 E2E 文档** | 1.5 | 无文档化的端到端测试步骤。手动验证依赖记忆 |
| **P1** | 6 | **run_all.sh 全量回归脚本** | 全局 | CONSTITUTION 铁律 3 要求"Phase N 开发前跑通 Phase 1..N-1 全量测试"。手动逐条跑不可持续 |
| **P1** | 7 | **wecom 单元测试** | Phase 2 | 加密/解密/签名/白名单——都是安全敏感纯逻辑，零测试高风险 |
| **P1** | 8 | **Phase 2 集成测试脚本** | Phase 2 | Phase 2 零自动化测试。后续改动无法检测 wecom 回调回归 |
| **P1** | 9 | **Helm Chart 完整目录** | Phase 3 | Phase 4 的 k3s 部署路径缺失。虽然 Phase 4 初期用 Docker Compose，但生产部署需要 k3s |
| **P2** | 10 | **wecom/message.go 分离** | Phase 2 | XML 转换逻辑内联在 callback.go 中。功能工作但不满足单一职责 |
| **P2** | 11 | **Phase 2 E2E 文档** | Phase 2 | 微信端到端步骤未固化 |
| **P2** | 12 | **Dockerfiles** | Phase 3 | 无容器镜像就无法做 docker-compose 编排 |
| **P2** | 13 | **Qdrant 集成** | 1.6 | 虽被设计推迟，但 Phase 4 长期趋势存储强依赖。Phase 4 启动前必须就绪 |

### 6.2 附加功能（可推迟到 Phase 4 完成后）

| # | 内容 | 所属 Phase | 理由 |
|---|------|-----------|------|
| A | Syncthing 文件同步 | 1.6 | 单机开发不需要多节点同步 |
| B | 多架构 Docker 镜像（amd64 + arm64） | Phase 3 | 生产部署时才需要，开发阶段用单架构即可 |
| C | `install.sh` 一键安装脚本 | Phase 3 | Phase 3 完整交付物，可推迟 |
| D | GPU 环境自动检测脚本 | Phase 3 | 随 install.sh 一起实现 |
| E | HPA 自动扩缩容 | Phase 3 | 生产优化，非功能需求 |
| F | NetworkPolicy 网络隔离 | Phase 3 | 生产安全，开发阶段不需要 |
| G | WeChat `auto_approve` 自动审批流 | Phase 2 | 设计 Phase 2 最小版用手动审批 |
| H | Web Chat 主题深度定制 | Phase 3 | 当前 chat-widget.js 已有 dark/light 主题，基本够用 |
| I | 跨环境可重现验证（另一台机器跑通） | Phase 3 | Phase 3 完整交付物 |

---

## 七、Go 单元测试缺口详表

| 模块 | 测试文件 | 状态 | 风险等级 |
|------|---------|:---:|:---:|
| `core/config.go` | `config_test.go` | ✅ 存在 | — |
| `vault/reader.go` | `reader_test.go` | ✅ 存在 | — |
| `chain/` | `chain_test.go` | ✅ 存在 | — |
| `gateway/http.go` | `chat_test.go` | ⚠️ 仅 551 bytes | 中 |
| `core/agent.go` | 无 | ❌ | 高 — Agent 主循环 |
| `core/router.go` | 无 | ❌ | 高 — 路由决策 |
| `core/session.go` | 无 | ❌ | 中 — 会话管理 |
| `core/session_store.go` | 无 | ❌ | 中 — 会话持久化 |
| `gateway/websocket.go` | 无 | ❌ | 高 — WebSocket 处理 |
| `memory/sedimentation.go` | 无 | ❌ | 中 — 记忆沉淀 |
| `memory/summarizer.go` | 无 | ❌ | 中 — 对话摘要 |
| `channel/manager.go` | 无 | ❌ | 高 — 通道管理 |
| `channel/wecom/*` | 无 | ❌ | 高 — 加密/解密/签名 |
| `filter/filters.go` | 无 | ❌ | 中 — 输出过滤 |

---

## 八、建议执行顺序（为 Phase 4 铺路）

### 第一轮：补齐测试基础设施（1-2 天）

1. 编写 `tests/integration/phase1_1_vault_bootstrap.sh`
2. 编写 `tests/e2e/phase1_5_end_to_end.md`
3. 编写 `tests/integration/run_all.sh`
4. 为核心模块补齐单元测试（优先 router → channel/manager → wecom → agent → filter）

### 第二轮：完善 Phase 2 遗留（2-3 天）

5. 实现 Cloud 模型路由（接入 DeepSeek API）
6. 编写 wecom 单元测试 + 集成测试脚本
7. 提取 wecom/message.go
8. 验证 WeChat 端到端（需要公网穿透 + 企业微信后台配置）

### 第三轮：搭建 Phase 3 基础（2-3 天）

9. 编写 Dockerfile + docker-compose.yml
10. 创建 Helm Chart 目录
11. 部署 Qdrant

### 第四轮：启动 Phase 4（5-7 天）

12. Home Assistant 部署 + 设备接入
13. HA Client 实现
14. 数据存储 + 规则分析引擎
15. 规则建议交互流程
16. 端到端验证

---

> **下次讨论重点**：Cloud 模型路由的具体实现方案（DeepSeek API 接入细节）、以及是否先做 Docker Compose 还是先补齐测试。
