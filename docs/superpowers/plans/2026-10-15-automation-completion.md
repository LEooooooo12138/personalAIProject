# 自动化发现与运行诊断 Implementation Plan

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按“发现建议 → 审阅与安装核对 → 运行诊断 → 明确管理操作”的顺序补齐自动化闭环，使数据不足、结果未知和真实故障都可以解释。

**Architecture:** 扩展现有 smarthome Manager、DeviceStore、Collector、确定性 Analyzer 和 Console 权限/确认流程。分析任务、证据、安装操作、外部观察与人工管理分别记录；所有 HA 写入仍由独立用户确认触发，诊断只读。

**Tech Stack:** 当前 Go 模块、原子 JSON 文件、现有 HA REST/WS OAuth 客户端、React/TypeScript、Vitest、Playwright；不新增数据库、模型分析服务或通用 HA 代理。

**Spec:** [自动化发现、审阅与运行诊断设计](../specs/2026-10-15-automation-discovery-and-diagnostics-design.md)。

状态：2026-10-15 实现计划，已获同意并执行中；完成情况与测试证据见实施记录（本地记录未公开），未安装真实规则。

## Global Constraints

- 源码根目录 `R=仓库根目录`；下列文件相对 R。记录时使用 HEAD `9c2a95f9` 加当时的工作区修改；后续执行前需核对实际代码状态。
- 执行前核对 AGENTS.md、Git 状态和工作树；保留原有修改。隔离按 using-git-worktrees 技能处理完整基线。任务提交先审查 staged diff，只选本任务文件/差异，禁止整体暂存既有改动。
- 保持现有14天请求窗口、至少7命中日且命中率75%的时间模式算法；不降低门槛制造建议。14天分母下至少11命中日。关联模式单独解释分母。
- 以已确认的家庭映射为准；不从名称猜 presence、负载或自动化归属。版本不可变，追加证据不修改已确认意图。
- 复用 Cookie/Origin/CSRF、会话撤销、身份世代、成员共享和独立确认；0次未授权 HA 写入是硬验收条件。
- 结果不确定进入 unknown，不自动重放。配置写入、运行时加载、触发、动作完成、物理负载成功是不同事实。
- 页面继续使用组件库和语义 SVG；正文16px、次要文字14px、交互目标44px；文字/图标共同表达状态。窄屏、键盘、读屏及“模拟预览”标记纳入验收。
- 隔离 fixture 与实际服务使用不同端口，具体端口以运行配置为准。真实规则安装/启停/删除和现场设备验收不由模拟测试替代。

## Review Focus

1. 空历史、旧文件缺收据、窗口重叠或损坏：不能把空事件认定完整覆盖，也不能重复累计天数（A1、A2）。
2. HTTP 已返回 run_id、随后会话撤销或服务停止：任务不能依赖已结束请求 context，也不能脱离撤销与生命周期（A3）。
3. HA 已写入但响应丢失、reload未完成、归档失败：保留独立阶段事实，核对不能重发写请求（A6、A7）。
4. 外部规则重名、映射不全、trace无权限或保留过期：不得认领所有权或显示绿色“正常”（A6、A8、A9）。
5. 成员能看某规则但不能看冲突对手、操作确认前规则或身份变化：证据不越权，过期确认不得写入（A10、A11）。

## 文件分工和依赖

所有后端新文件置于 `go-agent/internal/smarthome/`：`history_coverage.go` 管覆盖事实，`backfill.go` 管补采，`analysis_runs.go` 管任务，`suggestion_evidence.go` 管追加证据，`suggestion_revisions.go` 管受限版本，`automation_observation.go` 管 HA 配置/映射读取，`install_operations.go` 管安装，`diagnostic_sources.go` 管运行来源，`automation_diagnostics.go` 管纯诊断规则，`automation_management.go` 管人工管理。不拆改无关包。

依赖：A1→A2→A3→A4 为一期；A5→A6→A7 为二期；A8→A9→A10 为三期；A11→A12 为四期。A2先实现共用任务生命周期，A3复用；同一 Manager/Store/页面的修改顺序合并。每期可单独验收，不等待四期全部完成才展示成果。

## 公共数据合同（以下均为新增）

持久化对象统一 `schema_version:1`、服务器生成随机 ID、UTC 时间、原子替换；家庭时区单独保存。不得把 cookie/token 或原始 trace 变量写入这些对象。旧文件按原合同读取，缺字段表示未知，不推测补值。

