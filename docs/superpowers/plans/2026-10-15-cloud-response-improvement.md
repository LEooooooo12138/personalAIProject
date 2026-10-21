# 云端响应可观测性与调用优化 Implementation Plan

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让每轮云聊天的等待可以解释和测量，并让用户明确选择的普通聊天减少一次串行分类调用。

**Architecture:** 复用现有 AssistantClient、StepTrace、会话排队和 Console WebSocket；补充本机安全计时与服务器阶段事件。普通聊天使用显式入口，保留自动识别兼容路径；输出继续经过全文过滤、保存及授权发布。

**Tech Stack:** 当前 Go 模块、net/http/httptrace、zap、React/TypeScript、Vitest、Playwright；不新增运行依赖。

**Spec:** 云端响应耗时检查与优化设计（本地记录未公开）。

状态：2026-10-15 实现计划，已获同意并执行中；完成情况与测试证据见实施记录（本地记录未公开），正式服务尚未部署。

## Global Constraints

- 执行源码根目录 `R=仓库根目录`；以下文件均相对 R。记录时的基线为 HEAD `9c2a95f9` 加当时的工作区修改；后续执行前需核对实际代码状态。
- 执行前核对 Git 状态、适用 AGENTS.md、工具版本及已附工作树；保留现有修改。需要隔离时按 using-git-worktrees 技能完整保留当前基线，不能丢失未提交实现。每次提交只选择本任务新增的文件/差异，先检查 staged diff，禁止 `git add .`。
- 模式 local/cloud/hybrid、旧会话 provider pin、云历史筛选、固定身份与可选人格、隐私过滤、设备独立确认均保持原合同。入口类型不能复用 `AssistantMode`。
- 指标默认仅在本机；不记录 key、提示词、人格、会话正文、设备目录、原始错误或客户端自选 request_id。使用服务器生成的匿名 trace ID。
- 阶段由真实执行位置驱动，不显示虚构百分比；取消和身份撤销贯穿队列、HTTP、保存及发布。
- 运行端口以本机配置为准；Playwright 使用独立 fixture。模拟通过、真实 provider 测量和正式部署验收分别记录。
- 本轮实现不直接开启 SSE。未经完整过滤合同验证，不提前发布任何回答片段。

## Review Focus

1. 客户端把聊天原文放进 request_id、上游 error 或 StepTrace.Error：安全日志不能收录这些内容（C1）。
2. 排队时撤销登录、切换身份后迟到阶段、保存失败后迟到 response：不得跨会话发布或伪装完成（C2、C3）。
3. 普通聊天文本包含设备指令、同 request_id 改入口、设备 replay 恢复：不能意外进入控制路径（C4、C5）。
4. 复用连接导致 trace 钩子缺失、非流式 body 首字节早于可解析正文：缺失不是 0ms，HTTP 首字节不是首 token（C1、C7）。
5. 历史过长、人格为空或超过预算、旧会话模式未标注：不能静默丢失系统/人格或把本地历史上传云端（C6）。

## 文件与合同

新增 `internal/inference/call_trace.go` 只负责传输观测，`internal/gateway/chat_observation.go` 负责整轮安全记录和阶段，`internal/gateway/chat_entry.go` 负责入口解析；均位于 `go-agent/`。不新增通用追踪框架。前端复用 `web/src/chat/chat-state.ts`、`control-types.ts` 和现有 ChatPage 状态区域。

下列接口为本计划**新增**，不是已存在 API：

```go
// inference/call_trace.go；可选计时使用指针，未观测到必须为 nil。
type CallTrace struct {
    DNSMS, TCPMS, TLSMS, RequestWrittenMS *float64
    FirstResponseByteMS, FirstBodyByteMS, TotalMS *float64
    Reused bool
    StatusCode int
    RequestBytes, ResponseBytes int64
    InputTokens, OutputTokens *int64
    ErrorCode string // 固定枚举；禁止 err.Error()
}
func WithCallObserver(ctx context.Context, observe func(CallTrace)) context.Context

// gateway/chat_observation.go
type ChatStage string // queued, preparing, classifying, generating, saving
func (w *wsConn) publishChatStage(ctx context.Context, stage ChatStage) error

// gateway/chat_entry.go
type ChatEntry string // auto, chat；空值兼容为 auto
func parseChatEntry(value string) (ChatEntry, error)
```

