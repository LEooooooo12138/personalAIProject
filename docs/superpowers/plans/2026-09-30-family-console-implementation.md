# 家庭本地控制台 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在家庭本地交付独立账号、本人聊天、共享设备与知识、管理员建议确认和成员管理的中文响应式前端。

**Architecture:** Vite＋React＋TypeScript 构建静态文件，由现有单实例 Go 服务提供。新增独立 console 鉴权和 DTO 层，复用 Owned 会话、RAG、采集、建议确认与输出过滤；HA、模型和全部业务数据留在家庭本地。

**Tech Stack:** Go 1.26.4、Gin、gorilla/websocket、现有 x/crypto/argon2、React、TypeScript、Vite、React Router、普通 CSS；测试使用 Go testing、Vitest、Testing Library、Playwright 和现有 Node/Python 回归。

**Spec:** [家庭控制台设计](../specs/2026-09-30-family-console-design.md)；[本地部署与未来云扩展](../specs/2026-09-30-family-console-cloud-deployment.md)。

**Status:** 设计基本方案已认可；本计划待审阅和执行方式确认。尚未编写前端或账号产品代码。

## Global Constraints

- 首版前端、Go 服务、账号、Vault、会话、HA 采集及归档均在家庭主机运行和持久化，沿用现有本地 Ollama，不引入云模型依赖。
- 云服务器、公网 DNS、穿透隧道与云模型不作为首版完成前提；不向 ECS、OSS 或外部模型服务复制业务数据。
- 生产静态文件和 API 由同一个 Go 服务提供；无需 Node 生产进程，不依赖外部 CDN、在线字体或在线图标。
- 前端源码 `go-agent/web/`，产物 `go-agent/static/console/`，入口 `/app/`，API 前缀 `/api/console/v1`。
- 新家庭聊天 `channel="console"`，`OwnerID="console:"+userID`；管理员普通聊天也仅限本人，不进入全局语义索引或 personal/_memory 沉淀。
- 登录绝对有效期 7 天；用户名为 3–32 位 ASCII 字母、数字、下划线和连字符并按小写唯一；显示名 1–40 个 Unicode 字符；密码 12–128 个字符且 UTF-8 不超过 512 字节。
- Argon2id 参数 64 MiB、3 次迭代、4 线程；随机盐不少于 16 字节，摘要 32 字节。实施时先核对当前已锁定 x/crypto 版本 API。
- 页面可见时每 30 秒刷新快照；2 倍采集间隔内为 fresh，超过为 stale，不存在或时间异常为 unknown。
- 390px 手机宽度和 1440px 桌面宽度验收；中文界面，浅色背景、深色正文、蓝绿色主色，首版无主题切换。
- 八荣八耻适用于所有任务；接口依据、失败用例、最小修改、回归结果与审查意见记录到任务证据。不得以测试 mock 的能力冒充真实设备或生产能力。
- 原 P1/P2 修复尚未提交/合并；先记录基线，保留全部已知改动。只提交本轮可明确归属的文件或补丁，不能为分任务提交而覆盖或混入旧修改。

## Review Focus

1. 多标签页改密/退出、旧请求或长生成迟到：旧账号内容不得写入新账号 UI，已撤销 WS 不得继续发送（Task 1、2、5、6）。
2. 恶意、损坏或遗留数据：账号文件损坏不能触发 bootstrap；旧 HA 归档和缓存不能绕过共享知识边界（Task 0、3）。
3. 云要求搁置后本地启动失败：路径不可用、磁盘满或 SIGTERM 不得损坏已保存会话，恢复账号备份需撤销登录（Task 2、8）。
4. 家庭共享撤回、建议包含隐藏实体：列表、数量、正文、RAG 和缓存均遵循同一授权，不能只删一个响应字段（Task 3、4、7）。
5. 绑定生成新 ID、确认回复丢失、陈旧 applying：页面不确认旧版本，不在加载或重连时自动重放 HA 写入（Task 4、7、8）。

## 执行准备和模块边界