| 类型 | 确定字段/语义 |
|---|---|
| `TimeWindow` | `Start,End time.Time`，半开区间 `[start,end)`。 |
| `HistoryQuery` | `EntityIDs []string; Window TimeWindow`，显式实体范围。 |
| `HistoryReceipt` | 窗口、请求实体集合、成功实体集合、完成时间、来源实时/补采、错误码；请求成功不证明 recorder 完整保留。 |
| `HistoryReadResult` | `Entries []HistoryEntry; Coverage HistoryCoverage`。Coverage含 `RequestedUnionDuration time.Duration; CorruptFiles int`、逐实体质量、缺口和保留能力；未知字段不填0冒充事实。 |
| `JobScope` | `Context context.Context; Release func(); OwnerID string`。Manager消费一次scope并负责所有路径release；不持久化Context/Release。 |
| `AnalysisRun` | ID/status、algorithm_version、requested_window、home_timezone、起止时间、coverage、各模式 outcome、counts、error_code。status=`queued/running/completed/failed/cancelled`；outcome=`matched/no_pattern/insufficient_data`。 |
| `SuggestionEvidence` | ID/runID、窗口、algorithm_version、现有 SampleSize/PeriodDays、HitDays/EligibleDays可选指针、分母类型、排除计数/原因、本机事件引用。不能把 EligibleDays替换既有算法分母。 |
| `BackfillRun` | ID/status、目标窗口、独立游标、完成批次、缺口、能力状态及错误码；不复用实时checkpoint/pending文件。 |
| `InstallOperation` | ID、suggestionID/version、configKey、expectedDigest、phase、status、独立外部事实及本地归档结果。status=`applying/confirmed/failed/unknown`。 |
| `ManagedAutomation` | ID、来源suggestion/version、configKey、HA entityID、expectedDigest、映射证据、最近观察；configKey不等于entityID。 |
| `SourceStatus` | `available/unsupported/forbidden/retention_gap/unavailable`；非 available不能捏造空结果。 |
| `AutomationRef` | `Kind string; ID string`，Kind=`managed/external/unverified`；外部输入只能选择服务端已读取的automation实体，不能传任意URL/path。 |
| `DiagnosticSnapshot` | ID/ref、检查时间/窗口、state/config/trace/activity来源状态、checks、summary。每个check=`pass/finding/unknown`，含category/severity/reason/evidenceRefs。 |
| `ManagementOperation` | ID、规则ID、动作、expectedDigest、确认到期时间、状态、观察与备份引用；独立于即时设备proposal。 |

诊断 summary固定为 `attention/partial/pass/unknown`：有finding为attention；无finding且pass/unknown混合为partial；全部适用检查pass才pass；全部unknown或没有适用检查为unknown。文案分别为“需关注”“部分无法核对”“已检查项目正常”“无法判断”。建议分析按模式保存结果，任一matched时总体matched，否则任一模式不足时总体insufficient_data，全部满足检测前提但无匹配才no_pattern。

## Task 1: A1 历史覆盖收据与可解释读取

**Files:** Create `go-agent/internal/smarthome/history_coverage.go`, `history_coverage_test.go`; Modify `history_store.go`, `store.go`, `collector.go`（同包）。

**Interfaces:** 复用 `(*DeviceStore).SaveHistoryWindow(start,end time.Time,entries []HistoryEntry) error` 和去重；新增 `ReadHistory(ctx context.Context,q HistoryQuery)(HistoryReadResult,error)`。抽出不推进checkpoint的内部窗口提交，保存窗口内容和收据同一版本；实时包装完成后才推进原checkpoint。

- [ ] 写 `TestHistoryCoverageDistinguishesEmptyUnknownAndCorrupt`、`TestHistoryCoverageUnionAndHalfOpenEnd`，断言：
  ```go
  if coverage.RequestedUnionDuration != 24*time.Hour { t.Fatal("overlap counted twice") }
  if legacyEntityRetention != "unknown" || coverage.CorruptFiles != 1 { t.Fatal("quality facts lost") }
  if slices.Contains(includedEventIDs, eventExactlyAtEnd) { t.Fatal("end must be exclusive") }
  ```
  场景含成功请求零事件、部分实体失败、重叠窗口、相同起止但不同实体批次、旧文件。收据缺失只能说明未知；部分批次不能覆盖已存在的完整数据。
- [ ] R/go-agent运行 `go test ./internal/smarthome -run 'TestHistoryCoverage' -count=1`；预期新接口缺失/旧读取静默跳过错误。
- [ ] 实现覆盖读取、唯一批次收据和窗口安全合并；明确记录请求成功、有效状态和真实变化，不能互相代替。旧 `GetHistoryRange` 保留兼容，Analyzer改用明确半开新读取。
- [ ] 同命令通过，再运行 `go test ./internal/smarthome -run 'Test(History|Collector|InitialStates|Transitions)' -count=1`。
- [ ] 提交本任务差异 `feat: record history coverage and quality`。

