# 家庭控制台 React/Vite 首版执行计划

依据：已确认的 `2026-09-30-family-console-design.md`、原实施计划 Task 5–6，以及 2026-10-08 用户明确要求设计并编译 HTML + React + Vite 前端。延续家庭本地运行；本轮不部署、不操作真实 HA。复用已完成 Task 0–2 的后端。

## 全局约束与设计

- 保留现有 Go 架构、账户与会话所有权、人工确认及隐私过滤；不得用管理密钥调用家庭页面。
- 中文、浅色、深灰文字、蓝绿色主色；首页欢迎区、开始对话入口和真实最近对话。桌面左侧导航，手机底部导航；管理员额外显示成员入口。优先系统字体和本地 SVG。
- 当前页面：登录、首次强制改密、首页、对话与历史、账号、管理员成员。知识库、设备和建议依赖尚未完成的家庭 API，暂不显示对应入口，也不展示虚构状态/演示数据。
- 能力入口解释：对话页面同时承担生成和本人历史。只有 `sessions:read` 时保留“对话记录”入口，隐藏生成操作；两项能力都缺失时隐藏并拒绝该页面。模型不可用不能导致本人已保存历史不可阅读，沿用原设计的离线行为。
- 独立账户共享家庭服务，管理员也只能读取本人对话。只使用 `/api/console/v1` 的真实接口；认证 Cookie HttpOnly，CSRF 只在内存，写入通过同源 fetch 自动 Origin 和 `X-CSRF-Token`。
- 草稿、前端身份对象、临时口令及聊天内容只保存在内存，不写 localStorage/sessionStorage。会话深链只携带后端公开的 opaque SID，服务端仍核对本人所有权；SID 不另写浏览器存储。认证 Cookie 由 Go 设置为 HttpOnly，前端不得读取。身份变更清空私有状态并关闭 WS；所有异步结果必须核对请求身份代次。跨标签退出只广播失效信号。
- Vite base `/app/`，生产输出 `go-agent/static/console/`。由 Go 提供页面和资源；SPA 只回退明确的页面导航，失踪资源/API 必须保持真实错误状态。
- Markdown 禁用原始 HTML、危险链接和远程图片，外链带安全 rel；不使用 CDN、外部字体或浏览器直连模型。
- 每步记录八荣八耻检查、RED/GREEN 证据和独立审查。本轮新增改动与既存未提交修复分开评审，不重置、不混合提交、不推送。

## 已核对接口

GET `auth/status` → `{initialized}`；POST `auth/login` → `{user,capabilities,csrf_token}`；GET `auth/me` 同上；POST `auth/logout`/`auth/password` → 204，改密后必须重新登录。

User: `{id,username,display_name,role,must_change_password}`；成员列表额外有 `disabled`。GET `admin/members` → `{members}`；POST → `{user,temporary_password}`；PATCH `admin/members/:id` 只接受 `display_name` / `disabled`；POST `:id/reset-password` → `{temporary_password}`。

GET `sessions` → `{sessions:[{id,started_at,last_active_at,round_count,message_count,preview}]}`；GET `sessions/:id/messages` → `{id,messages:[{role,content,timestamp}]}`。

WS `chat/ws`：浏览器自动 Cookie 与 Origin；发送 `{session_id,content}`，空 SID 由服务器新建，依次收到 `session` 和完整 `response`；`error` 含可选 `code,message,session_id`。明确 `session_not_found` 或对应历史 404 可清除对应 SID，其余错误保留。后续同一 WS 会忽略消息 SID，所以切换会话必须关闭并重建 WS。

错误为 `{error:{code,message,request_id}}`，显示中文安全提示与可复制请求 ID，不直接渲染服务器正文。未初始化或未启用须给出真实状态。

### Task 0: 基线与契约

保存受影响源文件基线、验证现有 Go 测试、读取真实后端契约与官方库文档。建立专用 SDD 进度记录。不得读取用户 agent.yaml 或 HA 密钥。记录既存修改。

### Task 1: 前端工程、认证、布局及成员

所有权：`go-agent/web/`（除 Task 3 后续聊天文件）；新增 `.gitignore` 精确忽略 node_modules 和测试运行产物。不得编辑 Go 后端或 CI。

实现 Vite + React + TypeScript 工程、锁文件、HTML 入口、严格类型检查、Vitest/jsdom/Testing Library；选取实际 npm 已发布版本并记录 Node 要求。`npm run build` 类型检查后生成 `../static/console`，脚本包括 dev/typecheck/test/build。另预留 Playwright 测试依赖。dev 不用绕过同源认证，文档建议编译后由 Go 服务提供。

实现 API 层（Cookie、CSRF、AbortSignal、204、类型化错误）、AuthProvider（启动 me、登录、注销、强制改密、失效处理、身份代次、跨标签退出、focus/visibility 校验）、安全私有布局、首页真实最近对话、账号改密、管理员成员列表/创建/改名/禁用/重置口令。能力不足不显示对应入口；家庭成员手动访问管理页也不能呈现私有内容。

路由可复用轻量 React Router，支持 `/app/`、`/app/chat`、`/app/account`、`/app/members`。Task 3 替换聊天占位页，其余页面不得伪造未实现功能。首页开始对话的提示只填草稿、不自动发送。

视觉：首页有短欢迎语、突出的新对话卡、日常使用提示和最近对话；登录独立居中卡片和家庭助手图形；桌面侧栏、页眉账户头像；移动端清楚易点，无横向滚动；加载/空态/错误/禁用样式完整。表单有 label，弹窗键盘焦点和关闭后恢复焦点，尊重 reduced-motion。

