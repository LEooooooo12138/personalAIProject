# 家序前端可靠性修复 Implementation Plan

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复本轮审查的5项工程缺陷，使请求、会话、控制状态、设备语义与成员弹窗符合既有安全及业务契约。

**Architecture:** 保留Console与Go服务边界，拆分Worker业务等待与Cookie串行通道；以兼容HTTP前置条件保护并发身份。聊天继续使用现有reducer与消息索引，提议读取单独实现，设备状态复用共享纯函数。

**Tech Stack:** 现有React、TypeScript、Vite、Vitest、Playwright、Go；不新增依赖。

**Spec:** [主方案版本2](../specs/2026-10-10-frontend-reliability-and-visual-design.md)，本计划实现第4节。组件库和全页面重做见[组件库计划](2026-10-10-frontend-visual-plan.md)与[组件库规格](../specs/2026-10-10-component-library-design.md)。R1—R5为独立缺陷修复，不承担UI库建设。

## Global Constraints

- 全面重做组件库与所有现有界面；保留家序品牌、中文信息、现有导航目的地、路由参数与能力边界。
- 使用现有React、TypeScript、Vite、Vitest、Playwright和Go；本轮不新增UI、图表、动画、状态管理依赖。
- 组件库只依赖React、原生HTML和视觉资源，不反向依赖认证、API、请求Hook、业务状态或页面。
- 保留AuthProvider、coordinator、useFamilyQuery、useConsoleAction、chat-state、useVoice的现有职责；仅R1—R5及明确列出的计时增量修改相应业务行为。
- 浏览器只访问同源Console；保留Cookie、CSRF、身份epoch、权限与提议所有者检查。
- 设备动作与自动化安装继续需要明确确认；结果未知时不自动重发写操作；共享继续默认关闭。
- 复用现有业务能力、SafeMarkdown和SVG资产；基础UI以新契约重新建立，不让旧CSS继续主导新组件。
- 状态必须有明确文字；图形与颜色辅助判断。安装、配置、连接、数据新鲜度和实际执行结果分别表达。
- 正文16px、辅助文字至少14px、主要触控目标至少44px；这些字号和触控尺寸属于项目标准。
- 全部迁移页面的普通文字及占位符对比度至少4.5:1；支持200%文字放大，关键内容与操作保持可用。
- 动态进度采用原生progress、语义化HTML及CSS类，保持现有CSP；不引入CDN、内联脚本或模拟波形。
- 按组件族及页面逐批迁移，每批有交互验证和回退点；工程缺陷先补能够失败的回归再修复。

实施代码根目录用变量`PRODUCT_ROOT`表示，必须解析为基线提交84b5ebba90964857a4986f1055a2bc1f599aec17或已核对其后续改动的隔离工作树中的`go-agent`。下列文件路径均相对此目录。实施前记录git status与HEAD，不覆盖他人变更。

## Review Focus

- 认证超时／页面取消后迟到Set-Cookie：R1必须证明未提前开放后续登录。
- 旧新Worker版本与冻结标签混用：R1必须拒绝绕过屏障，保留可理解的恢复入口。
- 达轮数上限同时切换会话／播放结束：R2必须丢弃旧音频和旧回复回调。
- 执行状态监听隐藏后恢复、多个附件指向同一提议：R3必须去重、有界且不重发confirm。
- 历史DTO缺字段及成员一次性密码迟到：R4正确降级，R5不得丢进另一成员弹窗。

## 任务地图

R1独立；R2独立；R3独立但整合验证使用R1；R4独立；R5独立。它们可与组件库建设独立推进；同文件修改顺序整合，成员Dialog迁移在M5，对话在M6，规则摘要在M8。每个任务完成即可单独审阅。以下提交步骤仅适用于用户后续授权实施时；本次只交付方案。

### Task R1: 普通请求取消、并发与身份屏障

**Files:**
- Modify: `web/public/assets/console-auth-worker.js`、`web/src/coordinator.ts`、`web/src/api.ts`、`web/src/auth.tsx`、`web/src/App.tsx`。
- Modify: `internal/gateway/console_auth.go`、`internal/gateway/console_auth_test.go`、`internal/gateway/console_security_test.go`。
- Test: `web/src/coordinator.test.ts`、`web/src/auth.test.tsx`、`web/src/api.test.ts`、`web/e2e/auth-concurrency.spec.ts`。
- Create/Test: `web/src/console-auth-worker.test.ts`，在VM中执行实际Worker文件，使用可控制fetch和两个port。

