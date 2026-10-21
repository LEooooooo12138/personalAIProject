# 家序组件库

> 历史文档：设计状态与验证结论仅对应文档日期；本次发布仅脱敏，不代表重新执行验收或完成部署。

位置：`go-agent/web/src/ui/`；公共入口：`ui/index.ts`；单一视觉入口：`ui/ui.css`。

运行展示：`cd go-agent/web && npm run ui:catalog`，打开 `http://127.0.0.1:18082/app/catalog/index.html`。`?case=foundation|controls|forms|layout|feedback|auth` 选择独立示例。示例只有明确模拟数据与本地回调，不安装认证 Provider，不请求家庭 API，不建立产品路由。生产构建仍只使用原 `index.html`，不输出 catalog。

验证：`npm test`、`npm run typecheck`、`npm run check:ui-boundaries`、`node --test scripts/check-ui-boundaries.test.mjs`、`npm run test:ui`。本机若没有 Playwright 自带浏览器，使用已安装的 Edge：PowerShell `$env:CONSOLE_BROWSER_CHANNEL='msedge'`。

## 依赖与所有权

通用 UI 只依赖 React、React 类型、原生 HTML 与本层视觉资源。账号、能力、路由、Cookie/CSRF、操作代次、请求、草稿、提交校验、写重试和业务 status 的含义全部由原拥有者决定。UI 不导入 Router、auth、API、业务 Hook 或真实领域组件，既不自动写入，也不根据 Promise 设置成功状态。静态检查使用现有 TypeScript 7 native AST 与模块符号解析，递归检查实际模块图及路径别名；barrel、动态字符串导入、未知动态导入、测试模块绕过均受到限制。仅 catalog 入口可以导入 `react-dom/client`；catalog 不得导入已退场的 legacy CSS。

HTML 控件采用 React 19 原生 `ref` 属性，并透传标准属性和事件。只使用固定 variant、gap、size 和内容 slots；页面不能复制色值或重新定义基础 Button/Input 样式。新增组件须有真实页面消费者、清晰原生语义和可验证契约，不为规模感加入虚拟列表或自定义下拉框。

## 视觉与样式边界

三级 token：`--jx-palette-*` / `--jx-space-*` / `--jx-radius-*` → `--jx-color-*` / `--jx-font-*` → `--jx-button-*` / `--jx-field-*` / `--jx-card-*`。语义颜色引用单一 palette，组件使用语义或组件 token。字体为系统中文回退，无远程资源。正文 16px、辅助 14px、标题 20/24/30px、摘要 36px；相对 rem 随字号增大。间距 4/8/12/16/24/32/48px；44px 原生控件，comfortable 48px；焦点环 2px、间隔 2px。移动内容 16px，桌面 24px，页面最大 1120px，侧栏 240px；内容网格 600/1000px，外壳 760px。reduced-motion 关闭 busy 动画。

固定 layer 顺序：`jx-reset → jx-tokens → jx-ui → jx-layout → jx-features`，临时legacy层及顺序声明均已删除。所有通用组件以 `data-jx-ui` 标记边界；AppShell 只定位 slots。全部产品页面与 catalog 使用新 UI；旧 CSS、LegacyPageBoundary 与兼容图标入口已退出。原生 hidden 语义由 reset 保留，页面局部布局与阅读样式使用 features 层。迁移期两种旧／新组合的隔离测试证据留在 DS-report，完成退出后展示页不再保留旧组件示例。

## 动作与图标

| 导出 | props | 契约 |
| --- | --- | --- |
| Icon | name: IconName; size?: number（默认 20） | viewBox 24、stroke 2；装饰 aria-hidden，新录音/播放/停止/上传资产只定义一份 |
| Button | 原生 button/ref；variant?: primary/secondary/ghost/danger；size?: regular/comfortable；busy?: boolean | 默认 type=button；busy/disabled 阻止用户激活，保留原 label/type/name/value/form；显式提交必须 type=submit |
| IconButton | Button props + label:string + children | 最小 44px，label 成为可访问名称；只用于已有明确名称的辅助动作 |
| ActionLink | 原生 anchor/ref + variant | 真正 href 链接；Router Link 由页面提供并使用 jx-action-link 及 variant 类 |
| Avatar | label:string；size?: regular/large | 首个可用 Unicode 字符；空名称显示成员图标，不推断邮箱 |

busy 仅改变禁用和视觉表现，不绑定 Promise，也不猜测“正在保存”等业务文案；需要的状态文字由调用方传入。

## 原生表单与弹窗

| 导出 | props | 契约 |
| --- | --- | --- |
| Field | id; label:ReactNode; hint?:ReactNode; error?:string; required?:boolean; children | label htmlFor=id；hint/error 仅实际存在时产生 id-hint/id-error；required 是独立视觉标记，不污染 label 文本 |
| fieldDescriptionIds | id, hasHint, hasError, existingIds? | 返回实际描述 ID，去重并保留既有描述；没有 ID 时 undefined |
| Input | 原生 input 属性/ref + invalid? | 透传 type/name/id/required/autocomplete/readOnly/pattern/value/事件；invalid 映射 aria-invalid |
| Textarea | 原生 textarea 属性/ref + invalid? | 透传 rows、composition/key 事件，无发送规则 |
| Select | 原生 select 属性/ref + invalid? + children | 原生 options、键盘和提交值 |
| Checkbox | 原生 checkbox 属性/ref + label + hint? | 真实 input，关联 label/hint；分组由调用方 fieldset/legend 提供 |
| FileInput | 原生 file 属性/ref + invalid? | 禁止受控 value/defaultValue；文件校验与读取在页面 |
| Disclosure | title; defaultOpen?; ref?:Ref<HTMLDetailsElement>; children | 原生 details；初始展开一次，折叠不卸载；invalid capture 先打开以允许浏览器定位 |
| Dialog | open; title; description?; dismissible?; onRequestClose; children?; footer?; initialFocusRef?; returnFocusRef?; restoreFocus? | 原生 dialog/showModal，受控 open；默认可关闭及恢复焦点；不持有写操作状态 |

