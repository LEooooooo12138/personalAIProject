# 家序：前端补全与 HA 连接修订设计

日期：2026-10-09。状态：依据用户“反查旧 spec → 修订 → 改动”的明确指令进入实施。替代 2026-10-08 UI 修订稿的范围、导航与凭据方案；保留其视觉原则。旧文档作为历史依据，不删除承诺。

## 1. 反查结果与范围裁决

逐条审计见 `.superpowers/sdd/2026-10-09-family-console-completion/spec-audit-console.md` 和 `spec-audit-system.md`。2026-09-30 family-console-design §5–7 是知识和建议页面的原始合同，10-08 前端/区域计划的“本阶段暂缓”不是永久取消。

| 功能 | 已有状态 | 本轮处理 |
| --- | --- | --- |
| 登录、强制改密、本人历史、成员管理 | 已有真实 Go + React 实现 | 保留并回归，统一可访问设计 |
| 全区域→区域设备→设备实体 | 已完成，家人全可读 | 保留，修正状态和图标语义 |
| 公共知识搜索/正文 | 仅底层过滤基础，未接 Console | 补全公开 agent 入口 |
| 管理员 personal 检索、单次问答、文本导入 | 仅内部管理服务 | 补家庭管理员适配，不混普通聊天 |
| 建议列表、绑定、独立确认、忽略、明确重试 | 引擎已有，页面缺失 | 补管理员流程，所有安装测试使用模拟 HA |
| 已确认结果分享、采集详情、14 天分析 | Console 缺失 | 补显式分享及真实进度 |
| HA 后台令牌 | 静态临时 token 到期导致 401 | 复用已有 OAuth refresh grant，在服务端自动续期 |
| 即时设备控制、任意规则编辑/停用、私有知识发布、云部署 | 原设计排除或用户延期 | 不增加，不计作本轮失败 |

规则优先级：最新人类指令 > 本设计 > 旧阶段设计。全家读全部设备取代旧 EntityIDs 白名单；建议仍需管理员显式共享。现有分享存储中的旧 EntityIDs 保留但不使用、不暴露、不恢复开关。

## 2. 信息架构和页面

桌面主导航：首页、对话、区域与设备、知识、自动化结果、我的。手机保持五项：首页、对话、设备、知识、我的；自动化结果从首页和我的进入，避免六项拥挤。管理员成员管理、私人知识、建议审核与采集从我的集中进入；首页显示待处理摘要及入口。导航采用文字+一致 SVG，不以图标单独承担语义。

| 路由 | 页面目的和关键行为 |
| --- | --- |
| /app/ | HA/Ollama 独立状态和检查时间、区域概况、确认结果入口、管理员待处理摘要、最近本人对话 |
| /app/chat | 既有本人对话；移除无依据在线绿点；手机历史列表/会话切换，手机 Enter 换行、桌面 Enter 发送，IME 保留 |
| /app/areas 及现有子路径 | 中文类别、可验证 device_class 状态；技术 ID 放详情；不加控制按钮 |
| /app/knowledge | 明确“现有网页聊天也可使用的公开资料”，检索及安全正文；无结果/失败分别显示 |
| /app/automations | member 已授权确认结果；admin 全状态、结构、绑定→新版本→再次确认、忽略/明确重试、分享 |
| /app/admin/knowledge | 管理员私人检索/正文、当前页面单次问答+来源、文本导入 personal；不自动公开、不写普通聊天历史 |
| /app/admin/collection | 管理员采集阶段、状态快照和历史分别成功/失败、窗口批次、checkpoint、明确分析最近 14 天 |
| /app/account | 本人资料/改密、服务说明和能力对应入口；成员管理保留原路由 |

所有详情有返回入口；404 不显示空内容，403 不伪装无数据；身份 epoch 改变立即清空、取消请求。请求未知成功的写操作不自动重发。表单 422 保留输入，409 刷新版本后由用户重新决定。

## 3. 视觉、图标与可访问性

复用本地 Icon SVG wrapper，统一 24×24、2px 圆角线条和 16/20/24px 尺寸，不新增图标依赖。语义：home 首页；chat 对话；grid 区域；device 注册设备；sensor 实体；book 知识；workflow 自动化；user 我的；members 成员；plus 新建；refresh 重试；send 发送；arrow 前往；back 返回；chevron 下一级；check 成功；warning 注意；error 失败；clock 时间；lock 权限；spark 仅品牌。装饰 SVG aria-hidden；仅图标按钮必须中文名称，重复成员按钮关联姓名。

色板：背景 #F6F8F5，表面 #FFFFFF，正文 #20352F，次文 #53655E，品牌 #176B5B，选中背景 #E9F3EE，警告 #7A4C00/#FFF5DB，错误 #9F2D2D/#FFF1F0，信息 #245D83/#EEF6FC。正文 16px、辅助至少 14px，标题层次明确。主要操作触控区域至少 44px，键盘焦点可见，错误不只颜色，减少无用途装饰。桌面侧栏 224px、主区上限 1120px；手机边距 16px，内容驱动栅格，不横向溢出。支持 320/390/768/1440px、文字放大和 reduced-motion。

