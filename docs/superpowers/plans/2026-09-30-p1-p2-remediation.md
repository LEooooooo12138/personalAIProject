# P1/P2 审计问题修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 解决 2026-09-30 审查确认的全部 P1/P2 缺陷，在不控制真实设备的条件下完成可重复的代码与模拟契约验收。

**Architecture:** 保留 Go 原生 Ollama、Chain、双 Vault、SessionManager 和 HA Manager/Client/Store。按隐私、HA 数据与建议、RAG、会话四组实施；共享接口先确定，再让独立任务并行。修复必须保留人工确认、不可变建议、幂等执行、外部权限隔离和输出过滤。

**Tech Stack:** Go 1.26.4、Gin、gorilla/websocket、yaml.v3、Go testing/httptest、Node 内置 node:test/vm、Python unittest、Git Bash。

**Spec:** [CONSTITUTION](../specs/CONSTITUTION.md)、[Phase 1](../specs/2026-07-01-phase1-revised-design.md)、[Phase 3](../specs/2026-07-01-phase3-webchat-containerization-k3s-design.md)、[Phase 4](../specs/2026-07-01-phase4-smart-home-automation-design.md)、[本地 LLM/HA/Tuya 补充规划](../specs/2026-09-28-local-llm-ha-tuya-lan.md)。缺陷事实以本聊天 2026-09-30 审查为准，9/28 已修复项不得重新当成未修缺陷。

**Status:** Task 0–8 已完成，分组及整分支独立审查批准；最终 Go/race/vet/build、Node、Python 和 diff 检查通过。证据见 [验收记录](../../p1-p2-remediation-results-2026-09-30.md)。真实 HA/物理设备仍待现场验收。

## Global Constraints

- Windows 原生 PowerShell；沿用 go.mod 的 Go 1.26.4；本轮不增加第三方运行时依赖。
- I-4：External Channel 永远不能读取 `personal-vault`。
- I-5：External Channel 写入 `personal-vault` 仅限 `_memory/`，且通过 Memory Sedimentation。
- S-4：外部输出必须经过 Output Filter Chain；不能恢复过滤前逐 token 外发。
- S-5：智能家居操作必须具备人工确认环节，不允许 Agent 自主控制物理设备。
- REST/WS 已有字段不删除；协议扩展采用新增可选字段或新增管理端点。
- 保留现有用户修改、未跟踪文件和配置；不执行 `go-agent/tools/ha_*.py`，不输出秘密，不批量提交调试工具。
- 不创建真实 HA 自动化，不调用真实设备服务，不改变 HA/Tuya 配置，不重启现有服务；测试只使用临时目录与模拟服务。
- 历史接口与自动化条件以实际安装版本 HA 2026.7.2 为契约目标。
- 已有“有人在家”“日落至午夜”条件继续保留。缺少映射的建议明确不可执行；管理员补齐映射后产生新版本，再确认新版本。此为沿用现有语义的规划默认值，不猜测用户家庭实体。
- 每项任务先固定失败用例，再最小修复，再回归和审查；不得修改测试以掩盖原错误。

## Review Focus

1. 元数据格式损坏、BOM/CRLF、扩展字段：内部正文不能进入候选响应或模型上下文，合法页面仍可使用（Task 1）。
2. 历史分批、窗口重叠、重启和部分失败：不漏参数、不覆盖同秒数据、不把初态当操作、不隐藏采集失败（Task 2/3）。
3. 未知家庭时区、UTC 跨日和夏令时：不采用宿主机时区猜测家庭规则时间（Task 3）。
4. 旧建议被重新分析、条件绑定及重复确认：确认的语义不可变，旧 ID 不获得新权限（Task 4）。
5. Cookie 换身份、迟到的 404、多连接并发、长生成与取消：不读别人会话、不误清新会话、不丢最后一答、不遗留锁（Task 6/7）。

## 范围与完成映射