复用已附加工作区 `C:/Users/Admin/.codex/worktrees/p1-p2-remediation/personalAIProject`。执行前核对 attached artifacts、分支和工作区状态，保存当前差异清单，建立本轮任务证据。不要新建另一套缺少 P1/P2 修改的 checkout。

新 `internal/console` 包只负责账号、登录会话和共享策略，不能反向依赖 gateway/core。core 负责初始化与生命周期，gateway 负责 HTTP/WS 授权与调用现有业务对象。前端按功能组织在 `web/src/features/`，共享 API/身份逻辑放 `web/src/lib/`，视觉组件放 `web/src/components/`。

下文 `gateway/`、`core/`、`vault/`、`smarthome/` 文件简称均相对于 `go-agent/internal/`，`web/` 相对于 `go-agent/`；Go 命令在 `go-agent/` 执行，npm 命令在 `go-agent/web/` 执行。

建议顺序：Task 0 → 1 → 2；Task 3 与 Task 4 可在接口固定后并行；Task 5 可在 Task 1 契约确定后开展，Task 6/7 接入实际后端；Task 8 统一验收。涉及 http.go、app.go、config.go、websocket.go 和 CI 的修改串行合并。

## Task 0: 可持久化的账号、登录与共享策略

**Files:** 创建 `go-agent/internal/console/{types,password,store,auth,sharing}.go` 及对应 `_test.go`；修改 `go-agent/go.mod` 仅将已使用 x/crypto 依赖标为直接依赖（确有需要时）。

**Interfaces:**

- `OpenStore(dir string, now func() time.Time) (*Store, error)`；目录必须已存在可写，只有状态文件不存在表示未初始化。
- `User{ID, Username, DisplayName, Role string; Disabled, MustChangePassword bool}`；哈希不在公共 User 中。
- `Principal{UserID, Role, SessionID string; MustChangePassword bool}`；`Login{Token, CSRFToken string; ExpiresAt time.Time; Principal Principal}`。
- `Bootstrap(username, displayName, password string) (User,error)`；`Authenticate(username,password string) (Login,error)`；`Resolve(token string) (Principal,error)`；`CSRFToken(token string) (string,error)`。
- `Logout(token string) error`；`ChangePassword(token,oldPassword,newPassword string) error`；`CreateMember(username,displayName string) (User,string,error)`（第二返回值为临时密码）；`UpdateMember(id,displayName string,disabled bool) (User,error)`；`ResetMemberPassword(id string) (string,error)`；`ResetAdminPassword(password string) error`；`ListMembers() ([]User,error)`；`Initialized() bool`。
- `Sharing{Revision uint64; EntityIDs, SuggestionIDs []string}`；`GetSharing() (Sharing,error)`；`PutSharing(expectedRevision uint64, entities,suggestions []string) (Sharing,error)`，只存明确 ID，不解释设备权限。
- `BindSession(ctx context.Context,token string) (context.Context,func(),error)` 将撤销/过期与 HTTP/WS 取消关联，注册时在同一锁内复核有效性。
- `RevokeAllSessions() error` 原子撤销全部登录并取消已注册请求，供 Task 8 的离线恢复工具使用，不新增浏览器端点。
- 包级错误 `ErrUninitialized`、`ErrUnauthenticated`、`ErrForbidden`、`ErrConflict`、`ErrInvalid`、`ErrUnavailable`、`ErrBusy` 使用 errors.Is 判断，分别映射 503/401/403/409/422/503/429；HTTP 语法错误独立为 400。