HA 错误展示：ha_auth_required 授权已失效；ha_forbidden 权限不足；ha_timeout 请求超时；ha_unavailable 暂时无法连接；ha_invalid_response 返回异常；ha_not_configured 未配置。保留上次数据和 observed_at，并说明需要管理员处理授权。HA 上游 401 绝不透传为 Console 登录 401。Ollama 无法查询时模型可用性 unknown，不显示未安装；已查到列表才显示是否安装，安装不表示生成已验收。

## 4. 新接口与固定 DTO

均在 /api/console/v1；沿用 Cookie、role、CSRF、Origin、no-store、严格 JSON 和请求发布前身份复核。客户端不能选 vault/owner/channel。错误沿用 {error:{code,message,request_id}}，不能返回原始异常、凭据、绝对路径。GET read 可取消，所有写操作需要明确用户动作。

能力：knowledge:read（有 Reader 的正常身份）；knowledge:manage（admin + Reader）；suggestions:read（HA 已配置）；suggestions:manage、collection:read（admin + HA）；原成员/区域/会话能力保留。管理端 capability 不代替服务端 role 校验。

### 4.1 知识

- GET /knowledge/search?q=：固定 agent，{results:[{path,title,snippet}],count}，q 必填且至多 200 字符，最多 50 项；重新读取候选再过滤，count 为实际可读项。
- GET /knowledge/page?path=：{path,title,body}，仅公开 agent；相对路径、拒根外/symlink穿越、坏元数据、internal、HA 运行归档；不存在/无权统一 404。
- GET /admin/vault/status：{vault:"personal",page_count,total_bytes}，无本地路径。
- GET /admin/vault/search?q= 与 GET /admin/vault/page?path=：同形，固定 personal，管理员正文用于检索结果可读；仍拒损坏元数据和根外路径。
- POST /admin/knowledge/query {query}：1–4000 字符；{answer,sources:[{path,title,score}]}，复用 ChainExecutor 的 rag-answer、固定 personal，当前请求无历史、不写全局索引/记忆。私人 query 不进入 Info 日志。
- POST /admin/wiki/ingest {content,source_title}：content 非空且最多 100000 字符，title 最多 200；复用 wiki-ingest，返回 {vault:"personal",path,title}；无自动发布。失败保持表单但不自动重投，最终身份撤销时不发布私人结果。
- 新页统一 SafeMarkdown（原始 HTML、远程图片禁用，危险协议拒绝）。管理员来源链接指向私人正文，成员绝不可读取 personal。

### 4.2 自动化与采集

GET /suggestions → {suggestions:SuggestionView[],count}。SuggestionView:
{id,status,created_at,title,confidence,time_zone,entity_ids:[],rule:{triggers:[],conditions:[],actions:[]}|null,missing_bindings:[],unsupported_code:string|null,shared:boolean,can_bind:boolean,can_confirm:boolean,can_ignore:boolean,source_suggestion_id?:string,superseded_by?:string,evidence:{sample_size:number|null,period_days:number|null}}。
triggers/conditions/actions 使用既有经过验证 AutomationTrigger/Condition/Action 字段。标题由有效结构生成或安全固定描述；不裸传 Description、DataSource、LastError、raw AutomationConfig。数据不足明确 null，不能猜采样数。admin 可看真实全状态；member 仅 confirmed + explicit shared + 全部关联实体可在 fresh 安全 catalog 证明存在；旧不可解析规则整体隐藏，含 count。失去可验证目录时 member 返回安全上游错误而非“暂无结果”。成员不暴露内部版本链/错误。

- POST /admin/suggestions/:id/bindings {presence:{entity_id,state}} → {suggestion}；复用 BindSuggestion；管理员明确选择代表全家占用的实体，不按名推断。成功打开新 ID、旧版 superseded，绝不顺便确认。
- POST /admin/suggestions/:id/confirm {}、/ignore {} → {suggestion}；复用状态机；confirm 独立展示完整触发/条件/动作后确认。failed/applying 先刷新再让管理员明确重试；既有稳定 ID 幂等。旧版冲突 409，缺条件/不支持 422，上游 502，未配置 503。不能以 ignored 假称停用。
- GET/PUT /admin/sharing：{revision,suggestion_ids:[]}；PUT 携同体 expected revision，复用 Console sharing store；只可分享当前结构有效 confirmed 且所有关联实体已验证的 ID；catalog stale/unavailable 拒新共享（503），revision 冲突 409，非法 ID 422；默认空。
- GET /admin/collection：{phase,last_attempt_at,last_success_at,last_failure_at,error_code,snapshot_at,window_start,window_end,completed_batches,total_batches,checkpoint}。未知时间 null，进度来自 Collector 实际边界；snapshot_at 为已成功快照，last_success_at 为完整采集完成，checkpoint 仅磁盘进度。phase 为 idle/snapshot/history/failed；不能重启后由 checkpoint 伪造运行成功。
- POST /admin/analyze {days:14} → {period_start,period_end,suggestion_count}；同一 Manager 分析过程防并发重复，返回明确 busy 409，前端禁重复点击。分析不安装自动化。