| 编号 | 本次缺陷 | 任务 |
|---|---|---|
| R1 / P1 | HA 历史请求缺 `filter_entity_id` | 2 |
| R2 / P1 | frontmatter 错误导致内部页面过滤失效 | 1 |
| R3 / 主流程阻断 | 分析建议没有 Automation，无法进入确认流程 | 4 |
| R4a / P2 | UTC 时间直接作为家庭时间 | 3 |
| R4b / P2 | 历史初态被当成开启事件 | 2、3 |
| R4c / P2 | 关联检测只按随机 map 顺序分析一个方向 | 3 |
| R5 / P2 | 外部 RAG 触发词只取启动时 personal | 5 |
| R6 / P2 | 浏览器身份更新后旧 SID 持续失败 | 6 |
| R7 / P2 | 同用户多消息/同 SID 多连接交错问答 | 7 |
| 审查附项 / P2 | 普通 Markdown 来源标题退化为 untitled | 1 |
| 验证缺口 | RAG 脚本只验答案非空、缺跨模块契约 | 8 |

企业微信业务协议核定、自然语言即时设备控制、Tuya LAN 迁移、Compose/Helm、人格功能属于独立待建设范围，不借本次缺陷修复隐式实现。实体当前在线、物理动作成功和断 WAN 运行仍是现场验收事项。

## 实施顺序与职责

Task 0 建立基线；Task 1 和 Task 2 可独立开展。Task 3 依赖 Task 2，Task 4 依赖 Task 3；Task 5 依赖 Task 1。Task 6/7 可在接口对齐后开展，但两者都改 websocket.go，必须串行合并。Task 8 汇总回归并复审所有缺陷。

推荐子代理按 Vault/Chain、HA、会话三组分工；一个文件同时只有一位实施者。core/app.go、gateway/http.go、CI 由集成人统一合并。每项任务是独立可审查变更；提交时仅纳入该任务文件，禁止 `git add .`。

### Task 0: 基线、隔离与失败用例台账

**Files:** 本计划；实施期间新增 `docs/p1-p2-remediation-results-2026-09-30.md` 记录证据。

**Interfaces:** 无产品接口变化。

- [x] 记录 HEAD、工作区修改清单和当前测试基线；保留原文件。当前已知 HEAD 为 `d9d208a`，执行前重新核对。
- [x] 先查看 chat 附件并复用可用 worktree；需要隔离时使用 Codex managed worktree 工具，从已核对的代码基线创建，分支采用 `codex/` 前缀。只复制本计划等明确需要的材料，不混入未提交调试文件。
- [x] 运行 Go tests/vet/build、JS syntax、Python offline fixtures，记录退出码。已有审查基线为 189 个顶层 Go Test、55.9% 整体语句覆盖率、9 个 Python fixture；它们不是本次修复后的结果。
- [x] 为 R1–R7 建立“失败用例→代码变更→通过证据”对应表。原临时探针转成仓库内正式测试；并发问题先用 barrier 实现确定复现。

### Task 1: 元数据错误时拒绝使用页面，并恢复文件名标题

**Files:** 修改 `go-agent/internal/vault/reader.go`、`embedding.go`，必要时适配 `memory/sedimentation.go`；测试放入 `vault/reader_test.go`、`vault/integrity_test.go`、`chain/privacy_test.go`、`memory/integrity_test.go`。

**Interfaces:** 保留 `ParsePage(data []byte) (*Page, error)`、Reader 和 Writer；新增 `ErrInvalidFrontmatter`。解析层缺标题时保留空值；Reader 在知道路径的位置回退到 basename。普通 Markdown 不因此被视为无效。