- [ ] 写失败测试：`TestBootstrapRejectsCorruptState`、`TestConcurrentBootstrapCreatesOneAdmin`、`TestLoginSurvivesRestart`、`TestPasswordChangeRevokesAllSessions`、`TestSharingRevisionConflict`。断言第二次 bootstrap 冲突、磁盘无明文密码/原始 token、改密后所有旧 token 被拒绝、旧 revision 不覆盖新策略。
- [ ] 运行 `go test ./internal/console -count=1`，记录缺失实现造成的预期失败。
- [ ] 实现上述接口，所有状态写入通过串行事务和同目录临时文件原子替换，持久化成功后发布内存状态。写失败不返回成功；随机性失败拒绝操作。
- [ ] 哈希参数解析接受固定已支持格式并限制资源，错误账号使用同成本验证；最多 2 个并发哈希，超额返回可映射的 busy 错误。限流归 Task 1。
- [ ] 实现 7 天过期、临时密码改密标记、禁用/改密/重置撤销、重新启用不复活旧会话；禁止通过成员 API 修改 admin。实现 typed errors 供网关映射。
- [ ] 复跑包测试，并用真实临时目录重启 Store 验证，不以纯内存 mock 代替持久化测试。独立审查通过后记录证据和任务提交边界。

关键断言示例（省略临时目录与错误检查准备，实际测试必须检查）：

```go
_, err := store.Bootstrap("owner", "主人", "A-strong-local-password") // 首次已成功
if !errors.Is(err, ErrConflict) { t.Fatalf("second bootstrap: %v", err) }
if err := store.ChangePassword(login.Token, "A-strong-local-password", "Another-local-password"); err != nil { t.Fatal(err) }
if _, err := store.Resolve(login.Token); !errors.Is(err, ErrUnauthenticated) { t.Fatalf("old login survived: %v", err) }
```

## Task 1: 本地登录、管理员初始化与服务端权限

**Files:** 创建 `gateway/console_auth.go`、`console_routes.go`、`console_members.go` 及测试；修改 `core/config.go`、`core/app.go`、`gateway/browser_auth.go`、`gateway/http.go`、`config/agent.example.yaml`（均位于 `go-agent/`）。

**Interfaces:** 消费 Task 0 Store。新增 `ConsoleConfig{Enabled bool; DataDir,PublicOrigin string; AllowInsecureHTTP bool}` 和可配置 `ServerConfig.ListenAddress`。新增独立 console principal 上下文，不能通过旧 `principal.Admin` 赋予控制台账号全局权限。

`auth/status` 返回 `{initialized}`；login/me 返回 `{user:{id,username,display_name,role,must_change_password},capabilities:[],csrf_token}`；写请求头为 `X-CSRF-Token`。password 输入 `{old_password,new_password}`；logout/password 成功 204。成员列表 `{members:[]}`，创建和重置只在成功响应附 `temporary_password`；创建请求仅 `{username,display_name}`，修改仅 `{display_name,disabled}`。bootstrap 输入 `{username,display_name,password}`，reset-admin 输入 `{password}`，这两者始终位于旧管理 Bearer 域。

- [ ] 写失败测试 `TestConsoleAuthHTTP`：表驱动覆盖主设计第 7 节 auth/members 路由；匿名 401、成员 admin 操作 403、受限改密账号可读 me 和改密但业务 API 拒绝。
- [ ] 写 `TestConsoleOriginAndCSRF`：错误 scheme/host/port、缺少写请求 Origin 或 CSRF 拒绝；HTTPS origin 即使 Go 收到反代 HTTP 也发 Secure cookie；cookie 无 Domain，伪造 X-Forwarded-Proto 不改变行为。
- [ ] 写 `TestConsoleBootstrapRequiresManagementKey`：空/空白/占位密钥启用时失败、旧 Bearer 才可初始化/恢复、并发只创建一次、损坏存储不能走初始化。
- [ ] 运行 `go test ./internal/gateway ./internal/core -run Console -count=1` 记录失败。
- [ ] 按设计实现 auth、me、password、logout、成员管理及旧 `/internal/console/bootstrap`、`/reset-admin`。登录 JSON 返回公开身份和 CSRF，令牌仅 Set-Cookie；错误统一 snake_case DTO，不返回原始内部错误。
- [ ] 中间件显式分流 console 路径，关闭时返回未启用；legacy cookie/Bearer 契约回归保持。成员临时密码仅当次返回；账号字段校验与 Store 一致。
- [ ] 登录按来源 IP 和规范用户名组合限制为 5 分钟内最多 10 次尝试，来源 IP 总计 5 分钟最多 60 次；桶数有界且过期回收，不信任未配置代理的任意转发 IP，超额 429。验证未知账号同样受限。
- [ ] 配置监听地址，默认兼容原服务，控制台本地启动文档使用回环地址；局域网直接访问需显式配置。复跑 gateway/core 包及旧身份测试，独立审查。

