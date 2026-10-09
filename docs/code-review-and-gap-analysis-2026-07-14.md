# 代码审核 + 设计文档缺口分析 + Phase 4 概览

> 生成日期: 2026-07-14
> 审核范围: go-agent 全量代码 + inference-service + 所有设计文档
> 基准文档: CONSTITUTION.md, Phase 1-4 设计文档, Phase 1 实施计划

---

## 一、代码审核结果

### P0 — 架构不变量违反（必须修）

#### 1. I-3 违反：Gateway 承担了调度决策职责

| 文件 | 问题 |
|------|------|
| `go-agent/internal/gateway/http.go` | `handleChatLegacyHTTP` 自己做了 vault search + context assembly + routing，而不是委托给 Agent Router |
| `go-agent/internal/gateway/websocket.go` | `retrieveVaultContext` 实现了完整的 RRF fusion + chunk retrieval + system prompt assembly |

CONSTITUTION I-3 原文：**"Agent Router 是唯一的调度决策层"**。Gateway 应该只做协议转换（HTTP ↔ 内部消息），不做 vault 检索、上下文组装、路由选择。

#### 2. 单一职责严重违反：`gateway/websocket.go` 是上帝文件

该文件（16436 bytes）混合了以下职责，按 CONSTITUTION §3.1 的 400 行标准超标约 40 倍：

| 混在一起的职责 | 应该归属 |
|--------------|---------|
| WebSocket 连接管理 | gateway（保留） |
| RRF 融合检索 | `vault/retrieval.go` |
| chunk 分块 + scoring | `vault/chunker.go` |
| system prompt 组装 | `core/agent.go` 或 `chain/` |
| 推理调用 | `core/agent.go` |
| 过滤链执行 | `core/agent.go` |
| session 持久化 | `core/session_store.go` |
| memory sedimentation | `core/agent.go`（已有 `consumeSessionEnds`） |

#### 3. 依赖方向违规：`core/app.go` 直接 import `wecom` 包

```go
// go-agent/internal/core/app.go line 15
"github.com/yuanleyao/ai-agent/internal/channel/wecom"
```

**违反 I-9**：Channel 接口是所有通道的唯一抽象。`core` 包不应该知道 `wecom` 的具体类型。

**连锁问题**：`core/app.go` 的 `Bootstrap` 函数（第 60-75 行）手动做了 wecom 配置映射，把通道专属配置泄露到了核心包。

**修复方向**：
- wecom 包自己负责解析配置并注册到 channel.Manager
- core/app.go 只通过 `channel.Channel` interface 操作所有通道
- 引入一个 `channel.Factory` 或注册机制，让各 adapter 自注册

### P1 — 代码规范违反（应该修）

#### 4. 硬编码配置值

| 位置 | 硬编码 | 应改为 |
|------|--------|--------|
| `gateway/websocket.go` | `const rrfK = 60` | `config.retrieval.rrf_k` |
| `gateway/http.go` | `300*time.Second` | `config.inference.chain_timeout` |
| `gateway/http.go` | `30*time.Second` | `config.inference.search_timeout` |
| `gateway/http.go` | `10*time.Second` | `config.server.shutdown_timeout` |
| `core/router.go` `Route()` | `"gemma4:12b"`, `"llava:7b"` | 从 config 读取 |
| `core/app.go` `ollamaEmbedder.Embed()` | `"bge-m3"` | `config.models.embedding` |

#### 5. Gateway 存在两条并行处理路径

```
handleChatWithChain          handleChatLegacyHTTP
       ↓                            ↓
  chain.ChainExecutor        直接调 infer.Chat + 手动组装上下文
```

`core/agent.go` 里的 `handleMessage` 也同样有 chain vs legacy 分支。两套逻辑并存增加维护负担，且行为容易不一致。

#### 6. 重复逻辑

| 重复代码 | 出现位置 |
|---------|---------|
| `extractResponseContent` | `core/agent.go` + `gateway/websocket.go`（`extractWSResponse`） |
| `sessionToConversation` | `core/agent.go` + `gateway/websocket.go`（`sessionToConv`） |
| vault context assembly | `gateway/http.go:handleChatLegacyHTTP` + `gateway/websocket.go:retrieveVaultContext` |
| session 持久化 | `gateway/websocket.go`（多处 `go w.sessionStore.SaveSession`）+ `core/session_store.go` |

### P2 — 模块边界模糊（建议修）

#### 7. gateway 包越界

`gateway/` 包本该是 HTTP → 业务逻辑的薄适配层，实际变成了第二个 agent loop：

```
gateway/http.go
├── vault search + context assembly  → 应属于 core/router
├── chain execution                  → 应属于 core/agent
├── filter testing                   → 可接受
└── session history API              → 可接受

gateway/websocket.go
├── RRF fusion retrieval             → 应属于 vault/retrieval
├── chunk scoring + selection        → 应属于 vault/chunker
├── system prompt assembly           → 应属于 core/agent 或 chain
├── inference calling                → 应属于 core/agent
└── session persistence              → 应属于 core/session_store
```