## Task 2: A2 有界历史补采

**Files:** Create `go-agent/internal/smarthome/backfill.go`, `backfill_test.go`; Modify `collector.go`, `manager.go`, `history_store.go`, `history_coverage.go`。

**Interfaces:** 新增Manager方法 `StartBackfill(scope JobScope,start,end time.Time)(BackfillRun,error)`、`GetBackfillRun(id string)(BackfillRun,error)`、`CancelBackfill(id string) error`。从现有Collector抽出 `(*Collector).fetchHistoryWindow(ctx context.Context,ids []string,window TimeWindow)([]HistoryEntry,HistoryReceipt,error)`，实时与补采共同使用。 补充经复现确认的发布隔离：`HistoryReceipt.AcquisitionScope` 为仅补采允许的可选稳定任务ID（小写32位hex），作用域与窗口共同隔离暂存文件；新增 `CommitScopedHistoryWindow(scope,start,end)`，保留原实时/无作用域API。成功的实时、补采或另一补采任务不得发布其他尚未完成范围的数据；恢复仅复用本任务作用域的收据。

- [ ] 写 `TestBackfillDoesNotMoveLiveCheckpoint`、`TestBackfillYieldsAndResumes`、`TestBackfillUnknownRetentionStaysUnknown`；断言：
  ```go
  if !liveCheckpointBefore.Equal(liveCheckpointAfter) { t.Fatal("backfill moved live checkpoint") }
  if largestBatch > 50 || haWrites != 0 { t.Fatal("backfill exceeded read boundary") }
  if emptyOldWindowRetention != "unknown" { t.Fatal("empty history overclaimed retention") }
  ```
  覆盖取消/重启、批次中断、重复实体、跨DST、晚于now或超过14天范围拒绝。测试证明实时采集可在补采窗口之间取得调度。
- [ ] 运行 `go test ./internal/smarthome -run 'TestBackfill' -count=1`；预期缺任务实现。
- [ ] 保留现有1小时窗口、1分钟重叠、每批≤50实体和URL长度限制；独立pending/游标，窗口间释放采集调度权。默认最近14天，边界为实际时刻；同一Manager最多一个补采。先读取可用保留能力；若无可靠接口，明确unknown，允许有界读取但不声称历史完整。在manager.go实现公共JobScope生命周期：合并服务停止和授权context，生命周期锁内检查停止并登记workers，完成或受理失败均release一次；A3复用该实现。
- [ ] 同测试通过；模拟进程中断后只允许用户显式恢复，依据已提交收据去重，不能回退实时游标。
- [ ] 提交 `feat: add bounded resumable history backfill`。

## Task 3: A3 持久化分析任务和证据贯通

**Files:** Create `go-agent/internal/smarthome/analysis_runs.go`, `analysis_runs_test.go`, `suggestion_evidence.go`, `suggestion_evidence_test.go`; Modify `manager.go`, `analyzer.go`, `types.go`, `store.go`。

**Interfaces:** 新增 `StartAnalysis(scope JobScope,days int)(AnalysisRun,error)`、`GetAnalysisRun(id string)(AnalysisRun,error)`、`CancelAnalysis(id string)error`、`AppendSuggestionEvidence(id string,evidence SuggestionEvidence)error`；新增 Analyzer `AnalyzeContext(ctx context.Context,now time.Time,days int,loc *time.Location)(*DeviceReport,error)`，保留原 `AnalyzeInLocation` 和 `TriggerAnalysis` 包装。

- [ ] 写 `TestAnalysisRunOutcomesAndEvidence`、`TestAnalysisLifecycleCancellation`、`TestRepeatedSuggestionAppendsEvidence`；核心断言：
  ```go
  if len(timeSuggestionsWith10Hits) != 0 || len(timeSuggestionsWith11Hits) == 0 { t.Fatal("14 day threshold changed") }
  if !reflect.DeepEqual(originalIntent, confirmedAfterRepeat.Intent) { t.Fatal("confirmed intent changed") }
  if recoveredInterruptedRun.Status != "failed" || recoveredInterruptedRun.ErrorCode != "interrupted" { t.Fatal("stale running job") }
  ```
  同测首次1小时、6.9天、7/10/11命中日、完整14天无规律、无事件但完整请求、时区/DST、取消扫描/关联计算、并发手动与每日分析。所有结果区分算法前提不足与真实无匹配。
