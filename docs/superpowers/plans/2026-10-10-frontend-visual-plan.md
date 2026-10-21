# 家序组件库重置与全界面迁移 Implementation Plan

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 重建统一组件库和样式体系，将全部现有界面迁入新库，提升状态与操作的可理解性，同时保留现有前端业务架构。

**Architecture:** 新ui层只管理视觉、原生语义和受控交互；既有页面、Hook、认证、路由和聊天状态继续拥有业务。通过暂存的分层旧样式逐页迁移，最后删除兼容层；R1—R5为独立修复线，录音计时是单列增量。

**Tech Stack:** 现有React19、TypeScript、CSS、原生HTML、Vite、Vitest、Playwright与Go离线fixture；不新增运行或测试依赖。

**Spec:** [主方案版本2](../specs/2026-10-10-frontend-reliability-and-visual-design.md)及[组件库设计规格](../specs/2026-10-10-component-library-design.md)。本文件替代上一版V1—V8局部改进计划。

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

PRODUCT_ROOT来自审查基线84b5ebba90964857a4986f1055a2bc1f599aec17或已核对后续改动的可写隔离工作树，下列路径相对其go-agent。页面专属样式可重写；API、Route、Hook和reducer不得在UI任务中顺手重写。

## Review Focus

- Button默认type导致误提交、原生字段关联／ref丢失：DS2/DS3与M1必须证明真实表单及密码语义。
- Dialog关闭／重挂载泄漏敏感结果或抢回旧焦点：DS3与M5覆盖正常关闭、pending、身份／路由卸载。
- 新外壳包旧页面、旧页面嵌新UI时全局CSS污染：DS1/DS6/Q1验证隔离及最后删除旧样式。
- IME、760px边界、上翻、SID与语音迟到回调：M6必须保留原拥有者和交互，不交给基础UI判断。
- 未知状态、0/null、错误批次、未知规则及未保存草稿：DS5和M2/M6/M7/M8/M9显示真实数据，不伪造成功。

## 文件与依赖地图

- `web/src/ui/styles/`：layers、tokens、reset、controls、forms、layout、feedback；唯一基础样式来源。
- `web/src/ui/*.tsx`：通用组件，公共props以组件库规格第4节为准。
- `web/src/ui/index.ts`：只导出已稳定通用组件；业务与catalog不向它反向注册。
- `web/src/ui/catalog/`＋`web/catalog/index.html`：开发专用真实组件展示。
- `web/src/styles/legacy.css`＋临时LegacyPageBoundary：旧页迁移兼容，Q1删除。
- `web/src/styles/*.css`与`web/src/chat/presentation.css`：必要页面／业务布局；不重新定义控件基础样式。
- 既有familyViews、knowledgeViews、Home摘要、ControlCard、VoiceControls：沿用业务归属并消费新UI。
- R1—R5保持独立；M5成员验收依赖R5，M6依赖R2/R3/R4，M8设备语义复用R4。涉及App或MembersPage等共同文件时顺序整合。

全部步骤当前未执行。提交步骤只在后续实施授权中使用。完整路线为DS1—DS6→M1—M9→Q1—Q2；可独立页面任务在共用组件稳定后并行，但不得同时覆盖同一文件。

### Task DS1: 新视觉基础、CSS隔离与最小展示入口

**Files:**
- Create: `web/src/ui/styles/layers.css`、`web/src/ui/styles/tokens.css`、`web/src/ui/styles/reset.css`、`web/src/ui/ui.css`。
- Create: `web/src/styles/legacy.css`、`web/src/components/LegacyPageBoundary.tsx`。
- Create: `web/catalog/index.html`、`web/src/ui/catalog/main.tsx`、`web/src/ui/catalog/Catalog.tsx`、`web/src/ui/catalog/catalog.css`。
- Create/Test: `web/playwright.ui.config.ts`、`web/ui-e2e/foundation.spec.ts`。
- Modify: `web/src/main.tsx`、`web/src/App.tsx`、旧三份CSS及ChatPage的样式导入、`web/package.json`、`web/vite.config.ts`。

**Interfaces:**
- Produces: 三级jx token、规格第3节全部固定值；CSS层次jx-legacy→jx-reset→jx-tokens→jx-ui→jx-layout→jx-features。
- Produces: `LegacyPageBoundary({children: ReactNode})`只提供旧页面样式边界，不持有身份／请求状态。
- Produces: `npm run ui:catalog`使用Vite127.0.0.1:18082、strictPort，入口`/app/catalog/index.html`；`npm run test:ui`运行独立UI Playwright配置。

