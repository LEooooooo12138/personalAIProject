# 前端对话控制 HA 与后续补全 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 先让现有家庭前端实现受权限约束、人工确认、结果可核对的 HA 对话控制，再补本地可靠性和知识管理缺口。

**Architecture:** 复用 React Console、Go Chain/Router、Console身份、CatalogService和现有HA/OAuth客户端。模型仅产出受约束意图；新增独立ControlService/持久状态负责授权、提议、唯一执行和回读。微信等外部入口不作为依赖。

**Tech Stack:** Go 1.26.4、现有Gin/gorilla-websocket、React 19/TypeScript/Vite、Ollama、Go testing/httptest、Vitest、Playwright Chrome、Python unittest；不预设新增运行时依赖。

**Spec:** [前端对话控制HA设计](../specs/2026-10-09-console-ha-control-design.md)。当前缺口依据：[完整对比](../../spec-implementation-audit-2026-10-09.md)。

**Status（2026-10-09实施更新）:** 用户已批准继续规划并实现。M1即时控制工程链路已实现，完成离线联调与独立审查；真实模型/最终CI状态以[实施与验收记录](../../console-ha-control-results.md)为准。T8现场验证、T9–T11后续子项目未开始。默认管理员控制、成员只读，实际控制清单仍为空。下列是原始验收清单；未完成全部子条件的条目保留空框，不能据代码完成推断现场/模型/CI通过。

## Global Constraints

- 保留I-4/I-5双Vault边界、S-4外部输出过滤、S-5人工确认；Console聊天仍固定agent且不进入全局记忆。
- 所有LLM请求复用`inference.Client.Chat(context.Context,json.RawMessage)`，不从前端直接访问HA/Ollama。
- 继续同源Cookie/CSRF/Origin/no-store与账号撤销；前端不持HA token或管理Bearer。
- 单Go进程写入；初版单实体、允许清单内light/switch/fan的turn_on/turn_off，禁toggle/批量/高风险域。
- 使用当前CatalogService和真实entity_id；人工负载映射缺失时不得猜测，控制清单默认空。
- 提议默认120秒有效；回读10秒/500ms；存储默认保留30天/上限10000条，活动及unknown记录不可自动删除。
- HA写入不自动重放；不声称跨网络exactly-once，保障同提议至多一次主动发送，未知结果单独保留。
- REST/WS只增可选字段/端点，已有自动化建议流程保留；不为首版开放通用tools。
- 本机测试使用临时目录/mock服务；真实设备验收与代码测试分开。实施时先复用已归档分支的可用工作树，保留用户改动。

## Review Focus

1. 名称重复、多路负载未核验、目录过期：不选择错误设备（Task1/5）。
2. 确认前账号撤销、改密、策略变化、跨账号猜ID：没有HA写入（Task2/4）。
3. POST结果未知、进程退出、落盘失败和重复点击：不自动重放，不谎报成功（Task2/3/6）。
4. 模型输出伪造实体、提示注入、引用/否定/批量命令：不扩大执行范围（Task5）。
5. 历史刷新、跨标签身份切换、迟到响应：不恢复错误账号的有效确认卡（Task4/6）。

## 顺序、工期参考与验收门

| 里程碑 | 任务与依赖 | 可交付结果 | 参考投入 |
|---|---|---|---|
| M1a | T0→T1→T2→T3→T4 | 确定性后端控制服务及安全端点 | 3–5工程日 |
| M1b | T5依赖T1/T2，T6依赖T4/T5，T7整合 | 前端聊天→提议→确认→模拟动作→回读→历史 | 3–5工程日 |
| M2 | T8现场；T9本地可靠性 | 单设备验收、运行/恢复方案 | 2–4工程日及独立观察窗口 |
| M3 | T10知识/配置；T11余项分项目 | 来源一致性和原始目标补全 | 逐项估算，不给整体虚假承诺 |

工期是单工程师量级参考，不是交付承诺；实体映射、设备LAN兼容和现场配合可能决定总周期。M1不等待微信、容器化或Qdrant。

## Task 0：锁定已归档基线及确认范围

**Files:** 本计划、spec、实施时新建`docs/console-ha-control-results.md`。
**Interfaces:** 无产品接口变化。