- [x] 增加失败测试：`TestInvalidFrontmatterIsRejected` 覆盖 `tags: internal`、合法内部标签加错误 created 类型、重复键、坏 YAML、未闭合 frontmatter；断言 `errors.Is(err, ErrInvalidFrontmatter)`。
- [x] 增加 `TestInvalidMetadataNeverReachesModel`：真实临时 agent Vault + 完整检索步骤 + mock 模型，断言坏页不在 Search/dense/Sources/模型输入，合法公开页仍命中。
- [x] 增加 BOM、CRLF、空 frontmatter、结束分隔符处于 EOF、未知扩展字段的兼容测试；增加无 frontmatter 的两个文件分别返回各自文件名标题的测试。
- [x] 运行 `go test ./internal/vault ./internal/chain ./internal/memory -run 'Frontmatter|InvalidMetadata|FilenameTitle' -count=1`，确认失败来自当前缺陷。
- [x] 实现严格解析及错误传播；Search 不返回坏页 snippet；dense/记忆读取仅跳过该可判别的格式错误，其他 I/O 错误和取消仍传播。最终上下文继续检查 IsInternalPage。
- [x] BM25 保留完整清单校验，不因过滤坏页造成每次查询重建；缓存中的坏页内容不能重新进入上下文。测试公开→内部/损坏→修复无需重启即生效。
- [x] 运行目标测试及三个包全量回归；审查 diff 并形成独立变更。

### Task 2: 修复 HA 历史请求、分批采集与初态来源

**Files:** 修改 `smarthome/client.go`、`collector.go`、`types.go`、`store.go`；新增 `smarthome/history_test.go`；扩展 `remediation_test.go`、`lifecycle_test.go`。

**Interfaces:** 保留单实体 `GetHistory(ctx, entityID, start, end)` 供现有调用；新增 `GetHistoryForEntities(ctx context.Context, entityIDs []string, start, end time.Time) ([]HistoryEntry, error)`。DeviceStore 新增 `LoadHistoryCheckpoint() (time.Time, error)` 与 `SaveHistoryWindow(start, end time.Time, entries []HistoryEntry) error`。空实体列表不得发出无过滤请求。HistoryEntry 新增 `ObservationKind string`，值为 initial/change；旧 JSON 缺字段视为 legacy，保留读取能力。

- [x] 先写严格模拟 HA：缺失/空 `filter_entity_id` 返回 400；测试 URL 编码、鉴权头、起止 UTC、实体去重、空列表和取消。
- [x] `TestCollectorBatchesAllEntities` 使用 166 个短 ID 模拟实体、每批最多 50 个，断言 4 批覆盖全部且无重复；另测编码后的 URL 不超过 6000 字节，长 ID 须进一步拆分。某批失败须返回可诊断错误，不能算历史采集成功。
- [x] `TestHistoryInitialStateIsPreserved` 锁定 HA 2026.7.2 的首条窗口初态行为：保留边界状态供时长计算，按查询窗口标识其来源，不算真实转换；实际请求从 checkpoint 前 60 秒开始；保留返回的重叠真实变化，不按 checkpoint 二次裁剪。HA 排除请求终点，终点事件由下一轮重叠补入；初态只在每实体首条时间等于实际请求起点时标记。
- [x] 运行 `go test ./internal/smarthome -run 'History|CollectorBatches' -count=1`，确认当前实现失败。
- [x] Collector 从 `GetStates` 的真实实体 ID 构造批次，不能用注册表数量代替运行状态目录；整轮聚合后一次保存。快照可独立更新，但历史失败必须向调用/状态层明确报告。
- [x] 使用本地 checkpoint 记录成功采集进度；首次从一个 poll interval 之前开始，恢复时按最多一小时的窗口补采。所有批次使用相同起止值；部分失败不推进进度，下次重试同窗口。
- [x] 查询额外重叠前 60 秒，按实体/时间/状态去重；这不是任意长 recorder 延迟的零丢失保证。真实事件采用 RFC3339Nano 保留精度；同时间冲突状态留待 Task 3 保守处理。
- [x] 按确定的时间窗口文件名原子保存，历史写入后才更新 checkpoint；增加同秒不同窗口、checkpoint 写失败后重放、重启续采测试，保证幂等且不会覆盖不同窗口。旧时间戳数组文件继续可读，不批量迁移。
- [x] 运行目标及 smarthome 全包测试；不调用真实 HA 服务。

### Task 3: 家庭时区、真实转换与确定的双向关联

**Files:** 修改 `smarthome/analyzer.go`、`manager.go`、`types.go`；可新增 `smarthome/history_events.go`、`analyzer_patterns_test.go`；更新 `core/config.go`、`core/app.go`、`config/agent.example.yaml` 的必要配置接线。

