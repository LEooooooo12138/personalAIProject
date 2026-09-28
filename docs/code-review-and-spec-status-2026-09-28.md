# 计划完成度与代码审计（2026-09-28）

> 此文保留修复前的审计快照；后续修复、最新验证和剩余限制见 [修复结果](audit-remediation-2026-09-28.md)。F15 已据复测更正。不要将下列旧行号和旧测试数量视为修复后状态。

审计对象：`E:\personalAIProject` 当前工作目录，基准 HEAD 为 `1ac74b3`，包括已有未提交修改和未跟踪文件。使用 Superpowers 的并行审查、系统排查与完成前验证流程。此次只增加审计文档；没有修复或覆盖产品源码，没有执行 HA 凭据脚本或真实设备控制。

**结论：Phase 1 基础较齐，但未达到完整验收；Phase 2、Phase 3、Phase 4 均为部分实现。普通测试通过，不能证明外部通道安全、协议兼容及设备操作闭环已经完成。**

“已实现”指代码或资产有证据；“部分”表示接线、行为或验收有缺口；“未发现”仅限此次目录范围；“待环境验证”不等于功能不存在。不按文件数推算完成百分比。

## 1. 本次验证结果

| 检查 | 结果与边界 |
|---|---|
| `go test ./... -count=1 -timeout 90s` | 通过。当前有 13 个 Go 测试文件、106 个顶层 Test 函数定义；不是 106 个端到端场景。 |
| `go vet ./...` | 通过。 |
| `go build -o <临时目录>/personalAIProject-audit-agentd.exe ./cmd/agentd` | 通过，未覆盖现有程序。 |
| `node --check go-agent/static/chat-widget.js` | 通过；仅证明 JavaScript 语法有效。 |
| `go test ./... -count=1 -cover -timeout 90s` | 失败，多个包报 `invalid BOM in the middle of the file`。扫描到 40 个 Go 文件开头带 UTF-8 BOM；普通编译通过，不能把覆盖率插桩失败写成项目无法编译。未获得全项目覆盖率。 |
| `go test -race ./internal/core ./internal/channel ./internal/gateway -count=1 -timeout 60s` | 未执行到测试：当前 `CGO_ENABLED=0`，工具提示 race requires cgo。没有并发安全已验证的结论。 |
| Python pytest | 系统没有可用 Python 安装；改用应用自带 Python 后仍缺 pytest。未安装依赖，未声称 Python 测试通过。 |
| `bash tests/integration/phase1_1_vault_bootstrap.sh` | 第一条检查输出 PASS 后退出 1，未完成余下检查；原因是 `set -e` 配合初值为 0 的 `((PASS++))`。 |
| 独立 Shell 最小复现 | `set -e; PASS=0; ((PASS++)); echo reached_summary` 退出 1，未输出 summary。`run_all.sh` 的三个计数器有同类问题。 |
| 临时 Go overlay 审计探针 | 新增测试仅映射到临时文件，未写入产品源码。7 个顶层探针均暴露失败行为：Agent 依赖丢失、会话容量不释放、会话文件越界、受保护路径免鉴权、Vault 写入越界、HA 条件丢失、开启时长错误。鉴权探针测试中间件搭配合成 handler，不代表启动并测试了真实 HA。 |
| 双 Vault 元数据 | personal/agent 均有 9 个要求目录、AGENTS.md、可解析 manifest、index.md、log.md，各有 4 个 concepts 种子文件；未审阅私人正文。两者 .git 存在，但 Git 验证遇到沙箱用户 ownership 拒绝，未修改全局信任配置。 |
| Ollama | `localhost:11434/api/tags` 可达，列出 `gemma4:12b`、`bge-m3:latest`、`llava:7b`。未实测生成质量、embedding 维度、首 token 延迟或并发吞吐。 |
| Agent 与真实外部链路 | 本次 `localhost:8080/health` 在 3 秒内未响应；不能据此断言服务不存在。没有完成真实 Agent→Ollama E2E、企业微信收发、HA 三平台设备或 k3s 验收。 |