## Task 2: 家庭会话隔离、撤销与可靠保存

**Files:** 创建 `gateway/console_chat.go`、`console_sessions.go` 及测试；修改 `gateway/websocket.go`、`core/session_store.go`、`core/session_owner.go`、`core/agent.go`、`core/app.go`；新增 `core/console_session_test.go`、`session_persistence_test.go`。

**Interfaces:** 新 `/sessions` 与 `/sessions/:id/messages` 固定 console channel 并调用 Owned 接口；WS `/chat/ws` 使用 Task 1 认证及 Task 0 BindSession。为既有 WS 内部引入 server-only `webChatOptions{ChannelID,VaultName string; IndexMessages bool; ValidateSession func(context.Context) error}`，旧入口保留旧选项，新入口固定 console/agent/false。不允许客户端提交这些选项。

列表返回 `{sessions:[{id,started_at,last_active_at,round_count,message_count,preview}]}`，详情返回 `{id,messages:[{role,content,timestamp}]}`。这里的 id 是 WS 使用的 SID，不是带 channel 前缀的内部存储键；只在服务端转换，测试覆盖二者不同。WS 沿用 `session/response/error` 协议，HTTP 统一错误 DTO 不改变 WS 既有帧格式。

- [ ] 写 `TestConsoleCrossDeviceOwnerIsolation`：A 两个登录可读同一 SID，B 和 admin 猜该 SID 均 404；读不存在 SID 同样 404，IO 故障 500。
- [ ] 写 `TestConsoleRevokesIdleAndBusySockets`：退出、禁用、改密和过期分别关闭空闲/生成中连接；握手与撤销并发不能漏登记，最终输出不得跨撤销边界。
- [ ] 写 `TestConsoleExcludedFromGlobalIndexAndSedimentation`：通过真实结束消费者结束 console 会话，断言没有全局索引记录或 personal/_memory 文件，并最终 CompleteSession/释放资源。
- [ ] 写 `TestSessionAtomicSave`：先保存有效历史，再注入写入/替换失败，旧文件仍可冷读；关键目录不可写初始化失败而不是仅 warning。保留旧会话格式兼容。
- [ ] 运行针对测试记录失败；最小重构 WS 复用既有轮次、锁、过滤与持久化，不复制聊天引擎。会话关闭与登录退出分开，退出不删除历史。
- [ ] 在 session index 的写入和启动重建都排除 console；统一结束消费者跳过 console 沉淀仍完成状态迁移。会话文件原子写，初始化失败按核心持久存储错误处理；不将单个历史损坏伪装成不存在。
- [ ] 回归已有 `shared_turn`、`session_recovery`、FIFO、轮次/取消测试并运行 core/gateway 全包。独立审查。

## Task 3: 共享知识与 HA 运行资料的统一边界

**Files:** 创建 `gateway/console_knowledge.go`、`vault/content_policy.go` 及测试；按责任修改 `vault/filesystem.go`、`vault/reader.go`、`vault/embedding.go`、`vault/inverted.go`、`core/app.go`、`smarthome/automation.go`。报告当前由 `smarthome/store.go:SaveReport` 保存为 JSON，不新增 Markdown 报告生成器。

