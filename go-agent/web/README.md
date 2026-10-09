# 家序家庭控制台

HTML + React + TypeScript + Vite，由 Go 同源提供 `/app/`，运行时无需 Node 或外部 CDN。使用本地 SVG、系统中文字体和安全 Markdown。HA、模型、家庭账号与知识仍保留在家庭主机；云部署和子域名入口留作后续扩展。

本轮页面包括独立登录/首次改密、首页与分项服务状态、本人对话与历史、区域→设备→实体、公开知识、明确共享的自动化结果、我的和成员管理。管理员另有 personal 私人检索/正文/单次问答/文本导入、建议绑定新版本后独立确认、忽略、结果共享及采集/最近14天分析。

公开知识是现有网页聊天也能使用的 agent 资料，不是家庭私有库。personal 仅管理员可读；私人问答只保留当前页面，不写普通聊天历史，导入不自动公开。设备页面只读；ignored 不等于停用规则，confirmed 不等于持续监测规则启用。结果未知的写请求不会自动重发。

## 构建与运行

使用 Node 24（当前基线24.16.0）及锁文件；PowerShell 可用 `npm.cmd`。

```sh
cd go-agent/web
npm ci
npm run typecheck
npm test
npm run build
```

产物在 `go-agent/static/console/`，须完整保留 HTML/JS/CSS 并由 Go 提供。正式运行配置见 [本地初始化](../docs/console-local-setup.md)。不要覆盖已有账号与会话目录。管理密钥与 HA 凭据仅由受保护服务端使用，不能进入前端、URL或日志。

浏览器 Origin 须与 `console.public_origin` 严格一致；Cookie 为 HttpOnly。需要同源 SharedWorker，认证、私有请求与 WS 经本地 worker 协调；不支持时失败关闭。身份、CSRF、草稿、私人答案和临时口令只驻留内存，退出、改密、身份 epoch 更换后清空。管理员不能读取他人对话。

## Vite 开发代理

默认不代理未知后端。需要热更新时显式设置本地后端 origin；不含路径或凭据：

```powershell
$env:CONSOLE_DEV_TARGET = 'http://127.0.0.1:18083'
npm.cmd run dev -- --host 127.0.0.1
```

代理只包含 `/api/console/v1`（含 WS）和 `/app/auth-worker.js`，不开放 `/internal`。保留浏览器 Origin；开发 Go 服务的 `console.public_origin` 必须是实际 Vite origin（如 `http://127.0.0.1:5173`），本地 HTTP 仍须显式启用。请使用独立开发配置/临时账号，避免将正式服务的 Origin 改成开发地址。目标变量不是 `VITE_*`，不作为客户端配置公开；本示例没有令牌。正式及账号验收优先采用构建后的 Go 同源入口。

## 离线 fixture 与验证

```sh
cd go-agent
# 真实 Go gateway/account/Reader/Chain/SmartHome manager，外部依赖均为临时mock
go run ./tests/consolefixture -port 18081
```

访问 `http://127.0.0.1:18081/app/`。临时账号 owner/alice/bobby/familyreader，密码均 `fixture-password-123`，禁止用于真实部署。fixture 的 HA 注册表/历史/规则安装及 Ollama tags/inference 全部使用回环模拟服务，聊天仍是明确的确定性离线回答；不读取真实配置或凭据，不操作真实设备。知识与建议页面流程通过真实 Go handlers/Reader/Chain/Manager。

```sh
cd go-agent/web
npm test
npm run typecheck
# 串行集成时先准备构建/服务，再由根协调运行
npm run test:e2e
npm run build
```

CI 用 Chromium；已有 Chrome 可设 `CONSOLE_BROWSER_CHANNEL=chrome`。浏览器脚本不针对真实家庭服务。单测替代 fetch；SharedWorker、多标签 Cookie 和完整 Go 接线须另跑浏览器验收，不能仅以单测宣称通过。构建/E2E 与现场验收状态以本轮结果报告为准。

设计依据：[2026-10-09 完整修订](../../docs/superpowers/specs/2026-10-09-family-console-completion-design.md)。没有即时设备控制、任意规则编辑/停用、私人知识自动发布或云部署。本地自动刷新仅读取状态，30秒缓存并不等于刷新 HA 授权；HA授权失效不让家庭账号退出。触控目标44px是本产品目标，仍须实际布局/焦点/对比验收。