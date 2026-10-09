# 家庭区域与本机接入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 在现有家序控制台实现所有家庭成员可读的区域→设备子页，接入真实 HA/Ollama，并按用户规则整理 HA 区域。

**Architecture:** 复用单 Go 服务、React/Vite、Cookie身份协调、Owned会话和现有推理。HA注册表与状态合成只读目录；领域数据投影给家庭API，HA管理凭据留服务端。真实模型启动前完成HA运行目录RAG排除。

**Tech Stack:** Go 1.26.4、Gin、gorilla/websocket、React/TypeScript/Vite、现有Vitest/Playwright。

**Spec:** ../specs/2026-10-08-family-areas-live-integration-design.md

## Global Constraints

- 用户2026-10-08批准设计和执行，修正：负一楼=地下室；老人房厕所→老人房；原待分类内容全部→其他。
- 复用地下室B1既有ID和地下室别名。负二楼不自动等于地下室；其他已认可细分区域保留。
- 所有有效且已改密的家庭账号读全部区域/设备；不扩大其他成员聊天、personal、成员管理或自动化权限。
- 不执行设备开关、窗帘、场景或自动化动作，不新增任意HA服务代理。
- 注册表契约以HA 2026.7.2官方源码为准；目录30秒缓存，上游总超时10秒，单请求取消不取消共享刷新。
- 安全DTO、明确null、未知组合404、私有响应no-store；不暴露凭据/路径/原始attributes和registry。
- 服务端合成无归属入口名为其他；实际HA其他区域存在则复用，避免重复其他卡。元数据执行将无归属设备及独立注册实体归到真实其他。
- 已有未提交P1/P2与前端改动必须保留；本轮采用逐任务前文件快照和差异包，不混入旧改动提交；不merge/push。
- 八荣八耻逐步检查；测试真实行为，禁止只检查源文本；任务报告保存失败→通过命令结果。

## Review Focus

- 内部运行目录被旧向量/BM25缓存或源正文复读绕过：Task1上下文唯一事实测试。
- 多路控制器区属和实际负载地点混淆：Task2继承标记与分类预览，Task4详情提示。
- 全部实体覆盖到另一区时控制器消失/计数相加：Task2并集及去重边界。
- HA失败与浏览器取消造成旧数据被当新数据：Task2共享刷新和失败时间测试，Task4轮询/身份测试。
- 同名其他区域、已存在别名、响应丢失后重试造成重复：Task2分类重放测试，Task5写后读回。

## Task 0: 基线、修正规则与执行记录

Files: 本spec、此plan、本轮workspace/progress.md、baseline-files/与logs。
- [x] 更新书面spec中的用户修正及已批准状态，其他合同不改变。
- [x] 验证附加worktree与分支；保存现有工作文件快照（排除node_modules、静态构建、缓存、秘密配置）。
- [x] 运行现有Go与前端测试一次，记录基线。
- [x] 建立ledger和任务brief；用户明确要求执行，保留该授权，不再重复请求计划确认。

## Task 1: HA运行资料与RAG隔离

Files:
- Create go-agent/internal/vault/content_policy.go, content_policy_test.go
- Modify vault/reader.go, embedding.go, inverted.go；core/app.go；smarthome/automation.go
- Test vault/content_policy_test.go；gateway/rag_integration_test.go（新增专属测试文件亦可），core配置测试

Interfaces:
- NewContentPolicy(vaultRoots map[string]string, excludedAbsDirs []string) (ContentPolicy,error)
- ContentPolicy.Allows(vaultName,relPath string) bool
- NewFileReaderWithPolicy(personalPath,agentPath string, policy ContentPolicy) *FileReader
- NewScopedEmbeddingStoreWithPolicy(infer EmbedClient, vr Reader, vaultDir,vaultName,model string, logger *zap.Logger, policy ContentPolicy) *EmbeddingStore
- 旧构造器保留。Policy必须在核心构造reader/索引前生效，cfg.SmartHome.AgentVaultPath含已有归档，即使HA当前关闭也防旧归档泄露。
- 阻断ReadPage、Search、ReadIndex、Status、BM25/trigger、embedding旧cache及source重读；不能要求HA归档新增tags才阻断。
- RuleDocument新增internal标记，保留业务配置。