#### 8. Phase 2 特性过早集成到 Phase 1 骨架

- `core/app.go` 的 `Bootstrap` 包含 wecom adapter 初始化
- Memory sedimentation 已深度集成到 agent loop
- Chain system 和 legacy path 并存
- 这些应该在各自 Phase 完成后再集成，而非在 Phase 1 骨架中预埋

### 做得好的地方

| 模块 | 亮点 |
|------|------|
| `internal/inference/client.go` | 干净的 `Client` interface + `OllamaClient` 实现，符合 I-2 |
| `internal/filter/filters.go` | 清晰的 Filter Chain 模式，每个 Filter 独立、可测试 |
| `internal/channel/channel.go` | 简洁的 `Channel` interface + 权限矩阵，符合 I-9 |
| `internal/channel/manager.go` | fan-in 模式实现优雅，`Run()` 方法设计合理 |
| `internal/vault/reader.go` | Reader/Writer interface 分离，依赖倒置 |
| `internal/core/config.go` | Viper + 环境变量展开，结构清晰 |
| `internal/channel/wecom/` | 自包含的适配器包，加密/鉴权/回调/白名单各自独立 |

### 测试覆盖情况

| 模块 | 测试文件 | 状态 |
|------|---------|------|
| `core/config.go` | `config_test.go` | 有 |
| `vault/reader.go` | `reader_test.go` | 有 |
| `chain/` | `chain_test.go` | 有 |
| `gateway/http.go` | `chat_test.go` | 有（仅 551 bytes） |
| `core/agent.go` | 无 | **缺** |
| `core/router.go` | 无 | **缺** |
| `core/session.go` | 无 | **缺** |
| `core/session_store.go` | 无 | **缺** |
| `gateway/websocket.go` | 无 | **缺** |
| `memory/sedimentation.go` | 无 | **缺** |
| `memory/summarizer.go` | 无 | **缺** |
| `channel/manager.go` | 无 | **缺** |
| `filter/filters.go` | 无 | **缺** |

---

## 二、设计文档缺口分析

### 影响正常功能的缺失项（应在 Phase 4 之前完成）

| # | 缺失项 | 所属 Phase | 影响说明 |
|---|--------|-----------|---------|
| 1 | **Phase 1.1 vault bootstrap 集成测试脚本** | 1.1 | `tests/integration/phase1_1_vault_bootstrap.sh` 未创建，无法验证知识库基础结构 |
| 2 | **Phase 1.5 E2E 测试文档** | 1.5 | `tests/e2e/phase1_5_end_to_end.md` 未创建，缺少端到端回归基准 |
| 3 | **全量回归脚本** | 全局 | `tests/integration/run_all.sh` 未创建，无法一键验证所有历史阶段 |
| 4 | **GitHub Actions CI** | Phase 1 | `.github/workflows/check.yml` 未创建，CONSTITUTION §5.2 明确要求 |
| 5 | **Qdrant 向量数据库** | 1.6 | Phase 4 智能家居分析引擎依赖长期趋势数据存储 |
| 6 | **Cloud 模型路由仍是 stub** | Phase 2 | `model == "cloud"` 只返回字符串 `"deepseek-chat"`，无实际 API 调用 |
| 7 | **WeChat 端到端验证** | Phase 2 | 企业微信后台配置、公网穿透（ngrok/Cloudflare Tunnel）、实际消息收发未验证 |
| 8 | **Docker Compose 全服务编排** | Phase 3 | Phase 4 需要 HA 和 Agent 在同一 Docker 网络中运行 |
| 9 | **Helm Chart** | Phase 3 | `charts/ai-agent/` 目录不存在，k3s 部署基础缺失 |
| 10 | **Go 单元测试覆盖** | Phase 1 | 核心模块（agent, router, session, gateway, memory, filter）测试为空白 |

### 附加功能（可推迟到 Phase 4 之后）

| # | 内容 | 理由 |
|---|------|------|
| A | Syncthing 文件同步 | 单机开发场景不需要 |
| B | 多架构 Docker 镜像（amd64 + arm64） | Phase 3 生产部署时再做 |
| C | `install.sh` 一键安装脚本 | Phase 3 交付物 |
| D | GPU 环境自动检测脚本 | Phase 3 交付物 |
| E | HPA 自动扩缩容 | 生产优化，非功能需求 |
| F | NetworkPolicy 网络隔离 | 生产安全，非功能需求 |
| G | WeChat `auto_approve` 自动审批流 | Phase 2 最小版用手动审批即可 |
| H | Web Chat 主题定制 | Phase 3 增强功能 |
| I | 跨环境可重现验证 | Phase 3 交付物 |

---

