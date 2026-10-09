# Spec 与现有实现完整对比（2026-10-09）

> 后续实施更新：用户批准后，前端聊天即时控制已新增M1工程链路。本文保留实施前审计快照；最新能力、测试及未完成验收见[聊天控制实现记录](console-ha-control-results.md)。

## 结论与核对基准

系统已具备可用的家庭前端和大部分本地后端：账号、本人聊天、区域设备浏览、公开/私人知识、自动化建议审核、采集状态和 HA 授权续期均已实现。当前最主要的新功能缺口是**在前端聊天中查询实时设备状态、生成即时控制提议，经用户确认后执行并回读结果**。已有“确认安装自动化”不等于“现在打开设备”。

本轮交付是审计、方案、开发计划和现有工作分类归档；没有实施下述新控制功能，没有操作真实物理设备。

审计发现两个不同基线，不能混用：

| 位置 | 基线 | 作用 |
|---|---|---|
| `E:/personalAIProject` | `main` / `d9d208a`，与检查时远端 main 一致 | 9/28 旧代码及未归档历史文档 |
| `C:/Users/Admin/.codex/worktrees/p1-p2-remediation/personalAIProject` | `codex/p1-p2-remediation` / `d7deaf9`，另有大量未提交修改 | **本报告核对的最新实现**，包含 9/30–10/09 家庭控制台与修复 |

以下代码路径相对仓库根，行号为归档前当前内容的定位提示；以函数名和测试名为稳定依据。标记含义：**已实现**＝代码和对应测试存在；**部分**＝存在主体但缺少子要求；**待建**＝当前未接线；**现场未验**＝代码或模拟测试不能证明真实环境完成；**暂缓**＝不阻塞本次主线。不按文件数量计算虚假的总体百分比。

## 1. 规格优先级与路线调整

| 规格/计划组 | 当前判定及处理 |
|---|---|
| `specs/CONSTITUTION.md` | 保留本地优先、双库隔离、统一推理入口、兼容、S-5 人工确认。主体中的 Python 中间层、阶段状态、依赖示意有历史残留。 |
| 6/27 知识库架构、知识蒸馏、raw-source 标准；wiki-bootstrap 计划 | Wiki 文件/RAG 基础已具备；manifest、来源生命周期、自动维护和多机同步未完整落地。该组的 Phase 编号不等同于系统四阶段。 |
| 7/01 dual-channel 总设计、phase1-revised/full-plan/refinement | Go 原生调用 Ollama 已替代 Python 主链。双库、检索、鉴权、会话多数完成；通用文件复制、完整知识来源管理仍有缺口。 |
| 7/01 phase2 微信外部通道 | 部分适配/过滤/会话代码存在，真实微信闭环未验。**按本次需求整体暂缓外部消息入口**，不删除已有代码，不作为前端 HA 的前置依赖。 |
| 7/01 phase3 WebChat/容器/k3s | 家庭 Console 已明显超出旧挂件；Compose/镜像/Helm/跨环境部署未交付。当前单进程文件存储不能直接套旧 HPA/多副本方案。 |
| 7/01 phase4 智能家居 | 目录、采集、分析、绑定、人工安装规则完成代码闭环；即时控制、规则停用/恢复、多平台现场与断 WAN 未完成。 |
| 7/14 审查/修复、7/15 gap、9/28 审计/整改计划 | 历史快照，不能直接当当前缺陷清单。已有安全隔离、路径保护、SSE/脚本修复保留回归。 |
| 9/28 local-llm-ha-tuya-lan 和部署计划 | 目录、浏览器认证、模型接入已有实现；“先 HA Assist、再 WebChat”调整为**现有家庭前端→Go→本地模型→受约束 HA 控制**。LAN 仍单独验收。 |
| 9/30 p1-p2-remediation | R1–R7 在最新工作树已有实现及测试，见第4节；旧 main 尚未包含这些更改。 |
| 9/30 family-console-design/implementation、cloud-deployment | 账号/聊天/共享/知识/建议 UI 大部完成；运行恢复仍有缺口，云部署明确暂缓。 |
| 10/08 family-console-frontend、family-areas-live-integration、UI revision | 已交付；其中“静态 token 无续期”“缺知识/建议页”被10/09结果覆盖。全家可读全部区域设备，覆盖旧设备读取白名单。 |
| 10/09 family-console-completion spec/plan/results | 最新已交付基线；其“即时控制暂不做”是当轮边界，**由本次新需求提升优先级**，不算过去漏做。 |