- [x] 写失败测试：smart-home/rules公开样式旧页+唯一秘密在索引/cache/实际普通模型上下文消失；公开唯一事实仍存在，另Vault/private不进入。
- [x] 运行测试记录RED。
- [x] 实现目录政策和生产注入；验证目录等于Vault/越界/非规范路径/符号链接关系，保持允许的普通资料和旧构造器行为。
- [x] 跑受影响包及Go全套，记录GREEN、自查和八荣八耻；不改真实Vault文件。
- [x] 提交任务报告与本轮差异包，独立审查后完成。

## Task 2: HA注册表、区域目录与分类工具

Files:
- Create smarthome/registry.go, registry_test.go, catalog_types.go, catalog.go, catalog_test.go, area_classification.go, area_classification_test.go
- Modify smarthome/ws.go（仅认证复用必要处）, manager.go（目录生命周期）
- Create cmd/ha-areas/main.go（仅本机preview/apply；安全JSON报告）
- 不修改Task1拥有的automation.go、vault或core/app.go。

Interfaces:
- RegistrySnapshot {Areas []RegistryArea, Devices []RegistryDevice, Entities []RegistryEntity, Labels []RegistryLabel}
- (*HomeAssistantClient).GetRegistry(ctx context.Context) (RegistrySnapshot,error)
- .CreateArea(ctx,name string) (RegistryArea,error), .SetDeviceArea(ctx,id string,areaID *string) error, .SetEntityArea(ctx,id string,areaID *string) error
- CatalogSource interface { GetRegistry(context.Context)(RegistrySnapshot,error); GetStates(context.Context)([]EntityState,error) }
- NewCatalogService(parent context.Context, source CatalogSource) *CatalogService；.Get(context.Context)(CatalogSnapshot,error)；.Close()
- (*Manager).Catalog(ctx context.Context)(CatalogSnapshot,error)；Stop取消目录资源。
- CatalogSnapshot.ListAreas(query string)(AreaList,error)；.ListDevices(areaID string)(AreaDevices,error)；.Device(areaID,itemID string)(AreaDeviceDetail,error)
- ErrCatalogNotFound/ErrCatalogInvalid用于家庭404/422，其余safe503。
- 分类：BuildAreaPlan(reg RegistrySnapshot) AreaPlan；ApplyAreaPlan(ctx,client,plan)报告；默认preview，必须明确apply；复用/查证/写后读回，报告不得包含token。

Public JSON types（后端/前端共用契约）:
- 所有结果含meta {observed_at,last_attempt_at,last_success_at nullableRFC3339, freshness:fresh|stale|unknown, connection:connected|unavailable|unknown, error_code nullable安全码}
- AreaList {meta, totals:{devices,entities,standalone_entities}, areas:[{id,name,device_count,entity_count,standalone_entity_count,matches:[{id,name,kind}]}]}
- AreaDevices {meta,area:{id,name},devices:[DeviceView]}
- AreaDeviceDetail {meta,area:{id,name},device:DeviceView}
- DeviceView {id,kind:device|entity,name,area_id,direct_area_id nullable,membership:direct|entity|both,entity_count,domains:[],entities:[]EntityView,load_location_verified:false}
- EntityView {entity_id,name,domain,state/unit/last_changed/last_updated nullable,disabled:boolean,hidden:boolean,category nullable}
- IDs：a_ + UTF8 RawURLEncoding(areaID)，d_同样deviceID，e_同样entityID；无真实其他时合成u_other；真实其他存在时归属该真实ID。严格规范编码、UTF8、目录成员/区域组合校验。
- query最多128字符，匹配名称/entity_id；totals是全屋去重统计，过滤匹配只缩小areas/matches。

- [x] RED：假的HA WS服务严格核对命令、id/result、auth、失败、null、取消；不相关字段/Unix时间不破坏读。
- [x] GREEN：仅合法registry请求，不透传错误原文；状态通过既有REST读取。
- [x] RED/GREEN目录：设备/实体计数与有效area并集、跨区覆盖/空设备/独立实体/无state、其他复用、规范ID/未知404；共享取消/10秒超时/失败保旧时间/30秒缓存。
- [x] RED/GREEN分类：负一楼楼梯/中控/含负一楼场景→地下室B1；老人房厕所→老人房；原歧义→其他；地下室夹层长词优先；已有非空分类保留、标签冲突归其他并记原因、重跑无多余写、创建响应丢失读回、人工冲突。
- [x] CLI仅环境读取token/endpoint，输出文件不带凭据；逐项preview/apply/result，不提供设备service能力。
- [x] 受影响包测试、完整Go测试、报告和独立审查。