- [ ] 运行 `go test ./internal/smarthome -run 'Test(AnalysisRun|AnalysisLifecycle|RepeatedSuggestion)' -count=1`；预期缺任务及证据接口。
- [ ] 实现状态先落盘后工作、检查ctx的扫描/关联分析，保留原每日家庭时区调度。单个分析任务占用现有 analysisMu；busy返回现有运行ID或明确409，不重复排队。保存每run独立报告，不能复用按日期覆盖的旧报告文件充当run存储。
- [ ] 将 `DetectedPattern.SampleSize/PeriodDays` 贯通到追加证据和DTO来源，按模式补hit/eligible/denominator；不解析DataSource自由文本。相同语义ID保留旧规则并追加run唯一证据，重复提交同run幂等。复用A2生命周期；Stop取消后解锁等待，不持Store锁等待网络。锁顺序保持Manager动作/分析锁→短暂DeviceStore锁；不能在Store锁中反调Manager或嵌套公开Store方法。
- [ ] 同测试通过，运行 `go test ./internal/smarthome -count=1`；所有running/queued中断记录恢复为failed/interrupted，明确重跑才能启动。
- [ ] 提交 `feat: persist analysis runs and suggestion evidence`。

## Task 4: A4 检测页面、任务API与证据卡片

**Files:** Modify `go-agent/internal/gateway/console_collection.go`, `console_suggestions.go`; Create `go-agent/internal/gateway/console_analysis_runs_test.go`; Modify `go-agent/web/src/api.ts`, `pages/CollectionPage.tsx`, `pages/AutomationsPage.tsx`; Create `go-agent/web/src/useAnalysisRun.ts`, `pages/CollectionRuns.test.tsx`; Modify `go-agent/tests/consolefixture/main.go`。

**Interfaces:** 现有 POST `/admin/analyze` 保留 `{days:14}` 输入，改为202 `{run_id,status}`；新增 GET `/admin/analysis-runs/:id`、POST同路径 `/cancel`；POST `/admin/backfills` 输入 `{start,end}`，GET `/admin/backfills/:id`、POST `/cancel`。全部管理员权限。GET `/admin/collection` 增加coverage、最近run、下一计划；GET `/suggestions` 的现有evidence DTO填充A3字段。

- [ ] 写 `TestConsoleAnalysisRunsAuthority` 及前端任务测试；断言202返回后任务继续，logout/revoke取消；无CSRF/成员调用/别人的run ID不泄漏run数据；历史不足和无规律展示不同文案。轮询按2秒、5秒、10秒上限退避，终态/隐藏页面/身份切换取消，不重复启动任务。
  ```go
  if acceptedStatus != 202 || !ranAfterResponse { t.Fatal("job tied to HTTP lifetime") }
  if leakedRunData || workAfterRevocation { t.Fatal("job authority escaped session") }
  ```
- [ ] 运行 Go `go test ./internal/gateway -run 'TestConsoleAnalysisRuns' -count=1` 与前端 `npm test -- src/pages/CollectionRuns.test.tsx`，预期新合同缺失。
- [ ] gateway先按现有流程验证身份，再以 `context.WithoutCancel(c.Request.Context())` 创建脱离HTTP结束的根，调用现有 `console.Store.BindSession` 重新绑定过期/撤销，组成JobScope交Manager；Manager再合并服务生命周期取消。禁止直接把请求ctx交后台或持久化token。获取run仍重新认证和检查OwnerID；系统每日任务用明确system owner。
- [ ] 页面显示目标14天、请求窗口与实体质量分开、0条具体原因、分析记录、明确补采/取消按钮；证据用命中率和分母，不称成功概率。复用 `requestConsole` 的 expectedIdentity/signal；加载/错误/未知状态和模拟标签齐全。
- [ ] 同测试通过，并跑 `npm run typecheck`、`npm run check:ui-boundaries`。一期验收时只读，断言0次HA写入。
- [ ] 提交 `feat: explain automation discovery coverage and runs`。

## Task 5: A5 受限修订和版本差异

**Files:** Create `go-agent/internal/smarthome/suggestion_revisions.go`, `suggestion_revisions_test.go`; Modify `suggestion_intent.go`, `store.go`, `go-agent/internal/gateway/console_suggestions.go`, `go-agent/web/src/AutomationRuleView.tsx`, `go-agent/web/src/pages/AutomationsPage.tsx`; Test `go-agent/web/src/AutomationUsability.test.tsx`。

**Interfaces:** 新增 `SuggestionRevisionRequest{ExpectedVersion string; Intent SuggestionIntent; PresenceBinding *PresenceBinding}`，复用types.go已有PresenceBinding；`ReviseSuggestion(ctx context.Context,id string,req SuggestionRevisionRequest)(*RuleSuggestion,error)`；POST `/admin/suggestions/:id/revisions` 返回新版本及diff。建议旧ID作为版本令牌，旧版提交409；保留 `SaveBoundSuggestion` 的“Intent必须相同”合同，不放宽它兼任一般编辑。