**Interfaces:**
- Consumes: `coordinatedFetch(url: string, options: RequestInit, mutation: boolean, expected?: {userId: string; csrfToken: string} | null): Promise<Response>`及`openChatSocket(identity: Identity, signal: AbortSignal): Promise<WebSocket>`，保留对外签名。
- Produces: Worker协议version 2；内部`requestDeadlineMs(url: string, method: string): number`；普通请求、取消标记和socket租约均按port＋ID索引，Cookie mutation保持单一串行通道。
- Produces: 可选请求头`X-Console-Expected-User`与已有`X-CSRF-Token`联合作为服务端身份前置条件；不匹配为401 / `identity_changed`；auth/me、auth/status不绑定旧身份。

- [ ] **Step 1:** 在实际Worker VM新增测试`cancelled_query_does_not_block_another_port`：挂起query，另一port读取status应完成，cancel后query的signal.aborted为true，迟到reply不恢复pending。再测试两个port相同ID互不取消、bye清理所属请求、identity预检查同样可取消。
- [ ] **Step 2:** 运行`npm test -- --cache=false src/console-auth-worker.test.ts`（web目录）。确认旧实现因无signal或全队列阻塞失败，不把测试超时当唯一证据。
- [ ] **Step 3:** 在Go新增`TestConsoleExpectedIdentityPrecondition`：旧用户头＋新Cookie读取返回401且handler未执行；相同用户不同CSRF也拒绝；正确前置条件成功；无新头旧客户端行为保留；非GET既有Origin/CSRF检查仍生效。运行`go test ./internal/gateway -run 'TestConsoleExpectedIdentityPrecondition' -count=1`确认失败。
- [ ] **Step 4:** 修改consoleMiddleware：只在新头存在时附加用户与令牌比对，复用Resolve/CSRFToken和constant-time令牌比较；保留所有原权限检查。复跑Step 3命令，预期通过。
- [ ] **Step 5:** 实现Worker独立请求执行和真实abort；ordinary read=15000ms、business write=60000ms、三个指定长业务路径=310000ms、identity check=10000ms。Cookie变更保持串行，开始时封住业务入口、取消旧请求、等待5秒ACK，派发及回包再查generation；取消不等于服务端撤回。Worker从expected构造身份头，不信任调用方覆盖。
- [ ] **Step 6:** 为Cookie写入保留直到网络结算的占位；10秒等待提示只改变UI反馈，不能释放通道。保留AuthProvider先清除私人界面与既有revocationRef逻辑，App在等待状态显示上述提示；业务超时文案与未知写结果一致，无自动POST重试。
- [ ] **Step 7:** 增加握手10秒关闭逻辑：打开即可释放；连接中取消须关闭并确认close后释放；无法确认关闭时协调失败关闭。保持Worker URL/name，worker与coordinator版本同时改为2，版本不兼容显示“请关闭旧标签后重新打开”。
- [ ] **Step 8:** 补测试：`late_cookie_blocks_new_login`、`ack_timeout_prevents_cookie_write`、`connecting_socket_close_before_cookie_mutation`、`stale_generation_response_ignored`、`old_worker_version_rejected`、`ordinary_timeout_is_not_a_write_retry`。用延迟代理在真实多标签测试中制造旧请求与新Cookie交叉，断言后端前置条件拒绝，页面无新账号私密数据回填旧界面。
- [ ] **Step 9:** web目录运行`npm test -- --cache=false src/console-auth-worker.test.ts src/coordinator.test.ts src/auth.test.tsx src/api.test.ts`、`npm run build`、`npm run test:e2e -- e2e/auth-concurrency.spec.ts`；PRODUCT_ROOT运行`go test ./internal/gateway ./internal/console -count=1`。预期全通过，fixture自然退出；若清理挂起记录为未解决，不将人工终止当清理通过。
- [ ] **Step 10:** 仅暂存本任务实际改动并提交`fix(console): cancel business requests without bypassing identity barriers`。记录后端先行、前端后行的兼容发布次序。