## Task 3: 家庭API与集成状态

Files: gateway/console_areas.go,_test.go；console_integrations.go,_test.go；console_routes.go、console_auth.go、console_chat.go（仅能力声明）、console_static.go,_test.go、http.go（必要字段）；chain/llm_steps.go、llm_contract_test.go；gateway/chat_contract.go、chat_test.go（真实Ollama兼容修复及显式参数入口校验）
Interfaces: 使用Task2 Manager.Catalog和snapshot方法；GET areas、areas/:areaID/devices、areas/:areaID/devices/:deviceID、integrations/status。
- integrations/status {ha:meta,ollama:{connection,checked_at nullable,models:[{name,available:boolean}],error_code nullable}}；复用infer.ListModels，不发chat或embed健康检查，30秒cache，10秒总超时。
- [x] RED：member/admin读全部，匿名/未改密/禁用拒绝；HA错误安全503或stale；未知组合404；响应不含fixture secrets/原registry attrs。
- [x] GREEN：注册路由及areas:read，复用身份/no-store；不扩大任何admin写权限。
- [x] RED/GREEN：真实Ollama0.35.1忽略旧thinking.type参数，改用已查证reasoning_effort=none；普通请求默认关闭思考，明确caller reasoning_effort/reasoning不被覆盖。覆盖buildChatRequest及现有LLM辅助请求，保留其他caller参数。真实探针只改该字段：空正文67.44秒→中文正文0.40秒。
- [x] RED/GREEN：只合法嵌套SPA导航200；缺失资源/API404、路径遍历/非法编码拒绝；模型离线/模型缺失状态如实。
- [x] 受影响及全套Go，报告和独立审查。

## Task 4: 区域React页面与行为验证

Files: web/src/api.ts、app.tsx、icons.tsx、styles.css、pages/HomePage.tsx、AccountPage.tsx；新增pages/AreasPage.tsx、AreaPage.tsx、DevicePage.tsx、家庭查询hook与tests；tests/consolefixture/main.go、web/e2e按需。
- [x] RED：区域→设备→详情、其他/空区、跨区覆盖提示、设备通道不混计、搜索、nullable/禁用/不可用、服务断连/旧数据、身份切换迟到响应。
- [x] GREEN：严格按Task2/3JSON，不复制原业务；复用coordinatedFetch/epoch，不直接连接HA/Ollama；配置实体折叠但可查看。
- [x] 30秒可见轮询/隐藏暂停，route变化取消；嵌套路由深链、手机返回、键盘可达。
- [x] 更新真实Go fixture提供模拟HA registry/REST服务，保留canned聊天声明；加区域E2E与现有账号/私有历史回归。
- [x] npm test / typecheck / build、编译JS syntax、Chrome E2E，报告独立审查。

## Task 5: 本机接入与HA分类落地

Files: 独立.local运行配置/本机报告（不提交秘密）；go-agent/docs/console-local-setup.md；docs/family-areas-live-results-2026-10-08.md
- [x] 正常 HA 授权取得短期服务端凭据，不使用旧 auth 恢复脚本。
- [ ] 新的 365 天服务 token 具体权限与用途已说明，等待授权后创建；短期 token 不计长期运行完成。
- [x] 真实Ollama version/tags、非敏感中文生成、临时公开唯一事实RAG；不把fixture回复当实测。
- [x] 真实 Go 18083、独立账号目录、管理初始化、正常 Cookie 登录与区域/聊天/历史 API 通过；密码在受保护文件。
- [ ] 用户手动完成真实网页登录后，验收真实页面区域、聊天及历史；复制密码给沙箱被自动审批拒绝，未绕过。
- [x] 分类preview记录已有area-target影响/未验，执行用户已授权的区域metadata变更，逐条读回；无开关动作。
- [x] 重放 0 新增修改；真实服务重启后同一登录、本人两条历史完整一致；出具结果和现场边界。

## Task 6: 最终验收与完整审查

- [x] Go test/vet/build、Vitest/typecheck/build/Chrome、旧widget、Pythonfixture、diff检查；Linux CGO race。
- [x] 完整差异包、各任务独立审查/增量复审及最终跨模块集成审查通过；平台限制新审查线程，采用跨负责人审查，限制记入 ledger。实质问题有 RED/GREEN。
- [x] 记录八荣八耻逐项证据、真实服务结果、HA成功/冲突/失败数和现场未验范围；向用户提供真实页面入口与结果文档。
