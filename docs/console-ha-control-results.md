# 前端对话即时控制 HA：实现与验收记录

日期：2026-10-09。基线：`codex/p1-p2-remediation` / `3db8a0a24da14be3d920a9f10e5dd4c4b37435db`。

## 范围及结论

用户批准继续规划并实现聊天即时控制。本轮实现 M1 的前后端工程链路；微信等外部入口仍暂缓。工程实现阶段没有向真实 HA 发送设备动作。随后按用户授权，已于 2026-10-09 更新本地 Console 服务，仅启用一个明确授权的 switch 通道；页面登录与真实开关验收待完成，详见[本地验收记录](console-ha-live-acceptance.md)。LAN 断网验证、长期运行可靠性和知识管理余项仍按 M2/M3 排期。

| 能力 | 此前 | 本轮结果 |
|---|---|---|
| 前端自然语言设备查询 | 普通知识聊天、设备目录浏览 | 已接入当前 HA 状态读取，返回实体、名称、状态及观察时间 |
| 聊天即时控制 | 缺失 | 单实体 light/switch/fan 的开关提议；只能从显式允许清单选择；常规目标要求负载核验，授权 switch 通道测试有单独标记 |
| 显式确认 | 自动化建议已有，即时控制没有 | 独立提议卡及 REST 确认；聊天中的“确认”不执行 |
| 权限与隔离 | Console Cookie/CSRF/账号及会话隔离已有 | 复用身份；管理员控制、成员查询；提议绑定用户和聊天会话 |
| 执行可靠性 | 无即时动作状态机 | 持久化执行前状态、单实体串行、请求幂等、至多一次主动发送、未知结果不重放 |
| 回读与恢复 | 缺失 | 观察到目标状态才标为成功；超时/网络不确定保留 unknown；核对只读取 |
| 历史卡片 | 只有文本 | 保存提议 ID 引用，重新加载从服务端取当前状态；不会由旧文本恢复动作 |
| 模型契约 | 普通聊天输出 | 严格四字段 JSON；候选、动作、歧义及否定/引用/多目标防护；50条合成样例 |
| 模拟全链路 | 无即时控制路径 | 浏览器→真实 Go handlers→真实控制服务→模拟 HA，含写次数断言 |

## 实现位置与接口

- `go-agent/internal/smarthome/control*.go`：策略、目录候选、存储、提议、确认与回读。
- `go-agent/internal/chain/control_intent.go`：只负责意图解析，不持有 Confirm 或 CallService。
- `go-agent/internal/core/control_chat.go`：在 Console 普通 RAG 前分发设备意图；明确 chat 继续原聊天链。HA 目录不可用时只给模型空候选，禁止凭记忆控制。
- `go-agent/internal/gateway/console_control*.go`：当前身份、会话所有权、CSRF/Origin 及结果发布边界。
- `go-agent/web/src/chat/ControlCard.tsx`、`pages/ChatPage.tsx`：提议、取消、结果、未知状态核对与历史恢复。

REST 前缀 `/api/console/v1/control/proposals`：POST 创建；GET `/:id`；POST `/:id/confirm`、`/:id/cancel`、`/:id/reconcile`。后三者只接受 `{}`，实体和动作来自不可变提议。状态为 pending/executing/succeeded/failed/unknown/cancelled/expired。错误包括400无效请求、403权限、404所有权/记录、409冲突、410过期、422不支持、503不可用。

WS 输入新增可选 request_id；设备终态事件为 device_result/control_proposal，预留 control_result。HTTP操作直接返回最新提议；不需要额外WS广播。现代前端新发送生成随机 ID，显式重发保留原 ID；断线不自动重放。旧客户端缺 ID 时服务端补 ID，不承诺跨连接去重。

控制记录位于 Console data_dir 下 `control/proposals.json`，原子替换，单服务进程使用。默认保留30天/10000条；活动和 unknown 不自动清理。恢复旧 executing 为 unknown，不再次 POST。清空该文件会破坏审计与幂等，不作为恢复手段。

