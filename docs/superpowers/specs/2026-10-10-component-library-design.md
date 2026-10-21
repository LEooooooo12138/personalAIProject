# 家序组件库重置设计规格

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

日期：2026-10-10。版本：2方案配套规格。状态：待审阅，尚未实施。

主方案：[组件库重置与全界面重做](2026-10-10-frontend-reliability-and-visual-design.md)。实施计划：[组件库与全页面迁移](../plans/2026-10-10-frontend-visual-plan.md)。代码基线、业务原则和最终验收沿用主方案；本文件决定组件库如何重建及如何退出旧样式。

## 1. 交付范围与成功标准

建立 `go-agent/web/src/ui/`，覆盖所有现有页面所需的基础控件、表单、弹窗、布局、导航容器、状态、列表、进度和详情展开。新库建立清楚的公共props、状态矩阵、真实组件展示页和使用规范。全部现有页面重做布局及呈现，禁止只在旧样式末尾加覆盖层。

完成必须同时满足：组件在展示页可独立工作；12个页面及认证、无权限、异常界面完成迁移；通用UI不调用业务API或持有业务状态；旧基础UI无消费者，旧CSS与兼容层可去除；真实业务行为回归通过。组件数量不是验收指标，不开发当前页面没有消费者的复杂控件。

## 2. 依赖和状态边界

| 层 | 目录／文件 | 拥有的内容 | 不得承担 |
| --- | --- | --- | --- |
| 视觉基础 | ui/styles/、ui/Icon.tsx | token、reset、尺寸、图标资源、焦点与动效规范 | 用户、请求、路由、业务状态 |
| 通用UI | ui/基础、表单、布局与反馈组件 | 原生语义、内容slots、受控值、焦点、展开、busy表现 | 权限推断、API、写操作重试、设备／HA状态解释 |
| 业务展示 | 现有familyViews、knowledgeViews、chat/与新增小型presentation模块 | 已验证DTO、中文状态／摘要、业务回调组合 | 新的认证、请求或全局store |
| 页面／业务控制 | 现有pages、App、auth、query/action、chat-state、useVoice | 数据请求、身份、能力、SID、操作代次、草稿、语音与滚动 | 在页面重新定义Button/Input等基础样式 |

UI只依赖React、React类型、原生HTML和本层视觉资源。ui目录不能import auth、api、coordinator、familyQuery、consoleActions、pages、chat-state、useVoice；也不能间接通过公共barrel引入这些模块。页面中的React Router Link/NavLink通过children传入UI容器，UI不创建路由或守卫。

允许的UI局部状态：Dialog焦点与打开同步、Disclosure展开、组件内部可访问关系ID。账号、网络加载、提议状态、是否有权限、是否可提交、是否重试均由现有拥有者传入。busy只阻止重复用户激活，不根据Promise结果自动判断成功。

AuthProvider、BrowserRouter、Route path及search参数继续原样使用。App中`${identity.user.id}:${epoch}`代次key保留；组件视觉迁移不能给表单、聊天或Dialog添加新的随机key导致重挂载。

## 3. 新视觉基础

以家序绿色为品牌线索，重做界面的比例、表面、留白、控件、导航和反馈。全部页面共用规范；减少装饰英文和欢迎区占位，避免普通家人先读部署字段才知道下一步。

### 3.1 三级token

基础token采用 `--jx-palette-*`、`--jx-space-*`、`--jx-radius-*`；语义token采用 `--jx-color-*`、`--jx-font-*`；组件token采用 `--jx-button-*`、`--jx-field-*`、`--jx-card-*`。组件消费语义或组件token，不在页面重复色值。

| 语义token | 值 | 用途 |
| --- | --- | --- |
| color-canvas | #F6F8F5 | 应用背景 |
| color-surface | #FFFFFF | 内容、卡片、输入 |
| color-surface-subtle | #EEF3EF | 次级区块与分组 |
| color-text | #20352F | 正文与标题 |
| color-text-muted | #53655E | 辅助、占位符与时间 |
| color-brand | #176B5B | 主操作与选中导航 |
| color-brand-hover | #125748 | 主操作hover／active |
| color-on-brand | #FFFFFF | 主按钮文字 |
| color-selected | #E9F3EE | 选中背景 |
| color-border | #D7E1DB | 装饰边框和分隔线，不独自承担可操作边界 |
| color-field-border | #71867B | 输入控件边界 |
| color-focus | #176B5B | 明确焦点环 |
| color-warning / warning-bg | #7A4C00 / #FFF5DB | 风险和未核对 |
| color-danger / danger-bg | #9F2D2D / #FFF1F0 | 失败与危险操作 |
| color-info / info-bg | #245D83 / #EEF6FC | 中性说明与进行中 |
| color-success / success-bg | #176B5B / #E9F3EE | 已被实际证据支持的成功 |