## 2. 采用的需求基准与文档冲突

本次核对全部 9 份 specs、4 份 plans，以及 Phase 1 缺口计划、RAG spec，共 15 份基准文档。历史审计报告只作线索，不作为现状证据。

| 文档 | 适用方式 |
|---|---|
| [CONSTITUTION](E:/personalAIProject/docs/superpowers/specs/CONSTITUTION.md) | 最高约束，重点检查 I-4/I-5/I-6 隔离、I-7 API、S-1 密钥、S-4 输出过滤、S-5 人工确认。 |
| [Obsidian 架构](E:/personalAIProject/docs/superpowers/specs/2026-06-27-obsidian-wiki-architecture.md) | 框架研究与知识溯源约定；不能把介绍的所有外部 Skills 当成本项目必须立即实现的 Go 功能。 |
| [原始资料规范](E:/personalAIProject/docs/superpowers/specs/2026-06-27-raw-source-organization-standard.md) | raw 目录、来源标识、命名和冲突处理基准。 |
| [早期知识库设计](E:/personalAIProject/docs/superpowers/specs/2026-06-27-wiki-knowledge-base-design.md) | 单 Vault、NAS/Mac 路线属历史；其 Phase 编号不能与后来四阶段直接混用。 |
| [双通道总体设计](E:/personalAIProject/docs/superpowers/specs/2026-07-01-dual-channel-agent-system-design.md) | 总体愿景；与更新后的专项设计冲突时需按专项修订理解。 |
| [Phase 1 revised](E:/personalAIProject/docs/superpowers/specs/2026-07-01-phase1-revised-design.md) | Windows 原生、双 Vault、4 种子与 1.1–1.6 的主要基准。 |
| [Phase 2](E:/personalAIProject/docs/superpowers/specs/2026-07-01-phase2-wechat-external-channel-design.md) | 当前文件已是 WeCom v2，明确替代 QClaw/Node sidecar。缺 QClaw 不能再算未完成。 |
| [Phase 3](E:/personalAIProject/docs/superpowers/specs/2026-07-01-phase3-webchat-containerization-k3s-design.md) | WebChat、Compose、安装、多架构、Helm 与跨环境部署。容器清单中的旧 Python/Node 服务需同步删改。 |
| [Phase 4](E:/personalAIProject/docs/superpowers/specs/2026-07-01-phase4-smart-home-automation-design.md) | HA 采集、分析、建议确认、规则生命周期与通知。 |
| [早期 bootstrap](E:/personalAIProject/docs/superpowers/plans/2026-06-27-phase1-wiki-bootstrap.md) | 单 Vault 初始化历史计划；尾部“完成后状态”不是实际完成记录。 |
| [Phase 1–2 refinement](E:/personalAIProject/docs/superpowers/plans/2026-07-01-phase-1-2-refinement-checklist.md) | 历史收敛清单，需剔除被后续替代的接入方式。 |
| [Phase 1 full plan](E:/personalAIProject/docs/superpowers/plans/2026-07-01-phase1-full-plan.md) | Docker 前置、至少 5 种子、强制 Qdrant 等旧要求不能覆盖 revised 的调整。 |
| [7 月 14 日重构计划](E:/personalAIProject/docs/superpowers/plans/2026-07-14-p0-p1-code-fix-plan.md) | 8 步类型/RRF 去重、配置、工厂、legacy 清理、拆分与验证，逐项结果见下。 |
| [7 月 15 日补缺计划](E:/personalAIProject/docs/phase1-gap-fix-plan-2026-07-15.md) | 两脚本、E2E 文档与九模块测试；文件存在不等于用例目标全部覆盖。 |
| [RAG spec](E:/personalAIProject/go-agent/docs/rag-pipeline-spec.md) | 原有实现快照已过时：缓存、预热、overlap、标题和代码位置都已变化。 |

