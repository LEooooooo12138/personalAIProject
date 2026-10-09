# Family Console Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 补齐旧 spec 中知识与建议工作流，完成前端设计修订，并修复 HA 临时凭据到期断连。

**Architecture:** React + TypeScript + Vite 静态资源继续由 Go 同源提供。Console handlers 仅适配现有 vault Reader/ChainExecutor/SmartHome Manager，不复制知识推理或自动化引擎。服务端共享 OAuth provider 供 REST 与 WS 使用，旧静态 token 接口保留。

**Tech Stack:** Go 1.26.4、React 19、TypeScript、Vite、Vitest、Playwright Chrome、已有本地 SVG。

**Spec:** ../specs/2026-10-09-family-console-completion-design.md（精确 DTO、状态、页面与授权边界权威来源）

## Global Constraints

- 全家可读全部区域设备；成员只读显式共享 confirmed 建议；无即时设备控制。
- personal 仅管理员；普通聊天固定 agent，私人问答页面内暂存，不写聊天历史/全局索引/记忆。
- 前端不得持 HA Token、管理 Bearer、绝对 Vault 路径；严守 Origin/CSRF/no-store/角色与撤销。
- 不新建长期令牌，复用已有 OAuth refresh grant；不自动重放真实 HA 写请求。
- 保留附着 worktree 的既有全部修改；每任务保留增量 diff 与 RED/GREEN/审查证据，不自动 commit。
- 最新用户已明确要求“修订设计方案再进行改动”，本轮无需再次请求泛化实施批准。分文件并行节省时间，公共接线由 root 串行；平台 agent 数量限制时复用 agent，审查人不得审自己任务。

## Review Focus

- 页面打开后权限撤销或账号更换，旧 personal 回答不能显示或进入另一账号状态（T1/T4）。
- token 在多请求及 REST/WS 同时过期，不出现续期风暴或重复执行（T3）。
- 成员猜 ID、旧版建议、stale catalog、不可解析实体条件，不泄漏或弱化规则（T2/T4）。
- 公共文档缓存后转 internal、损坏 metadata 或 symlink 跳出根，正文/搜索/计数一致拒绝（T1）。
- 手机长名称/200%字号/失败与重试/未知device_class，页面可读且不虚报状态（T3/T4/T5）。

## Task 0: 反查与保存基线（root）

Files: `.superpowers/sdd/2026-10-09-family-console-completion/{spec-audit-console.md,spec-audit-system.md,baseline-source/,go-baseline.log,frontend-baseline.log}`。
- [x] 阅读旧 specs/plans 和 CONSTITUTION，输出逐条需求矩阵，标记新用户规则覆盖项。
- [x] 保存源码快照和 git status，不触碰秘密/runtime；复用附着 worktree。
- [x] 运行 `go test ./...`（通过）；`npm.cmd test -- --run`（66/66）、`npm.cmd run typecheck`（通过）。
- [x] 完成 spec/plan 自检后向独立审查人交付设计；若范围偏差先修订，不补假功能。

## Task 1: Console 知识服务（areas_privacy_task1）

Files: create `go-agent/internal/gateway/console_knowledge.go`, `console_knowledge_test.go`（按职责可拆 public/private）；modify `go-agent/internal/chain/executor.go` 仅私人输入日志策略和对应 tests。不要编辑 console_routes.go、console_chat.go、http.go、web。
Interfaces: methods `handleConsoleKnowledgeSearch`, `handleConsoleKnowledgePage`, `handleConsoleVaultStatus`, `handleConsoleVaultSearch`, `handleConsoleVaultPage`, `handleConsoleKnowledgeQuery`, `handleConsoleWikiIngest`（*gin.Context）；新增 `setupConsoleKnowledgeRoutes(g *gin.RouterGroup)` 供 root 注册。消费 vault.Reader、ChainExecutor.Run(rag-answer/wiki-ingest)、ChainState.Sources/Data。产生 spec §4.1 精确 JSON。
- [x] RED: HTTP fixture 验证成员公共可读/私人403、admin私人可读、坏metadata/internal/archive/根外拒绝、搜索复读后count不含私页、无绝对路径、CSRF/撤销，期待新路由404或新能力未实现。
- [x] RED: 真chain mock inference验证私人事实+来源进入问答、query不写Info、wiki-ingest成功结果，failed step返回失败而非成功。
- [x] 实现严格小边界：固定Vault、限制长度、安全DTO、取消/超时、最终权限复核，现有Safe policy复用；不直接调旧HTTP handler。
- [x] GREEN: `go test ./internal/gateway -run ConsoleKnowledge -count=1` 与 `go test ./internal/chain -count=1`；保存真实日志。
- [x] 自检八荣八耻、增量diff和task1-report.md；root另派审查。

## Task 2: Console 自动化与采集（areas_catalog_task2）

Files: create `gateway/console_suggestions.go`, `console_sharing.go`, `console_collection.go`,相应 tests；modify `smarthome/collector.go`, `manager.go` 只采集观测/分析并发；create `smarthome/collection_status.go`+tests。不改 client/ws/catalog/types 或 root接线文件。
Interfaces: `setupConsoleSmartHomeRoutes(g *gin.RouterGroup)`；`Collector.Status() CollectionStatus`、`Manager.CollectionStatus() (CollectionStatus,error)`；consume BindSuggestion/ConfirmSuggestion/IgnoreSuggestion/TriggerAnalysis/GetStore、console Store GetSharing/PutSharing、catalog只读投影。`CollectionStatus` 和 `SuggestionView` 使用 spec §4.2 DTO。Manager.TriggerAnalysis 保留原签名，新增忙碌错误映射409。
- [x] RED: admin/member/CSRF真实HTTP contract、member只explicit confirmed、全关联实体校验、stale/未知规则隐藏、sharing revision 409、EntityIDs原值保留。
- [x] RED: 分析结果→binding新ID→旧superseded→独立confirm mockHA一次→归档、重复/并发确认、旧版本409、未知entity422。
- [x] RED: Collector真实模拟部分失败：snapshot_at已有、history失败checkpoint不推进、completed/total来自实际批数；新进程last_success null。
- [x] 最小实现接口、投影与观测，复用业务引擎；未知写结果不自动重放，明确重试沿原幂等。
- [x] GREEN: `go test ./internal/gateway -run 'Console(Suggestion|Sharing|Collection|Analyze)' -count=1`；`go test ./internal/smarthome -count=1`。
- [x] 记录task2-report、增量diff与八荣八耻自检，交独立审查。