**Interfaces:** vault 包定义 `NewContentPolicy(vaultRoots map[string]string,excludedAbsDirs []string) (ContentPolicy,error)` 与 `ContentPolicy.Allows(vaultName,relPath string) bool`。新增 `NewFileReaderWithPolicy(personalPath,agentPath string,policy ContentPolicy) *FileReader` 和 `NewScopedEmbeddingStoreWithPolicy(infer EmbedClient,vr Reader,vaultDir,vaultName,model string,logger *zap.Logger,policy ContentPolicy) *EmbeddingStore`；旧构造器使用空排除策略。生产 App 在创建索引/Reader 前计算受管理 HA 运行目录关系；metadata 过滤仍单独强制，空排除策略不能取消 metadata 检查。

Search 返回 `{results:[{path,title,snippet}],count}`，Page 返回 `{path,title,body}`。私人问答输入 `{question}`，使用 server-fixed personal/wiki-query/model，返回 `{answer,sources:[{title,path,score}]}`，不接受客户端改变 Vault；首版请求无跨轮历史。导入复用 `{content,source_title}` 与 `{message,steps}` 契约。

- [ ] 写 `TestConsoleKnowledgeVisibilityAcrossEntrypoints`：公开页可检索/阅读，损坏和 internal 页不可读；路径穿越、根外符号链接、无权路径均不返回正文或绝对路径。
- [ ] 写 `TestRuntimeArchiveExcludedAfterCacheReload`：将有唯一秘密的旧无标签 HA 归档写入临时运行目录，建立旧缓存后启用策略，验证 BM25、向量候选、触发实体、正文与实际模型上下文均不包含该事实；同目录外合法页仍可用。
- [ ] 执行 vault/chain/gateway 定向测试确认失败。
- [ ] 复用严格 ParsePage 与 IsInternalPage，补齐不自带过滤的 ReadIndex/ReadPage/Status 调用边界。旧 internal API 保持其既有管理语义；新 console 公开查询固定 agent，不接受绕过用途的 vault 参数。
- [ ] 排除策略以规范路径为基础，覆盖缓存版本/失效判断与来源回读；运行目录等于整个 Vault 时配置失败。新规则 Markdown 标 internal；现有 JSON 报告由运行目录策略保护，未来若生成 Markdown 报告须标 internal。已有归档按目录策略保护，不批量重写用户文件。
- [ ] 管理区 private 搜索/问答/导入使用 server-fixed personal 与现有 chain；问答独立于 console 会话，无私有原文进入前端持久缓存。sources 只返回安全相对路径。
- [ ] 完成唯一事实/实际上下文正反向测试、动态触发实体与双 Vault 原回归，独立审查。

## Task 4: 家庭设备、建议投影与采集可观测状态

**Files:** 创建 `gateway/console_smarthome.go`、`console_sharing.go`、`console_home.go` 及测试；新增 `smarthome/collection_status.go`；修改 `smarthome/store.go`、`collector.go`、`manager.go`。

**Interfaces:** Store 增加 `LatestSnapshotWithMetadata() (Snapshot,error)`，`Snapshot{UpdatedAt time.Time; Entries []EntityState}`，旧 LatestSnapshot 兼容委托；Collector 增加 `Status() CollectionStatus` 返回加锁快照，包含进程内尝试/阶段状态及已知批次计数。消费 Task 0 Sharing，复用 Manager 的 Bind/Confirm/Ignore/TriggerAnalysis。

`CollectionStatus{Phase,ErrorCode string; LastAttemptAt,LastSuccessAt,LastFailureAt,WindowStart,WindowEnd,Checkpoint *time.Time; CompletedBatches,TotalBatches int}`；Phase 取 `idle`、`snapshot`、`history`、`failed`，未发生事件的时间为 nil，不用零日期代替未知。

devices 返回 `{devices:[],count,snapshot_at,poll_interval_seconds,freshness}`；suggestions 返回 `{suggestions:[],count}`。home 返回 `{recent_sessions:[],visible_device_count,visible_suggestion_count,collection_summary?}`，其中 collection_summary 仅 admin 可见。admin 的 bindings/confirm/ignore/analyze 请求体与现有对应业务接口一致，避免第二套自动化语法。collection 返回 `{phase,last_attempt_at,last_success_at,last_failure_at,error_code,window_start,window_end,completed_batches,total_batches,checkpoint}`，未知时间为 null；字段分别代表完整采集运行与当前窗口，不用快照成功冒充历史成功。

