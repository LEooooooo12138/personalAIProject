# 人格与管家自动融合实施计划

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

日期：2026-10-12。用户已要求先解释自动融合机制并继续开发。

## Goal

任意新上传的合法人格 MD 无需人工改写、无需角色专属代码，下一次新回复自动采用其人格；普通对话充分体现行为与表达特点，个人管家职责、权限、设备事实和独立确认保持有效。

## Architecture

普通对话继续使用固定 system 与完整人格参考资料一次生成。设备判断保持现状；新增本地人格表达选择器只从人格原文推导受限的通用表达风格，不接收设备事实、不输出任意事实句。服务端根据阶段选择表达，原有事实正文保持完整。操作结果成为持久化、去重的助手消息，界面与朗读共用正文。

Tech stack: Go / React / TypeScript / Ollama。Spec: docs/superpowers/specs/2026-10-12-persona-butler-fusion-assessment.md；本计划的自动适配要求替代其中“新人物暂无合适话术”的限制。

## Global Constraints

- 不修改上传的人格原文；不硬编码角色名或逐角色模板；普通对话不丢弃完整人格。
- 人格数据不提升为 system，不改变意图解析、候选选择、授权、确认或控制状态机。
- 表达推导强制本地；设备名称、状态、目录、历史结果不得发送到云端。
- 所有事实正文由服务端确定。表达失败、超时或非法结果降级为原事实，不能触发重试控制。
- 人格变更只影响后续新消息；已保存正文不可重写，重复业务事件不得追加重复历史。
- 保持会话所有权、FIFO、取消、再次鉴权、云端历史过滤及本地朗读边界。
- 使用现有 component-library-reset 工作树；保留其中无关自动化修改，不提交或覆盖他人工作。
- 遵循先失败测试再实现。模拟、真实本地模型、云端、物理设备验证分别记录。

## Task 1: 本地通用人格表达组件

Files: go-agent/internal/persona/presenter.go and presenter_test.go（新包，无其他文件）。

API:
`type Event struct { Stage string; Fact string }`
`type Presentation struct { Content string; PersonalityRevision string; Version string }`
`func NewPresenter(client inference.Client, model string) *Presenter`
`func (p *Presenter) Present(ctx context.Context, markdown string, event Event) Presentation`

Stage values: clarify, pending, query, already_satisfied, succeeded, failed, unknown, cancelled, expired, executing, recovered.

采用可组合、严格枚举的语气/节奏表达画像（例如 gentle/lively/concise/formal + soft/plain），由完整 MD 自动选择，不按人物名匹配。模型只输出枚举，不允许自由文字；非法 schema 整体回退。每阶段的通用表达不含状态、动作、设备承诺，必须保留传入 Fact 原样连续出现。空文档或失败直接返回 Fact。画像按文档 SHA256 缓存，上限 16，支持并发同文档合并、取消和不超过 6 秒推导时间；失败不长期缓存，避免上次文档污染新文档。请求强制 local、无知识/历史/设备字段。使用现有 Ollama 协议并严检返回。

Tests: 两份相反人格 + 第三份未预设文档自动适配；完整原文进入本地推导但 Fact 不进入模型；全部阶段事实不变；非法多余自由文字/注入回退；缓存、变更、并发、超时、取消、空文档；无执行器依赖。

## Task 2: Gateway 与持久化消息承接

Files: internal/core/control_chat.go/session.go/session_owner.go（按需新 session_event.go）；internal/gateway/console_persona.go（新）、console_control*.go、websocket.go、console_chat.go、http.go；相关 Go 测试。不得改 Task 1 包或 chain。

接入 Task 1 API，Server 及 webChatOptions 共享 Presenter（配置的本地模型）；每次新设备回复读当前 PersonalityContent，读取失败仅回退事实。ControlChatResult 增加 Stage，禁止从字符串猜测状态。

core.Message 增加可选 `event_id`, `personality_revision`, `presentation_version`, `device_result`，深复制 DeviceResult 指针字段。设备消息保持 local 来源。WS 初始回复保存完整正文/元数据/事实并发出同样字段；同 request_id 重放从已有事件读取，不重新生成或操作。