新方案见 [前端对话控制 HA 设计](superpowers/specs/2026-10-09-console-ha-control-design.md)，任务见 [开发计划](superpowers/plans/2026-10-09-console-ha-control-roadmap.md)。新文档只覆盖本次路线变化，历史记录保留日期及原有事实。

## 2. 基础后端、知识与记忆

| ID | 要求 | 状态与现有证据 | 剩余工作 |
|---|---|---|---|
| B01 | Go Agent、本地推理、OpenAI兼容入口 | 已实现：`core/app.go:100`、`inference/client.go:191`、`gateway/chat_contract.go`；chat/embed/models及协议测试 | 公共REST不支持任意tools/tool_choice，明确拒绝；首版HA无需开放通用工具执行。 |
| B02 | 双Vault、安全读写/搜索 | 已实现：`vault/reader.go:187,215,439`、`filesystem.go:31`、`content_policy.go` | 不等于对任意目录具有完整复制/文件操作工具。 |
| B03 | BM25+dense+RRF、缓存刷新 | 已实现：`embedding.go:127`、`inverted.go:274`、`chain/go_steps.go`；重启/增删改/取消测试 | 不重复新增缓存/预热；后续做真实语料质量与延迟评测。 |
| B04 | 按库动态触发RAG | 已实现：`reader.go:567`、`go_steps_decide.go:54`、`app.go:193`；`triggers_test.go` | 两字以下等现有直答规则须在评测中解释，不以“有RAG”推断所有问题都会检索。 |
| B05 | 元数据、内部页、HA归档隐私 | 已实现：`reader.go:320`、`content_policy.go`、`metadata_routing_test.go`、`content_policy_integration_test.go` | 持续保留缓存后变私有/损坏、symlink、来源复读的负向测试。 |
| B06 | 公共知识搜索/正文 | 已实现：`gateway/console_knowledge.go:24`、`web/src/pages/KnowledgePage.tsx` | 普通成员固定agent库，不开放personal。 |
| B07 | 管理员私人问答/来源/文本导入 | 已实现：`console_knowledge.go:249,282`、`AdminKnowledgePage.tsx` | 写页与更新索引非事务；失败可能已有文件，不可自动重投。 |
| B08 | 原始来源manifest、hash/版本溯源 | 部分：有wiki-ingest链和索引；`chain/chains.go:65` | 缺`.manifest.json`生命周期、来源hash幂等、pages_created/updated、冲突恢复；`source_title`虽收取，`LLMIngestStep`尚未使用。 |
| B09 | 长期记忆 | 旧通道已实现：`memory/sedimentation.go:452`；摘要/TF-IDF/落盘 | `memory/summarizer.go:64`模型硬编码；无持久失败重试，重复内容跳过而非语义合并。 |
| B10 | 家庭账号记忆隔离 | 按设计完成：`core/agent.go:193`、`console_session_test.go` | Console不进全局语义索引/_memory是隐私决策。个体长期记忆待独立owner-aware设计。 |
| B11 | Wiki维护与私人知识发布 | 部分：已有synthesize/cross-link等chain | 缺完整lint/tag/hot/log/来源维护服务和人工脱敏发布；私人发布按新Console设计延期。 |
| B12 | 指定目录读写复制搜索终极目标 | 部分：Vault读写搜索可用；`agent/tool.go`仅Tool/Registry抽象 | 通用copy及实际工具执行接线待建；需要允许根目录、覆盖冲突和审计。 |
| B13 | 配置一致性 | 部分：主模型/路由/时限/Console已接 | Personality、Logging、SearchTimeout有定义但未完整使用；`app.go:63`固定development logger；部分Bootstrap仍Fatal。 |
| B14 | 资源与检索边界 | 部分：分段/overlap/top-k存在 | 单个超长段落缺硬上限；向量维度不符只得0，无明确诊断/重建策略。 |
| B15 | 云模型、Qdrant、Syncthing、多机 | 暂缓：Qdrant最小client不等于Bootstrap接线 | 当前本地路线可运行，不为旧清单强行引入新依赖。 |