**Interfaces:** 新增 `AnalyzeInLocation(now time.Time, lookbackDays int, location *time.Location) (*DeviceReport, error)`；保留旧 `Analyze` 作为兼容入口，生产 Manager 必须传入已验证的家庭时区。HA `/api/config.time_zone` 始终是自动化时间权威来源；可选 `smarthome.time_zone` 仅校验期望值，不覆盖 HA。读取失败、缺失、无效或不一致时不发布新的时间建议，也不确认受影响的规则，不默用宿主机时区。

- [x] `TestPatternsUseHomeTimezone`：14 天 UTC 10:00 的真实 off→on 记录，在 Asia/Shanghai 得到 18:00；覆盖 UTC 跨日、本地日期命中率和非法时区。
- [x] `TestInitialStatesDoNotCreateTimePatterns`：常开灯 14 天每小时初态，模式数为 0，同时开启时长仍正确。连续 on 属性更新、unknown/unavailable 不能当 off→on。
- [x] `TestCorrelationsAreBidirectionalAndDeterministic`：门→灯与灯→门分别统计；不同 map 插入顺序重复运行得到相同方向、置信度、ID 和排序。同一对先后记录不能因遍历顺序漏报。
- [x] 运行 `go test ./internal/smarthome -run 'HomeTimezone|InitialStates|Correlations' -count=1`，观察预期失败。
- [x] 先按实体排序、去重并提取有证据的状态转换；初态只建立基线，普通 on→on 不计操作，缺少前态的首条记录不推断人为操作。时间、关联和汇总复用这一转换口径。
- [x] 时间模式使用 HA 家庭时区的小时和日期，窗口为过去 lookbackDays 个已结束的本地自然日，确保命中率分母与日期集合一致。每日调度新增纯函数 `nextAnalysisTime(now time.Time, hour int, location *time.Location) time.Time`，按日历递进处理夏令时；嵌入标准库 time/tzdata 确保精简环境可加载地区时区。
- [x] 建议记录生成时区，确认前核对 HA 当前时区；变更后旧时间建议需重新生成。legacy 记录可用于展示/时长汇总，不作为未经核验的动作证据；同时间冲突状态中断该点连续性，不猜顺序。
- [x] 新增 `StateTransition{EntityID, From, To string; At time.Time}` 和 `stateTransitions(entries []HistoryEntry) []StateTransition`；时间/关联共享该结果。保留 `0 < delta < 5分钟`、命中率 0.75、至少 7 个不同本地日期的条件；每个源转换最多计一个后续目标转换。
- [x] 对有序实体对分别计算两个方向，稳定排序输出；标题描述实际状态变化，不把任意设备推断为“门”。增加同日重复事件不足 7 天、长窗口、重叠采样、旧历史文件和取消边界回归，再运行全包测试。

### Task 4: 分析建议→补齐条件→确认的完整闭环

**Files:** 修改 `smarthome/types.go`、`analyzer.go`、`automation.go`、`manager_actions.go`、`store.go`；新增 `smarthome/suggestion_bindings.go`、`suggestion_bindings_test.go`、`gateway/smarthome_bindings.go`；gateway/http.go 仅接路由；扩展确认及网关测试。

**Interfaces:** 新增管理员端点 `POST /internal/smarthome/suggestions/:id/bindings`，不接受任意 Automation JSON。新增 `Manager.BindSuggestion(ctx context.Context, id string, bindings SuggestionBindings) (*RuleSuggestion, error)` 与 `DeviceStore.SaveBoundSuggestion(sourceID string, revision RuleSuggestion) (*RuleSuggestion, error)`。数据结构如下，均为现有字段之外的可选扩展：

```go
type PresenceBinding struct { EntityID string; State string }
type SuggestionBindings struct { Presence *PresenceBinding }
type SuggestionIntent struct {
    SchemaVersion int
    Kind string // time / correlation
    EntityID, RelatedID, At, TimeZone string
    RequiredConditions []string // presence_home / sunset_to_midnight
}
// RuleSuggestion 新增 Intent *SuggestionIntent、PresenceBinding *PresenceBinding、
// SourceSuggestionID string、SupersededBy string、MissingBindings []string。
// 新字段 JSON 使用 snake_case/omitempty；保留所有已有字段。
```