- [ ] 写 `TestConsoleDeviceProjection`：member 默认 []，共享后仅指定实体和允许字段；新实体不自动加入；home 计数一致；快照时间恰好 2 倍间隔为 fresh，超过为 stale，未来/缺失为 unknown。
- [ ] 写 `TestConsoleSuggestionAllEntitiesAuthorized`：仅显式共享且 confirmed、所有实体可见的建议出现；遗漏 presence/关联端/旧无法解析结构时整条隐藏，包括计数、标题与错误字段。
- [ ] 写 `TestCollectionSnapshotSuccessHistoryFailure`：模拟当前状态保存成功、第二历史批失败，快照时间更新但 checkpoint 不推进，重启后未知的过程时间不伪造。
- [ ] 写 `TestConsoleBindingsAndConfirmation`：member 越权不调用 HA；绑定返回新 ID、旧 ID 确认 409；新 ID 独立确认、并发重试不重复写 HA。分别覆盖 422 与 502。
- [ ] 运行相关测试记录失败；实现小写 DTO、[] 空数组、后台安全错误说明，不返回任意 attributes/绝对路径/Token/原始 HA 请求。
- [ ] 新共享请求校验 entity_id 和 suggestion ID，建议须可分享；原子 revision 冲突不覆盖策略。撤回共享对下一请求立即生效，member DTO 不使用未筛选描述。
- [ ] 采集进度只读观察现有 collect 流程，不改变历史窗口/去重/时间边界；轮询页面不触发 HA 采集。admin 分析调用既有流程，绑定/确认逻辑仍唯一。
- [ ] 复跑 smarthome/gateway 全包及 P1/P2 历史与确认回归，独立审查。

## Task 5: 前端构建、登录和响应式页面框架

**Files:** 创建 `go-agent/web/{package.json,package-lock.json,tsconfig.json,vite.config.ts,index.html}`、`src/{main.tsx,App.tsx,styles.css}`、`src/lib/{api,auth}.ts(x)`、`src/components/`、`src/features/auth/`、`src/features/members/`；创建网关静态资源路由和测试，避免继续扩大 http.go。

**Interfaces:** `apiFetch<T>(path:string,init?:RequestInit):Promise<T>` 使用同源 cookie、写操作 CSRF、标准错误 DTO 和 AbortSignal；`AuthProvider/useAuth` 提供当前身份、能力、登录状态、logout 和身份 epoch。依赖 Task 1 实际契约，不在前端保存管理密钥。

- [ ] 核对 React/Vite/测试工具官方运行要求，锁定相容稳定版本和 Node engines；为测试安装必要工具，禁止生产远程 CDN 依赖。
- [ ] 写组件/行为失败测试：登录失败、must_change_password、me 刷新、成员误入 admin 路由、退出清空缓存、旧账号异步响应丢弃。URL 跳转不能允许外部 redirect。
- [ ] 实现 Vite base `/app/` 与代理 HTTP/WS，开发 origin 显式配置；Go 静态 fallback 只覆盖页面路由，缺失 JS/CSS 和 API 保持真实 404。
- [ ] 按主设计完成桌面侧栏、手机三主入口、公共空态/错误/加载组件；成员管理支持创建、禁用/启用、重置及临时密码仅当次展示，成功重置后从界面移除敏感值。
- [ ] Markdown 渲染关闭 HTML、限制链接协议、默认不加载远程图片；可用受维护组件，但不能加入允许任意 HTML 的配置。
- [ ] 验证 `npm run typecheck`、`npm test -- --run`、`npm run build`，执行静态路由 Go 测试。独立审查。

## Task 6: 普通聊天、跨设备历史与真实 fixture

**Files:** 创建 `web/src/features/chat/{ChatPage,SessionList,Composer,MessageList}.tsx`、`chat-client.ts` 与测试；创建 `go-agent/tests/consolefixture/main.go`、`web/playwright.config.ts`、`web/e2e/chat.spec.ts`。