代码路径 B01–B15 省略共同前缀 `go-agent/internal/`，显式 `web/` 路径位于 `go-agent/`。

## 3. 前端、账号与对话

| ID | 要求 | 当前实现 | 缺口/边界 |
|---|---|---|---|
| F01 | 独立账号、首改密、成员管理、撤销 | 已实现：`internal/console/`、`gateway/console_auth.go`、`console_members.go`；Cookie/CSRF/Origin/身份epoch | 管理员恢复有受保护HTTP入口；旧计划命令行恢复工具未交付。 |
| F02 | 本人历史、跨设备恢复、账号隔离 | 已实现：`console_sessions.go`、`console_chat.go:44`、`core/session_owner.go`；真实Go fixture测试 | 不允许跨账号取历史；失效SID恢复与FIFO已修。 |
| F03 | 中文响应式前端 | 已实现：`go-agent/web/src/App.tsx`及各pages；桌面/手机导航、图标、安全Markdown、深链 | 完整屏幕阅读器、200%系统字号、真实多手机矩阵未全验。 |
| F04 | 区域→设备→实体、搜索、旧数据提示 | 已实现：`console_areas.go`、`smarthome/catalog.go`、Areas/Area/Device页面 | 注册归属不等于物理负载位置；尤其多路控制器不得猜测。 |
| F05 | 知识、建议、采集、首页状态 | 已实现：Knowledge/AdminKnowledge/Automations/Collection页面和对应Go handlers | 10/08实施前“页面缺失”结论已失效。 |
| F06 | 聊天流式和任意站点嵌入 | 部分/架构调整：保留旧挂件，Console同源；`websocket.go`最终过滤后输出 | 当前不逐token外发，是隐私取舍；新控制卡扩展不得恢复过滤前流。跨站嵌入暂缓。 |
| F07 | 聊天实时查询HA | 待建：普通聊天仅`Run("chat")`，目录页能读HA不等于模型已接实时状态 | 加只读设备意图分支和有时间戳的结构化结果。 |
| F08 | 聊天即时控制HA | 待建：无控制提议/确认/回读服务、消息类型和卡片 | 本次P1主线；与“建议安装自动化”分开。 |
| F09 | 前端性能 | 构建通过，单JS约512.51kB/gzip156.10kB | 有>500kB提示；后续路由分包，不调高阈值掩盖。 |

前端身份协调器依赖SharedWorker，不支持时会拒绝运行（`web/src/coordinator.ts:51`）；当前E2E为Chromium，手机尺寸模拟不等于Safari或真实手机浏览器已验。

## 4. HA、自动化与旧修复计划