- [ ] 写 `TestSuggestionRevisionIsImmutableAndRestricted`：原版本不变、新版本supersedes、旧版不能确认、缺expectedVersion拒绝；重名/过期/不支持目录目标不自动选中；presence必须明确选择。时间模式只改时间和已支持目标，关联模式只改已支持源/目标；保留强制条件和当前turn_on意图，任意YAML/未知动作拒绝。
  ```go
  if !reflect.DeepEqual(original.Intent, reloadedOriginal.Intent) { t.Fatal("revision mutated source") }
  if oldConfirmStatus != 409 || haWrites != 0 { t.Fatal("stale or unconfirmed revision executed") }
  ```
- [ ] 运行 `go test ./internal/smarthome -run 'TestSuggestionRevision' -count=1`；预期缺修订接口。
- [ ] 实现校验和原子版本链提交；复用 BuildAutomation、能力目录、房间→设备→实体选择器和现有确认UI。展示差异，保存修订不安装；未知旧Intent只能只读。若未来支持turn_off须另加Intent版本和合同，不能本任务悄悄扩展。
- [ ] 同测试通过；前端 `npm test -- src/AutomationUsability.test.tsx` 验证键盘选择、字段错误聚焦、diff和独立确认。
- [ ] 提交 `feat: add validated automation suggestion revisions`。

## Task 6: A6 只读配置、映射与运行时证明适配

**Files:** Create `go-agent/internal/smarthome/automation_observation.go`, `automation_observation_test.go`; Modify `client.go`, `registry.go`; Test `client_test.go`（同包）。

**Interfaces:** 新增HomeAssistantClient方法 `ReadAutomationConfig(ctx context.Context,configKey string)(AutomationConfigObservation,error)` 与Manager方法 `ObserveAutomation(ctx context.Context,ref AutomationRef)(AutomationObservation,error)`。ConfigObservation含SourceStatus、Exists可选指针、原始配置的安全本地引用、规范化完整摘要；AutomationObservation另含 `Ownership string`（managed/external/unverified）、映射来源、运行实体及 `RuntimeLoadProof`（Verified、ExpectedDigest、Source、ObservedAt）。registry只在接口确实返回时保留platform/unique_id，不能从friendly_name推导。

- [ ] 写 `TestAutomationObservationDistinguishesMissingAndUnavailable`、`TestAutomationObservationDoesNotInferOwnership`、`TestConfigMatchIsNotRuntimeProof`；断言404和403/网络失败分开，unknown字段保留在完整配置摘要中；同名实体不构成映射，摘要匹配但无加载证据时 `RuntimeLoadProof.Verified == false`；全部读取0次HA写入。
  ```go
  if observation.RuntimeLoadProof.Verified { t.Fatal("config read cannot prove loaded version") }
  if observation.Ownership == "managed" || haWrites != 0 { t.Fatal("name match claimed ownership") }
  ```
- [ ] 运行 `go test ./internal/smarthome -run 'Test(AutomationObservation|ConfigMatch)' -count=1`；预期缺读取接口。
- [ ] 复用现有OAuth/GET401单次刷新及redirect拒绝，新增窄读取合同；只对已确认支持的端点把404解释为不存在，unsupported端点404仍未知。不要把任意HA配置解码成受限AutomationConfig后丢弃外部字段。部署HA版本必须先读取，版本未知则能力未知；不把参考版本当部署版本。
- [ ] 实现RuntimeLoadProof的保守合同：部署版本合同核验后，读取管理员WS `automation/config` 的当前内存raw_config，联合唯一configKey→entity映射、当时on/off状态和完整配置摘要；unavailable实体即使raw_config相同也不通过。只证明观察到的加载版本，不证明触发挂载、运行或物理成功。旧trace仅证明该历史run，不用于当前加载盖章。单独配置GET、旧实体存在、POST返回200均不充分；无证明继续unknown，不为取证触发规则。规范化只处理JSON编码/键序，不删除ID、alias、未知字段或擅自注入默认值。
- [ ] 同测试通过，并回归GET401重试一次、写请求不重试、响应体上限和重定向测试。
- [ ] 提交 `feat: observe automation configuration and ownership`。

## Task 7: A7 安装操作日志与只读收敛

**Files:** Create `go-agent/internal/smarthome/install_operations.go`, `install_operations_test.go`; Modify `manager_actions.go`, `automation.go`, `store.go`, `go-agent/internal/gateway/console_suggestions.go`, `go-agent/web/src/pages/AutomationsPage.tsx`。