数字与字体：辅助14px、正文16px、小标题20px、区块标题24px、页面标题30px、必要摘要值36px；正文行高1.6，控件文本行高1.4。字体使用现有系统字体与中文回退，不加载远程字体。字重400/500/600，主要信息不用低对比细字。

间距4/8/12/16/24/32/48px；圆角8px控件、12px一般卡片、20px重点表面、999px标签；输入与按钮常规最小44px、舒展48px，icon按钮44px。手机内容边距16px，桌面24px，页面max1120px，侧栏240px。网格断点600/1000px，外壳与聊天交互760px；窄屏自适应，不强行固定列数。

阴影仅用于Dialog和需要浮起的重点表面；普通内容面以背景、分隔和留白区分，不给所有段落套卡片。焦点环2px并留2px间隔，不能依靠去outline改善外观。动效只用于状态切换或busy，120/180ms，reduced-motion时关闭非必要动画。

disabled使用明确文字和视觉状态，但不能降低未禁用的辅助文字对比。可操作边界和焦点在实际背景上检查；普通文字／占位符至少4.5:1。上述数值是本项目设计决策，实施需检查实际computed style。

### 3.2 图标与视觉语法

复用已有SVG路径，集中至ui/Icon.tsx，新增当前录音、播放、上传等确有用途的SVG资产。viewBox 24、stroke 2，标准显示16/20/24/32px；装饰图标aria-hidden，按钮仍有可访问名称和可见文字。

统一语法：标题＋一行任务说明＋操作；数量用Metric；状态用StatusBadge；失败用Alert；空／加载用对应状态组件；流程用StepSequence；合法进度用ProgressSummary；技术标识用Disclosure与KeyValueList。多层信息不全部加相同卡片边框，不通过图标推断业务成功。

## 4. 组件清单与公共契约

以下签名为实施接口。HTML属性／事件／ref使用React19现有类型透传；禁止无消费价值的多态组件体系。所有CSS类名jx开头。视觉props优先固定variant或slot，页面不得用style属性注入任意色值／尺寸。

### 4.1 基础控件

| 文件／导出 | 核心props | 行为 |
| --- | --- | --- |
| Icon.tsx / Icon | name: IconName; size?: number | 默认20px，复用现有IconName并按需添加；IconName在此单一定义，旧icons仅单向重导出 |
| Button.tsx / Button | 原生button props；variant?: primary/secondary/ghost/danger；size?: regular/comfortable；busy?: boolean | 默认type=button；busy或disabled不可激活；页面显式type=submit |
| Button.tsx / IconButton | Button props＋label: string；children: ReactNode | 44px，label作aria-label；不自动触发请求 |
| Button.tsx / ActionLink | 原生anchor props＋variant | 真正链接，不用button模拟；Router Link由页面带jx-action-link类呈现 |
| Avatar.tsx / Avatar | label: string; size?: regular/large | 展示首个可用字符或通用图标；空名称不报错、不暴露邮箱推断 |

busy不改变控件的type、name或form属性；为原标签保留可访问名称，新增忙碌文字须由页面指定。普通Button不内置loading文本猜测操作，也不在收到Promise后自动切状态。IconButton的视觉标签省略仅限已具明确可访问名称的辅助动作。

### 4.2 表单与弹窗

| 文件／导出 | 核心props | 行为 |
| --- | --- | --- |
| Field.tsx / Field | id: string; label: ReactNode; hint?: ReactNode; error?: string; required?: boolean; children | 生成id-hint/id-error；label htmlFor=id；统一关联说明与错误 |
| Input.tsx / Input | 原生InputHTMLAttributes＋ref；invalid?: boolean | 原样透传id/name/type/value/onChange/required/disabled/readOnly/autocomplete/pattern等 |
| Textarea.tsx / Textarea | 原生TextareaHTMLAttributes＋ref；invalid?: boolean | 透传composition/key事件和rows，不拥有发送规则 |
| Select.tsx / Select | 原生SelectHTMLAttributes＋ref；invalid?: boolean; children | 使用原生select/options，不重做自定义下拉键盘状态机 |
| Checkbox.tsx / Checkbox | 原生checkbox属性＋label: ReactNode; hint?: ReactNode | 真实input；保留fieldset/legend分组语义 |
| FileInput.tsx / FileInput | 原生file属性＋ref；value禁止作为受控值 | 原生文件选择；校验和异步读取在页面 |
| Dialog.tsx / Dialog | open: boolean; title: string; description?: ReactNode; dismissible?: boolean; onRequestClose: () => void; children; footer?: ReactNode; initialFocusRef?: RefObject<HTMLElement\|null>; returnFocusRef?: RefObject<HTMLElement\|null>; restoreFocus?: boolean | 原生dialog/showModal；标题关联、焦点进入／返回；Escape、关闭按钮、遮罩统一关闭策略 |