需要统一的规则：Python 推理层已被 Go 原生取代（Constitution 末尾明确说明），但旧拓扑仍保留它；来源 manifest 的“绝对 source key”与“相对 page path”未区分清楚；RAG 从每次检索改为实体触发，需正式记为产品决策。Phase 4 管理端点本身并不自动违反统一 LLM 入口约束，重点是其身份、确认及执行语义。

## 3. 已完成、部分完成和未完成清单

| 阶段/能力 | 当前已具备 | 未完成或待验证 |
|---|---|---|
| Phase 1.1 双 Vault | 目录、AGENTS、manifest、4 种子及 index/log 的资产存在；Reader/Writer 有接口与实现。 | bootstrap 脚本中途退出；种子正文、frontmatter、真实 wiki-query、Git 提交未验收。 |
| Phase 1.2 推理底座 | Ollama 可达且三个模型已安装；Go 原生 chat/embed/models 客户端与验证脚本存在。 | 真实回答、1024 维 embedding、中文质量、首 token <5s 等未实测。 |
| Phase 1.3 Agent/Gateway | 薄 main、配置加载、HTTP 端点、/v1 Bearer、日志框架、Vault 文件访问；普通 test/vet/build 通过。 | 管理接口授权不成立；REST 丢失完整消息与参数；模型路由未用于生产聊天。 |
| Phase 1.4 | Python FastAPI 历史代码仍在；默认主链已直接调用 Ollama。 | 不应再要求默认部署 Python；需同步文档。若继续作为受支持备用服务，需补依赖、pytest-asyncio、配置和契约验证。 |
| Phase 1.5 Chat/RAG | chat/direct/RAG Chain 已注册，HTTP/WS 有调用路径；E2E 文档已补。 | 未完成真实 E2E；集成脚本只检查回复非空，不能证明引用种子。文档要求的 chat 后 agent/log 记录、Codex 接入验收未落实。 |
| Phase 1.6 | Qdrant 的 VectorStore/Index/Search 包装存在。 | 未接 Bootstrap/collection 生命周期，Syncthing 未验；revised 允许延期，不是 Phase 1 必须硬阻断项。 |
| Wiki 工作流 | ingest、summarize、synthesize、cross-link 链和页面写入存在。 | index-update 正常路径不更新 index；未形成 manifest/hash/来源溯源、raw 分类及重名后缀、人工 promotion、lint/维护完整闭环。通用复制工具未实现。 |
| RAG 检索 | BM25 倒排、dense、RRF、分块、150 字 overlap、frontmatter 标题、磁盘缓存、启动预热已实现。 | 跨 Vault/内部元数据过滤、缓存重载路径、增删改同步、写后失效和相关回归测试不足。 |
| Phase 2 通道 | Channel 接口、Manager fan-in、factory、WeCom token/crypto/callback/client/白名单组件存在。 | NewAgent 漏存 Chain 依赖，微信入站不能正常回复；真实平台接口与公网回调未验。 |
| Phase 2 路由与隐私 | ModelRouter 决策函数、五级 Filter、权限辅助函数存在。 | cloud 仍是 stub，未见云端执行/hybrid/fallback；权限未强制进入生产链；WebChat 读取 personal，原始流在过滤前发出；审计记录被丢弃。 |
| Phase 2 会话/记忆 | 状态机、磁盘持久化、语义搜索、摘要、TF-IDF、_memory 写入已实现。 | 所有权校验、路径限制、容量回收、断线恢复、去重执行和防覆盖均有缺口。 |
| Phase 3 WebChat | 单文件 JS、气泡、Markdown、主题、WS 流式、历史界面。 | 外部权限、会话恢复、最终过滤渲染有缺陷；思考过程展开未发现；未通过统一 Channel Adapter 接入。 |
| Phase 3 部署 | CI 有 Go vet/test/Linux amd64 build。 | 未发现 Dockerfile、Compose、install.sh、buildx 多架构镜像、Helm Chart、k3s 与跨机验收资产。 |
| Phase 4 HA 数据 | REST 状态/历史/服务客户端、WS 订阅定义、JSON 存储、采集器、分析器与 HTTP 管理端点存在。 | App.Run 未调用 SmartHome.Start/Stop，周期采集/每日分析未启动；WS 订阅只有定义；三平台和长期数据未验。 |
| Phase 4 建议/规则 | HTTP confirm/ignore、规则文档生成函数存在。 | 创建 URL 不对、条件被丢弃、触发/动作写死；无完整建议持久化、稳定 ID、幂等确认、修改/撤销、WeCom 通知与确认交互。 |