**Interfaces:** 保留 `ConfirmSuggestion(ctx,id)(*RuleSuggestion,error)`，内部接入操作记录；新增 `GetInstallOperation(id string)(InstallOperation,error)`、`ReconcileInstallation(ctx context.Context,operationID string)(InstallOperation,error)`。GET `/admin/install-operations/:id`、POST `/admin/install-operations/:id/reconcile`；核对请求只读外部系统，仍使用CSRF和管理员权限。

- [ ] 写 `TestInstallOperationAmbiguousOutcome`、`TestInstallReconcileNeverWrites`、`TestInstallArchiveFailureIsSeparate`；断言外部写入前操作已持久化，写后响应丢失/reload未知/重启均unknown，已明确拒绝且无应用才failed；摘要匹配但运行未证实仍unknown；归档失败不能改成“HA安装失败”。
  ```go
  if lostResponse.Status != "unknown" || reconcileWrites != 0 { t.Fatal("ambiguous install replayed") }
  if !operationExistedBeforeWrite { t.Fatal("external write preceded durable operation") }
  ```
- [ ] 运行 `go test ./internal/smarthome -run 'TestInstall' -count=1`；预期旧流程错误合并/重复执行断言失败。
- [ ] 拆开 `ExecuteRule` 的create与reload阶段记录，保留其兼容包装；未知旧applying记录迁移为unknown。重复confirm返回同一操作，不重发；只在配置、映射及加载证据齐全后confirmed并写受管记录。外部已存在但不匹配时标漂移/冲突，不覆盖。
- [ ] 同测试通过，补gateway DTO验证和UI“配置已保存，加载结果待核对”“本地归档待完成”场景；核对按钮不能调用confirm。旧confirmed仅是历史安装记录，映射未知时不伪造enabled或managed。
- [ ] 提交 `feat: track automation installation outcomes safely`。

## Task 8: A8 只读运行证据和能力状态

**Files:** Create `go-agent/internal/smarthome/diagnostic_sources.go`, `diagnostic_sources_test.go`; Modify `registry.go`, `automation_observation.go`。

**Interfaces:** 新增 `ReadAutomationEvidence(ctx context.Context,ref AutomationRef,window TimeWindow)(AutomationEvidence,error)`；AutomationEvidence含state/config/trace/activity四来源及其SourceStatus、实际覆盖窗口、最小事件引用，不导出原始trace变量。扩展现有 registrySession.request 的错误分类，不新增任意WS命令代理。

- [ ] 写 `TestDiagnosticSourcesDegradeExplicitly`、`TestTraceEvidenceExcludesPrivateVariables`；模拟unsupported/forbidden/retention_gap/unavailable，断言不变成成功空列表；明确条件未满足、动作失败、完成的trace分类正确。每次只允许 state/config/registry/trace只读操作，0次service/write/debug命令。
  ```go
  if traceStatus != "forbidden" || mutatingCommands != 0 { t.Fatal("capability boundary broken") }
  if strings.Contains(exportedEvidence, privateMarker) { t.Fatal("private trace variables leaked") }
  ```
- [ ] 运行 `go test ./internal/smarthome -run 'Test(DiagnosticSources|TraceEvidence)' -count=1`；预期缺来源适配。
- [ ] 基于读取到的部署版本与隔离合同接入 `trace/list`、`trace/get`，限制窗口/条数/响应大小和并发；仅保存所需证据摘要及本地引用。活动归因缺context或无可靠logbook接口时标unsupported，不能用state_changed冒充执行来源；无明确保留范围不能仅因空trace声称没有执行。未知或超过读取上限要标覆盖不足。
- [ ] 同测试通过；将经过校验的trace配置作为带run ID和时间的历史运行证据，保持A6当前加载观察独立，再运行安装收敛测试以证明旧trace不会误确认当前版本。
- [ ] 提交 `feat: collect read only automation runtime evidence`。

## Task 9: A9 可解释诊断规则

**Files:** Create `go-agent/internal/smarthome/automation_diagnostics.go`, `automation_diagnostics_test.go`; Modify `manager.go`。

**Interfaces:** 新增 `DiagnoseAutomation(ctx context.Context,ref AutomationRef,window TimeWindow)(DiagnosticSnapshot,error)`，纯规则函数 `EvaluateAutomationDiagnostics(ref AutomationRef,evidence AutomationEvidence) DiagnosticSnapshot`；消费者只得到结构化证据，不接收HTTP客户端。

- [ ] 写 `TestAutomationDiagnosticsEvidenceBoundaries` 表驱动用例，断言：
  ```go
  if noTraceRunCheck.Status != "unknown" || incomparableConflict.Status != "unknown" { t.Fatal("missing evidence overclaimed") }
  if provenActionFailure.Status != "finding" { t.Fatal("action failure hidden") }
  if disabledWithoutExpectedEnabled.Category == "fault" { t.Fatal("disabled is not itself a fault") }
  ```
  覆盖不存在/不可达、停用、配置漂移、实体不可用、正常条件不满足、trace动作失败、全部unknown、缺运行证据、已证明与可能冲突、无因果的状态变化、跨DST和手动触发规则。