AutomationCondition 严格支持 state 和 sun 两种类型；新增 `After string`，sun 仅允许 after=sunset。state 的 entity/state 必填且禁止 After；sun 禁止 entity/state。稳定语义 ID 包含结构化意图版本、条件要求、时区和绑定结果，避免新旧统计语义混淆。

- [x] 建立从真实分析结果开始的失败测试，禁止以手工完整 executableSuggestion 代替：历史→Analyze→持久化建议→绑定→读取展示→Confirm→mock HA→规则归档。
- [x] 时间规则保留“有人在家”：没有管理员指定的聚合 occupancy/home 实体时，返回明确待映射原因且确认返回 422；绑定只补已有条件，不允许改动作目标、服务、时间或删除条件。
- [x] 关联规则保留“日落至午夜”：生成 HA 原生 `sun after:sunset` 条件，不能换成整个夜间为真的 below_horizon。仅对已支持 light/switch/fan 域生成明确 turn_on 动作，其他域返回具体不支持原因。
- [x] `TestBindingCreatesImmutableSuggestion` 断言新语义生成新 ID、原 ID 不变；相同绑定幂等，不同绑定对已替代源返回 409；confirmed/ignored/applying/failed 建议不能被改写。原 pending 的状态变为 superseded，确认旧 ID 返回 409。
- [x] `TestBindingNeverCallsHAWrite`、`TestVisitorCannotBindOrConfirm`、`TestUnknownOrUnavailableEntityRejectsBinding`：绑定阶段只允许只读验证。HTTP 未知字段/坏类型返回 400，缺失映射/不支持语义 422，不存在 404，状态冲突 409，上游不可用 502。
- [x] 运行 `go test ./internal/smarthome ./internal/gateway -run 'Binding|GeneratedSuggestion|Confirm' -count=1`，确认先失败。
- [x] 服务端从持久化结构化意图构造执行 payload 和展示文案；不解析自然语言字符串猜命令。完整可支持的关联建议直接包含 Automation；缺 presence 的时间建议携带 MissingBindings，必须补齐后再确认。验证实体存在、可用、服务与域一致；presence 映射由管理员明确表达为“有人在家”的可信聚合状态，不靠名称推断，也不能用随意添加另一种条件冒充。
- [x] 对旧版自然语言建议，保留读取/忽略；缺少可靠结构化意图时要求重新分析，不自动猜测转换。对已有完整结构化建议保持兼容，不能削弱原条件或未知字段校验。
- [x] 新 pending 版本、旧版本 superseded 及双向关联必须在同一次 suggestions.json 原子写完成；绑定和确认共用 actionMu，重复绑定返回既有新 ID，后续修改只能针对当前 pending 子版本。并发确认和重启继续复用稳定 config_key、状态机和原子写。
- [x] 增加绑定与确认竞争、存档失败、超时后重试、时区变化、重分析不改变已确认规则的测试。superseded 原语义再次被分析发现时保持原状态，不重新变成可执行 pending。
- [x] 通过上述端到端模拟链及现有确认回归，记录真实 HA 创建尚未执行。

### Task 5: 按目标 Vault 动态决定 RAG

**Files:** 修改 `vault/reader.go`、`chain/go_steps_decide.go`、`chain/chains.go`、`core/app.go`；新增或扩展 vault/chain/core 的实体路由与接线测试。

**Interfaces:** 在 chain 使用处定义 `TriggerEntityProvider { TriggerEntities(context.Context, string) ([]string, error) }`；同一个 FileReader 实现此窄接口。`NewEntityTriggerDecideStep(provider TriggerEntityProvider, logger *zap.Logger)` 和 ChainDeps 注入 provider，替换启动时静态实体列表。Reader 主接口不扩大。