重要实现依据：[主依赖装配](E:/personalAIProject/go-agent/internal/core/app.go:65)、[Chain 组装](E:/personalAIProject/go-agent/internal/chain/chains.go:29)、[检索步骤](E:/personalAIProject/go-agent/internal/chain/go_steps.go:53)、[embedding 预热](E:/personalAIProject/go-agent/internal/core/app.go:198)、[HA Manager](E:/personalAIProject/go-agent/internal/smarthome/manager.go:52)、[工具接口](E:/personalAIProject/go-agent/internal/agent/tool.go:1)。工具 registry 存在不能代替真实工具实现和调用链。

## 4. 两份修复计划逐项核对

| 项目 | 状态 | 判断依据 |
|---|---|---|
| 7/14 Step 1 EmbeddingHit 统一 | 已实现 | Searcher/Adapter 使用 vault.EmbeddingResult。 |
| Step 2 RRF 去重 | 主体实现 | 活跃 Chain 复用 vault.RRFMerge；调用仍写死 60。 |
| Step 3 配置集中 | 部分 | HTTP timeout、会话 embedding model 已注入；Retrieval 配置未驱动 Chain，WS 300 秒、topK、截断和部分模型仍硬编码。 |
| Step 4 core 去 WeCom 依赖 | 已实现主体 | 工厂注册与 main 空白导入已接；core 无直接 wecom import。 |
| Step 5 legacy 删除/去调度 | 部分且有回归 | legacy 主聊天路径已删；internal page filter 未迁移；NewAgent 丢依赖。 |
| Step 6 重复函数 | 基本达到目的 | 已消除主要重复转换与解析；无跨包需求的私有函数无需机械导出。 |
| Step 7 WS 拆分 | 字面未照做，规模目标达到 | websocket.go 约 290 行，无 ws_conn/ws_types 文件；这本身不是功能缺陷。http.go 已增长到 691 行。 |
| Step 8 验证 | 部分 | 普通 test/vet/build 通过；coverage、回归 runner、真实冒烟未闭合。 |
| 7/15 Gap 1 bootstrap 脚本 | 文件已补，行为未完成 | 首个 PASS 后退出。 |
| Gap 2 E2E 文档 | 文档已补，验收未完成 | 只固化步骤；Codex 及 log 验收仍有差距。 |
| Gap 3 run_all | 部分 | 计数退出、无确定阶段顺序、Phase 4 路径错误、跳过数量与说明不一致。 |
| Gap 4 九模块测试 | 九文件存在，覆盖部分完成 | 尚缺 idle 扫描、embedding cache/search、启动失败/cancel、非空依赖接线等计划用例。 |

不能沿用旧报告中的“无 embedding 持久化/无预热”“仍有三份 RRF”“core 仍直接依赖 wecom”等旧结论。也不能沿用旧计划对 middleware/filter/inference “无需修改”的评价。

## 5. 代码问题（按实际影响排序）

P1 表示应优先修复的安全或主流程阻断，P2 表示明确的功能/一致性问题。尚未证实生产部署方式或真实泄露事件，因此未将所有问题夸大为 P0。