## 配置与启用

`go-agent/config/agent.example.yaml` 提供关闭状态的示例。必须同时启用认证 Console 与 HA，再设置 `smarthome.control.enabled: true`。`targets: []` 时只允许查询，不宣称可写。

常规目标要求实际 entity_id、显示名称、实际负载房间、允许动作以及 `load_location_verified: true`。最多8个不重复别名，名称和别名不超过128字符且256 UTF-8字节。多路控制器的安装房间不能代替负载房间，配置修改后重启生效；旧提议策略版本不一致时拒绝执行。

```yaml
smarthome:
  control:
    enabled: false
    proposal_ttl: 120s
    readback_timeout: 10s
    readback_interval: 500ms
    retention: 720h
    max_records: 10000
    targets: []
```

先在现场核对一台低风险设备的映射，再填写允许清单并单独启用。普通成员保持只读。禁止 toggle、批量、定时及锁/门/窗帘/场景/脚本/空调等本轮未支持的动作。

明确授权的开关通道测试可在单个精确 `switch` 目标上设置 `switch_test_authorized: true`，同时保留 `load_location_verified: false`；该标记默认 false，不能用于 light/fan。名称应注明通道测试，区域仅表示登记区域，不能据此声称下游负载房间已验证。通配符、动作范围、确认和回读约束保持生效；改变此授权同样改变策略版本。

## 测试与审查证据

测试均使用临时目录与合成设备；实际模型评测例外，仅访问本机推理端点，不持有 HA 客户端。各组件先加入失败测试再实现：未注册端点404、历史附件丢失、无认证控制配置被接受、HA离线阻断普通聊天、策略字段边界不一致均被复现并修复。组件详细红绿过程记录在本地 `.superpowers/sdd/2026-10-09-console-ha-control-roadmap/`。

独立审查的问题已纳入修复：

1. 仅可写设备不能消除同名只读设备造成的歧义；对全部可见候选判断唯一性，再独立检查写权限。
2. 确认响应丢失后先 GET 提议；只有服务端也为 unknown 才做只读核对。服务端已成功、失败、待确认、执行中均直接恢复显示，不重发动作。
3. 配置名称/别名约束与模型候选约束对齐，避免启动后才发现配置不可解析。

4. 会话达到轮数上限时，先查原会话/请求的持久结果再换会话；原输入摘要不一致返回冲突。重放不新增历史、不调用模型、不写HA。真实WebSocket重连回归先复现再通过。