新增 WS 阶段帧固定为 `{type:"chat_stage",session_id,request_id,stage}`，三个业务字段必填。服务器计时另含 queue/settings/personality/classify/generate/filter/persist/publish 的耗时、终态码及上游调用序号；这些内部阶段不都需要向用户显示。非流式没有可观测首 token，禁止用 `FirstBodyByteMS` 填充 `first_content`。

## Task 1: C1 安全 HTTP 计时与现有日志脱敏

**Files:** Create `go-agent/internal/inference/call_trace.go`, `call_trace_test.go`; Modify `go-agent/internal/inference/assistant.go`, `go-agent/internal/chain/executor.go`; Test `go-agent/internal/chain/executor_privacy_test.go`。

**Interfaces:** 消费现有 `(*AssistantClient).Chat(context.Context,json.RawMessage)(json.RawMessage,error)`、`StepTrace`；产出上面的 `WithCallObserver`、`CallTrace`。观察器只描述一次实际 HTTP 调用，不改变请求、重试或连接池策略。

- [ ] 写 `TestCallTraceSeparatesHeadersAndBody`、`TestCallTraceMissingHooksAreUnknown` 和 `TestConsoleChainLogsExcludePayload`。断言核心：
  ```go
  if *trace.TotalMS <= *trace.FirstResponseByteMS { t.Fatal("body timing lost") }
  if reusedTrace.DNSMS != nil { t.Fatal("missing DNS hook must stay unknown") }
  if strings.Contains(capturedLogs, secretMarker) { t.Fatal("payload leaked into logs") }
  if upstreamCalls != 1 { t.Fatalf("calls=%d, want 1", upstreamCalls) }
  ```
  用受控 HTTP handler 分别阻塞 headers/body；将 secretMarker 放入 query、上游错误正文、StepTrace.Error 和客户端 request_id。计数与同步屏障为主，避免依赖窄毫秒阈值。
- [ ] 在 R/go-agent 运行 `go test ./internal/inference ./internal/chain -run 'Test(CallTrace|ConsoleChainLogsExcludePayload)' -count=1`；预期因新增接口缺失或日志泄漏断言失败。
- [ ] 实现 observer，组合已有 context trace，不替换 transport；处理连接复用、DNS 多回调并发和 observer 单次完成。只从已有成功响应读取 usage；不为了计时改变解析/超限行为。删除 Console agent query 截断日志，新增计时使用错误码白名单，不直接投影原始 StepTrace.Error。
- [ ] 同命令通过，并运行 `go test ./internal/inference ./internal/chain -count=1`；检查测试日志不含 marker、URL query 或授权头。
- [ ] 只暂存本任务差异，检查 staged diff，提交 `feat: add private cloud request timing`。

## Task 2: C2 整轮计时、统一父 deadline 和授权阶段发布

**Files:** Create `go-agent/internal/gateway/chat_observation.go`, `chat_observation_test.go`; Modify `go-agent/internal/gateway/websocket.go`, `console_control_chat.go`; Test `go-agent/internal/gateway/console_chat_test.go`, `chat_error_contract_test.go`。

**Interfaces:** 消费 C1、现有 `SessionManager.AcquireTurn(ctx,channelID,userID)(func(),error)`、`options.WithSession`；产出 `publishChatStage` 和阶段帧。已有 `writeJSONContext` 不包含身份校验，不能直接作为发布授权入口。

- [ ] 写 `TestChatStagesFollowActualWork`、`TestChatRoundBudgetIncludesQueue`、`TestChatStageRevocationSuppressesPublish`。测试断言：
  ```go
  if !reflect.DeepEqual(stages, []string{"queued", "preparing", "classifying", "generating", "saving"}) { t.Fatalf("stages=%v", stages) }
  if upstreamCallsAfterQueueCancellation != 0 { t.Fatal("cancelled queue started inference") }
  if framesAfterRevocation != 0 { t.Fatal("published after revocation") }
  ```
  另覆盖 filter/persist 失败没有成功 response、分类超时不启动回答、各计时只完成一次；设备结构化终态可以在分类后结束，不伪造 generating/saving。