## 三、Phase 4：智能家居 + 自动化 — 完整细节

### 总体架构

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

### Task 1: Home Assistant 部署

**目标**：在 Docker Compose 中新增 HA 服务，接入三平台设备。

**步骤**：
1. 在 `docker-compose.yml` 新增 `home-assistant` 服务
2. 安装 HACS → 安装涂鸦集成 (`tuya-home-assistant`)
3. 安装小米集成 (`ha_xiaomi_home`)
4. 安装 MoloBot（天猫精灵）
5. 配置 Long-Lived Access Token（HA 管理界面 → 用户资料 → 长期访问令牌）
6. 验证三平台设备在 HA 仪表板中可见可操作

**交付物**：
- `docker-compose.yml` 更新（含 HA 服务定义）
- `config/ha/configuration.yaml`（HA 配置参考）
- 集成测试脚本：验证 HA API 可访问 + 设备列表非空

### Task 2: Go Agent HA Client

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

### Task 3: Agent 侧长期数据存储

**目标**：HA 只保留 7 天历史，Agent 侧需要更长期的数据用于趋势分析。

**设计方案**：
- 定时任务（cron）：每小时从 HA 拉取设备历史 → 存入 Agent 侧
- 存储格式：结构化 JSON Lines 文件 `agent-vault/smart-home/history/{entity_id}/{YYYY-MM}.jsonl`
- 或使用独立的 SQLite DB（如果数据量大）

```go
// 定时任务
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

### Task 4: 规则分析引擎

**目标**：从历史数据中发现用户行为模式，生成自动化建议。

**分析维度**：

| 维度 | 检测内容 | 示例 |
|------|---------|------|
| 时间模式 | 每天/每周固定时间的设备操作 | "过去 14 天，93% 的日子在 18:00-19:00 客厅灯被打开" |
| 关联模式 | 设备之间的因果链 | "开门传感器触发后 30 秒内，走廊灯有 85% 概率被打开" |
| 异常模式 | 偏离正常范围的传感器数据 | "温度传感器在凌晨 3 点异常飙升 5°C" |
| 节能机会 | 无人时设备仍开启 | "客厅灯每天平均有 2.3 小时在无人状态下亮着" |

**建议生成流程**：

```
1. 分析引擎扫描历史数据
2. 发现候选模式 → 计算置信度（≥ 0.7 才推送）
3. 去重：检查是否已有类似规则
4. 生成 RuleSuggestion 结构体
5. 通过 WeChat 推送给用户
```

**置信度计算考虑因素**：
- 命中率（天数/总天数）
- 时间一致性（标准差）
- 数据量（样本是否充足，至少 7 天）

### Task 5: 规则建议交互流程

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

### Task 6: Agent 人设集成

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

### Task 7: 端到端验证

**验证清单**：

- [ ] 三平台设备（涂鸦/天猫精灵/小米至少各 1 个）在 HA 中可见
- [ ] `GET /api/states` 返回设备列表
- [ ] Agent 能通过 HA Client 查询任意设备实时状态
- [ ] Agent 能通过 HA Client 查询历史数据
- [ ] Agent 侧长期数据存储正常工作（运行 24h+ 验证）
- [ ] 规则分析引擎能发现至少一个使用模式
- [ ] WeChat 通知 → 确认 → HA 规则创建 全流程跑通
- [ ] 创建的规则在 HA 中生效（触发条件满足时自动执行）
- [ ] agent-vault/smart-home/rules/ 中有完整规则文档

### Phase 4 对现有系统的侵入汇总

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

## 四、建议的执行顺序

### 阶段 A：修架构问题（优先）

1. 拆分 `gateway/websocket.go`：将检索逻辑移到 `vault/retrieval.go`，agent 逻辑移到 `core/agent.go`
2. 消除 `core/app.go` 对 `wecom` 的直接依赖，引入 channel 工厂/注册机制
3. 清理硬编码配置值，全部收归 `config/agent.yaml`

### 阶段 B：补齐缺失的基础设施

4. 创建 `.github/workflows/check.yml`
5. 创建 `tests/integration/run_all.sh`
6. 创建 `tests/integration/phase1_1_vault_bootstrap.sh`
7. 创建 `tests/e2e/phase1_5_end_to_end.md`
8. 为核心模块补齐单元测试

### 阶段 C：完成 Phase 2 遗留

9. Cloud 模型路由完善（实际接入 DeepSeek API）
10. WeChat 端到端验证

### 阶段 D：完成 Phase 3

11. Docker Compose 全服务编排
12. Helm Chart
13. 多架构镜像

### 阶段 E：启动 Phase 4

14. Home Assistant 部署 + 设备接入
15. HA Client 实现
16. 规则分析引擎
17. 规则建议交互流程
18. 端到端验证

---

> **下次讨论重点**：Phase 4 的具体实现方案（从哪个 Task 开始、技术选型细节、风险点）