| 验证 | 结果 |
|---|---|
| `go test ./... -count=1 -timeout 180s` | 全包通过（包括smarthome/gateway/core/chain/consolefixture） |
| `go vet ./...`、`go build ./cmd/agentd` | 通过，二进制仅保存本地忽略目录 |
| `npm run typecheck`、`npm run build` | 通过；约521kB bundle提示仍存在 |
| Vitest | 126/126通过；两条旧搜索测试明确等待异步更新，新增延迟初始加载回归 |
| Chrome Playwright | 24/24通过；新增3条控制用例，包含四种宽度、取消、确认、历史、成员查询 |
| Python离线脚本 | 14/14通过 |
| 旧widget/JS语法 | 5/5通过，两个浏览器脚本语法检查通过 |
| Linux race/远端CI | 工程基线 `baf6846` 的[GitHub CI](https://github.com/LEooooooo12138/personalAIProject/actions/runs/37881767740) 全部通过，含 Linux race、前端与浏览器测试；本轮 switch 授权增量的远端结果另记 |

## 真实本机模型评测

模型 `gemma4:12b`，digest `4eb23ef187e2c5462566d6a1d3bbbc2f1346d0b4327cbb66d58fffbcc9b2b05c`。只使用50条合成中文样例，不发送真实设备名称或秘密，评测程序没有HA执行接口。

最初请求 max_tokens=512、temperature=0、未设置思考参数：总体14/50（28%），支持的查询/单设备指令4/20（20%），36条安全拒绝。诊断显示本机模型默认thinking=true，一条失败请求消耗512个token后以length结束，JSON不完整。相同样例仅增加 `reasoning_effort: none` 后，20token即返回完整有效JSON。该参数及布尔思考语义依据[Ollama官方兼容性文档](https://docs.ollama.com/api/openai-compatibility)，并用本机 `/api/show` 和实际请求验证。

最终仅设备意图请求使用 `reasoning_effort: none`，版本 `ha-intent-v2`；普通聊天模型配置、512token上限和严格验证规则保持原逻辑。结果：

- 支持的查询/单实体控制：**19/20（95%）**，达到本批唯一目标样例验收阈值。
- 全部样例：**44/50（88%）**；5条解析/安全拒绝，1条引用请求分类为chat而非预期clarify。
- 模型曾产生锁/窗帘及注入/伪造目标的无效控制候选，均被服务器验证拒绝；返回可执行的不安全控制为0。
- 本机本批热运行P50约606ms，P95约716ms；不代表并发、冷启动或所有家庭用语的性能。
- 本评测没有HA客户端，HA写入必为0；实际“提议零写、确认一次写”由独立模拟HA集成测试验证，二者不能混淆。

保留[最终50例](console-ha-model-evaluation-2026-10-09.json)、[初始失败基线](console-ha-model-evaluation-2026-10-09-baseline.json)、[默认思考诊断](console-ha-model-diagnostic-2026-10-09.json)、[关闭思考对照](console-ha-model-diagnostic-no-thinking-2026-10-09.json)。残余例包括锁状态查询安全拒绝和引用语句分类差异；不能宣称所有中文指令都已准确理解。

复测命令（明确选择本机模型才运行，默认CI跳过）：`HA_INTENT_LIVE=1 go test ./internal/chain -run '^TestHAIntentLiveEvaluation$' -count=1 -timeout 12m -v`，从go-agent目录执行；Windows以PowerShell环境变量方式设置。

## 计划调整与剩余事项

原计划 Task5 的 chain step 由独立意图解析器加 Console dispatch 实现，复用已有 inference.Client 与普通 RAG，减少对通用 ChainState 的侵入。Task1采用显式结构化配置，默认禁用且空清单；人工已核验负载显示名覆盖同实体的控制器目录名称。以上调整未扩大设备权限。

| 任务 | 状态与后续 |
|---|---|
| T0 基线/范围 | 工程基线已锁定；后续经用户授权，本地允许清单仅加入一个 switch 测试通道 |
| T1–T3 策略/持久化/执行回读 | 已实现并完成离线故障、并发和写次数验证 |
| T4 Console/历史 | 已实现；身份、CSRF、跨账号、篡改字段及重复确认验证 |
| T5 意图 | 工程及50例离线契约完成；本机支持查询/单实体样例19/20达95%，总体44/50，残余语义失败留档 |
| T6 前端 | 已实现；确认恢复、迟到响应、身份/会话切换、键盘及四种宽度验证 |
| T7 联调 | 全链路模拟及独立审查完成；以最终命令/远端CI实际结果为准 |
| T8 现场/LAN | 本地部署已完成，页面登录及真实开关验收待完成；物理负载映射、LAN/WAN、重启和跨 token 有效期验证仍未完成 |
| T9–T11 | 仍待独立实施，详见开发路线图；本轮未扩展范围 |

HA 回读只能证明观察到状态，不能证明所有物理结果必然由本次命令造成。跨网络不声称 exactly-once；对不确定结果不自动重试写入。


测试稳定性补充：第一次聚合运行旧Areas搜索测试在1秒内未得到目标DOM；隔离测试和延迟加载回归未复现产品覆盖问题。将即时mock搜索的React异步更新显式等待后，全量126例通过。另读取首轮归档CI发现设备详情导航未完成时断言匹配了区域列表两个标签；新增详情heading等待及局部定位。没有增大全局超时或跳过测试。