### Task R2: 新会话与朗读索引一致

**Files:**
- Modify: `web/src/chat/chat-state.ts`、`web/src/pages/ChatPage.tsx`。
- Test: `web/src/chat/chat-state.test.ts`、`web/src/pages/ChatPage.test.tsx`、`web/src/chat/Voice.test.tsx`。
- Test: `internal/gateway/session_recovery_test.go`、`internal/gateway/console_assistant_test.go`，补充新SID持久化与消息索引契约；`web/e2e/completion.spec.ts`。

**Interfaces:**
- Consumes: `transition(state: ChatState, action: Action): ChatState`和现有`serverSession` / `response` / `structured`动作，签名不变。
- Produces: 仅sending中的旧SID→新SID保留pending用户消息、清空proposals；epoch/revision不变；null→SID保留首次消息，同SID幂等。
- Produces: ChatPage的SID变更同时取消旧语音与控制读取，保持当前请求响应有效；手动与自动TTS继续使用现有SID＋index＋content hash契约。

- [ ] **Step 1:** 写`rollover_preserves_only_pending_turn`：旧问答2条，send后serverSession(newSID)，response后messages.length===2且新回复index===1，proposals为空，revision未变。补null→SID、同SID、非sending换SID及过期epoch/revision用例。
- [ ] **Step 2:** web运行`npm test -- --cache=false src/chat/chat-state.test.ts`，旧实现应在length／旧提议断言失败。
- [ ] **Step 3:** 修改transition的serverSession分支，保留末尾当前pending用户消息的真实timestamp，不重建发送时间；保留本轮phase和pending，忽略不合法换SID。
- [ ] **Step 4:** ChatPage处理服务器换SID时取消旧录音／音频任务、旧控制controller及错误缓存，更新URL而不触发重新选择；随后回复仍由原epoch/revision接收。
- [ ] **Step 5:** 补页面测试`rollover_tts_uses_new_session_index_one`，分别验证手动／自动朗读一次、新回答可正常接收、旧音频完成回调无效；补达MaxRounds的新SID Go持久消息断言和E2E，不在生产配置中修改MaxRounds，使用独立fixture。
- [ ] **Step 6:** web运行`npm test -- --cache=false src/chat/chat-state.test.ts src/pages/ChatPage.test.tsx src/chat/Voice.test.tsx`、`npm run typecheck`、`npm run test:e2e -- e2e/completion.spec.ts`；Go运行`go test ./internal/gateway -run 'Session|Assistant|Speech' -count=1`，预期通过并能证明新SID消息索引。
- [ ] **Step 7:** 提交`fix(chat): align rollover messages and speech with the new session`，记录无需消息ID迁移、旧历史保留。

### Task R3: executing提议的有界读取与恢复

**Files:**
- Create: `web/src/chat/useProposalRefresh.ts`。
- Modify: `web/src/pages/ChatPage.tsx`、`web/src/chat/ControlCard.tsx`。
- Create/Test: `web/src/chat/useProposalRefresh.test.tsx`。
- Test: `web/src/pages/ChatPage.test.tsx`、`web/src/chat/ControlCard.test.tsx`、`web/e2e/control.spec.ts`。

**Interfaces:**
- Consumes: `requestConsole<T>(path, options)`、`isControlProposal(value)`、现有proposal action与AuthProvider epoch/signal。
- Produces: `useProposalRefresh({epoch, revision, sid, proposals, signal, isCurrent, onProposal, onUnauthorized})`，其中proposals为`Record<string, ControlProposal | null>`，`isCurrent: () => boolean`，`onProposal: (proposal: ControlProposal) => void`，`onUnauthorized: () => void`。
- Returns: `{ refresh: (id: string) => Promise<void>, staleIds: ReadonlySet<string>, errors: Readonly<Record<string,string>> }`；refresh只GET。ControlCard增加可选`refreshing?: boolean`、`refreshError?: string`和`onRefresh?: (id: string) => Promise<void>`；现有onReconcile语义不变。