| ID | 要求 | 当前证据/状态 | 剩余工作 |
|---|---|---|---|
| H01 | REST/WS目录、状态及注册关系 | 已实现：`smarthome/catalog.go:48,185`、`registry.go`、`client.go:93,111` | 复用，不再新建目录系统。 |
| H02 | HA授权稳定性 | OAuth TokenProvider已实现：`token_provider.go`、`client.go:47`；并发单飞、提前续期、GET401最多重试一次 | 长时间跨真实有效期、HA重启、断网恢复现场验收仍欠缺；写请求不自动重放。 |
| H03 | 历史采集/进度/时区 | 已实现：`history_store.go`、`collector.go`、`analyzer.go`、`collection_status.go` | 现场长期数据完整性与负载仍需观察。 |
| H04 | 结构化建议→绑定→确认→归档 | 已实现：`suggestion_intent.go`、`suggestion_bindings.go`、`manager_actions.go` | 生成建议不能拿到真实占用映射时明确待绑定；本次不替用户猜家庭presence含义。 |
| H05 | 建议显式共享与成员只读 | 已实现：`console_suggestions.go`、`console_sharing.go`，revision冲突保护 | 全家可读设备与建议显式共享是两套不同规则。 |
| H06 | 即时服务调用 | 仅有低层`CallService`（`client.go:191`），生产用途目前为automation.reload | 缺独立即时ControlService、可信目标映射、所有权/有效期/单次确认及回读。 |
| H07 | 自动化完整生命周期 | 部分：创建、归档、忽略未执行建议已有 | 无完整安装后停用/恢复/编辑/回滚服务；ignore不是disable。 |
| H08 | 统计/LLM建议融合 | 统计分析器已有，不是LLM规则生成系统 | 自然语言规则编辑与任意复杂编排后置。 |
| H09 | 三平台和Tuya LAN | 现场未验：9/28局域网方案是规划，HA在线不足以证明LAN | 逐型号/固件/协议/DP确认，一台低风险设备断WAN与重启测试；不默默回退云。 |
| H10 | HA事件驱动采集 | 客户端事件订阅有基础，业务仍主要轮询 | 首版控设备用有界GetState回读即可，不设事件化重构前置。 |

9/30计划逐项核销（以最新工作树为准）：

| 旧编号 | 最新状态 | 证明位置 |
|---|---|---|
| R1 历史缺实体filter | 已修 | `smarthome/history_test.go`，批次/空ID/恢复进度 |
| R2 元数据fail-open、来源untitled | 已修 | `vault/metadata_test.go`、`chain/metadata_routing_test.go` |
| R3 建议缺Automation | 已修 | `suggestion_intent.go`、`suggestion_bindings_test.go`、Console浏览器绑定→确认用例 |
| R4 UTC/初态/单方向关联 | 已修 | `analyzer_patterns_test.go`，家庭时区、DST、真实变化、双向关联 |
| R5 启动时personal触发词 | 已修 | `TriggerEntities`及`triggers_test.go` |
| R6 Cookie变化后旧SID循环失败 | 已修 | `gateway/session_recovery_test.go`、`tests/frontend/chat-widget.test.cjs` |
| R7 同用户/同SID并发乱序 | 已修 | `channel/fifo_test.go`、`core/session_turn_test.go`、`gateway/shared_turn_test.go` |
| RAG脚本仅验非空 | 已修 | `tests/integration/phase1_5_full_chain.sh`强制唯一事实和来源；14条离线fixture |

## 5. 部署、运行维护和验收缺口

| ID | 状态 | 内容/下一步 |
|---|---|---|
| O01 | 已实现 | Go测试/vet/build、React类型/行为/构建、Playwright真实Go fixture、旧widget、Python离线fixture均已进CI。 |
| O02 | 部分 | 本机Go+前端静态资源能运行，有`go-agent/docs/console-local-setup.md`。稳定开机自启、轮转日志/容量界限、停机排空仍需产品化。 |
| O03 | 部分 | 文件存储有原子写和失败关闭；缺完整一致性备份/恢复演练、旧计划本机恢复CLI；重置管理员的HTTP接口已存在。 |
| O04 | 未交付 | Dockerfile、Compose、安装脚本、多架构镜像、Helm/k3s/域名云部署；按最新路线后置。 |
| O05 | 设计冲突需先解决 | 现有账号、会话、控制状态依赖单进程；不可把旧Phase3多副本/HPA直接应用到本机存储。先单副本有状态部署。 |
| O06 | 未完整验收 | 10账号/3并发、长模型生成、长时间续期、断WAN/恢复、备份恢复、现场负载和真实自动化效果。 |

