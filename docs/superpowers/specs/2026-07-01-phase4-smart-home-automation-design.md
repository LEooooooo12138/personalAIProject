# Phase 4 设计：智能家居 + 自动化

> **依赖**: Phase 1-3 完成（知识库 + Agent + WeChat + 容器化）
> **目标**: Agent 接入 Home Assistant，定期分析设备数据，建议并创建自动化规则

---

## 1. 设备接入架构

### 1.1 三平台通过 Home Assistant 统一接入

```
涂鸦设备 ──Tuya Open API──► ┐
天猫精灵 ──MoloBot────────► │ Home Assistant ──REST API──► Go Agent
小米设备 ──MIOT (官方)──────► ┘
                              │
                              ├─ 实时状态
                              ├─ 短期历史 (HA 内置 DB)
                              └─ 设备控制指令
```

### 1.2 各平台接入方式

| 平台 | HA 集成 | 来源 | 接入方式 |
|------|---------|------|---------|
| **涂鸦** | `tuya-home-assistant` (962⭐) | https://github.com/tuya/tuya-home-assistant | Smart Life App → User Code → HA 扫码 |
| **天猫精灵** | `molobot` (79⭐) | https://github.com/haoctopus/molobot | HACS 安装 → 配置手机号/密码 → 天猫精灵 App 绑定 |
| **小米** | `ha_xiaomi_home` (21,833⭐) | https://github.com/XiaoMi/ha_xiaomi_home | 小米账号登录 → 自动发现 MIOT 设备 |

### 1.3 Home Assistant 部署演进

```
Phase 4 初期: 台式机 Docker
  ┌──────────────────────────────────┐
  │  Docker Compose                   │
  │  ├── agent          (Go)          │
  │  ├── inference      (Python)      │
  │  ├── sidecar        (Node.js)     │
  │  ├── qdrant                      │
  │  └── home-assistant  ← 新增       │
  │       同 Docker Network            │
  │       零网络延迟                    │
  └──────────────────────────────────┘

Phase 4 后期: 迁移到 NAS 或独立硬件
  Home Assistant 独立运行 (NAS Docker / 树莓派 / HA Yellow)
  Agent 通过 HTTP REST API 远程调用
  24/7 在线，不依赖台式机
```

---

## 2. 数据架构

### 2.1 双层存储

```
Home Assistant (短期/实时)           Agent 侧 (长期/分析)
┌──────────────────────┐           ┌──────────────────────┐
│  SQLite / MariaDB    │           │  Qdrant / 独立 DB     │
│                      │           │                      │
│  设备实时状态         │  定时拉取  │  长期趋势数据          │
│  最近 7 天历史        │──────────►│  设备使用模式          │
│  自动化规则           │           │  异常检测结果          │
│  场景/脚本           │           │  自动化建议记录        │
└──────────────────────┘           └──────────────────────┘
```

### 2.2 数据流

```
HA 设备状态变更 → HA 事件总线
    │
    ├─ Agent Webhook 监听 (实时) → 触发即时响应
    │
    └─ Agent 定时任务 (每小时) → 拉取历史数据
         │
         ├─ 存入 Agent 侧 DB (长期趋势)
         ├─ 分析设备使用模式
         └─ 生成自动化建议
```

---

## 3. 自动化规则引擎

### 3.1 建议模式（人工确认）

```
Agent 定期分析 → 发现模式 → 生成建议 → 通知用户 → 用户确认 → 创建规则
    │                          │                      │
    │                          ├─ 知识库存档            │
    │                          │  agent-vault/          │
    │                          │  smart-home/rules/     │
    │                          │                        │
    │                          └─ WeChat 通知           │
    │                                                  │
    └─ 每天分析一次                                       └─ HA API 写入
       分析维度:                                            automations.yaml
       - 时间模式 (几点开灯/关灯)                            + automation.reload
       - 关联模式 (开门→开灯)
       - 异常模式 (异常温度/湿度)
       - 节能机会 (无人时自动关)
```

### 3.2 规则生命周期