**Interfaces:** 使用 Task 2 WS 的 session/response/error 帧与 HTTP 历史，state 与 account ID/epoch、expectedSID 同时关联；首次新会话由服务器发 SID。fixture 创建临时账号/Vault/HA 数据、模拟 inference/HA，复用真实 gateway 和 Store，不连接实际配置或家庭服务。

- [ ] 写失败行为测试：新会话、列表恢复、另设备恢复、长生成、生成失败保留 SID、旧 SID 404 恢复、切换会话后的迟到 404 不清新 SID、退出后的迟到回答不展示。
- [ ] 写输入测试：中文组合输入 Enter 不发送、Shift+Enter 换行、空白不发送、提交中禁用重复发送；后端 FIFO/跨连接锁仍由 Task 2 回归验证。
- [ ] 实现完整回答模式与“正在生成”、断线状态和显式重试。发生不确定提交时先读历史，禁止重连时盲目重发；草稿只在当前身份内存中保留。
- [ ] fixture 仅监听回环并在临时目录运行，拒绝加载用户 agent.yaml；可控延迟/故障场景只存在于 fixture 进程，生产不得注册测试端点。
- [ ] Playwright 使用两个账号和同账号两个独立 browser contexts 验证实际 Go cookie/历史/WS；不能仅用前端 route mock 宣称全链路通过。
- [ ] 执行组件、浏览器与原挂件行为测试，390px/1440px 检查聊天布局、长代码块、键盘焦点。独立审查。

## Task 7: 设备、自动化审核、共享知识与私人管理页面

**Files:** 创建 `web/src/features/{home,devices,suggestions,knowledge,admin-knowledge}/` 各页面、API 适配和测试；新增 `web/e2e/{sharing,suggestions,knowledge,members}.spec.ts`；扩展 Go fixture 场景。

**Interfaces:** 使用 Task 3、4 的 DTO 和已有建议结构字段；suggestion detail 来源于全量管理员列表并按 ID 匹配，不新增未设计的任意编辑接口。前端按钮能力由 auth/me 和当前真实状态共同决定，后端仍再鉴权。

- [ ] 写失败测试：设备未配置/首次等待/无共享/陈旧/请求失败各有不同 UI；只在页面可见时 30 秒轮询，离开页面 abort 并取消定时器。
- [ ] 写 `bindings-create-version.spec`：从模拟分析生成建议出发，选择 presence，保存后断言新 ID、旧 superseded，且 HA 写入次数仍为 0；再次明确确认才写入并归档，使用 fixture 服务读出的调用次数核验。
- [ ] 写 409/422/502、旧 applying、丢失确认响应后刷新的测试；无自动 confirm、无页面刷新重试写入，“忽略”不表示停用已安装规则。
- [ ] 实现结构化建议详情与分步操作，未知/不支持条件不能确认；展示数据依据、HA 时区和受影响实体，不从自然语言生成请求配置。
- [ ] 完成共享知识检索与正文、私人问答来源、文本导入目标提示；私有问答不存入普通聊天历史，退出清空。成员账号不能通过地址或开发者请求访问私人服务。
- [ ] 完成共享白名单 revision 冲突处理、计数更新与撤回即时生效。由后端投影 member 的确认结果；前端不自行过滤原始完整 HA payload。
- [ ] 测试 Markdown XSS/恶意链接、长实体名、空结果、键盘对话框和移动端操作，复跑浏览器集成测试并独立审查。

## Task 8: 本地运行可靠性、全体验收和文档

**Files:** 修改 `.github/workflows/check.yml`、`go-agent/Makefile`、`STARTUP.md`；新增 `docs/family-console-local-run.md`、`docs/family-console-acceptance.md`、`tests/integration/test_console_local.py`、`go-agent/cmd/console-recovery/main.go` 及测试；修改 `core/app.go` 的平台退出处理，新增 `core/shutdown_unix.go`、`core/shutdown_windows.go` 及对应测试。