- [ ] 记录当前branch/HEAD与dirty清单，确认读取最新Console代码而非旧main；从[发布记录](../../github-publication-2026-10-09.md)找到归档基线。
- [ ] 核对首版角色选择、首批控制实体/负载已验证映射；没有映射先只用临时fixture，不以文件名猜业务。
- [ ] 重跑审计报告第6节命令；记录失败和环境条件。创建结果台账，每任务保存红→绿证据，不提交秘密/原始设备清单。
- [ ] 对照spec冻结新增DTO与状态名后开工，阶段变更先更新文档。产物：可追溯基线和范围。

## Task 1：控制策略与可信目标解析

**Files:** 新建`go-agent/internal/smarthome/control_types.go`、`control_policy.go`、`control_policy_test.go`；修改`catalog.go`提供安全内部解析；新建`core/control_config.go`和测试，修改`core/config.go`及`config/agent.example.yaml`。
**Interfaces（均为新增）:** `ControlActor{UserID,SessionID string}`；`ControlTarget{EntityID,Name,AreaName string; AllowedActions []string; LoadLocationVerified bool}`；`ControlPolicy{Revision string; Targets []ControlTarget}`；`ResolveControlTargets(ctx context.Context, query string) ([]ControlTarget,error)`；`ValidateControlTarget(ctx context.Context, actor ControlActor, entityID,action string) (ControlTarget,error)`。角色授权由调用边界核验，service仍核验actor归属/目标策略。

查询独立使用`QueryTarget{EntityID,Name,AreaName,Domain string}`和`ResolveQueryTargets(ctx context.Context,query string)([]QueryTarget,error)`，从全量安全目录提供候选；不受ControlPolicy、允许动作域或负载核验限制。

- [ ] 先写表驱动`TestControlPolicyRejectsUnknownAmbiguousAndUnverified`：空映射/重名/禁用/unknown/unavailable/虚构ID/不支持域/未核验负载全部拒绝；精确且唯一的可信映射返回单目标；HA写计数始终0。
- [ ] `TestEmptyControlPolicyStillAllowsSensorQuery`：空写清单下成员仍可查询温湿度/门磁状态及时间；unknown/unavailable以只读未知状态展示，不猜值；0次HA写。
- [ ] `TestControlConfigDefaultsAndValidation`固定120s/10s/500ms/30d/10000默认值及空清单，拒绝重复entity、非法动作、过长名称和失配域；不接受浏览器控制的角色/策略。
- [ ] Run `go test ./internal/smarthome ./internal/core -run 'ControlPolicy|ControlConfig' -count=1`，先FAIL再最小实现并PASS。复用目录关系而非重建catalog。
- [ ] 加stale拒绝、候选上限20、名称作为数据不作为指令的测试；精确匹配失败返回澄清，不用相似度最高项替用户决定。
- [ ] 独立提交`feat: define HA control policy and trusted targets`。

## Task 2：持久提议及状态机

**Files:** 新建`smarthome/control.go`、`control_store.go`、`control_store_test.go`；修改Task1类型。
**Interfaces:** `ControlService.Propose(ctx context.Context, actor ControlActor, requestID string, intent ControlIntent) (*ProposalOutcome,error)`；`ProposalOutcome{Kind string; Proposal *ControlProposal; DeviceResult *DeviceQueryResult}`，kind=`proposal/already_satisfied`，对应字段二选一。`Get(ctx,actor,id)`、`Cancel(ctx,actor,id)`均返回`(*ControlProposal,error)`；`ControlStore.Update(ctx context.Context,id string,expectedStatus string,next ControlProposal) error`执行原子比较更新。使用构造注入的clock以测试有效期。

- [ ] `TestProposalIsImmutableOwnedAndExpiring`断言随机ID、120秒到期、不可变目标动作、另一owner统一not-found；同owner/sessionID/requestID+相同意图返回同ID，不同意图409，防重连创建多份；跨会话相同requestID相互独立。
- [ ] `TestControlStoreCrashRecoveryAndBounds`覆盖executing恢复为unknown、损坏文件失败关闭、原子落盘失败不前移状态、30天清理只删终态、10000上限不删活动记录。
- [ ] `TestProposalNeverExecutes`断言生成/读取/取消/过期/同requestID重试全为0次HA POST；已达目标用只读结果而非可确认提议。
- [ ] Run `go test ./internal/smarthome -run 'Proposal|ControlStore' -count=1`，确认失败后实现。存储放配置Console数据目录的`control/`，不放Vault；清理的最终路径必须限定根目录。
- [ ] 回归重启与owner隔离后提交`feat: persist immutable HA control proposals`。