- [ ] **Step 1:** 写`restored_executing_reaches_terminal_by_get_only`：初始executing，GET先executing后succeeded，页面显示目标状态，所有自动请求method===GET，confirm调用数为0。覆盖丢失confirm响应后手动核对读到executing的入口。
- [ ] **Step 2:** web运行`npm test -- --cache=false src/pages/ChatPage.test.tsx src/chat/ControlCard.test.tsx`，旧实现应缺终态更新／refresh入口而失败。
- [ ] **Step 3:** 实现hook的执行队列：立即GET、前15000ms间隔1000ms、随后3000ms、60000ms停止；同ID不重叠、全页并发≤2、重复提议去重。绑定epoch/revision/SID，返回记录必须ID和session_id一致；401调用现有expire，404移除当前提议引用并明确不可读取。
- [ ] **Step 4:** 页面隐藏中止请求和timer，恢复可见先GET并按当前executing状态重启有界周期。失败停止本周期；显示读取错误和刷新入口，保留服务端状态，不伪造unknown或failed。切SID、身份或卸载立即清理。
- [ ] **Step 5:** ControlCard为executing显示“刷新状态”；60秒未获终态显示“暂未取得最新结果”。保留unknown的核对流程：GET确认仍unknown后才手动reconcile POST；确认仍只在pending且具能力时可用。
- [ ] **Step 6:** fake timers验证`stops_at_sixty_seconds`、`hidden_pauses_and_visible_reads`、`duplicate_attachment_reads_once`、`two_request_limit`、`old_sid_response_ignored`、`unknown_manual_reconcile_only`；E2E控制fixture由executing异步进入succeeded/unknown，断言无额外服务调用。
- [ ] **Step 7:** web运行`npm test -- --cache=false src/chat/useProposalRefresh.test.tsx src/pages/ChatPage.test.tsx src/chat/ControlCard.test.tsx`、`npm run typecheck`、`npm run test:e2e -- e2e/control.spec.ts`，预期通过；既有设备授权、过期、取消、重放用例保持通过。
- [ ] **Step 8:** 提交`fix(chat): recover executing proposals with bounded read-only refresh`。

### Task R4: 安全设备元数据与共用状态formatter

**Files:**
- Modify: `internal/smarthome/control_types.go`、`internal/smarthome/control.go`。
- Test: `internal/smarthome/control_test.go`、`internal/gateway/console_control_test.go`。
- Create: `web/src/entityState.ts`；Create/Test: `web/src/entityState.test.ts`。
- Modify: `web/src/familyViews.tsx`、`web/src/chat/control-types.ts`、`web/src/chat/ControlCard.tsx`。
- Test: `web/src/Areas.test.tsx`、`web/src/chat/ControlCard.test.tsx`、`web/e2e/control.spec.ts`。

**Interfaces:**
- Produces: Go DeviceQueryResult新增`Domain string`、`DeviceClass *string`、`Unit *string`，JSON分别为`domain,omitempty`、`device_class,omitempty`、`unit,omitempty`，已有四字段不变。
- Produces: TS DeviceResult对应可选`domain?: string`、`device_class?: string | null`、`unit?: string | null`，isDeviceResult校验所有存在的字段；旧记录合法。
- Produces: `type EntityStateInput = {state: string | null; domain?: string | null; device_class?: string | null}`；`formatEntityState(input: EntityStateInput): string`位于entityState.ts。
- Consumes/Adapts: `entityStateLabel(entity: EntityView): string`调用共享formatter；`deviceStateText(result: DeviceResult): string`替代原无类型state参数，旧DTO的有效entity_id前缀仅提供domain。