- [ ] 运行 `go test ./internal/gateway -run 'Test(ChatStages|ChatRoundBudget|ChatStageRevocation)' -count=1`；预期缺帧、缺整轮 deadline 或授权断言失败。
- [ ] 在获取会话 turn lease 前开始整轮 context 和 queue 计时；Console沿用已有生成上限300秒作为整轮上限，local设备分类原有60秒子上限保留。分类/生成子 context 不得延长父 deadline；保留 transport 当前更短限制。服务器匿名 trace ID 关联各次调用；阶段发布经过 `WithSession`，闭包只做短发布，不运行推理或文件读写。
- [ ] 运行上述定向测试及 `go test ./internal/gateway ./internal/core -count=1`。验证 existing publication ordering、queued revocation、busy socket revoke、write deadline 测例仍通过。
- [ ] 提交本任务差异：`feat: expose authenticated chat progress stages`。

## Task 3: C3 前端严格阶段协议与等待状态

**Files:** Modify `go-agent/web/src/chat/control-types.ts`, `chat-state.ts`, `chat-state.test.ts`, `go-agent/web/src/pages/ChatPage.tsx`, `ChatPage.test.tsx`; Create `go-agent/web/src/chat/chat-stage.ts`, `chat-stage.test.ts`。

**Interfaces:** 消费 C2 帧；新增 `parseChatStageFrame(value:unknown):ChatStageFrame|null`。`ChatStageFrame` 所有字段必填；reducer 的 stage action 携带当前 epoch、revision、session ID、request ID 及 stage，只在 sending 接受。

- [ ] 写解析及 reducer 测试，核心断言：
  ```ts
  expect(parseChatStageFrame({ type: 'chat_stage', stage: 'generating' })).toBeNull()
  expect(transition(sending, staleIdentityAction)).toEqual(sending)
  expect(transition(done, delayedStageAction)).toEqual(done)
  ```
  ChatPage 测试验证有效帧更新 `role="status"`、旧帧/缺 ID 帧不覆盖、终态/取消清除阶段，连接尚未确认时只显示“正在连接”。
- [ ] 在 R/go-agent/web 运行 `npm test -- src/chat/chat-stage.test.ts src/chat/chat-state.test.ts src/pages/ChatPage.test.tsx`；预期缺解析器或阶段文案断言失败。
- [ ] 实现严格 parser 和 reducer；原状态区域分别显示“正在排队”“正在准备”“正在理解请求”“正在生成回答”“正在保存”。错误沿用现有可恢复/未知终态，不自动重发；不以计时器轮播阶段。
- [ ] 运行同一测试及 `npm run typecheck`；键盘、读屏状态只在阶段变化时更新，不重复播报每秒计时。
- [ ] 提交 `feat: show server reported chat progress`。

## Task 4: C4 服务端显式普通聊天入口

**Files:** Create `go-agent/internal/gateway/chat_entry.go`, `chat_entry_test.go`, `go-agent/internal/core/session_request.go`; Modify `go-agent/internal/gateway/websocket.go`, `go-agent/internal/core/session.go`, `session_store.go`, `session_event.go`; Test `go-agent/internal/gateway/console_model_contract_test.go`, `console_control_replay_test.go`, `go-agent/internal/core/session_persistence_test.go`。

**Interfaces:** 新增 `clientMessage.Entry ChatEntry` 与 `parseChatEntry`。在 `core/session_request.go` 新增 `(*Session).BindRequestEntry(requestID,content,entry string) error` 和 `core.ErrRequestEntryConflict`；Session与sessionFile添加相同的 `RequestEntries map[string]RequestEntryBinding`，其中绑定只保存 `Entry string; ContentSHA256 string`。在现有turn lease内、replay和上游调用前原子保存；CloneSession及离线事件快照均复制该字段。同ID的内容或入口变化冲突；旧事件无入口按auto。记录随会话保存、保留到会话删除，不独立无限缓存；每会话最多1024个绑定，达到上限要求新会话，不能驱逐后允许旧ID改入口。legacy auto缺ID仍兼容，显式chat必须有ID。