## Task 3：单次执行、回读与未知结果核对

**Files:** 新建`smarthome/control_execute.go`、`control_execute_test.go`；最小修改`client.go`/`client_errors.go`以提供需要的安全错误分类，复用现有方法不变更其契约。
**Interfaces:** 依赖窄接口`ControlHAClient {GetState(context.Context,string)(*EntityState,error); CallService(context.Context,string,string,map[string]interface{}) error}`。新增`Confirm(ctx context.Context,actor ControlActor,id string) (*ControlProposal,error)`、`Reconcile(ctx context.Context,actor ControlActor,id string) (*ControlProposal,error)`。执行前身份复核通过构造注入的`AuthorizeControl func(context.Context,ControlActor) error`完成，不信任LLM。

- [ ] `TestConfirmPersistsBeforeWriteAndSendsOnce`：未确认0写；并发Confirm仅1写；写executing失败0写；按实体锁序列化，第二个旧before提议409。
- [ ] `TestControlReadbackSeparatesAcceptedFromSucceeded`：HA200但实体不变不得succeeded；期望状态+观察时间才成功；10秒窗口超时unknown，fake clock推进。
- [ ] `TestUnknownWriteNeverReplays`：超时/401/连接中断/进程恢复/成功后存盘失败均不再次POST；Reconcile只GET，显示当前观察而不虚构因果；Confirm旧unknown为409。
- [ ] `TestRevokedOrChangedProposalNeverWrites`：角色撤销、账号禁用/改密、过期、实体移除、策略revision改变、执行前状态变化全部0写；取消待执行有效，取消在途返回明确冲突。
- [ ] Run `go test ./internal/smarthome -run 'Confirm|ControlReadback|UnknownWrite|RevokedOrChanged' -count=1`，红→绿；保留OAuth写入不重试回归。提交`feat: confirm HA actions with bounded state verification`。

## Task 4：Console控制端点与持久历史契约

**Files:** 新建`gateway/console_control.go`、`console_control_test.go`；修改`console_routes.go`、`console_chat.go`、`websocket.go`、`core/app.go`、`core/session.go`、`core/session_store.go`；新增`core/control_attachment_test.go`。
**Interfaces:** spec §4五类端点；`setupConsoleControlRoutes(*gin.RouterGroup)`；可选`core.Message.Attachments []MessageAttachment`，`MessageAttachment{Kind,ProposalID string}`；WS扩展`device_result/control_proposal/control_result`并带session_id/request_id，设备DTO不含credentials/raw attributes。服务端从有效principal生成actor并核对session owner。

WS输入新增可选request_id：新前端随机生成，首次收到服务端session_id即保存；明确重发复用session_id/request_id。旧客户端由服务端补ID并回传，不保证缺ID的跨连接重发幂等。新增测试断连后显式重发只有一个提议、不同session相同request_id不串卡。

- [ ] 先写HTTP契约测试：Origin/CSRF、成员403、跨owner404、未知字段400、过期410、冲突409；Confirm传入目标/动作等未知字段拒绝；撤销与执行并发有确定barrier。
- [ ] `TestControlHistoryResolvesCurrentProposalState`覆盖旧纯文本JSON、附件保存重启、过期卡不可确认、跨账号附件不返回、记录清理后显示不可用。
- [ ] Run `go test ./internal/gateway ./internal/core -run 'ConsoleControl|ControlHistory|ControlAttachment' -count=1`，红→绿；注册capability `devices:query`与按角色授予`devices:control`，未配置目标/服务不可用时不宣称可写。
- [ ] 保持旧WS和REST行为，控制错误不能触发家庭账号错误登出；unknown返回状态DTO而非泛化“重试动作”。
- [ ] 回归Console/Session测试后提交`feat: expose owned HA control workflows in console`。

## Task 5：本地模型受约束意图与只读查询