普通页面 30 秒可见轮询只读 API，隐藏暂停。首页复用这些 API，不为满足旧拟路径额外增加 /home 聚合端点。

## 5. HA 授权恢复和安全数据

已验证根因：旧 access_token 到期，同一实例旧 token 401，原有 refresh grant 取得新 token 后 GET /api/ 200。采用官方 refresh_token grant（POST /auth/token，application/x-www-form-urlencoded），不新建长期服务 token。新增可选 smarthome.oauth_credentials_file 指向既有受保护 JSON，字段 base_url/client_id/refresh_token，access_token/expires_at 可读但启动时直接续期；限制凭据 base_url 必须与配置相同，禁止带 userinfo/query/fragment，HTTP 仅本地/家庭可信已有配置，重定向不携秘密。

Client 持有共享 TokenProvider，REST 与 WS 使用同一 provider；旧静态 token 配置继续支持且二者不能同时配置。短期 access_token 仅进程内，过期前 60 秒按需续期，单飞并发和可取消等待、10 秒截止、失败冷却，错误不含 token/body/path。不修改已有 refresh grant，不反复重启续期。REST GET 收到 401 最多刷新并重试一次；非 GET 不自动重试，防重复动作。WS 在发命令前认证失败可刷新重连一次；发过写命令不重放。HTTP/WS 统一类型错误投影到安全 code。服务退出取消 provider 工作。

EntityView 增加 device_class:string|null，仅允许已知安全字符串类别，长度<=64；不输出 attributes。已知 binary_sensor door/window/opening：on=打开/off=关闭；tamper：on=检测到防拆/off=未检测到防拆；motion/occupancy/presence：on=检测到/off=未检测到；未知保留原始 on/off，不臆造警报。light/switch/fan 可解释开关状态；数值和单位仍只读。

官方依据：[HA authentication](https://developers.home-assistant.io/docs/auth_api/)、[HA WebSocket](https://developers.home-assistant.io/docs/api/websocket/)、[WCAG target size](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html)。44px 为本产品舒适目标，不冒称 WCAG AA 全部要求44px。

## 6. 分步交付和八荣八耻验收

T0 旧规格矩阵与基线；T1 知识服务适配；T2 建议/采集/共享适配；T3 HA续期与状态 DTO；T4 页面/设计系统；T5 集成和独立审查。T1/T2/T3 分文件并行，公共routes/能力由根统一接线，T4在固定DTO下推进。

每个行为先失败测试再最小实现；必须覆盖公共→internal竞态、穿越与缓存、成员越权、撤销/迟到请求、绑定新ID与显式confirm、并发确认/分享revision、真实Collector部分失败、token过期并发/拒绝/WS/取消、未知device_class、模型unknown；另跑真实 Go fixture 的浏览器流程，不能只用前端mock宣称集成。

验收记录具体接口、需求ID、RED/GREEN、独立审查与未验项。最终 Go 全套/vet/build、TS/Vitest/Vite/浏览器/旧Node/Python、Linux CGO race 与 diff。真实只读 HA REST+WS、页面和 Ollama 可测；设备动作、实际规则安装、断WAN和现场负载语义不能用模拟结果冒充。保留既有脏工作区，基线快照替代自动提交，不批量回滚、merge或push。
## 本轮澄清

2026-10-09：Ollama `models[].available` 使用 boolean|null；当无法读取模型列表时返回 null，而不是断言未安装。此变更和新增 `device_class` 一起由本轮前端/fixture消费。所有版本/页面实施结果以最终验收报告为准。

## 交叉审查补充（2026-10-09）

- 首页管理员采集异常摘要仍执行旧 spec 承诺：仅具备 collection:read 的管理员请求采集数据，展示实际失败/进行中/无本进程成功记录并提供采集详情入口；成员不触发管理读取。
- 共享管理必须允许撤销已经存在但现已缺失、结构失效或不再满足确认条件的旧授权。显示安全 ID 和“旧授权无法核对”提示，只允许删除，不能把失效授权重新加入；沿用草稿原 revision 和后端允许离线撤销的子集更新。
- 新知识、私人知识、采集、自动化页面必须支持直接打开与刷新。Go SPA 只添加这四条明确路径，未知子路径继续404，API角色检查保持独立。
- 当前构建单JS包约512.51kB有Vite体积提示；不调高阈值隐藏警告。它不是功能失败，后续可独立按路由拆包；最终报告保留该限制。

实施后状态与测试证据见[2026-10-09验收报告](../../family-console-completion-results-2026-10-09.md)，现场/长期运行/系统文字缩放未验范围按报告保留。