- [ ] 写 `TestExplicitChatUsesOneUpstreamCall`、`TestChatEntryCannotReplayControl`、`TestChatEntryRetryConflictSurvivesReload`；断言：
  ```go
  if explicitChatUpstreamCalls != 1 || autoChatUpstreamCalls != 2 { t.Fatal("unexpected call counts") }
  if explicitChatControlReplayCalls != 0 || haWrites != 0 { t.Fatal("chat entered control path") }
  if !errors.Is(changedEntryErr, core.ErrRequestEntryConflict) { t.Fatal("entry change accepted") }
  ```
  覆盖明确设备命令/否定/恶意 prompt 在 chat 中也不调用设备解释或 proposal；未知 entry 被拒绝，缺省 entry 保持 auto；普通生成不新增自动幂等重试承诺。
- [ ] 运行 `go test ./internal/gateway ./internal/core -run 'Test(ExplicitChat|ChatEntry)' -count=1`；预期调用数、replay 或持久化冲突断言失败。
- [ ] 实现入口绑定，在现有设备 replay **之前**检查；chat 路径跳过 replay 和 classifier，直接进入原普通生成。auto 保留原逻辑。入口只限制可用能力，不改变 provider pin；原设备 confirm HTTP 仍是唯一执行入口。冲突错误使用明确安全码，不能静默换新 ID 重试。
- [ ] 同命令通过，再运行 `go test ./internal/gateway ./internal/core -count=1`；核对旧会话和跨重连 proposal 恢复回归。
- [ ] 提交 `feat: add explicit conversation only chat entry`。

## Task 5: C5 入口选择、语音与重试一致性

**Files:** Modify `go-agent/web/src/pages/ChatPage.tsx`, `ChatPage.test.tsx`, `ChatPage.persona.test.tsx`, `go-agent/web/src/chat/control-types.ts`。语音发送入口使用现有ChatPage测试的语音mock覆盖；独立播放回归文件为 `go-agent/web/src/chat/useVoice.playback.test.tsx`。

**Interfaces:** 请求新增 `entry:'auto'|'chat'`；`requestRef`/`retryRequestRef` 快照增加 entry。沿用唯一 `send(event?,override?)`，转写发送也消费当前入口快照。

- [ ] 写入口 UI 测例，断言：
  ```ts
  expect(sent.entry).toBe('chat')
  expect(retried.entry).toBe(original.entry)
  expect(voiceSent.entry).toBe(selected.entry)
  ```
  覆盖发送中入口不可改、切换身份清空等待/重试、旧会话默认 auto、chat 中不会呈现新设备提案。
- [ ] 运行 `npm test -- src/pages/ChatPage.test.tsx src/pages/ChatPage.persona.test.tsx`；预期请求缺 entry 或 UI 断言失败。
- [ ] 使用现有选择组件提供“自动识别”“普通聊天”；旁文“普通聊天只回答问题，不发起设备操作”。默认 auto，发送时冻结；显式重试复用原入口；发送完成后可以改变下一轮入口。不新增第三种 device 模式。
- [ ] 同测试通过，并运行 `npm run typecheck`、`npm run check:ui-boundaries`。
- [ ] 提交 `feat: bind chat entry to text voice and retries`。

## Task 6: C6 上下文预算测量与无损边界

**Files:** Create `go-agent/internal/chain/chat_budget.go`, `chat_budget_test.go`; Modify `go-agent/internal/chain/llm_steps.go`, `go-agent/internal/gateway/websocket.go`, `go-agent/web/src/chat/control-types.ts`, `go-agent/web/src/pages/ChatPage.tsx`, `ChatPage.test.tsx`; Create `go-agent/internal/gateway/chat_context_benchmark_test.go`。

**Interfaces:** 新增 `ChatBudget{MaxInputBytes int; MaxHistoryMessages int}`、`BudgetResult{History []map[string]string; DroppedPairs int; InputBytes int}` 与 `ApplyChatBudget(system,personality,current string,history []map[string]string,budget ChatBudget)(BudgetResult,error)`。仅按 UTF-8 字节精确计量，不把估算字符数说成 provider token；历史保持完整问答对。新增 `BenchmarkChatContextAssembly(b *testing.B)` 使用下列合成场景，对装配结果计量，不访问网络。