**Interfaces:** 本地启动使用已配置 origin 和 console.data_dir，保留现有 Ollama 配置。运维文档给出初始化 API 的安全调用方式、受信任 LAN HTTP 与本机/家庭 HTTPS 的区别，不将云参数或实际家庭秘密写入仓库。

离线恢复工具 `go run ./cmd/console-recovery --data-dir <已恢复的账号目录> --revoke-sessions` 只在应用停止时使用，调用 Task 0 Store 的 RevokeAllSessions，不启动 HA/推理客户端，不打印账号令牌，不接受缺失或损坏状态为可初始化状态。平台函数 `shutdownSignals() []os.Signal`：Windows 返回 os.Interrupt，Unix 额外返回 syscall.SIGTERM。

- [ ] 写进程级失败测试：Linux SIGTERM 触发现有退出链、在途操作取消并冷读有效历史；本地关键目录缺失/不可写不能报告业务就绪；Windows 构建不依赖 syscall 的 Linux 专属常量。
- [ ] 写 `TestConsoleAvailableWhenDependenciesOffline`：模拟 Ollama 与 HA 均不可达，静态入口和本人已保存历史仍返回 200，生成/分析返回安全依赖错误，设备页显示带时间的旧快照；不因外部依赖故障重置账号或历史。
- [ ] 实现并验证目标平台停机处理、自启说明、日志容量限制、持久目录布局。启用控制台初始化必须显式区分空状态与损坏状态，不自动迁移或覆盖用户现有文件。
- [ ] 在临时目录执行一致性备份/恢复演练；首版提供停止应用后备份与验证步骤，不引入未验证的热备份。恢复后、重新启动前执行离线撤销工具；验证账号/会话/checkpoint 可读、所有旧登录失效、旧自动化操作不自动重放。
- [ ] 在 CI 配置固定 Node 工具链、`npm ci`、typecheck、组件测试、build 和 Chromium fixture 测试；保留旧 Node/Python 和 Go 检查。新 CI 的步骤需能在本地复现。
- [ ] 运行 `go test ./... -count=1`、`go vet ./...`、`go build ./cmd/agentd`；Linux/CGO 环境运行 `go test -race ./... -count=1` 和构建；运行原 Python 离线 fixture 与 Node 挂件测试。
- [ ] 运行前端 typecheck/test/build/Playwright；浏览器检查手机与桌面全部主流程并保存临时 fixture 截图作为证据，明确模拟数据来源。
- [ ] 仅本地模拟压力场景验证 10 个账号、3 个并发推理请求、索引与 HA 采集并行时无 OOM/越权/乱序；报告实际时延，不将模拟推理时延当作真实模型性能。
- [ ] 运行差异检查，核对每项设计能映射到实现和测试。新鲜独立审查覆盖整轮差异，并与原 P1/P2 基线比较，修复本轮引入的问题。
- [ ] 验收文档逐项记录命令、实际结果、未验证的家庭设备/真实 HA 动作/断 WAN 和未来云扩展。提供可打开的本地页面及启动命令，不宣称已部署到家庭主机或子域名。

## 计划自审与执行建议

覆盖映射：账号/权限→Task 0/1；聊天/数据隔离→Task 2/6；知识→Task 3/7；设备/建议→Task 4/7；页面与成员管理→Task 5/7；本地可靠性与整体证据→Task 8。云扩展保留在补充文档，不作为任务依赖。

Store 不依赖 core/gateway；所有新身份和角色只来自鉴权上下文。共享策略由 Store 持久化、服务端业务适配验证与投影，前端不复制授权判断作为安全边界。Task 5/6/7 以实际后端 DTO 为准，遇到契约差异先更新双方测试与设计，不在前端猜接口。

建议使用子代理分任务实施并独立审查；同文件修改由主代理串行整合。每项任务完成后检查失败到通过的证据，最后做整体验收。实施开始前需要用户审阅这份计划并选定执行方式；不要求再次确认已认可的技术栈、独立账号或本地部署方向。