**Files:** 新建`chain/control_steps.go`、`control_intent_test.go`；修改`chain/chains.go`、`chain/types.go`、`core/app.go`、`gateway/websocket.go`的依赖注入与dispatch；新增`tests/fixtures/ha-intents.json`（仅合成设备）。
**Interfaces:** 新增`IntentCandidates{Query []QueryTarget; Control []ControlTarget}`与`ControlIntentParser.Parse(ctx context.Context,query string,candidates IntentCandidates) (ControlIntent,error)`；chain使用处定义窄`ControlProposer`与`DeviceQuerier`接口，依赖Task2 Propose，不可见Confirm/CallService。`DeviceQueryResult{EntityID,Name,State string; ObservedAt time.Time}`。chain输出可选结构附件，不破坏FinalAnswer/Sources。

- [ ] `TestChatControlOnlyProposes`使用真实chain+mock inference：查询走GET并带时间；“打开客厅灯”只Propose且0POST；普通知识聊天仍保留RAG sources。
- [ ] `TestIntentRejectsInjectionAndUnsupportedActions`：坏JSON、未知字段、虚构entity、数据中嵌指令、否定/引用、toggle/多目标/高风险域全部澄清或拒绝，0写；20候选上限明确。
- [ ] Run `go test ./internal/chain ./internal/gateway -run 'ChatControl|IntentRejects' -count=1`，红→绿；内部请求使用严格response_format，先用实际本机兼容端点验证支持，不假定模型tag即工具可靠。
- [ ] 合成50条中文评测保存模型/prompt/schema版本；唯一目标准确率≥95%，误写/越权/未确认写=0。在线模型评测不加入离线必过CI，输出失败也须留档。
- [ ] 提交`feat: route console chat through constrained HA intents`。

## Task 6：前端控制卡、澄清和恢复

**Files:** 新建`web/src/chat/ControlCard.tsx`、`ControlCard.test.tsx`、`control-types.ts`；修改`pages/ChatPage.tsx`、`chat/chat-state.ts`及测试、`api.ts`、`consoleActions.ts`；沿用现有身份协调器。
**Interfaces:** 消费Task4 WS及REST DTO；`ControlCard({proposal,onConfirm,onCancel,onReconcile})`回调只传ID。聊天历史附件引用通过GET重取，不从旧文本复建待执行命令。

- [ ] 先写Vitest：卡片显示设备/房间/动作/观察时间/有效期；连点仅1请求；成员无确认；cancel/expired/unknown按钮语义正确；在途取消不显示“已撤回”。
- [ ] 测identity epoch、切会话/账号、迟到WS/HTTP、刷新历史、404已清理卡，断连不自动重发；普通文本与SafeMarkdown回归。
- [ ] Run `npm.cmd test -- ControlCard chat-state ChatPage`，红→绿；确认按钮为独立操作，不将用户普通文字“确认”当写授权。
- [ ] Run `npm.cmd run typecheck`和全前端测试；键盘焦点/ARIA/320px长名称测试；提交`feat: add chat action confirmation and result cards`。

## Task 7：完整模拟链、回归与独立审查

**Files:** 修改`go-agent/tests/consolefixture/`提供严格mockHA状态计数；新建`web/e2e/control.spec.ts`；扩展`.github/workflows/check.yml`仅在现有脚本未覆盖时；更新结果文档。

- [ ] 浏览器走真实Go handlers/真实意图chain，fixture仅mock模型与HA：查询→提议0写→明确确认1写→回读→刷新历史状态；不能用前端拦截代替后端。
- [ ] 覆盖重名澄清、旧卡过期、member、双击、换账号、撤销、POST超时且HA已执行、回读不变、进程恢复unknown；结果和调用次数同时断言。
- [ ] 先build再E2E，完成后再最终Go build，避免构建扫描与浏览器清理同时进行。全跑Go/vet/build、96+新增Vitest、21+新增E2E、14 Python和5 widget及语法检查。
- [ ] Linux/CGO跑race；读取本次远端CI实际结果。独立审查spec覆盖、写次数、权限和恢复；有效问题补用例修复，不只看覆盖率。
- [ ] 结果文档写清模拟与现场边界并提交`test: verify end-to-end console HA control lifecycle`。M1在此签收。

## Task 8：单设备现场及LAN验证（M2）

**Files:** 新建`docs/console-ha-live-acceptance.md`，只放非秘密汇总；现场清单和凭据保留受保护本地文件。