- [ ] 写 `TestChatBudgetPreservesRequiredContext`、`TestChatBudgetDropsOldestPairs`，断言 system/personality/current 原字节不变、不会截断单条消息、旧本地/设备/附件历史仍被过滤。预算不足以容纳必需上下文时返回 `context_budget_exceeded`，不静默裁剪。空人格仍保留 Console 固定 system prompt 的开关语义。
  ```go
  if !bytes.Equal(requiredBefore, requiredAfter) { t.Fatal("required context changed") }
  if result.DroppedPairs != 1 || len(result.History)%2 != 0 { t.Fatal("history pair budget broken") }
  ```
- [ ] 运行 `go test ./internal/chain ./internal/gateway -run 'TestChatBudget' -count=1`；预期缺预算接口。
- [ ] 实现纯预算函数并接入原 `buildCloudHistory` 之后。首批保留已有最多20条历史；字节预算默认禁用（0），只有形成测量结论后才设生产值，不凭猜测截断用户内容。保留已经组装的system/personality来源，不改变固定身份开关。response安全元数据新增 `omitted_history_pairs`，仅在大于0时显示“本轮未包含较早的 N 轮对话”，测试断言通知与实际省略完整对数一致。
- [ ] 同测试通过；运行 `go test ./internal/gateway -run '^$' -bench BenchmarkChatContextAssembly -benchmem`。基准使用合成 0/20 条历史、约 11KiB 与 256KiB 人格，记录实际请求字节和本机装配开销；不能把它说成云延迟改善。真实长上下文合成对照归入 C7，未取得数据则预算保持禁用。
- [ ] 提交 `feat: add explicit chat context budget boundary`。

## Task 7: C7 端到端验收与性能记录

**Files:** Modify `go-agent/tests/consolefixture/main.go`; Create `go-agent/web/e2e/chat-progress.spec.ts`; 原始测量与现场调查仅存本机忽略目录，公开报告仅保留脱敏摘要。

**Interfaces:** 消费 C1–C6；fixture 通过现有装配注入受控 provider，必须覆盖 cloud direct chain，不能把原 offline local chain 视为云路径覆盖。

- [ ] 新增浏览器场景：可控分类/正文延迟、入口选择、排队取消、身份切换、保存失败。断言阶段早于回答、auto/chat 分别 2/1 次模型调用、0 次 HA 写入、未知结果没有自动重试；测试从浏览器提交分别记录首阶段/首可见文字/终态，不把阶段当首 token。
- [ ] 运行 `npm run test:e2e -- e2e/chat-progress.spec.ts`；先确认新场景在缺少对应接线时失败，再只补 fixture 接线。
- [ ] 最终运行 Go `go test ./...`、`go vet ./...`；前端 `npm test`、`npm run build`、`npm run check:ui-boundaries` 和相关 `console.spec.ts`、`control.spec.ts`、`auth-concurrency.spec.ts`。浏览器套件串行使用 18081。受平台限制的 race 检查记录原因，不能冒充已通过。
- [ ] 使用已有诊断凭据读取机制做小批合成对照，不打印密钥、不上传私人历史；先一批确认后再在三个独立时段记录同模型/同输入/usage/连接复用与各阶段。每时段 auto/chat 交错各 3 次，另记合成上下文配置；不后台创建定时任务，不用重复立即采样冒充跨时段。未完成三个时段时标“长期性能待验证”，不报告 P95 或网络细分归因。
- [ ] 在报告中分别列出自动化回归、真实 provider、完整 Console、未部署及尚未测量项目；提交本任务代码与安全报告差异 `test: verify chat latency and progress contract`。

## 交付与后续边界

C1→C2→C3 可以先形成一个可用交付；C4→C5 在前者通过后接入；C6 不依赖入口选择，可独立评审；C7 最后汇总。共享 websocket/ChatPage 文件禁止并行编辑。

SSE、人格摘要、自动改变模型/区域和 hybrid 调优均不在本计划发布范围。它们需要新的证据或独立过滤设计；不能为了减少等待移除全文隐私检查。部署使用主机既有的受保护启动与回滚流程，先核对受保护配置、回滚材料及当前未部署修复，完成现场验收后才把“未部署”改成“已部署”。

自检：设计的分段计时/安全日志→C1–C2；等待 UX→C3；显式入口与设备隔离→C4–C5；预算与长上下文测量→C6–C7；多时段基准→C7；SSE 延后理由如上。以上为待执行清单，文档存在不等于测试已通过。