测试先失败再实现：fetch 同源/CSRF/204/错误；登录失败与初始化；强制改密不露出主页面；普通成员直接管理路由被拒绝；退出/账户切换丢弃迟到 me/私有数据；管理员创建/禁用/重置真实 DTO。临时口令只在结果弹窗内存展示，关闭即清除。

独立审查两项结论：需求符合度、任务质量。记录命令/结果、文件、八荣八耻逐条核对，不做混合提交。

### Task 2: Go 静态入口与真实离线 fixture

所有权：`go-agent/internal/gateway/console_static.go` 与测试、`http.go` 必要路由一行、`go-agent/tests/consolefixture/main.go`。如公共静态路径授权需要调整，仅改相关判断并测试；不修改 console 身份/权限业务。

`/app` 重定向 `/app/`；`/app/` 和已知页面路径返回已编译 index；JS/CSS/本地资源按正确类型返回。未知文件、资源缺失、API 路径不返回 index。限制 GET/HEAD，阻止目录遍历、目录枚举、源码和 symlink 越界；未编译时清楚 503，不泄露源路径。index 不缓存；内容哈希资源可长缓存。入口须未登录可访问，并不放宽 API/旧路由鉴权。CSP 只允许本地脚本/样式/图像/连接（浏览器 WS 同源需覆盖），nosniff 和禁止 iframe。安全路由不能受请求内存资源路径动态污染。

fixture 为 standalone 测试命令：只绑定 loopback，临时目录放账户/Vault/会话，Go 真实 console store/HTTP/WS/历史，注入确定性 chat chain 返回 Markdown，不读取任何真实 config，不连接 HA/模型。创建已改密的 owner/alice/bobby 账户，固定仅用于 fixture 的口令 `fixture-password-123`，另保留需要首次改密的临时账户以测闭环。支持 flag 指定 loopback port（默认 18081），正确 origin、graceful shutdown、清理 temp。从 go-agent 工作目录读取已编译静态产物，不注入生产测试端点。

RED/GREEN 测试：未登录页面入口成功/API仍401；嵌套已知页面；文件类型；丢失资源与 API 不回退；POST/path traversal/symlink 越界；缺少构建产物；disabled console仍返回正确 API 错误。运行该任务 Go 测试和 fixture build。

每步八荣八耻与独立审查。与 Task 1 文件完全分离，可并行；本调度决定沿用用户允许的独立任务并行授权。

### Task 3: 私有聊天与真实浏览器行为

依赖 Task 1/2，所有权：前端 chat 组件/lib/chat-client、必要 App 路由接线与样式、Playwright 配置/e2e；禁止修改后端业务。

支持创建对话、真实历史列表/阅读/恢复、WS 完整答案、生成中、断线、重试。以身份代次和 SID 核验消息；会话切换关闭旧 WS、取消旧历史；无凭据入 URL/存储，深链 SID 仅是资源标识。只有匹配的历史 404 或 session_not_found 清当前 SID；失败/过载/未知错误保留 SID。新 SID 从服务器 session 帧取得，禁止客户端自行生成；响应账户失效清空页面。网络断开不自动重发；已提交但结果未知时先获取历史，显示明确状态，然后由用户选择重试，避免重复发送。

发送按钮生成时禁用；Enter 发、ShiftEnter 换行、IME composition 不发送，空内容不发；draft 只在当前身份内存。Markdown 用禁 HTML/禁图像/过滤协议渲染。提供滚动和可达标签；界面适配 390px 和 1440px。

Vitest：迟到历史 404/响应不清新 SID、不显示前账户；异步回收；IME/空发送/重复点击；一般 error 保留 SID；不安全 Markdown。Playwright 必须走真实 Go fixture Cookie/CSRF/WS，不得全部 API mock：至少登录/强制改密、管理员成员创建与普通账号拒绝、两用户不同上下文隔离、同用户另一设备历史、退出后页面清理、深链刷新、390px 与1440px无横向溢出。可单独测试迟到响应时用定向mock，核心闭环仍真实后端。

独立需求/质量审查后合并接线。

### Task 4: 编译、验收和交付

补充 CI 的 npm ci/typecheck/Vitest/build/Playwright（真实 loopback fixture）和 Node 兼容版本；保留旧测试。生成生产产物，不运行真实 agent 配置；留下本地 fixture 预览供用户核对（说明它是离线示例）。

实际跑 TypeScript/Vitest/生产编译/真实 Playwright、Go 全套/vet/build、旧 widget 行为和 Python fixture、diff 校验。必要时 Linux/CGO race；若环境不支持真实浏览器需坦诚报告，不能将测试清单说成通过。

查看桌面和手机实际截图，检查页面层次、对齐、可读性、溢出、焦点、空态；发现问题修正后针对性回归。最终独立整体审查，修复明确缺陷后完成。

写 `go-agent/web/README.md` 本地开发/编译/运行与已实现界限（开启console、bootstrap见已有文档，勿写真实密钥）；成果和测试证据存 `docs/family-console-frontend-results-2026-10-08.md`。更新旧计划对应进度为部分完成，勿将未实现 Task3/4/7/8 标全完成。


> 2026-10-09 反查更新：公共知识、管理员私人知识、建议审核/共享与采集页面的旧承诺已纳入本轮补全；“本阶段暂缓”不代表永久移除。当前范围、设备全家只读覆盖规则与HA授权续期以 `docs/superpowers/specs/2026-10-09-family-console-completion-design.md` 和对应实施计划为准，实际完成证据等待本轮验收报告。