- [ ] 运行 `go test ./internal/smarthome -run 'TestAutomationDiagnostics' -count=1`；预期缺规则函数。
- [ ] 检查结论严格绑定来源与时间。仅支持现有定时/状态意图的可解释预期窗口；没有明确延迟合同不按“24小时没运行”报警。静态冲突仅比较可判定触发、重叠条件、同实际目标和相反动作；复杂模板显示unknown/possible，不猜家庭用途。条件不满足与停用按事实显示，明确期望矛盾才升级。
- [ ] 同测试通过；证明纯规则函数无法发设备命令，Summary遵守公共合同。相同发现以稳定键去重，新发现/严重程度改变/恢复才更新需关注提示，不自行创建邮件或第三方通知。
- [ ] 提交 `feat: diagnose automation state with evidence`。

## Task 10: A10 已安装、需关注和成员证据投影

**Files:** Create `go-agent/internal/gateway/console_automations.go`, `console_automations_test.go`; Modify `console_suggestions.go`, `go-agent/web/src/api.ts`, `go-agent/web/src/pages/AutomationsPage.tsx`, `go-agent/web/src/AutomationRuleView.tsx`; Create `go-agent/web/src/pages/AutomationDiagnostics.test.tsx`。

**Interfaces:** GET `/automations` 返回已授权受管/历史待核对投影；POST `/admin/automations/:id/diagnostics` 输入window，GET `/automations/:id/diagnostics/latest`；外部诊断使用单独管理员 POST `/admin/automation-diagnostics` 输入服务端已读取的AutomationRef。新增诊断操作不会安装/接管外部规则。

- [ ] 写 `TestConsoleAutomationEvidenceProjection`：成员只看sharing授权suggestion对应受管规则；未授权证据、房间、冲突对手不出现在计数/文案/引用中；外部与unverified仅管理员。前端断言旧confirmed≠enabled，全部unknown≠绿色成功，读取失败保留上次观察时间并标过期。
  ```go
  if strings.Contains(memberJSON, privateRoomMarker) || strings.Contains(memberJSON, hiddenRuleID) { t.Fatal("projection leaked evidence") }
  if allUnknownSummary != "unknown" { t.Fatal("unknown checks reported healthy") }
  ```
- [ ] 运行 `go test ./internal/gateway -run 'TestConsoleAutomationEvidenceProjection' -count=1` 与 `npm test -- src/pages/AutomationDiagnostics.test.tsx`；预期缺投影/页面行为。
- [ ] 实现“待审阅”“已安装”“需关注”及单独归档；每卡显示具体名称、核对时间、启用事实、运行证据与下一步。安装unknown在列表明确“待核对”，不把它算作已确认安装；ignored不能显示停用。成员投影复用现有sharing规则，但必须逐条过滤evidence/conflict引用。
- [ ] 同测试通过；检查窄屏、键盘、错误状态、无trace、外部只读和HA原生链接（URL仅从配置可信BaseURL构建）。
- [ ] 提交 `feat: show managed automation diagnostics`。

## Task 11: A11 独立确认的受管规则管理

**Files:** Create `go-agent/internal/smarthome/automation_management.go`, `automation_management_test.go`; Modify `install_operations.go`, `go-agent/internal/gateway/console_automations.go`, `go-agent/web/src/pages/AutomationsPage.tsx`。

**Interfaces:** 新增 `AutomationActionRequest{ManagedID,Action,ExpectedDigest string; RevisionID string; StopActions *bool}`、`PrepareAutomationAction(ctx context.Context,req AutomationActionRequest)(ManagementOperation,error)`、`ConfirmAutomationAction(ctx context.Context,operationID string)(ManagementOperation,error)`。Action只接受enable/disable/update/delete；新增 POST `/admin/automations/:id/actions`、POST `/admin/automation-actions/:id/confirm`、GET同操作路径。

- [ ] 写 `TestManagementRequiresFreshConfirmedAuthority`、`TestManagementUnknownNeverReplays`、`TestManagementDeleteRequiresBackup`；断言prepare、诊断、旧版本/无映射/成员/会话撤销均0次写入；确认仅目标受管规则写一次；响应丢失变unknown；完整配置备份失败阻止删除。模拟prepare之后外部编辑、身份撤销和迟到确认。
  ```go
  if writesBeforeConfirm != 0 || writesAfterRevocation != 0 || deletesAfterBackupFailure != 0 { t.Fatal("management guard bypassed") }
  if writesAfterLostResponseRetry != 1 { t.Fatal("unknown management action replayed") }
  ```
