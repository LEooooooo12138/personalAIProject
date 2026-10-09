# 家庭控制台本地初始化与接口验收

本阶段提供账号、本人聊天和历史 API，2026-10-08 起由 React/Vite 首版接入登录、改密、首页、聊天和成员管理，编译与页面运行见 [前端说明](../web/README.md)。Go 服务、账号和会话保存在家庭主机，不需要云服务。现已接入区域与设备只读页面、本机 HA/Ollama 状态和普通聊天；现场行为及无人值守部署仍需另外验收。示例配置默认关闭控制台。

先在自己的配置中设置：

```yaml
server:
  listen_address: "127.0.0.1:8080"
  internal_key: "${AGENT_INTERNAL_KEY}"
console:
  enabled: true
  data_dir: "./data/console"
  public_origin: "http://127.0.0.1:8080"
  allow_insecure_http: true
```

保留原有 inference 和 Vault 配置。`AGENT_INTERNAL_KEY` 必须是已在服务进程环境设置的有效管理密钥；应用不会自动加载 `.env`。账号目录必须与两个 Vault 及 `./static` 分开，不能放入它们内部或包含它们，目录别名和符号链接也会检查。配置相对路径沿用服务启动工作目录。仅单个 Go 进程可使用同一账号目录。

首次使用示例路径时，从 `go-agent/` 工作目录显式预先创建账号目录：`New-Item -ItemType Directory -Path ./data/console`，然后按现有 [STARTUP.md](../../STARTUP.md) 启动服务。已有目录可跳过此步骤；不要覆盖、删除或重建其中已有的 `state.json`。配置指向不存在的账号目录会阻止启动；服务不会自动创建空账号库，以免掩盖目录误配或存储丢失。

`listen_address` 是完整的主机和端口。留空时保持原来的 `:port` 行为。家庭主机本机访问使用回环地址；局域网访问需显式改为该主机的局域网地址或 `0.0.0.0:8080`，并把 `public_origin` 改为浏览器实际使用的完整 Origin。HTTP 仅在显式允许时启用。HTTPS 反向代理配置仍使用浏览器端的 HTTPS Origin，后端即使收到 HTTP 也发 Secure cookie。任意转发头不会改变 cookie 属性或登录限流来源 IP；本阶段没有受信代理配置。

`public_origin` 必须已经是浏览器序列化后的形式；可在浏览器控制台读取 `location.origin` 核对。服务端不自动改写配置：scheme 和 DNS 主机名必须小写，HTTP 默认端口 `:80`、HTTPS 默认端口 `:443` 必须省略，非默认端口保留且不用前导零，结尾不带 `/`。例如用 `https://family.example`，不用 `https://Family.Example:443`；用 `http://[::1]`，不用 `http://[0:0:0:0:0:0:0:1]:80`。

本阶段支持普通 ASCII DNS 名（字母、数字和标签内部连字符）、规范四段十进制 IPv4，以及小写、压缩、带方括号的 IPv6。为避免浏览器 URL 解析差异，拒绝 Unicode/IDN/Punycode 主机名、尾点 DNS 名、缩写/八进制/十六进制 IPv4、IPv4 映射 IPv6 和 zone ID；遇到这些格式请使用该家庭主机的普通 ASCII 名或规范 IP。格式不符合要求时启动报错并提示检查 `location.origin`，不会等到登录时才失败。运行时仍要求请求 Origin 与配置完全相等。

在本机 PowerShell 中初始化管理员。使用交互提示读取口令，避免把密码写入命令参数、脚本文件或历史。管理密钥仅用于运维命令，不进入前端代码、浏览器存储或家庭账号。

```powershell
$consoleAdminCredential = Get-Credential -Message '输入管理员用户名及新密码（12–128 个字符）'
$consoleDisplayName = Read-Host '显示名（1–40 个字符）'
$consoleRequestBody = @{
  username = $consoleAdminCredential.UserName
  display_name = $consoleDisplayName
  password = $consoleAdminCredential.GetNetworkCredential().Password
} | ConvertTo-Json
try {
  Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8080/internal/console/bootstrap' `
    -Headers @{ Authorization = "Bearer $env:AGENT_INTERNAL_KEY" } `
    -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($consoleRequestBody))
} finally {
  Remove-Variable consoleRequestBody, consoleAdminCredential
}
```