- [ ] 选一台用户明确指定的低风险灯/插座，人在现场核验entity→实际负载、房间、允许动作；确认使用的集成路径，不能从“Tuya/Smart Life可用”推断LAN。
- [ ] 前端提议必须0写；用户卡片确认后状态回读及现场观察分别记录。开/关共至少10次，只在现场同意的窗口操作；失败不自动重试。
- [ ] 独立验证WAN断开/恢复、HA/设备重启和至少跨两个实际token有效期；记录依赖云的设备，不静默回退云控制。
- [ ] 记录成功/失败/未知及复核方法，必要时只撤销控制清单权限，不自动改回物理状态。M2现场通过需真实证据。

## Task 9：补本地运行可靠性（独立子项目）

**Files:** 修改`core/app.go`；新增平台信号适配与`cmd/console-recovery/`；新增`docs/local-operations.md`及恢复测试。复用`console.Store.RevokeAllSessions`和现有管理恢复逻辑，避免另造认证库写法。

- [ ] 先核实旧计划Task8未交付项：SIGTERM/Windows退出、自启、日志轮转/容量、恢复CLI、10账号3并发、备份恢复。
- [ ] 独立设计服务退出/在途写unknown和单进程互斥；离线CLI拒绝服务正在写入的目录，恢复默认撤销旧会话。
- [ ] 用临时目录失败用例验证备份快照一致、恢复后账号/本人历史/控制记录完整、旧executing不重放、旧登录失效；在支持环境演练停机恢复。
- [ ] 10账号3并发记录响应与资源上限、长模型取消和长时间续期；性能目标由基线明确，不用测试数量代替负载验收。
- [ ] Windows隐藏后台自启方案、日志大小和保留策略参数化，Linux单进程方案另列；只在授权部署窗口变更现有服务。

## Task 10：知识与配置一致性（独立子项目）

**Files:** `memory/summarizer.go`、`core/config.go/app.go`、`chain/llm_steps.go`、`vault_steps.go`、`vault/reader.go`及相关测试；新增来源manifest/恢复模块前先写子spec。

- [ ] 先用非默认模型测试摘要走配置；logging/search_timeout或接线或明确弃用；source_title进入蒸馏和来源记录，断言不会从不可信标题构造越界路径。
- [ ] 定义content-hash、来源ID、生成页面关系、同来源重导入、人工修改冲突和失败恢复语义；确认后分步实现manifest及原子提交/补偿日志，禁止盲目覆盖用户页面。
- [ ] 测写页成功索引失败重试不重复、进程中断恢复、公开→内部失效；保留Console私人回答不进公共历史/记忆。
- [ ] 测超长段落上限、embedding维度变化诊断、真实语料召回率；不以更换Qdrant代替当前缺陷修复。

## Task 11：剩余规格分类排期

| 项目 | 执行前条件/验收 |
|---|---|
| 更多设备动作/成员写授权 | M1/M2通过；每域参数schema、能力校验、授权及现场负载单独测试。 |
| 规则停用/恢复/编辑 | 独立不可变版本、确认、HA实际生命周期与归档一致性；ignore不可冒充disable。 |
| 私人知识发布/成员长期记忆 | owner-aware权限和人工脱敏方案先审；不得恢复全局混合沉淀。 |
| 通用本地文件copy | 允许根目录、符号链接、覆盖冲突、取消与日志测试；满足原终极目标但不阻塞HA。 |
| Wiki维护、人格、源追踪全链 | 按B08–B14拆子spec；每项明确用户可见结果，复用现有chain。 |
| 单副本Compose/多架构 | Go静态前端+Ollama+既有HA网络/卷/备份；去掉旧Python/sidecar必选项，跨平台启动验证。 |
| 云/域名/k3s多副本 | 单机稳定后再设计持久状态与锁/调度；不得原样启用旧HPA。 |
| 微信等外部渠道、云模型、Qdrant/Syncthing | 暂缓，后续复用同一权限和ControlService；不得新增绕过确认的通道。 |

## 计划自检

- [x] 对比矩阵B/F/H/O及旧R1–R7都有已完成、后续任务或明确暂缓位置。
- [x] 新HA主线不重做账号、目录、知识和自动化建议；即时动作使用独立状态。
- [x] 核心任务给出文件、接口、失败情形、验证命令及独立提交边界。
- [x] 模型不能确认；所有确认在服务端复核身份，unknown不自动重放。
- [x] 新增控制能力、模拟验证、真实物理/LAN验收分别签收。
- [ ] 实施时补齐每项红→绿、审查、现场与CI证据后再勾任务完成。