Field提供纯函数 `fieldDescriptionIds(id: string, hasHint: boolean, hasError: boolean, existingIds?: string): string | undefined`，只返回实际渲染的hint/error ID并合并已有描述ID。采用显式组合：Field不clone任意children；调用方给控件同一个id，设置aria-describedby和invalid，控件将invalid映射为aria-invalid。Field.required只表达标记，原生控件required也显式传入。Field不包含密码长度、实体名称或文件格式业务校验；保留已有校验文案。

```tsx
<Field id="login-name" label="用户名" hint="使用自己的家庭账号" error={error} required>
  <Input id="login-name" name="username" autoComplete="username" required
    value={username} onChange={onUsernameChange} invalid={Boolean(error)}
    aria-describedby={fieldDescriptionIds('login-name', true, Boolean(error))} />
</Field>
```

Dialog open=true时仅在尚未打开的节点调用showModal，open=false时关闭，重复渲染不重复调用。dismissible默认true；false时阻止原生cancel默认关闭，关闭按钮与遮罩也不可请求关闭。用户关闭只发送一次onRequestClose；程序同步、原生close事件及卸载清理不能再次发送。表单不能通过method=dialog绕过该策略。不把关闭当作撤销网络操作。

长内容主体可滚动，最大高度为100dvh减32px，标题和当前操作可访问。restoreFocus默认true；仅仍挂载组件的open由true变false时，向仍连接、可聚焦的触发元素／returnFocusRef恢复，并在恢复前再次检查节点。卸载清理不主动恢复焦点。业务拥有者还可传restoreFocus=false；身份／路由卸载永远可执行。MembersPage使用保持挂载的受控Dialog，R5等待时dismissible=false，代次校验仍在MembersPage。

### 4.3 布局与内容

| 文件／导出 | 核心props | 行为 |
| --- | --- | --- |
| PageFrame.tsx / PageFrame | children; className? | 页面main、宽度、边距；每页一个主要main |
| PageHeader.tsx / PageHeader | title: string; description?: ReactNode; leading?: ReactNode; actions?: ReactNode | 标题、任务说明与主操作；手机自然换行 |
| Card.tsx / Card | heading?: ReactNode; description?: ReactNode; children; footer?: ReactNode; tone?: neutral/info/warning/danger | 静态section，默认不点击；不生成权限或动作 |
| Section.tsx / Section | title?: string; description?: ReactNode; actions?: ReactNode; children | 分区；需要留白时不用强制卡片边框 |
| Layout.tsx / Stack, Grid, ActionGroup | gap?: 8/12/16/24/32；Grid columns?: 1/2/3；children | 统一布局，Grid按容器宽度折叠；无任意业务布局判断 |
| KeyValueList.tsx / KeyValueList | items: readonly {id:string; label:string; value:ReactNode}[] | dl/dt/dd，长标识换行 |
| Disclosure.tsx / Disclosure | title: string; defaultOpen?: boolean; ref?: Ref<HTMLDetailsElement>; children | 原生details管理展开，defaultOpen只决定初始值；折叠不卸载；捕获invalid先将原生open设为true，再允许浏览器聚焦 |
| ListRow.tsx / ListRow | leading?; title: ReactNode; description?; meta?; trailing? | 只做行内容布局；外层链接／按钮与列表语义由页面明确选择 |
| Breadcrumb.tsx / Breadcrumb | label?: string; children | nav容器；页面传原Router links，不新建路由 |

Disclosure本轮不提供额外受控展开模式，沿用当前原生details所需能力。页面的客户端／服务端业务错误通过传入ref主动展开并定位字段；UI不检测异步业务错误。链接式卡片使用单个Router Link／anchor包内容，动作式卡片使用真实button；Card不通过onClick＋tabIndex模拟两者。包含成员动作按钮的ListRow不能再放进可点击整行。暂无通用DataTable、虚拟列表、Combobox、拖拽或图表消费者，本轮不为库的规模感开发这些组件。