| 编号 | 优先级 | 触发、影响、位置与修复方向 |
|---|---|---|
| F01 | P1 | **管理和会话路径免鉴权。** [middleware.go:17](E:/personalAIProject/go-agent/internal/gateway/middleware.go:17) 放行 /internal/*、/channels/*、/sessions/*；服务监听全部网卡。可达端口的未认证请求能搜索个人库、搜索/读取会话、触发 ingest 或 HA confirm。中间件探针确认无 token 返回 200。应为管理操作建立身份和授权白名单。精确边界：配置 key 时 /sessions 本身仍需 key，带后续路径的 /sessions/... 才被豁免。 |
| F02 | P1 | **外部通道读私库，dense 索引也未分库。** [websocket.go:180](E:/personalAIProject/go-agent/internal/gateway/websocket.go:180)、[agent.go:98](E:/personalAIProject/go-agent/internal/core/agent.go:98) 指定 personal；[app.go:115](E:/personalAIProject/go-agent/internal/core/app.go:115) 只建 personal embedding store，[go_steps.go:76](E:/personalAIProject/go-agent/internal/chain/go_steps.go:76) 不按 vault 隔离。只改 state.Vault 仍不足以隔离 dense 命中。应将可信通道身份、检索域和文件权限贯穿整条链，并在加入上下文前执行 internal/visibility 过滤。Agent 的问题目前被 F06 阻断，修接线时需同时修权限。 |
| F03 | P1 | **先泄露流式原文、后过滤。** [websocket.go:218](E:/personalAIProject/go-agent/internal/gateway/websocket.go:218) 发 token，247 行才过滤；[chat-widget.js:400](E:/personalAIProject/go-agent/static/chat-widget.js:400) 有流式 DOM 时不以最终正文覆盖。应在发送前完成安全过滤，不能安全处理的内容先缓冲；最终渲染也应校准。 |
| F04 | P1 | **会话路径越界与身份越权。** [session_store.go:167](E:/personalAIProject/go-agent/internal/core/session_store.go:167) 仅替换冒号；WS 原样接受客户端 ID。临时测试确认合成 ID 可写出 sessions 目录的 JSON 文件。历史读取/语义搜索/WS 恢复也未验证所有权。应使用服务端不透明存储 ID、根目录校验及独立的用户授权。 |
| F05 | P1 | **Vault 路径未受根目录约束。** [vault_steps.go:199](E:/personalAIProject/go-agent/internal/chain/vault_steps.go:199) 采纳 LLM category，再由 [reader.go:333](E:/personalAIProject/go-agent/internal/vault/reader.go:333) 直接 Join/写入；ReadPage 同类。临时探针证明 ../escaped.md 写出指定根。应对 category 设允许列表，在文件层检查规范化/符号链接后的归属，并防止静默同名覆盖。 |
| F06 | P1 | **企业微信 Agent 主流程未接通。** [agent.go:43](E:/personalAIProject/go-agent/internal/core/agent.go:43) 没保存注入的 ChainExecutor/ChainRouter，消息处理必然进入未初始化分支。临时非空依赖测试失败。应补构造校验并测 Manager→Agent→Send 的完整调用。 |
| F07 | P1 | **企业微信密钥写入日志。** [crypto.go:29](E:/personalAIProject/go-agent/internal/channel/wecom/crypto.go:29) 记录 token、EncodingAESKey、AES hex；签名失败日志及 [callback.go:152](E:/personalAIProject/go-agent/internal/channel/wecom/callback.go:152) 还含秘密/解密正文。删除这些字段，核查已有日志传播范围；已进入日志的有效密钥应轮换。本报告不展示值。 |
| F08 | P1 | **HA 子系统未启动。** Bootstrap 创建 Manager，但 [app.go:186](E:/personalAIProject/go-agent/internal/core/app.go:186) Run 没调用 SmartHome.Start/Stop；因此配置 enabled 也不会自动周期采集或每日分析。接入统一生命周期，测试启动、取消与退出。 |
| F09 | P1 | **HA 确认创建接口地址不正确。** [client.go:141](E:/personalAIProject/go-agent/internal/smarthome/client.go:141) POST 地址缺 config_key。标准官方实现要求 /api/config/automation/config/{config_key}。应持久化稳定 ID、匹配目标 HA 版本并用模拟服务器锁定请求契约。未对真实 HA 执行创建。 |
| F10 | P1 | **HA 执行规则与确认建议不一致。** [automation.go:14](E:/personalAIProject/go-agent/internal/smarthome/automation.go:14) 强制 time + light.turn_on，丢弃 Condition。条件丢失已用探针复现；关联触发和非 light 实体也不能正确转换。用有类型的触发/条件/动作结构，确认展示与提交使用同一份数据；不支持的建议明确拒绝。 |
| F11 | P2 | **REST 不是完整的兼容聊天契约。** [http.go:488](E:/personalAIProject/go-agent/internal/gateway/http.go:488) 仅解析 Model/Messages，随后只用最后 user 文本；history/system、image、metadata、stream/采样参数丢失，模型实际固定却回显请求 model。应保留规范化完整请求，统一路由；不支持参数明确报错。不能声称当前敏感请求已经上云：当前 cloud 路由尚未执行。 |
| F12 | P2 | **会话容量和恢复错误。** [session.go:191](E:/personalAIProject/go-agent/internal/core/session.go:191) 按 map 总长度限额，CompleteSession 不删除；探针中容量 1 的会话关闭后仍拒绝下一个。断线 EndSession 后同 ID 又新建空会话，并覆盖同名历史文件。应区分连接生命周期、会话实例与归档，安全释放活跃容量。 |
| F13 | P2 | **流式结果静默丢失。** [inference/client.go:125](E:/personalAIProject/go-agent/internal/inference/client.go:125) 与 [llm_steps.go:83](E:/personalAIProject/go-agent/internal/chain/llm_steps.go:83) 在缓冲满时丢 token；WS 有部分 token 时不使用完整 FinalAnswer 校准。改成可取消背压，明确传播 scanner/网络错误，以快生产慢消费测试验证。 |
| F14 | P2 | **过滤器组合漏清理。** [filters.go:84](E:/personalAIProject/go-agent/internal/filter/filters.go:84) 遇首类 secret 就返回，password 与 token 共存时只处理一类；PersonalRef 检测与替换的大小写规则不一致。应遍历全部规则，增加组合与大小写测试。 |
| F15 | P2 | **审计更正：回调正文日志不当且请求体未限长。** 原 callback.go 在校验前记录 body[:200]。2026-09-28 复测与 Go io.ReadAll 源码表明短体通常容量为 512，切片可扩至 cap，故撤回“短 POST 必然越界/panic”的结论。应去除正文日志、限制 body、对无效/超大请求返回 400/403/413；已补对应回归。 |
| F16 | P2 | **embedding 缓存恢复丢 provenance。** [embedding.go:304](E:/personalAIProject/go-agent/internal/vault/embedding.go:304) 恢复 ChunkPage 结果但不回填 PagePath，重启后 dense 来源为空，RRF 按空键合并。测试首次索引与重载等价，并恢复完整来源元数据。 |
| F17 | P2 | **缓存不完整处理新增/修改/删除。** [inverted.go:230](E:/personalAIProject/go-agent/internal/vault/inverted.go:230) 只验证旧文件；[embedding.go:319](E:/personalAIProject/go-agent/internal/vault/embedding.go:319) 有部分有效条目就接受缓存，新页不补、失效块直接丢弃；运行中 indexed 后不再检查。应使用文件清单、内容签名、版本标识和写后失效机制。 |
| F18 | P2 | **ingest 正常成功却不更新 index。** [vault_steps.go:132](E:/personalAIProject/go-agent/internal/chain/vault_steps.go:132) 只在 AppendLog 失败时才写 index.md，正常路径仅写 log 却记录 index updated。应把 index 更新做成明确 Writer 操作，并验证真实文件内容。 |
| F19 | P2 | **记忆去重不影响写入，同分钟可能覆盖。** [sedimentation.go:426](E:/personalAIProject/go-agent/internal/memory/sedimentation.go:426) 不论 IsNew 都写入，MatchPath 非实际文件；文件名精确到分钟后直接 WriteFile。应明确跳过或合并规则、返回真实路径、使用唯一 ID/排他创建；落实 MinMessages。 |
| F20 | P2 | **HA 建议状态未形成闭环。** [manager.go:150](E:/personalAIProject/go-agent/internal/smarthome/manager.go:150) 手动分析不保存，confirm 查存储导致新 ID 不可确认；[store.go:146](E:/personalAIProject/go-agent/internal/smarthome/store.go:146) 覆盖旧建议；确认无状态幂等、存档失败仍可回成功。应先发布不可变版本、稳定 ID，再执行状态迁移，保存 ha_automation_id 以供修改/撤销。 |
| F21 | P2 | **HA 开启时长统计错误。** [analyzer.go:317](E:/personalAIProject/go-agent/internal/smarthome/analyzer.go:317) 先重设 lastOn，再相减；正常 on→off 不累计。探针的一小时开启返回 0。应按状态转换结算并处理窗口边界。 |
| F22 | P2 | **步骤错误未一致传出。** [executor.go:62](E:/personalAIProject/go-agent/internal/chain/executor.go:62) 把步骤失败装入 result.Error，但部分调用方只检查外层 err，例如 [http.go:428](E:/personalAIProject/go-agent/internal/gateway/http.go:428)。可能 HTTP 200 掩盖写入/推理失败。统一错误契约和状态码，保留可诊断日志。 |
| F23 | P2 | **回归脚本不能证明全量通过。** [run_all.sh:37](E:/personalAIProject/tests/integration/run_all.sh:37) 的后置计数配合 set -e 会中止；关联数组不保证顺序；Phase 4 脚本实际位于 go-agent/tests/integration；其部分断言使用 `|| echo FAIL` 吞错误。应按有序清单执行、正确累计状态、确保失败非零退出。 |

F09 的外部依据是 [HA 官方配置路由源码](https://raw.githubusercontent.com/home-assistant/core/dev/homeassistant/components/config/view.py) 和 [automation 路由注册](https://raw.githubusercontent.com/home-assistant/core/dev/homeassistant/components/config/automation.py)。F23 的退出语义可见 [GNU Bash 官方手册](https://www.gnu.org/software/bash/manual/bash.html)，本机也已实证。

另有一组应隔离管理的未跟踪 HA 调试脚本：例如 [ha_fresh.py](E:/personalAIProject/go-agent/tools/ha_fresh.py:50) 覆盖认证存储、[ha_pw.py](E:/personalAIProject/go-agent/tools/ha_pw.py:8) 使用固定密码、[ha_cred_full.py](E:/personalAIProject/go-agent/tools/ha_cred_full.py:5) 输出凭据。未执行、未确认凭据有效性；这些不应与产品功能一起批量提交。当前 config/.env 已由嵌套的 [go-agent/.gitignore](E:/personalAIProject/go-agent/.gitignore:5) 忽略，不能误报为完全没有忽略规则。

## 6. 按“八荣八耻”评价与优化

| 原则 | 当前证据 | 可执行改进 |
|---|---|---|
| 以查档求证为荣 | HA 创建 URL 与官方接口不一致；WeCom 发消息和 padding 契约尚未完成官方核验。 | 为选定版本记录官方接口、请求/响应与错误码，用本地模拟服务做契约测试；不能以伪代码当协议。 |
| 以对齐需求为荣 | 多份文档互相覆盖；RAG spec 与生产行为不同。 | 制定单一有效阶段清单，为被替代的 Python/QClaw 标注状态，将“代码有/接通/实测验收”分开。 |
| 以请示规则为荣 | 外部 private vault、记忆合并、HA 条件被隐式决定。 | 把库权限、去重策略、可支持的设备动作变成显式规则；未授权或不支持的情形默认拒绝。 |
| 以复用存量为荣 | 已完成 RRF/类型去重、通道工厂、Inference/Reader/Writer 接口。 | 保留这些结构；让 REST、WS、WeCom 复用统一请求处理和权限流程，避免重复实现路由、过滤、会话保存。 |
| 以完备测例为荣 | 106 个测试定义仍漏掉本次 7 类已复现边界；WeCom/inference/smarthome 无原有单测。 | 优先增加权限矩阵、非空依赖接线、跨用户、流式泄漏、缓存重载/增删改、记忆重复、HA 确认语义和脚本退出码测试。 |
| 以恪守规范为荣 | main 很薄、工厂改善依赖；但 I-4/I-6/S-4 尚不满足，core 内仍有 Fatal。 | 将策略集中到核心，接入层只转换协议；核心返回错误，由 main 决定退出；保持人工确认与执行数据一致。 |
| 以坦诚存疑为荣 | 普通测试通过不代表 E2E、云路由或三平台可用。 | 保留本报告验证边界，不把环境受限写成代码缺失，不把静态推断写成真实泄露事件。 |
| 以分步迭代为荣 | 工作区有跨多模块修改及一批未跟踪工具，难以单一归因。 | 按安全边界、接线、契约、数据一致性分组变更，每组使用对应失败用例回归，避免一次性重写架构。 |

优化顺序：

1. **先确保正确与安全**：修 F01–F07，补最小失败测试；恢复 WeCom 回复时同步落实外部隔离，避免修好接线后放开私库。
2. **接通生命周期与契约**：修 Agent/SmartHome 启停、完整 Chat 请求、过滤前发送、HA 创建及确认数据一致性。
3. **处理数据一致性**：缓存清单/失效、索引更新、会话恢复、记忆去重、建议版本及幂等；文件写入用临时文件加原子替换。
4. **再优化性能与结构**：使用有限队列与可取消背压、批量/增量 embedding；配置驱动 topK/RRF/模型/timeout；通过数据量和延迟测量决定是否接 Qdrant。
5. **最后推进部署交付**：补可提交且无秘密的配置模板，CI 从 go.mod 读取 Go 版本（目前 workflow 1.22 与 go.mod 1.26.4 不一致，不能直接推断必定失败，因为可能自动下载 toolchain）；建立 race/cover/契约任务，再完善 Compose/Helm 与真实平台 E2E。

超过 Constitution 400 行提醒阈值的主要文件有 http.go 691、sedimentation.go 642、reader.go 489、session_store.go 460、analyzer.go 416、embedding.go 405。按职责拆分有维护价值，但不能把拆文件排在隐私和执行错误之前。前端已有 HTML escape，不因 innerHTML 的存在就直接判为 XSS。

## 7. 外部可行性核验与未验证项

| 项目 | 查证结果 |
|---|---|
| Go 直连 Ollama | [Ollama 官方文档](https://docs.ollama.com/api/openai-compatibility) 支持部分 OpenAI 兼容接口，因此去掉中间 Python 在方向上可行；应用仍需保留受支持的消息、图像和流式语义。 |
| gemma4:12b | [Ollama 官方模型清单](https://ollama.com/library/gemma4/tags) 有该标签，本机 tags 也列出它；不能据模型可用推断本机达成延迟/显存目标。 |
| HA 数据访问 | [HA 官方 REST 文档](https://developers.home-assistant.io/docs/api/rest/) 提供状态/历史/服务能力并要求 Bearer；本项目 Agent 自身的管理接口仍需独立授权。 |
| HA 创建规则 | 当前实现缺少官方配置路由的 config_key；对应接口应锁定目标 HA 版本后测路径、payload、权限与错误行为。 |
| WeCom | 官方页面在本次工具访问中未能可靠打开。发送端点与加解密 padding 等仅列契约待核验，不以二手材料断言具体协议错误。真实白名单、回调签名和回复需测试环境。 |
| 部署与物理设备 | 未验证公网/反向代理隔离、NAS/Mac/k3s、多平台设备、长期采集、HA 真实创建和人工确认。这些应作为独立验收记录。 |

建议下一个里程碑是“安全的本地 Agent + 可验证的单条外部通道闭环”，验收标准应覆盖真实调用结果与负向权限用例，再扩展部署和智能家居场景。