- [ ] 运行 `go test ./internal/smarthome ./internal/gateway -run 'TestManagement' -count=1`；预期缺管理接口。
- [ ] 实现操作先落盘、确认有效期5分钟（新确认合同）、执行前重新验证管理员/映射/当前完整摘要；只经窄领域方法调用已验证HA合同。停用必须显式选择StopActions，页面说明它不能撤销已发生物理效果。update复用A5修订和A7安装操作，保留该受管规则原configKey，不能创建第二条同义规则。
- [ ] 删除前原子保存完整配置和可审阅恢复材料；恢复是新的明确操作，本期不自动恢复。HA无原子比较交换时记录“读后外部并发编辑”剩余风险，不宣称跨系统事务；发现漂移即停止。未知结果只读核对，不能借确认重试写入。外部/unverified持续只读。
- [ ] 同测试通过；UI确认单列规则、变更和影响，保留当前设备proposal的独立存储及语义。
- [ ] 提交 `feat: manage verified automations with confirmation`。

## Task 12: A12 分期回归、兼容迁移和现场验收记录

**Files:** Modify `go-agent/tests/consolefixture/main.go`; Create `go-agent/web/e2e/automation-lifecycle.spec.ts`; Create `docs/automation-completion-validation.md`。

**Interfaces:** 汇总A1–A11；fixture只模拟读写并计数，不能读取正式家庭配置或连接HA。

- [ ] 新增浏览器完整场景：数据不足→补采→分析→证据→明确presence→修订→独立确认→安装unknown→核对→诊断→管理确认；另覆盖无权限、未知能力、取消、迟到响应、320px宽度及键盘。断言unknown路径不增加HA写次数，`document.documentElement.scrollWidth <= clientWidth`。
- [ ] 各期先运行 `npm run test:e2e -- e2e/automation-lifecycle.spec.ts` 对应已实现部分，再补最小fixture支持；禁止跳过失败场景后声称整个闭环通过。保持18081单worker，与云端浏览器测试串行。
- [ ] 迁移测试使用旧JSON副本：缺证据、confirmed无映射、applying中断、损坏新文件均兼容或明确unknown；迁移前备份，失败不覆盖旧文件，schema不支持明确报错。测试本地归档与外部结果分离。
- [ ] 最终运行 `go test ./...`、`go vet ./...`、前端 `npm test`、`npm run build`、`npm run check:ui-boundaries` 及相关completion/control/auth E2E；保留命令、退出码及失败原因，已通过后不无故重复全量。
- [ ] 写分期验收记录：代码/模拟/只读真实能力/真实安装与运行/部署分别标记。真实阶段先由用户给定低风险设备与明确用途，再审阅、确认安装、等待自然触发、核对HA证据与物理负载；无此现场证据只能写“未验收”。部署经现有受保护脚本和回滚流程单独处理。
- [ ] 提交本任务变更 `test: verify automation discovery and management lifecycle`。

## 官方接口证据与未解决的外部条件

以下是已查阅的 **Home Assistant 2026.7.2 源码参考**，不是当前部署版本声明：

- [config/view.py](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/config/view.py)：配置写入响应与异步后续处理分离；不能仅凭200证明加载完成。
- [config/automation.py](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/config/automation.py)：自动化写后reload及配置key与注册表关系需按版本核对。
- [trace/websocket_api.py](https://raw.githubusercontent.com/home-assistant/core/2026.7.2/homeassistant/components/trace/websocket_api.py)：只读trace/list与trace/get需要管理员；debug/step/stop等命令不属于诊断读取。
- [automation/__init__.py](https://github.com/home-assistant/core/blob/2026.7.2/homeassistant/components/automation/__init__.py#L1155)：只读automation/config返回当前内存raw_config；须排除持有raw_config的unavailable实体。删除并不承诺停止在途动作，不能把停用时的stop_actions参数套到DELETE。

部署版本、recorder保留范围、configKey→entity映射以及运行配置证明能力在实装时逐一只读核对。无法证明某项能力时对应功能以unsupported/unknown交付，其余发现与状态检查仍可完成；禁止猜接口或把不支持当成功。

自检映射：采集/覆盖→A1–A2；分析/证据/解释→A3–A4；编辑/独立确认→A5；安装核对/兼容→A6–A7；来源/诊断→A8–A10；管理→A11；安全投影/迁移/UI/真实验收→A10–A12。现有算法和固定条件保持不变，任意YAML编辑、自动修复和外部规则接管不在本计划内。