## 6. 本轮实际验证

测试针对最新工作树重新执行；没有把旧报告的PASS当作本轮运行。既有10/09上午真实只读记录（21区域、74设备注册项、187实体）属于历史验收材料，本轮未重新连接真实HA，也未确认其当前在线状态。注册项数量不等于物理设备数量。

| 命令/范围 | 本轮结果 |
|---|---|
| `go test ./... -count=1 -timeout 180s` | PASS，全包；gateway 65.345s，smarthome 23.954s |
| `go vet ./...`、`go build ./...` | PASS，各步骤退出0 |
| `npm.cmd test` | 9文件、96/96 PASS |
| `npm.cmd run build` | TypeScript和Vite PASS；保留512.51kB包体提示 |
| `CONSOLE_BROWSER_CHANNEL=chrome npm.cmd run test:e2e` | 21/21 PASS，23.8s；临时Go后端+mock HA，非真实设备 |
| Python `unittest discover -s tests/integration` | 14/14 PASS，22.607s |
| `node --test tests/frontend/chat-widget.test.cjs` | 5/5 PASS；旧JS语法通过 |
| `git diff --check` | 按仓库正常换行配置通过；一次错误覆盖autocrlf=false造成CRLF误报，未据此批量改文件 |
| race | 本机`CGO_ENABLED=0`，本轮未重跑race；旧报告记有Linux race通过，新推送仍需以远端CI实际结果为准 |

初次在旧main运行Go测试因默认缓存权限失败；改用仓库已忽略的`.gocache`后通过。此环境错误不作为产品缺陷。

所有未跟踪文件加入暂存后，另检出少量EOF空行/Markdown尾空格并作格式整理；最终staged检查通过。详见发布记录，不能用暂存前仅覆盖已跟踪文件的检查代替全量检查。

## 7. 开发优先级与完成判据

1. **M1：前端对话HA闭环（最高优先级）**。可信实体映射/写策略→只读查询→持久提议→独立人工确认→单次HA动作→真实状态回读→历史恢复。默认首版管理员控制、成员查询；这是待确认的权限规划默认值，不声称用户已批准扩大写权限。
2. **M2：本地稳定运行与现场验收**。一台低风险设备现场验证、续期/重启/网络恢复、服务自启、备份恢复、日志容量及并发验证。M1模拟通过与M2现场通过分别签收。
3. **M3：知识与配置完整性**。模型/日志配置接线、来源标题、导入幂等/manifest/事务恢复、检索边界；再评估通用文件复制和知识维护。
4. **M4：按需扩展**。更多设备动作、成员写授权、规则停用恢复、私人发布/个体记忆、单副本容器化；微信等外部入口、云模型、k3s/Qdrant/Syncthing暂缓。

详细依赖、接口、文件和验收见开发计划。M1的完成不是“模型回复已打开”，而是用户确认后唯一一次服务调用、目标状态读回、结果准确展示且可恢复，并通过权限负向用例。

## 8. Git归档边界

以现有`codex/p1-p2-remediation`命名分支保存并推送，不覆盖main，不重写历史。按忽略规则、后端、前端与CI、规格/结果/历史记录分类提交。后端共享接线文件跨多个功能，作为一个可验证后端批次，避免拆出编译不完整的中间版本。

只纳入源码、测试、锁文件、示例配置、文档和审阅后的UI素材。排除`.local/`、`.superpowers/`运行证据、真实配置/凭据、数据库/缓存、node_modules、构建产物以及`go-agent/tools/ha_*.py`一次性凭据恢复/调试脚本；它们保留本地，不删除、不执行。历史文档中的本地证据链接不代表对应秘密文件会上传。

main下独有的7月审计、9/29 HA记录、旧E2E步骤和部署更新一并作为带日期的历史材料纳入分支；不将其旧状态覆盖本报告。最终提交hash和远端验证记录见 [发布记录](github-publication-2026-10-09.md)。