- [ ] **Step 1:** 记录三份旧CSS全局规则、important、页面消费者与覆盖关系；foundation.spec对实际色板／排版与隔离后的原页面计算样式写验收，确认当前无新token及展示入口，检查失败有具体原因。
- [ ] **Step 2:** 按规格值建立layers/tokens/reset；新基础控件将标data-jx-ui，外壳不标该排除边界。旧规则进入单个legacy入口，限定旧页面并排除新UI；逐条处理标题、链接、焦点、hidden、继承和important，立即取消旧直接导入，旧源暂存。
- [ ] **Step 3:** 用既有Vite建立独立catalog入口，仅展示token／原生基础样例。package添加上述脚本，Vitest exclude新增ui-e2e/**，产品Vite构建仍单入口，Router和Go静态规则不改。
- [ ] **Step 4:** web运行`npm run typecheck`、`npm run build`、`npm run test:ui -- ui-e2e/foundation.spec.ts`及既有`npm run test:e2e -- e2e/console.spec.ts`；预期新基础值正确，原认证与页面仍可访问。记录隔离前后差异，不以隐藏内容修复布局。
- [ ] **Step 5:** 提交`feat(ui): establish design tokens and isolated migration styles`。

### Task DS2: 图标、按钮与头像的统一契约

**Files:**
- Create: `web/src/ui/Icon.tsx`、`web/src/ui/Button.tsx`、`web/src/ui/Avatar.tsx`、`web/src/ui/styles/controls.css`、`web/src/ui/index.ts`。
- Modify: `web/src/icons.tsx`为单向兼容重导出；catalog样例。
- Create/Test: `web/src/ui/Button.test.tsx`、`web/ui-e2e/controls.spec.ts`。

**Interfaces:**
- Produces: Icon/IconName、Button/IconButton/ActionLink、Avatar，签名按规格4.1；Button默认type=button。
- Consumes: DS1 token/reset；旧IconName名称保持，必要SVG增量单一定义。

- [ ] **Step 1:** 写真实表单用例：Button默认点击不submit，显式type=submit提交一次，disabled/busy不能点击或键盘激活；IconButton有label，ref可定位原生button，name/form属性不丢失。
- [ ] **Step 2:** web运行`npm test -- --cache=false src/ui/Button.test.tsx`确认新契约缺失；实现三种按钮导出、regular44px／comfortable48px及busy表现，不绑定任何Promise／API。
- [ ] **Step 3:** 移动SVG资产，旧icons只重导出；新增实际录音／播放／上传所需图形。Avatar空名称降级，图标标准尺寸与aria-hidden按规格。catalog展示variant、focus、disabled、busy和长标签。
- [ ] **Step 4:** 运行上述Vitest、`npm run typecheck`、`npm run test:ui -- ui-e2e/controls.spec.ts`；检查computed color、对比、触控尺寸与focus-visible。预期表单行为正确，新组件在legacy容器里也符合规范。
- [ ] **Step 5:** 提交`feat(ui): rebuild action controls and icon assets`。

### Task DS3: 原生表单、详情与受控Dialog

**Files:**
- Create: `web/src/ui/Field.tsx`、`web/src/ui/Input.tsx`、`web/src/ui/Textarea.tsx`、`web/src/ui/Select.tsx`、`web/src/ui/Checkbox.tsx`、`web/src/ui/FileInput.tsx`、`web/src/ui/Disclosure.tsx`、`web/src/ui/Dialog.tsx`、`web/src/ui/styles/forms.css`。
- Create/Test: `web/src/ui/Field.test.tsx`、`web/src/ui/Dialog.test.tsx`、`web/ui-e2e/forms-dialog.spec.ts`。
- Modify: ui/index与catalog。

**Interfaces:**
- Produces: Field与原生控件按规格4.2；`fieldDescriptionIds(id, hasHint, hasError, existingIds?)`返回string或undefined，合并实际描述ID。
- Produces: Dialog受控open、dismissible、restoreFocus、onRequestClose及HTML节点focus refs；卸载不主动恢复焦点。
- Produces: Disclosure使用defaultOpen与原生details ref，折叠不卸载，invalid先展开。

- [ ] **Step 1:** Field用例验证label/描述ID/错误关联、合并已有安全提示、required仍需原生传入；Input/Textarea透传ref及composition/key事件，Select/Checkbox/FileInput保留原生提交值。先运行对应Vitest确认失败。
- [ ] **Step 2:** 实现显式Field组合，不clone任意children；控件只透传HTML属性、invalid与ref。Disclosure捕获原生invalid时先open，业务错误由页面ref主动定位；不引入校验库或业务规则。
- [ ] **Step 3:** 实现Dialog open同步和showModal幂等、用户关闭回调去重；dismissible=false防止cancel默认关闭，遮罩／按钮一致，method=dialog不可绕过。正常open→false才按连接状态恢复焦点，卸载不恢复。catalog包含长内容及不可关闭场景。
- [ ] **Step 4:** web运行`npm test -- --cache=false src/ui/Field.test.tsx src/ui/Dialog.test.tsx`、`npm run typecheck`、`npm run test:ui -- ui-e2e/forms-dialog.spec.ts`。真实浏览器验证Tab/ShiftTab、Escape、初始／返回焦点、200%字号、折叠中无效字段能定位；jsdom不代替原生焦点约束。
- [ ] **Step 5:** 提交`feat(ui): rebuild native forms disclosure and dialog contracts`。

### Task DS4: 页面布局、列表与导航呈现

**Files:**
- Create: `web/src/ui/PageFrame.tsx`、`web/src/ui/PageHeader.tsx`、`web/src/ui/Card.tsx`、`web/src/ui/Section.tsx`、`web/src/ui/Layout.tsx`、`web/src/ui/KeyValueList.tsx`、`web/src/ui/ListRow.tsx`、`web/src/ui/Breadcrumb.tsx`、`web/src/ui/AppShell.tsx`、`web/src/ui/Navigation.tsx`、`web/src/ui/AuthFrame.tsx`、`web/src/ui/styles/layout.css`。
- Create/Test: `web/ui-e2e/layout.spec.ts`；Modify: ui/index/catalog。

**Interfaces:**
- Produces: 规格4.3/4.5全部呈现接口，Layout导出Stack/Grid/ActionGroup。
- Produces: `navigationItemClass(active:boolean): string`；slots接受ReactNode，NavLink与能力筛选在调用方。
- Consumes: DS1 token、DS2按钮图标、DS3 Disclosure。

- [ ] **Step 1:** catalog模拟长标题、长标识、列表带链接／带动作两种结构、侧栏／手机导航与大字号；先写landmark和布局验收，无新组件时确认失败。
- [ ] **Step 2:** 实现非交互Card、ListRow内容slots和语义列表；链接／按钮由调用者用真实元素，禁止嵌套交互。PageFrame生成唯一main，AppShell不重复main/nav，Navigation拥有nav。
- [ ] **Step 3:** 实现760px外壳切换、600/1000px内容网格与max1120，所有长内容换行；AuthFrame只接展示内容。catalog导航使用模拟原生链接，UI不import Router。
- [ ] **Step 4:** 运行`npm run typecheck`、`npm run test:ui -- ui-e2e/layout.spec.ts`；检查320/390/768/1440与759/760/761px，200%字号关键操作可访问、无横向溢出、landmark不重复。
- [ ] **Step 5:** 提交`feat(ui): rebuild layout and navigation presentation primitives`。

### Task DS5: 状态、数量、阶段与可信进度

**Files:**
- Create: `web/src/ui/StatusBadge.tsx`、`web/src/ui/Alert.tsx`、`web/src/ui/LoadingState.tsx`、`web/src/ui/EmptyState.tsx`、`web/src/ui/Metric.tsx`、`web/src/ui/StepSequence.tsx`、`web/src/ui/ProgressSummary.tsx`、`web/src/ui/styles/feedback.css`。
- Create/Test: `web/src/ui/feedback.test.tsx`、`web/ui-e2e/feedback.spec.ts`；Modify: ui/index/catalog。

**Interfaces:**
- Produces: `StatusTone = 'success'|'warning'|'danger'|'info'|'neutral'`与规格4.4全部接口。
- Produces: `StepItem = {id:string;label:string;state:'waiting'|'active'|'complete'|'failed'|'unknown'}`。
- Consumes: 已确定tone/label/数量/进度值，UI不能解释业务code或缺值为零。

- [ ] **Step 1:** 写Metric(null)显示暂无数据、Metric(0)显示真实0；进度0/0、NaN、负数、超界不出现有效progress；StatusBadge默认无live，Alert播报由调用者指定，阶段每项有可读状态。
- [ ] **Step 2:** web运行`npm test -- --cache=false src/ui/feedback.test.tsx`先失败，再实现对应组件及CSS，progress只接受合法有限整数；不模拟识别／生成百分比。
- [ ] **Step 3:** catalog完整展示tone、busy、error、stale业务组合的模拟状态及长文字；明确success表现不能用于安装或配置推断，示例不调用业务函数或网络。
- [ ] **Step 4:** web运行上述Vitest、`npm run typecheck`、`npm run test:ui -- ui-e2e/feedback.spec.ts`；预期状态不只靠颜色、对比达标，读取阶段只在需要时播报。
- [ ] **Step 5:** 提交`feat(ui): rebuild feedback metrics and verified progress displays`。

### Task DS6: 组件展示、文档与依赖方向检查

**Files:**
- Modify: catalog、ui/index、package脚本及UI Playwright用例。
- Create: `web/scripts/check-ui-boundaries.mjs`、`web/scripts/check-ui-boundaries.test.mjs`。
- Create: 仓库根目录`docs/component-library.md`。

**Interfaces:**
- Produces: `npm run check:ui-boundaries`，用已有TypeScript解析／解析模块依赖，检查ui运行代码及catalog不引入业务层；测试文件单独排除。
- Produces: 使用文档与完整状态矩阵。产品目录保持默认Vite单入口，展示页不进入Go静态产物。

- [ ] **Step 1:** 为依赖检查构造临时模块图，断言直接／barrel间接／动态字面量导入auth/API/业务Hook被拒绝，纯ui/React/CSS通过；拒绝无法静态确认的业务动态导入，不以文件名正则代替依赖图。
- [ ] **Step 2:** 实现脚本及`check:ui-boundaries`命令，catalog入口只允许UI和明确模拟数据，不能通过真实ControlCard带入auth/api。运行`node --test scripts/check-ui-boundaries.test.mjs`，预期通过。
- [ ] **Step 3:** 文档覆盖规格第4节所有公共props、状态、slots、表单例子、focus策略、业务边界与新增组件条件；完善真实catalog，不另安装Storybook或样式框架。
- [ ] **Step 4:** 运行`npm run check:ui-boundaries`、`npm run test:ui`、`npm run build`；确认catalog无API请求，生产输出不包含catalog入口，new shell+legacy page、legacy page+new controls两类组合样式正确。
- [ ] **Step 5:** 提交`docs(ui): publish component contracts and enforce dependency boundaries`。

### Task M1: 应用外壳、认证与全局状态界面

**Files:**
- Modify: `web/src/App.tsx`；Create: `web/src/components/Brand.tsx`、`web/src/styles/auth.css`；Test: `web/src/App.test.tsx`、`web/src/auth.test.tsx`、`web/e2e/console.spec.ts`、`web/e2e/errors.spec.ts`。

**Interfaces:**
- Consumes: AppShell/Navigation/AuthFrame、新表单／反馈组件。
- Produces: 既有App/AuthProvider/PrivateApp边界与Routes保持，导航children使用原NavLink和items；旧页临时包LegacyPageBoundary。

- [ ] **Step 1:** 将旧能力导航、登录、首次改密、uninitialized/unavailable/logout_failed/loading/denied用例作为迁移契约；额外断言唯一main与正确landmark、显式submit和password autocomplete。
- [ ] **Step 2:** 重做Brand和外壳呈现，App只替换JSX与视觉结构；保留Route path、守卫、items过滤、identity＋epoch key、logout处理与所有auth Hook调用位置。未迁移页用旧样式边界，避免重新挂载业务根。
- [ ] **Step 3:** 登录／强制改密使用统一表单与反馈，保留原字符串长度验证和密码清空行为；R1等待提示在原状态路径衔接，不新建认证状态机。
- [ ] **Step 4:** 运行`npm test -- --cache=false src/App.test.tsx src/auth.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/console.spec.ts e2e/errors.spec.ts`；预期角色入口、深链、退出失败与私人隔离不退化。
- [ ] **Step 5:** 提交`feat(console): rebuild shell and authentication presentation`。

### Task M2: 首页重做与真实服务摘要

**Files:**
- Modify: `web/src/pages/HomePage.tsx`、`web/src/HomeFamilySummary.tsx`、`web/src/HomeCollectionSummary.tsx`。
- Create: `web/src/homeStatus.ts`、`web/src/homeStatus.test.ts`、`web/src/styles/home.css`。
- Test: `web/src/Completion.test.tsx`、`web/e2e/completion.spec.ts`；Create/Test: `web/e2e/redesign.spec.ts`。

**Interfaces:**
- Produces: `integrationStatusItems(status:IntegrationsStatus|null,readFailed:boolean): {id:string;label:string;tone:StatusTone;detail:string}[]`。
- Produces: `HomeFamilySummary({areas,integrations})`接受原useFamilyQuery返回值，查询由HomePage按既有enabled条件调用，不重复请求。
- Consumes: 新Section/Metric/StatusBadge/ListRow、既有sessions与suggestions数据。

- [ ] **Step 1:** 写connected＋stale不是整体健康、installed不是生成验证、失败读取保留旧数据警告、null不能显示0／成功用例；旧呈现不能满足分离状态时确认失败。
- [ ] **Step 2:** 重做欢迎→主入口→异常→家庭概况→最近对话的布局，管理员摘要权限及统计口径保留。时刻、模型名进Disclosure，异常外露，不新增健康／推理探测。
- [ ] **Step 3:** 320×740普通名称下断言主对话入口完整高于底部导航；长名称、异常和200%字号允许滚动。权限、SID、草稿跳转不改变。
- [ ] **Step 4:** 运行`npm test -- --cache=false src/homeStatus.test.ts src/Completion.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/completion.spec.ts e2e/redesign.spec.ts`；保存首页正常／旧数据／失败截图。
- [ ] **Step 5:** 提交`feat(home): rebuild entry points and verified family summaries`。

### Task M3: 全部家庭目录界面

**Files:**
- Modify: `web/src/pages/AreasPage.tsx`、`web/src/pages/AreaPage.tsx`、`web/src/pages/DevicePage.tsx`、`web/src/familyViews.tsx`。
- Create: `web/src/styles/family.css`；Test: `web/src/Areas.test.tsx`、`web/e2e/areas.spec.ts`、redesign.spec。

**Interfaces:**
- Consumes: 新Page/Card/Field/Select/Metric/ListRow/Disclosure；既有AreaList/AreaDevices/AreaDeviceDetail和R4 formatter。
- Produces: familyViews保留原导出与业务标签，内部组合新UI，不把HA DTO传入通用StatusBadge。

- [ ] **Step 1:** 固定q/areaID/deviceID、搜索128字符、跨区域筛选、device/entity分类、去重统计、membership与诊断分组契约；数据未知和不可用回归保留。
- [ ] **Step 2:** 重做区域列表、区域详情和实体详情全部呈现，统一搜索／卡片／数字／单位／时间／诊断详情；负载地点未验证提示始终可见，禁止增加控制动作。
- [ ] **Step 3:** 验证受影响familyViews消费者一起迁移；设备名与entity_id长文本换行、卡片嵌套匹配子链接无交互嵌套，未知类别按R4降级。
- [ ] **Step 4:** 运行`npm test -- --cache=false src/Areas.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/areas.spec.ts e2e/redesign.spec.ts`，预期全部目录与权限契约通过。
- [ ] **Step 5:** 提交`feat(family): rebuild area device and entity presentation`。

### Task M4: 公开与私人知识全界面

**Files:**
- Modify: `web/src/pages/KnowledgePage.tsx`、`web/src/pages/AdminKnowledgePage.tsx`、`web/src/knowledgeViews.tsx`。
- Create: `web/src/styles/knowledge.css`；Test: `web/src/Completion.test.tsx`、`web/src/chat/SafeMarkdown.test.tsx`、completion/redesign E2E。

**Interfaces:**
- Consumes: 新页面／表单／列表／反馈；原KnowledgeBrowser、useFamilyQuery/useConsoleAction和SafeMarkdown。
- Produces: 保持KnowledgeBrowser(personal?)、URL q/path与单次问答／导入接口；Markdown仅换阅读面样式。

- [ ] **Step 1:** 保留公开／personal访问、来源链接、搜索200字符、私人问题4000、导入100000、来源标题200与单次问答不进历史测试；安全HTML／链接／图片用例保持。
- [ ] **Step 2:** 重做检索、搜索结果、阅读面、来源、私人问答及导入分区；q/path、失败独立提示、已导入未公开含义不变。基础控件替换全部完成，文本正文保持安全Markdown。
- [ ] **Step 3:** 长标题／来源路径可读，搜索与导入submit显式，失败／busy不自动重试；不要为新阅读面改用dangerouslySetInnerHTML。
- [ ] **Step 4:** 运行`npm test -- --cache=false src/Completion.test.tsx src/chat/SafeMarkdown.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/completion.spec.ts e2e/redesign.spec.ts`；保存列表／正文／私人管理截图。
- [ ] **Step 5:** 提交`feat(knowledge): rebuild search reading and private knowledge surfaces`。

### Task M5: 我的与成员管理

**Files:**
- Modify: `web/src/pages/AccountPage.tsx`、`web/src/pages/MembersPage.tsx`。
- Create: `web/src/styles/account.css`；Test: `web/src/App.test.tsx`、`web/src/pages/MembersPage.test.tsx`、console/redesign E2E。

**Interfaces:**
- Consumes: 新资料／入口／表单／Dialog组件；原useAuth与成员consoleApi调用，R5操作代次。
- Produces: 使用始终挂载的受控Dialog，open=dialog!==null；pending传dismissible=false；临时密码仅归属当前操作。

- [ ] **Step 1:** 先确保R5缺陷回归通过，保留密码12—128字符、改密后失效、成员role/CSRF与一次性密码用例；不将成员请求强行换到新封装。
- [ ] **Step 2:** 重做个人资料、家庭入口、安全表单与成员列表，替换页内Modal为新Dialog，删除旧Modal重复实现。create/rename/reset/toggle以及dialog generation保留原拥有者。
- [ ] **Step 3:** 正常关闭open→false恢复焦点；身份／路由卸载不抢旧焦点，敏感结果清空。busy不能关闭或换成员；发生在途切换或迟到回应时R5检查继续有效。
- [ ] **Step 4:** 运行`npm test -- --cache=false src/App.test.tsx src/pages/MembersPage.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/console.spec.ts e2e/redesign.spec.ts`；检查弹窗200%字号、长名与临时密码归属。
- [ ] **Step 5:** 提交`feat(account): rebuild profile security and member dialogs`。

### Task M6: 全对话界面、控制附件与语音反馈

**Files:**
- Modify: `web/src/pages/ChatPage.tsx`、`web/src/chat/ControlCard.tsx`、`web/src/chat/VoiceControls.tsx`、`web/src/chat/useVoice.ts`。
- Create: `web/src/chat/presentation.css`；Test: ChatPage、ControlCard、Voice、chat-state、SafeMarkdown既有测试及control/completion/redesign E2E。

**Interfaces:**
- Consumes: 新布局／卡片／表单／状态组件，R2/R3/R4、原refs/reducer/useVoice。
- Produces: useVoice仅新增`recordedSeconds:number`和`maxRecordingSeconds:60`，其余签名不变；按实际recorder start与performance.now计算。
- Produces: ControlCard继续原props及R3 refresh接口；业务状态不搬进Card。期限一旦确认过期不因时钟回拨重新启用。

- [ ] **Step 1:** 将R2—R4回归、IME composition、桌面Enter／手机换行、上翻不跳底、unknown核对／明确重试、只读历史固定为迁移契约；新增过期时钟回拨及60秒ready不自动发送的有效失败用例。
- [ ] **Step 2:** 全面重做历史列表、消息、错误恢复区、输入区、查询与控制附件、语音区；仅替换呈现和局部样式，不移动状态Hook/ref所有权，不引入独立聊天store或Composer状态机。
- [ ] **Step 3:** 控制呈现设备／房间、当前→目标、状态、有效期、动作与观察详情；unknown与现场提示外露，执行中用R3 GET刷新。非法expires_at或已判过期禁用确认；权限和重复动作锁保留。
- [ ] **Step 4:** 实现单列录音计时增量，权限等待不计时，60秒停止进入ready，取消／SID／卸载清理。识别和朗读配置分开，云端提示近操作；playback受阻可手动恢复，自动朗读默认关闭，秒数不每秒播报。
- [ ] **Step 5:** 运行`npm test -- --cache=false src/pages/ChatPage.test.tsx src/chat/ControlCard.test.tsx src/chat/Voice.test.tsx src/chat/chat-state.test.ts src/chat/SafeMarkdown.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/control.spec.ts e2e/completion.spec.ts e2e/redesign.spec.ts`。检查759/760/761px行为，保存各提议状态／录音截图。
- [ ] **Step 6:** 提交`feat(chat): rebuild conversation attachments and voice presentation`；报告区分UI迁移、期限回拨回归和录音计时增量，真实音质单列未验证。

### Task M7: 全采集界面与可信阶段

**Files:**
- Create: `web/src/collectionPresentation.ts`、`web/src/collectionPresentation.test.ts`、`web/src/styles/collection.css`。
- Modify: `web/src/pages/CollectionPage.tsx`、`web/src/HomeCollectionSummary.tsx`；Test: `web/src/Completion.test.tsx`、`web/e2e/completion.spec.ts`、`web/e2e/redesign.spec.ts`。

**Interfaces:**
- Produces: `collectionPresentation(data:CollectionView): {title:string;steps:StepItem[];progressKnown:boolean;currentSnapshot:boolean}`。
- Consumes: 新StepSequence/ProgressSummary、原CollectionView与14天分析操作。

- [ ] **Step 1:** 写0/0、非法批次、超出总数、旧／本轮snapshot、idle无完整成功、checkpoint非成功的用例；先运行`npm test -- --cache=false src/collectionPresentation.test.ts`确认失败。
- [ ] **Step 2:** 实现确定性映射，重做采集流程、进度、窗口、最近完整成功、失败与分析分区；非法日期保持未知。完整时间戳／checkpoint进Disclosure，限制在摘要旁保留。
- [ ] **Step 3:** 分析仍明确触发、只产生建议；刷新显示不额外采集。首页摘要复用同一派生语义，管理员能力条件不变。
- [ ] **Step 4:** 运行上述单测、`npm test -- --cache=false src/Completion.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/completion.spec.ts e2e/redesign.spec.ts`，保存history/failed手机桌面截图。
- [ ] **Step 5:** 提交`feat(collection): rebuild verified collection stages and analysis UI`。

### Task M8: 全自动化界面与确定性规则关系

**Files:**
- Create: `web/src/automationPresentation.ts`、`web/src/automationEntityDirectory.ts`、`web/src/automationPresentation.test.ts`、`web/src/automationEntityDirectory.test.ts`、`web/src/styles/automations.css`。
- Modify: `web/src/pages/AutomationsPage.tsx`；Test: Completion与completion/redesign E2E。

**Interfaces:**
- Produces: `AutomationEntity=Pick<EntityView,'entity_id'|'name'|'domain'|'device_class'>`；`summarizeRule(rule:SuggestionRule|null,entities:ReadonlyMap<string,AutomationEntity>): {supported:boolean;triggers:string[];conditions:string[];actions:string[];unresolvedEntityIds:string[]}`。
- Produces: `readAutomationEntityDirectory(signal:AbortSignal): Promise<ReadonlyMap<string,AutomationEntity>>`，提取原readEntities，目录设备请求并发≤4，按原身份和选中建议检查回填。
- Consumes: R4 formatter、新Card/ListRow/状态/详情/表单组件；绑定和摘要使用同一已授权目录读取能力。

- [ ] **Step 1:** 对照Go automation.go验证器，测试time/state触发、sun sunset/state条件、light/switch/fan turn_on/off且domain匹配，非法／未知结构不能生成可靠摘要；未知occupancy off不得译成无人。
- [ ] **Step 2:** 实现纯函数，重做列表、详情、何时→条件→动作、版本／证据／授权与绑定区域。明确任一触发、全部条件、动作顺序；原规则保留，关系图不标成已执行阶段。
- [ ] **Step 3:** 提取原目录读取，fresh/connected才标已核对名称，能力不足／403／stale降级为ID；页面切换／旧epoch不得回填。supported只影响摘要，不给予confirm能力。
- [ ] **Step 4:** 保留can_*、绑定换版本、先核对后明确重试、sharing revision/dirty及旧授权只可撤销；新Checkbox不改变确认或共享规则。
- [ ] **Step 5:** 运行`npm test -- --cache=false src/automationPresentation.test.ts src/automationEntityDirectory.test.ts src/Completion.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/completion.spec.ts e2e/redesign.spec.ts`；Go运行`go test ./internal/smarthome ./internal/gateway -run 'Suggestion|Automation' -count=1`，预期规则与权限回归通过。
- [ ] **Step 6:** 提交`feat(automations): rebuild review sharing and verified rule relationships`。

### Task M9: 全助手设置界面

**Files:**
- Modify: `web/src/pages/AssistantPage.tsx`；Create: `web/src/styles/assistant.css`。
- Test: `web/src/Assistant.test.tsx`、completion/redesign E2E。

**Interfaces:**
- Consumes: 新表单／状态／详情／卡片组件；原AssistantSettings、ServiceMode、PersonalityMetadata与PUT body，接口不变。
- Produces: 文字回答、语音识别、语音朗读的模式分区与上下文提示；高级详情ref由页面控制业务错误展开。

- [ ] **Step 1:** 固定草稿不被周期读取覆盖、版本冲突、密钥留空／移除、Markdown UTF-8/256KiB测试；追加切模式不自动保存／不丢地址、高级字段错误能展开定位。
- [ ] **Step 2:** 全面重做服务模式对比、密钥／区域、部署、人格分区；配置与真实可用性区分，保存不测试收费服务。
- [ ] **Step 3:** 地址和路径进Disclosure，模式选择保留字段值；云端数据和人格发送提示靠近相关动作。文字模式新对话生效、人格下一条回复生效，错误与成功反馈分别呈现。
- [ ] **Step 4:** 运行`npm test -- --cache=false src/Assistant.test.tsx`、`npm run build`、`npm run test:e2e -- e2e/completion.spec.ts e2e/redesign.spec.ts`，检查320px与200%字号下选择／密钥／上传／保存可用。
- [ ] **Step 5:** 提交`feat(assistant): rebuild service modes deployment and personality UI`。

### Task Q1: 旧基础UI和样式的最终退出

**Files:**
- Delete after zero consumers: `web/src/styles.css`、`web/src/console.css`、`web/src/pages/chat.css`、`web/src/styles/legacy.css`、`web/src/components/LegacyPageBoundary.tsx`、`web/src/icons.tsx`。
- Modify: main、App、UI样式入口、所有残留旧基础类消费者及迁移清单。
- Test: ui-e2e全量、`web/e2e/redesign.spec.ts`。

**Interfaces:**
- Produces: 单一基础UI库，业务专属styles与chat/presentation.css有明确归属；无legacy import／boundary／旧基础组件使用。
- Consumes: DS1—DS6、M1—M9全部完成；保留SafeMarkdown和业务纯函数。

- [ ] **Step 1:** 用rg清点旧导入、边界、基础类与icon入口消费者，逐个确认迁移；生产入口在禁用旧CSS状态运行全部页面，不能仅检查构建通过。
- [ ] **Step 2:** 删除零消费者旧文件／规则及过渡导出，保留必要业务局部样式。确保没有在feature样式重新复制Button/Input/Modal基础定义；逐页解除临时边界。
- [ ] **Step 3:** 运行`npm run check:ui-boundaries`、`npm run typecheck`、`npm run build`、`npm run test:ui`、`npm run test:e2e -- e2e/redesign.spec.ts`；预期无旧CSS也完整可用，兼容层查询结果为空。
- [ ] **Step 4:** 保存全页面迁移清单与旧文件退出证据，提交`refactor(ui): retire legacy controls and stylesheet overrides`。本任务只清理呈现与样式，不重构业务架构。

### Task Q2: 全面验收、fixture清理和交付

**Files:**
- Modify: UI与业务E2E、`web/playwright.config.ts`、package脚本。
- Create: `web/scripts/build-consolefixture.mjs`；必要时按退出挂起证据修改`tests/consolefixture/main.go`、`tests/consolefixture/main_test.go`、`tests/consolefixture/family_dependencies.go`。
- Create: 仓库根目录`docs/frontend-redesign-acceptance-2026-10-10.md`；截图保存到本机忽略目录，不提交仓库；执行日期改变时同步命名。

**Interfaces:**
- Produces: pretest:e2e用Node参数数组编译独立fixture binary到PRODUCT_ROOT内忽略的.tmp/consolefixture；Playwright直接运行binary（Windows为.exe），不通过go run中间进程。
- Produces: 原18081、baseURL、独立测试数据不变；用例成功／失败／取消后自有fixture结束、端口释放、runner自然结算。

- [ ] **Step 1:** 先复现旧fixture退出挂起并按systematic-debugging记录父子进程、开放连接及退出日志；不预判原因就是go run。直接binary仍挂起时按证据修复fixture清理并补测试，不无依据修改生产gateway。
- [ ] **Step 2:** 业务redesign用例覆盖全部12页及认证／无权限／错误状态、四种宽度、760px边界、长中文／ID、Tab/Enter/Escape、computed style和关键动作；组件用例单独保持业务依赖隔离。
- [ ] **Step 3:** 人工检查200%系统文字缩放及完整读屏关键流程；记录用户寻找入口、辨认异常、解释控制／规则／云端发送的任务结果。CSS放大辅助测试不能冒充系统文字缩放。
- [ ] **Step 4:** web运行`npm run check:ui-boundaries`、`npm test -- --cache=false`、`npm run build`、`npm run test:ui`、`npm run test:e2e`；PRODUCT_ROOT运行`go test ./internal/gateway ./internal/console ./internal/smarthome ./tests/consolefixture -count=1`。记录实际数量／退出码和资源清理，人工停止不算清理通过。
- [ ] **Step 5:** 报告映射5个工程缺陷、8个原设计问题、组件状态矩阵与全部界面迁移；包括旧CSS退场、依赖方向、权限、CSP、未知写结果、草稿与敏感数据归属。
- [ ] **Step 6:** 保存正常／旧数据／失败／unknown／executing／录音／配置状态截图与各批回退点；列明真实硬件、音质、供应商、长期断网的验证边界。提交`test(console): verify the rebuilt component library and all interfaces`。

## 自审与执行边界

本计划有17个组件库／界面任务，另有独立R1—R5。核心组件有真实消费者；所有现有界面纳入迁移；props与规格一致；用户未要求的全局store、请求封装、路由体系、第三方UI库均未引入。M6对期限回拨与录音计时的增量明确单列，不能用“只改外观”跳过回归。

技术实现仍需按批准后的规格和实施方法逐任务执行。本轮仅修订方案，不代表组件、界面或新增测试已实现／通过。