确认、取消、核对结果：业务状态先保存，然后创建助手结果消息。REST 保持原 ControlProposal 所有顶层字段，额外返回 `assistant_message?: { index: number, message: core.Message }`。GET proposal 也可返回已经保存的对应结果（恢复用途，不重新执行）。业务成功但消息持久化失败不变成控制失败；返回 proposal，省略 assistant_message，可附 `assistant_message_unavailable: true`。

稳定事件 ID 由 request_id 或 proposalID + status + observation/error 形成；同事件先读后生成，已有正文不被新人格覆盖。写入须获得同一会话 turn，校验 owner、再次鉴权，去重保存；保存失败重试只保存消息，不执行控制。注意旧/轮数结束会话仍可保存操作结果，避免 BrowserSession(create=false) 丢弃结果。只有已持久化消息可返回索引。POST/GET readback 不自动重试写设备。

事实正文沿用已有 pending/query/already_satisfied/clarify；terminal result 描述状态机观察结论：succeeded 表示读取到目标状态，不等同现场证明；unknown 不声明成功或未执行；cancelled/expired/failed 各自清晰。模型不能改写这些事实。

Tests: 上传切换下一次新回复适用、重放保留原文、terminal 持久化/索引/刷新恢复/去重、DeviceResult 深拷贝与历史兼容、保存失败不重放写、所有权/过期鉴权/并发 turn、cloud route 本地事实隔离；既有 control/security/privacy tests 必须通过。

## Task 3: 确认后回复与朗读前端

Files: go-agent/web/src/api.ts（保留已有无关修改）、chat/chat-state.ts、pages/ChatPage.tsx 及专门测试。不得改 gateway 或无关自动化 UI。

消费 Task 2 的可选 assistant_message envelope，兼容旧 bare proposal。ConsoleMessage/ChatFrame 加可选事件和人格版本字段。终态文字作为助手消息显示，历史恢复保留 device_result；朗读使用其存储正文与服务端 index。

应用结果前校验 epoch/revision/sid、proposal 归属和 event_id；同 event 只显示/自动朗读一次。仅当返回 index 与本地历史相符时追加。遇到间隙或同时聊天导致冲突，只在安全 idle 时读取权威历史，不能覆盖未完成 user turn，不能误朗读错误 index。卡片更新不依赖语言字段；超时未知和核对不重试控制。

Tests: confirm/cancel/reconcile 新正文、相同响应重复、切换会话/登录、与聊天交错、legacy response、刷新、准确 TTS 参数；typecheck/build。

## Task 4: 普通人格融合与质量验收

Files: internal/chain/assistant_prompt.go、assistant_prompt_test.go、assistant_identity_live_test.go、必要合约测试；人格说明文案与本计划/验收记录。

删除每个能力回答强制管家首句与固定能力分组，只有身份询问明确服务身份。完整人格影响情绪回应、建议关注点、表达异议、兴趣、社交距离和节奏；角色的技术弱项不削弱系统能力。虚构履历与现实职责分离。不要硬编码人物名；不强制口癖/emoji。

保留消息角色/知识/历史隔离回归。真实本地模型用生产参数验证完整当前文档 + 至少两份相反人格，多轮与未用于调提示词的任务；记录实际输出与局限。云端调用须有目标专属授权，本轮不假设；不操作物理设备。

## Execution and review

Task 1/2/3 的接口已冻结，可按文件所有权并行；Task 4 与接口无写冲突。全量测试前完成集成。各任务先测试再实现，记录证据；完成后独立评审接口、并发/安全、人格质量。只在验证实际完成后报告结果，部署另记真实运行版本与回滚证据。

## Review Focus

自动适配任意新文档是否只靠枚举泛化仍足够自然；保存失败与已执行状态是否被混淆；旧会话、索引、并发消息是否错位；人格是否进入设备决策/云端；完整 MD 是否每轮替换、历史是否保持不变。