首次成功返回 201；已经初始化返回 409。`GET /api/console/v1/auth/status` 只返回 `initialized`。账号文件损坏或无法加载会阻止启动，不能删除它来绕过恢复检查；恢复前先备份并排查存储。

管理员忘记密码时，在同一管理 Bearer 边界恢复。此操作撤销管理员的全部旧登录；成员登录不受影响。

```powershell
$consoleResetCredential = Get-Credential -Message '输入新管理员密码（用户名此处不使用）'
$consoleRequestBody = @{ password = $consoleResetCredential.GetNetworkCredential().Password } | ConvertTo-Json
try {
  Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8080/internal/console/reset-admin' `
    -Headers @{ Authorization = "Bearer $env:AGENT_INTERNAL_KEY" } `
    -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($consoleRequestBody))
} finally {
  Remove-Variable consoleRequestBody, consoleResetCredential
}
```

家庭登录走 `/api/console/v1/auth/login`，请求包含 username/password 和配置一致的 Origin。返回的登录令牌只在 HttpOnly、SameSite=Strict cookie 中；后续写请求同时发送 Origin 和身份响应中的 `X-CSRF-Token`。成员临时密码只在创建或重置的当次响应显示，首次登录必须改密。改密会撤销该账号所有旧登录，需重新登录；退出只撤销当前设备的登录。登录绝对有效期七天。

登录尝试按实际 TCP 对端 IP 与小写用户名限制：滚动五分钟最多十次，同一 IP 总计最多六十次；不存在的用户名也受限，超限返回 429。在反向代理后，各用户共享代理 IP 的总额度。

完成首次改密后，同一登录 cookie 可访问以下本人会话接口：

| 接口 | 行为 |
|---|---|
| `GET /api/console/v1/sessions` | 返回本账号的会话列表，`id` 是可恢复聊天的公开 SID |
| `GET /api/console/v1/sessions/:id/messages` | 返回本账号的会话历史；读取他人或不存在的 SID 均为 404，管理员也遵循这一规则；读取故障为 500 |
| `GET /api/console/v1/chat/ws` | 升级 WebSocket，沿用 `session/response/error` 帧；浏览器自动发送 cookie，服务端核对完整 Origin，不在 URL 放登录令牌 |

手机和电脑使用同账号的独立登录，可读取同一会话；退出仅撤销当前登录并关闭相关连接，不删除历史。聊天固定使用 `console` channel 和 `agent` Vault，客户端不能更改 owner、channel 或 Vault。该账号的普通聊天不进入全局语义索引或 personal/_memory 沉淀。

登录和 me 返回的 `capabilities` 反映当前服务组件是否具备：聊天组件缺失时不提供 `chat:use`，历史存储组件存在时仍提供 `sessions:read`；首次改密前没有业务能力。能力列表不代表模型在线健康检查。原始磁盘权限仍属于主机运维边界。关键会话目录不可用会阻止启动；模型暂时离线不会阻止读取已有历史。

## 区域与本机服务接入（2026-10-08）

前端已加入“家庭”区域列表、区域设备子页和设备详情。所有完成首次改密的有效成员和管理员拥有 `areas:read`；该能力不扩大他人的会话、personal Vault、成员管理或自动化权限。新页面只读。

| 接口 | 行为 |
|---|---|
| `GET /api/console/v1/areas?q=` | 全屋去重统计及区域列表；查询最多 128 字符 |
| `GET /api/console/v1/areas/:areaID/devices` | 区域的设备和独立实体 |
| `GET /api/console/v1/areas/:areaID/devices/:deviceID` | 指定区域下的设备详情；未知组合 404 |
| `GET /api/console/v1/integrations/status` | HA 目录状态及配置模型的 Ollama 可用情况 |

上述接口使用既有家庭 Cookie 鉴权和 no-store 响应。网页只访问 Go 服务；HA/Ollama 地址与 token 不进入前端。HA 原始注册表、attributes 和本机路径不会直接透传。状态失败可保留旧目录，并提供原成功时间、失败时间及 stale 标记；模型状态只查询模型列表，不以生成或 embedding 当作健康检查。

真实服务沿用已有配置，例如：