## Task 3: HA 续期和状态准确性（root）

Files: create `smarthome/token_provider.go`, `client_errors.go` +tests；modify `client.go`, `ws.go`, `registry.go`, `catalog.go`, `catalog_types.go`, `types.go`, `core/config.go`, `core/app.go`, `config/agent.example.yaml`。manager.go由T2持有，构造接线root等T2完成后串行补。
Interfaces: `TokenProvider` with `Token(context.Context)(string,error)`, `Invalidate(string)`, `Close()`；`NewOAuthTokenProvider(baseURL,credentialsFile string,timeout time.Duration)(*OAuthTokenProvider,error)`；client provider可选，Token旧字段兼容；`HAErrorCode(error) string` 返回spec安全code；EntityView新增DeviceClass *string。配置 SmartHomeConfig/HAConfig.OAuthCredentialsFile。
- [x] RED: mockauth过期、并发单飞、400/403/timeout、安全错误无秘密、重定向拒绝、credential URL mismatch、等待取消、Close；REST GET401仅一次重试、POST不重放；WS auth_invalid刷新最多一次。
- [x] RED: Catalog保留旧snapshot时间并标授权/权限/timeout；已知device_class投影、未知/超长/错类型null。
- [x] 实现10秒上限、提前60秒续期、5秒失败冷却、内存access token、原refresh只读；安全code不带原响应。
- [x] GREEN: `go test ./internal/smarthome -run 'OAuth|Token|Auth|Catalog|Registry' -count=1`、配置回归。
- [x] 更新本地运行说明，真实环境仅正常已有refresh与只读REST/WS，不做现场动作。

## Task 4: 前端完整页面和设计系统（ha_acceptance_inventory）

Files: owns `go-agent/web/src/**`, `web/e2e/**`, `web/vite.config.ts` 和 README；create KnowledgePage/AdminKnowledgePage/AutomationsPage/CollectionPage、对应hooks/DTO/tests，按职责拆分，复用requestConsole/Auth/useFamilyQuery/SafeMarkdown。不改后端或fixture；root接 fixture。
Interfaces: 消费 spec §4所有固定JSON与capability。GET通过身份epoch/abort；业务POST带CSRF但不用Cookie mutation flag。导出新页面并在App路由，首页/account入口整合。
- [x] RED: unit渲染公开/私人分区与安全正文、403/404/500/noresults、私人迟到响应丢弃。
- [x] RED: 建议binding只改新ID不confirm；新ID结构确认独立点击；分享revision409、422表单保留、failed先刷新、busy防连点；真实collection显示null/部分失败。
- [x] RED: HA授权提示不退出、模型unknown、door/tamper/unknown不同显示；移除无依据绿点；手机Enter/IME/历史切换；导航与可访问名称。
- [x] 实现spec所有页面、统一本地SVG/44px/16px/中文类别/语义颜色；保留旧功能，完善Vite开发代理（显式env目标，不硬编码秘密）。
- [x] GREEN: `npm.cmd test -- --run`、`npm.cmd run typecheck`，保存task4-report；build/E2E留T5串行。
- [x] 增量diff、八荣八耻自检；root/后端owner独立检查跨层工作流。

## Task 5: 接线、真实fixture、审查与交付（root协调）

Files: `gateway/console_routes.go`, `console_chat.go`能力、必要http.go初始化；`tests/consolefixture/*`、文档与本轮验收报告。
- [x] 注册T1/T2 routes；按真实依赖发布capability，restricted仍无业务；不改变旧internal API。
- [x] Go fixture启用临时agent/personal知识+模拟HA分析/建议/采集，保留测试账号，不调用真实HA/模型。新增Chrome跨页知识/绑定/确认/分享/member权限浏览器测试。
- [x] 独立交叉spec审查和质量审查，P1/P2修复后重跑相应测试；不把旧测试当新证据。
- [x] `go test ./...`、`go vet ./...`、`go build ./...`；Vitest/typecheck；先Vite build→Chrome browser（全部结束）→最终Go编译与生成JS语法检查；旧Node/Python fixture；Linux CGO `go test -race ./...`。
- [x] CUA检查真实构建320/390/768/1440关键页面，检查键盘、布局和错误文案。运行时只在确认ownedpid/exepath后替换binary并Hidden启动，已有OAuth文件保持私有。
- [x] 实证HA只读REST+WS、Console API与Ollama；令牌续期边界用fake clock/mock严格验证并记录真实长期观察限制，不声称现场验收。
- [x] 更新旧spec/plan状态链接与新需求矩阵，记录实测通过和未做事项；提供设计/页面/结果报告链接。

## 最终验收说明

2026-10-09本轮实现与上述任务完成。实际结果和明确未验的现场、长期运行、200%系统字号/屏幕阅读器范围见[验收报告](../../family-console-completion-results-2026-10-09.md)。不能将任务勾选理解为真实设备动作或完整WCAG认证。