Field 不 clone children；控件显式传同一 id 和 aria-describedby。Field.required 与控件 required 必须分别传入。示例：

```tsx
<Field id="name" label="用户名" hint="使用家庭账号" error={error} required>
  <Input id="name" name="username" autoComplete="username" required
    value={name} onChange={onNameChange} invalid={Boolean(error)}
    aria-describedby={fieldDescriptionIds('name', true, Boolean(error), 'security-note')} />
</Field>
```

Disclosure 中的服务端错误由页面通过 details ref 设置 open 并定位字段；UI 不观察业务错误。

Dialog 只在尚未打开时 showModal；重复渲染不重复打开。Escape/关闭按钮/真正遮罩点击统一发送一次 onRequestClose，直到下一次 open 周期；程序同步和 close 事件不发回调。dismissible=false 禁止这三种请求，`method=dialog` 与 submitter 的 formmethod 也不能绕过。关闭不代表取消请求。原生 modal 配合首尾 Tab 边界检查，初始焦点由 initialFocusRef 指定。长正文单独滚动，标题和 footer 可访问。只有仍挂载的 open true→false 才恢复到仍连接、可聚焦、非 inert 的 returnFocusRef 或触发元素；卸载清理不主动恢复，页面可 restoreFocus=false。

## 布局、列表与外壳

| 导出 | props / slots | 语义与布局 |
| --- | --- | --- |
| PageFrame | children; className? | 唯一 main；直接子项 24px 纵向间距；1120px 宽度与内容容器 |
| PageHeader | title:string; description?; leading?; actions? | h1、任务说明与操作，自然换行 |
| Card | heading?; description?; children; footer?; tone?:neutral/info/warning/danger | 非交互 section；不通过 onClick/tabIndex 模拟链接 |
| Section | title?; description?; actions?; children | 区块语义与留白，无强制卡片边框 |
| Stack / Grid / ActionGroup | children; gap?:8/12/16/24/32；Grid.columns?:1/2/3 | 纵向、自适应网格、可换行操作组；无业务布局判断 |
| KeyValueList | readonly {id,label,value:ReactNode}[] | dl/dt/dd，长标识换行 |
| ListRow | leading?; title; description?; meta?; trailing? | 只布局；外层 ul/li、链接和独立按钮由调用方选择 |
| Breadcrumb | label?（当前位置）；children | nav；调用方传原 Router links |
| AppShell | brand; navigation; profile?; topbar?; mobileNavigation?; children | aside/header 与 slots；不创建 main 或额外 nav |
| Navigation | label; variant:side/bottom; children | owns nav；目的地、能力筛选和 aria-current 由调用者提供 |
| navigationItemClass | active:boolean | 生成统一选中 class，不判断权限 |
| AuthFrame | brand; title; description?; children; footer? | 独立认证页 main；不嵌套 PageFrame，不调用 auth |

链接行用一个真实链接包 ListRow；含按钮的列表行不再变为可点击整行。`.jx-plain-list` 提供无圆点 ul 与相邻行分隔。PageFrame 与 AuthFrame 各自拥有 main，不能嵌套；catalog 外壳示例也不套额外 main/nav。

## 状态与数据契约

| 导出 | props | 状态矩阵 |
| --- | --- | --- |
| StatusBadge | tone:success/warning/danger/info/neutral; label; icon?; live? | 静态默认无 live；live=true 使用 status；图标与颜色不能取代文字 |
| Alert | tone:info/warning/danger; title?; children; actions?; live?:false/polite/assertive | 默认静态，polite=status，assertive=alert；页面决定播报 |
| LoadingState | label; live?（默认 true） | polite status，无假百分比；catalog 关闭播报 |
| EmptyState | title; description?; icon?; actions? | 静态文字与操作，无多余 alert |
| Metric | label; value:string/number/null; unit?; description? | null=暂无数据；0=真实零；不补业务缺值 |
| StepSequence | label; readonly StepItem[] | ol；id/label/state（waiting/active/complete/failed/unknown），每阶段有文字，active 标 aria-current=step |
| ProgressSummary | label; completed:number; total:number; unit:string | 仅 safe finite integer、total>0、0≤completed≤total 呈现原生 progress；其他为“进度暂无法确认” |

展示矩阵覆盖按钮 variants/default/submit/busy/disabled、字段值/hint/error/required/disabled/readOnly/file、Dialog 正常/等待锁定/长正文、长标题/长标识/列表两种交互、空/加载/失败、Metric 0/null、阶段全部状态、进度合法零/零总量/超界，以及家庭/控制/语音/规则的模拟组合。installed、stale、未知结果不是 UI 可推断的健康成功；识别/生成没有正式总量只用 busy，语音手动播放/自动朗读由页面明确控制。