- [ ] **Step 1:** 写聊天卡测试：binary_sensor door on显示打开、tamper on显示检测到防拆、未知类别on原样保留；旧sensor on不得显示开启。区域与聊天对相同类别的label相同。
- [ ] **Step 2:** web运行`npm test -- --cache=false src/chat/ControlCard.test.tsx src/Areas.test.tsx`，确认旧无条件on/off翻译导致失败。
- [ ] **Step 3:** Go测试查询及before/after只添加domain、经safeDeviceClass过滤的类别和字符串unit，无attributes；未知类别不透传；旧JSON反序列化和既有持久记录兼容。运行`go test ./internal/smarthome ./internal/gateway -run 'Control' -count=1`，先确认新字段断言失败。
- [ ] **Step 4:** 后端从已授权实体前缀与GetState结果投影字段，不添加第二次HA读取或目录扫描；复用safeDeviceClass，unit按已有目录投影约定。不要改变target/action白名单。
- [ ] **Step 5:** 提取formatter原有语义，统一unknown/unavailable/null分支；查询卡和控制before/after改传完整对象，unit独立文本显示。isDeviceResult拒绝错误类型元数据；无法核对类别时保留原值，技术详情可见原始状态。
- [ ] **Step 6:** entityState测试覆盖light/switch/fan/input_boolean、door/window/opening、tamper、motion/occupancy/presence、未知domain、旧DTO与畸形entity_id；不基于名字判定有人、负载或设备种类。补E2E门磁查询。
- [ ] **Step 7:** web运行`npm test -- --cache=false src/entityState.test.ts src/chat/ControlCard.test.tsx src/Areas.test.tsx`、`npm run typecheck`、`npm run test:e2e -- e2e/control.spec.ts`；Go运行`go test ./internal/smarthome ./internal/gateway -run 'Control|Catalog' -count=1`，预期通过，序列化字段范围符合Step 3。
- [ ] **Step 8:** 提交`fix(control): share verified device state semantics across console views`。

### Task R5: 成员弹窗的操作代次与敏感结果

**Files:**
- Modify: `web/src/pages/MembersPage.tsx`；Create/Test: `web/src/pages/MembersPage.test.tsx`。
- Test: `web/e2e/console.spec.ts`。

**Interfaces:**
- Consumes: MembersPage现有consoleApi成员调用、auth.epoch/isCurrent与create/rename/reset接口，调用和接口保持；不因UI重置强行换请求Hook。
- Produces: 页面局部dialog generation＋mounted检查；每次打开/关闭/离开更新代次，操作捕获成员ID和代次；等待中关闭／切换被锁定。

- [ ] **Step 1:** 写`pending_reset_cannot_replace_another_member_dialog`与`pending_rename_cannot_close_new_dialog`：延迟响应，尝试Escape／遮罩／切换应保留当前等待界面；路由卸载再打开页面不接收旧结果。双击操作计数为1。
- [ ] **Step 2:** web运行`npm test -- --cache=false src/pages/MembersPage.test.tsx`，确认旧弹窗可关闭／切换并被迟到响应影响。
- [ ] **Step 3:** create/rename/reset提交时捕获generation、member ID、epoch及mounted；只允许匹配操作更新结果。在等待状态禁用关闭、Escape和切换入口，显示离开不会撤销的说明；结果与失败后恢复可操作。敏感临时密码不得跨身份／页面保存。
- [ ] **Step 4:** 补一次性密码归属、失败输入保留、账号切换隐藏密码、确认完成后可关闭及焦点返回触发按钮的用户交互测试；沿用服务端角色检查，不改权限。
- [ ] **Step 5:** web运行`npm test -- --cache=false src/pages/MembersPage.test.tsx`、`npm run typecheck`、`npm run test:e2e -- e2e/console.spec.ts`，预期通过并且成员管理原有路径仍正常。
- [ ] **Step 6:** 提交`fix(members): keep async results bound to the submitting dialog`。

## 子项目出口

- [ ] PRODUCT_ROOT记录HEAD、git status及改动文件；web运行`npm test -- --cache=false`、`npm run build`。
- [ ] PRODUCT_ROOT运行`go test ./internal/gateway ./internal/console ./internal/smarthome ./tests/consolefixture -count=1`。
- [ ] web运行`npm run test:e2e`；runner自然结束和退出状态必须记录，fixture清理问题交给组件库计划Q2完成后才能整体验收。
- [ ] 逐项核对设计第4节与R1—R5，记录新增测试数、未验证真实服务和残余问题；不得仅复制上一轮184／27的结果。
- [ ] 审阅身份前置条件与部署次序、SID索引、未知写结果、设备投影、临时密码归属；提供小范围diff和实际验证证据。

## 自审记录

已覆盖审查5项工程问题；HTTP并发后的身份竞态通过可选后端前置条件补齐；Cookie取消与业务取消区别明确。R3GET刷新与现有unknown reconcile POST区分；R4兼容旧记录且供组件库计划M8复用。真实设备、收费语音及生产认证不由fixture替代。本文件为实施计划，未标记任何步骤完成。