### 4.4 状态与数据展示

| 文件／导出 | 核心props | 行为 |
| --- | --- | --- |
| StatusBadge.tsx / StatusBadge | tone: StatusTone; label:string; icon?:IconName; live?:boolean | StatusTone=success/warning/danger/info/neutral；live默认false，必要时role=status |
| Alert.tsx / Alert | tone: info/warning/danger; title?:string; children; actions?:ReactNode; live?:false/polite/assertive | 静态默认不播报；实际错误使用assertive，阶段变化polite |
| LoadingState.tsx / LoadingState | label:string; live?:boolean | 默认polite状态；不使用虚假百分比；展示页可关闭播报 |
| EmptyState.tsx / EmptyState | title:string; description?:ReactNode; icon?:IconName; actions?:ReactNode | 文字与操作；静态无多余alert |
| Metric.tsx / Metric | label:string; value:string/number/null; unit?:string; description?:ReactNode | null显示暂无数据，0是真实零；不从页面业务取值 |
| StepSequence.tsx / StepSequence | label:string; steps:readonly StepItem[] | ol；StepItem={id,label,state:waiting/active/complete/failed/unknown}，状态有文字 |
| ProgressSummary.tsx / ProgressSummary | label:string; completed:number; total:number; unit:string | 仅有限整数total>0且0≤completed≤total渲染progress；否则说明进度暂无法确认 |

计时条用已实际录制的秒数，批次条用服务端批次。未知总量不给伪value；没有正式总量的识别／生成阶段只显示busy。颜色、图形和标签不改变业务status；UI不把stale数据或installed模型呈现成健康成功。

### 4.5 应用外壳

`AppShell.tsx`导出 `AppShell({brand, navigation, profile, topbar, mobileNavigation, children})`，slots为ReactNode；aside/header/nav结构清晰。`Navigation.tsx`导出 `Navigation({label, variant:'side'|'bottom', children})`及 `navigationItemClass(active:boolean): string`。

AppShell只提供slot的位置容器，navigation/mobileNavigation本身由Navigation生成nav，不再次套nav；AppShell不生成main，PageFrame拥有单一页面main。App继续按能力生成items，通过现有NavLink使用class函数；NavLink的active/aria-current继续由Router控制。菜单的过滤、目的地、logout和身份变化均在现有App。`AuthFrame.tsx`接受brand、title、description、children、footer，统一登录和改密表面，独立生成认证页main，不嵌套PageFrame、不调用useAuth。

品牌可提取到应用层 `components/Brand.tsx`，不是UI组件。业务展示组件保留原位置；没有消费者需要时不额外拆出独立Composer、ConversationStore或聊天布局引擎。

## 5. 业务展示组合与组件复用

| 现有拥有者 | 新UI组合 | 保留行为 |
| --- | --- | --- |
| familyViews | Card、Metric、StatusBadge、KeyValueList、Disclosure、Breadcrumb | 日期／域／归属／状态formatter与已校验实体DTO |
| HomeFamilySummary / HomeCollectionSummary | Section、Metric、状态、进度与详情 | HA与AI分离，新鲜度和安装限制，不另加请求 |
| knowledgeViews / SafeMarkdown | Field、Input、ListRow、LoadingState、Alert、阅读面局部样式 | q/path、公开／私人边界、HTML跳过、图片降级、安全链接 |
| ControlCard / DeviceResultCard | Card、StatusBadge、Metric、KeyValueList、ActionGroup、Button | 能力、截止时间、R3读取、R4语义、unknown核对、现场提示 |
| VoiceControls | Card、StatusBadge、ProgressSummary、Checkbox、Button | useVoice业务适配、60秒ready、默认不自动朗读、取消与手动播放 |
| AutomationsPage RuleView | Card、Step风格关系布局、KeyValueList、Disclosure | 任一触发／全部条件／动作顺序与原规则；关系不是执行阶段 |
| MembersPage | ListRow、Avatar、StatusBadge、Dialog、表单 | 成员操作、R5代次、临时密码关闭销毁 |

组件展示页中的领域示例仅传模拟数据与无网络回调；真实领域判断仍由原组件与页面负责。所有业务摘要使用确定性纯函数，不通过模型改写家庭规则。

## 6. 状态矩阵与展示页