```yaml
inference:
  endpoint: "http://127.0.0.1:11434"
smarthome:
  enabled: true
  base_url: "http://127.0.0.1:8123"
  token: "${HA_ACCESS_TOKEN}"
```

模型名称、超时、Vault 和 smarthome.agent_vault_path 按现有配置格式填写，完整字段参见 [示例配置](../config/agent.example.yaml)。通过正常 HA 授权获取凭据，放在服务进程环境或受保护本机文件；不要覆盖 HA 认证存储或使用旧恢复脚本。10-08版本的短期 access token 会到期且没有自动刷新；10-09版本的已有OAuth授权续期见下节，系统服务启动管理仍需按主机环境配置。

HA 运行归档目录在 Reader、全文/向量索引、旧缓存和最终模型上下文中排除；该政策在 HA 关闭时仍生效。目录不能等于或包含整个 Vault，路径和符号链接关系会检查；不要通过将运行归档放入知识目录来绕过隔离。

本机分类工具 `go run ./cmd/ha-areas` 默认只生成预览；读取 `HA_BASE_URL` 与 `HA_TOKEN` 环境变量，授权并审阅预览后才使用 `--apply`。它仅创建区域和写区域归属，不提供实体服务动作。保留已有非空分类；“其他”接收无法确定归属的项目。运行报告中的 load_location_verified=false 表示名称归属尚未经过物理负载验证。

本轮真实入口为 [18083](http://127.0.0.1:18083/app/)，配置和账号目录独立于旧模拟预览。当前运行的凭据和实际网页登录的限制见 [验收记录](../../docs/family-areas-live-results-2026-10-08.md)，不要把 18082 或 Chrome fixture 视为真实 HA 验收。

## 完整家庭页面与持续 HA 授权（2026-10-09）

设计与接口依据：[修订方案](../../docs/superpowers/specs/2026-10-09-family-console-completion-design.md)。本版增加公共知识搜索/正文、管理员私人检索/问答/文本导入、自动化建议和共享结果、采集详情与手动14天分析。管理员确认安装为独立按钮；读取页面和保存绑定都不会安装自动化，设备页面保持只读。普通聊天与私人问答的存储和权限仍分开。

新增能力 `knowledge:read`、管理员 `knowledge:manage`，HA配置存在时 `suggestions:read`、管理员 `suggestions:manage` 与 `collection:read`。角色/CSRF在服务端强制验证；隐藏按钮不能替代鉴权。未完成首次改密的身份没有业务能力。

后台可继续使用运维明确配置的静态 token；若已有正常 OAuth 授权，推荐配置已有受保护凭据文件进行自动续期，两种方式互斥：

```yaml
smarthome:
  enabled: true
  base_url: "http://127.0.0.1:8123"
  token: ""
  oauth_credentials_file: "E:/private-runtime/ha-oauth.json"
```

该 JSON 须包含 `base_url`、正常授权时的原始 `client_id`、`refresh_token`；base_url必须匹配配置。不要自行编造refresh grant或把文件放在公开Vault/web目录。Windows仅运行服务的本机用户/管理员可读，Linux使用0600；字段均不进入网页。已保存的access_token/expires_at可保留，但后台启动时会正常续期，之后新短期access token只留进程内，不改写refresh文件。不会自动创建新的长期令牌。

REST和WS共用续期，提前60秒按需刷新；并发共用一次，失败冷却5秒。GET授权失效最多重试一次；写入不自动重放。grant撤销/用户被停用仍需管理员通过HA正常授权重新配置，前端刷新无法绕过这一点。令牌续期不等同系统服务自启、备份或家庭网络可用性。

状态页分授权失效、权限不足、超时、网络异常与未配置；旧目录有原时间和过期提示。Ollama无法查询列表时模型安装情况为未知；模型列表成功不代表实际生成已测。门窗、防拆等仅按安全 device_class 解释，未知类别保留原值。

公共知识沿用原有 `IsInternalPage` 元数据规则；concepts页面需符合既有公开标签规范。私人导入不等于公开发布。导入链写文件与更新索引不是同一事务：失败时先搜索核对是否已写入，再明确决定重试；页面不会自动重放。

2026-10-09当前版本的真实只读检查、页面补全与自动续期运行状态见[最新验收报告](../../docs/family-console-completion-results-2026-10-09.md)。10-08记录保留为历史依据。