- [x] `TestTriggersAreScopedAndDynamic` 使用同一 router：agent-only Orion 可触发；personal-only 实体在外部不触发；personal 目录不存在时 agent 查询仍成功。
- [x] 按新增→改名→删除→改 internal→改坏→修复的顺序修改临时页，断言下一请求即更新；增加保持 mtime/长度不变的修改、隐藏目录、系统页、符号链接越界和取消测试。
- [x] 运行 `go test ./internal/vault ./internal/chain ./internal/core -run 'Triggers|Entity|Dependency' -count=1`，确认失败。
- [x] 复用当前安全读文件、目录排除、ParsePage 和 IsInternalPage，每次决策仅扫描目标 Vault，返回去重排序结果；未知 Vault 或读取失败报错，不回退 personal。
- [x] 更新所有构造器和测试调用，移除启动时仅从 personal 取实体的接线。当前无共享实体缓存，因此本次不新增 TTL、Watcher 或第三套缓存。
- [x] 用完整 chain + 真实临时库 + mock inference 验证唯一事实确实进入模型上下文，另一个 Vault 的标记绝不进入；运行相关包回归。

### Task 6: 浏览器失效会话恢复与结构化错误

**Files:** 修改 `gateway/websocket.go`、`static/chat-widget.js`；扩展 `gateway/remediation_test.go`；新增 `tests/frontend/chat-widget.test.cjs`。

**Interfaces:** WS serverMessage 新增可选 `code`；`core.ErrSessionNotFound` 对应 `session_not_found`，容量/临时错误对应 `session_unavailable`。JS 新增内部 `resetExpiredSession(expectedSID)`，不改变 ChatWidget 公共 API。

- [x] Node 内置 node:test/vm 加载原脚本，mock DOM/fetch/WebSocket/localStorage。覆盖新 owner+旧 SID+404、WS session_not_found、收到新 session ID 后持久化。
- [x] 断言下一次发送的 SID 为空，并且用户旧输入不被自动重放；500、容量不足、生成失败保留 SID；旧历史请求晚到的 404 不能清除新 SID。
- [x] 运行 `node --test tests/frontend/chat-widget.test.cjs`，确认当前脚本复现错误。
- [x] 前后端按明确错误码恢复；保留 type/message 和 owner 校验。避免调用依赖未接线 sessionsList 的 newSession；恢复逻辑只管理已存在的会话状态。
- [x] 运行 JS 行为测试、语法检查和 gateway 测试，确认新身份仍不能恢复旧 owner 会话。

### Task 7: 同用户 FIFO 与跨连接会话事务

**Files:** 修改 `channel/manager.go`、`core/agent.go`、`core/session.go`、`gateway/websocket.go`；新增 `core/session_turn.go`、`session_turn_test.go`；扩展 manager/core/gateway 现有回归测试。

**Interfaces:** 不改变 Channel 或 `Manager.Run(ctx, handler)`。SessionManager 新增 `AcquireTurn(ctx context.Context, channelID, userID string) (release func(), err error)`，用于可取消的会话租约；实现引用回收和明确锁顺序。

- [x] `TestSameUserMessagesStayFIFO` 通过 channel barrier 阻塞 A，再投递同用户 B 和不同用户 C：B 不能进入模型，C 可完成；A 释放后 B 历史必须含 A/answerA，不出现 A/A。
- [x] `TestSharedWebSocketSessionSerializesTurns`：两个同 owner/SID 连接共用会话，模型及持久化问答顺序保持完整。`MaxRounds=1` 时先保存最后一答，再开始新会话。
- [x] `TestTurnCancellationReleasesWaiters`、`TestIdleScanSkipsInflightTurn` 覆盖取消、模型/发送失败、长生成超过 idle timeout；不用 sleep 控制主要顺序。
- [x] 运行 `go test ./internal/channel ./internal/core ./internal/gateway -run 'FIFO|SerializesTurns|Turn|Cancellation|RoundLimit' -count=1`，确认原缺陷失败。
- [x] Manager 在 merged 接收顺序中同步登记 `{ChannelID,UserID}` 队列，再启动 handler；同键 FIFO，不同键并发，取消后不启动待处理项，等待在途退出并回收队列。
- [x] Agent 在 GetOrCreate 前获取租约，覆盖历史快照、轮次旋转、推理、发送与 assistant 入库；WS 共用该租约，轮次更换 SID 时在公布新 SID 前取得新租约。
- [x] 推理/网络期间不持有 SessionManager 全局锁或 session 数据锁；idle scanner 与租约登记原子协调，执行中不结束会话，释放后正常过期。所有出口 defer 释放。
- [x] 保留取消等待、旧会话完成不删除替代会话、WS shutdown 等已有回归。测试队列/租约最终为空；单测通过后在支持 CGO 的 Linux CI 运行 race。