创建开发专用 `web/catalog/index.html`＋`web/src/ui/catalog/`。使用现有Vite加载，入口独立于App，不接AuthProvider、不创建生产Route；生产构建仍以既有入口为准，Go静态允许列表不为展示页扩大。测试固定18082端口和独立Playwright配置，产品fixture18081不变。按case选择独立示例；AppShell／AuthFrame示例不再包在展示页的main或nav内。catalog的渲染入口允许react-dom/client，通用运行组件仍只依赖React及本层资源。

展示页至少包含：token色板与排版；Button各variant/type/busy/disabled；原生字段value/error/hint/disabled/readOnly/长内容；Dialog正常／不可关闭／长内容；Card、ListRow、导航、空／加载／错误；Metric的0/null；阶段全状态；进度合法／0总量／超界；家庭状态、控制、语音与规则的模拟组合。展示页数据使用明确测试名称，不读取家庭数据。

关键验证：可访问名称、键盘、真实表单submit、ref透传、焦点进入／返回、fieldset组、Disclosure中的无效字段、Dialog等待锁定、状态播报策略、200%字号与四种宽度。简单Card边框和纯文案不写镜像实现的单元测试，用展示页与浏览器证据检查。

在静态检查中限制ui不得import业务模块；catalog也不得通过领域组件带入请求。UI公共index只导出已稳定通用组件，catalog不从index反向导出。提供 `docs/component-library.md`，说明props、状态、使用／禁用规则、业务边界和新增组件条件。

## 7. 新旧样式迁移与退场

建立 `ui/styles/layers.css` 定义顺序：jx-legacy、jx-reset、jx-tokens、jx-ui、jx-layout、jx-features。新基础选择器以jx命名或受控reset为边界，业务专属样式放features层，禁止页面重新定义通用Button/Input等。

迁移前清点三份旧CSS的消费者与全局规则；启用 `styles/legacy.css`分层兼容入口时，立即移除main和ChatPage的三个旧直接导入，旧源文件暂存至Q1。所有旧全局规则限制于LegacyPageBoundary，包括控件、标题、段落、链接、焦点、hidden、继承属性及important；逐条处理important，不能认为layer自动解决优先级。body/基础字体由新reset接管，hidden原生隐藏语义保留。

基础交互／内容组件以data-jx-ui标记新UI边界，旧选择器排除该边界自身和后代；AppShell只是位置容器，不标成此排除边界，允许其内未迁移的LegacyPageBoundary仍工作。新UI组件内不再嵌套旧基础控件。静态检查与浏览器分别验证“新外壳中旧页面”和“旧页面中新组件”两种组合，不依赖覆盖顺序偶然成立。全部消费者迁移后删除旧源及兼容入口。

按M1—M9迁移。每批只改该页呈现、CSS与必要UI组合；业务Hook的调用位置／依赖数组、key与reducer不改变。一次完成一类组件及其消费者，删除该批已失效规则。共享业务文件如familyViews与knowledgeViews同时影响多页，按迁移矩阵一起验证，不批量正则替换全部标签。

Q1出口：全部页面不再有LegacyPageBoundary；main不导入旧styles.css/console.css，ChatPage不导入旧chat.css；旧基础选择器无消费者，三份旧文件及legacy兼容层删除。聊天仅保留在 `chat/presentation.css`里的必要布局／消息／附件专属样式，公开知识阅读样式迁到 `styles/knowledge.css`。关闭旧CSS后必须通过全部界面浏览器验收。

原icons.tsx可在迁移期重导出新Icon，所有消费者迁移后退出兼容导出，不复制SVG路径两份长期维护。按组件族／页面提交；回退同时恢复该批组件和匹配样式，不增加旧／新UI产品切换或第二套数据流。

## 8. 完成定义

- 全部页面及认证、无权限、初始加载／失败状态完成新组件迁移；没有“其余页面只改token”的遗留项。
- 通用UI静态与运行检查无业务依赖／网络请求；页面业务能力继续由原拥有者控制。
- 控件type、name、autocomplete、label、ref、IME事件与表单提交语义核对完成；Dialog和Disclosure交互通过。
- 所有新界面满足16/14px、44px、4.5:1及320/390/768/1440px、760px边界、200%字号要求。
- 旧组件基础样式、旧三份CSS与兼容层退出；单一token来源，无尾部重复覆盖。
- R1—R5独立回归与迁移后的同一业务契约再次通过；fixture自然退出。
- 组件文档、真实组件展示页、页面截图、测试结果与未验证项齐全。方案文件本身不作为上述实现已完成的证据。