```
1. Agent 发现模式:
   "过去 14 天，每天 18:00-19:00 客厅灯都被打开"

2. Agent 生成建议:
   ┌─────────────────────────────────────┐
   │  自动化建议 #001                      │
   │  触发条件: 每天 18:00                 │
   │  执行动作: 打开客厅灯                 │
   │  置信度: 0.85                        │
   │  数据来源: 14 天历史，命中率 93%       │
   │                                      │
   │  [确认创建] [修改] [忽略]             │
   └─────────────────────────────────────┘

3. 用户确认 → Agent 执行:
   a. 生成 HA 自动化 YAML
   b. 通过 HA REST API 写入 automations.yaml
   c. 调用 automation.reload
   d. 规则文档写入 agent-vault/smart-home/rules/
   e. WeChat 回复确认
```

### 3.3 规则文档模板

```yaml
---
title: "自动化规则：傍晚自动开灯"
created: 2026-07-01
source: agent-analysis
trigger: "每天 18:00"
action: "打开客厅灯"
confidence: 0.85
data_period: "14天"
hit_rate: "93%"
ha_automation_id: "automation.evening_light"
status: active
---
## 发现模式
过去 14 天中，13 天在 18:00-19:00 之间客厅灯被手动打开。

## 规则设计
- 触发: 时间 18:00
- 条件: 家中有人 (基于传感器)
- 动作: 打开客厅灯
- 安全: 超时 2 小时自动关闭
```

---

## 4. Home Assistant 集成接口

### 4.1 Go Agent ↔ HA 通信

```go
// 内部模块: internal/smarthome/client.go
type HomeAssistantClient struct {
    BaseURL string   // http://homeassistant:8123
    Token   string   // HA Long-Lived Access Token
}

// 查询设备状态
func (c *HomeAssistantClient) GetStates() ([]EntityState, error)

// 查询历史数据
func (c *HomeAssistantClient) GetHistory(entityID string, start, end time.Time) ([]HistoryEntry, error)

// 控制设备
func (c *HomeAssistantClient) CallService(domain, service string, data map[string]interface{}) error

// 创建自动化
func (c *HomeAssistantClient) CreateAutomation(automation AutomationConfig) error

// 重载自动化
func (c *HomeAssistantClient) ReloadAutomations() error

// 监听事件 (WebSocket)
func (c *HomeAssistantClient) SubscribeEvents(ctx context.Context, handler EventHandler) error
```

### 4.2 Agent 侧存储设计

```
agent-vault/smart-home/
├── rules/                    # 自动化规则设计文档
│   ├── 2026-07-01-evening-light.md
│   └── 2026-07-02-door-light.md
├── reports/                  # 定期分析报告
│   ├── 2026-07-week1.md
│   └── 2026-07-week2.md
└── devices/                  # 设备信息页
    ├── living-room-light.md
    └── front-door-sensor.md
```

---

## 5. 实施阶段

### Task 1: Home Assistant 部署
- Docker Compose 新增 HA 服务
- 安装涂鸦集成 + 小米集成 + MoloBot
- 配置 Long-Lived Access Token
- 验证三个平台设备可见

### Task 2: Go Agent HA Client
- 实现 HomeAssistantClient (REST API)
- 实现设备状态查询
- 实现设备控制
- 实现历史数据查询
- 实现 WebSocket 事件监听

### Task 3: Agent 侧数据存储
- 设计设备状态 DB schema
- 定时任务：每小时拉取 HA 历史数据
- 存入 Agent 侧 DB

### Task 4: 规则分析引擎
- 时间模式检测
- 关联模式检测
- 异常模式检测
- 建议生成 + 置信度计算

### Task 5: 规则建议流程
- WeChat 通知建议
- 用户确认/修改/忽略交互
- HA 自动化 YAML 生成 + 写入
- 规则文档写入 agent-vault

### Task 6: Agent 人设集成
- 智能家居相关 system prompt
- 设备命名和房间映射
- 建议语气和格式

### Task 7: 端到端验证
- 涂鸦/天猫精灵/小米设备通过 HA 可控
- Agent 可查询设备状态和历史
- 自动化建议流程完整跑通
- WeChat 通知 + 确认 + 创建规则

---

## 6. 完成标准

- [ ] 三个平台设备通过 HA 可见可控
- [ ] Agent 可查询设备实时状态和历史数据
- [ ] Agent 侧长期数据存储正常
- [ ] 规则分析引擎能发现使用模式
- [ ] WeChat 推送建议 → 用户确认 → HA 规则创建 全流程跑通
- [ ] 规则文档在 agent-vault 中有完整记录