### Task 8: 跨模块验收、CI 与结果文档

**Files:** 修改 `tests/integration/phase1_5_full_chain.sh`、`test_scripts.py`、`.github/workflows/check.yml`、`go-agent/docs/rag-pipeline-spec.md`；新增/更新结果记录，链接本计划。

**Interfaces:** RAG 冒烟脚本显式接收 `RAG_TEST_QUERY`、`RAG_EXPECTED_SOURCE_PATH`、`RAG_EXPECTED_TEXT`；缺少数据时不能报告 RAG 验收通过。脚本不自动写用户真实 Vault。

- [x] 先增加离线 fixture：非空回答但无来源、来源错误、缺少唯一事实都必须非零退出；合法响应通过。HTTP JSON 用解析器构造，避免变量直接拼接破坏引号。
- [x] 增加 chat 路由及强制 wiki-query 两条真实 chain 集成测试；模型上下文和 sources 使用临时页唯一事实验证。将 Node 行为测试加入 CI，保留 Go race/coverage、vet、Linux build 和 Python fixture。
- [x] 每组修复完成后独立复审，最后由未实施对应代码的审查者核对 R1–R7、权限负向用例、绑定/确认幂等和会话取消；发现有效问题补用例后修复。
- [x] 在 go-agent 目录运行 `go test ./... -count=1 -timeout 120s`、`go vet ./...`、`go build ./cmd/agentd`。coverage 参数在 PowerShell 整段加引号，输出写入已忽略缓存目录。
- [x] 在仓库根运行 `node --check go-agent/static/chat-widget.js`、`node --test tests/frontend/*.test.cjs`、`python -m unittest discover -s tests/integration -p 'test_*.py' -v`、`git diff --check`。本机 Python 使用已发现的可用运行时；测试禁止真实网络依赖。
- [x] Linux/CGO 环境运行 `go test -race ./... -count=1`。若本机仍为 CGO_ENABLED=0，明确标为本机未验证，并读取实际 CI 结果后再宣称 race 通过。
- [x] 更新 RAG spec 中过时的缓存、overlap、预热描述和本次权限/实体路由规则；结果文档逐项记录红→绿证据、最终命令/退出码与现场验收边界。
- [x] 对照完成映射逐项签收，不用整体覆盖率代替缺陷测试。Go Agent 启动、真实 HA 凭据与物理控制的后续验收分别记录，不能因 mock 通过标记完成。

## 外部契约依据

- [HA 2026.7.2 历史路由：必需实体过滤及默认窗口初态](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/history/__init__.py)
- [HA 2026.7.2 Recorder 历史查询：严格排除请求终点](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/recorder/history/__init__.py)
- [HA 2026.7.2 历史状态模型：UTC 与初态时间](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/recorder/models/state.py)
- [HA 2026.7.2 Sun 条件：基于家庭当地日期的 after sunset](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/sun/condition.py)
- [HA 条件文档：after sunset 到午夜的语义](https://www.home-assistant.io/docs/scripts/conditions/)

## 本轮规划自检

- [x] 本次主报告全部 7 组问题及 3 个统计子问题都有任务和验收。
- [x] 明确生产代码与模拟验证、真实设备验收的边界。
- [x] 复用现有权限、存储、确认结构；没有放宽条件或引入自由执行工具。
- [x] 标注同文件合并顺序、接口新增点、旧数据和旧协议兼容要求。
- [x] 把已确认的小范围来源标题问题纳入同文件修复，未展开无关新功能。
- [x] 实施证据已写入验收记录，现场边界单独保留。
