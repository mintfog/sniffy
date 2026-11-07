# Sniffy 设计规范

Sniffy 是低饱和、高密度的桌面工作台。界面色由配色方案决定，默认的中性方案为纯灰阶，主操作用单色（亮色近黑、深色近白）；彩色只用来表达语义。token 见同目录 [tokens.css](tokens.css)，可视化预览见 [preview.html](preview.html)。

## 原则

1. **颜色只承载含义。** 状态码、HTTP method、标记色、错误用语义色；按钮、标签、图标使用当前方案的界面色。唯一的例外是选中：它用方案主色色相的低饱和染底，见下文「选中」。
2. **扁平，靠线分层。** 层次靠底色明度差加 1px 发丝线表达。控件扁平，阴影只用于菜单、对话框、提示这类浮层。
3. **密度优先。** 默认控件高 32px，工具条和标签栏 36px，流量筛选栏 28px（紧凑模式 24px），表格行 26px（紧凑模式 22px）。字号 13px，次要信息 12px，徽章 11px。
4. **数据用等宽字体。** URL、头部、body、耗时、大小、端口、状态码都用 `font-mono`；数字列统一用 `tabular-nums`。

## 分层

| 层 | token | 亮色 | 深色 | 用在 |
| --- | --- | --- | --- | --- |
| chrome | `base` | #F5F5F5 | #1B1B1E | 标题栏、图标栏、标签栏、状态栏 |
| 次级面 | `inset` | #FAFAFA | #19191C | 侧栏、表头、工具条、代码行号 |
| 内容 | `surface` | #FFFFFF | #131416 | 流量表、详情面板、编辑器 |
| 浮层 | `popover` | #FFFFFF | #1E1E20 | 菜单、对话框、提示、下拉框 |
| 控件填充 | `elevated` | #F0F0F0 | #2A2A2D | secondary 按钮、分段轨道 |
| 悬停 | `hover` | #F0F0F0 | #2E2F33 | 行、菜单项、ghost 按钮 |

亮色下内容区最亮，chrome 偏灰；深色下反过来，内容区最深，chrome 略亮。chrome 上的悬停改用 `line`，否则看不出变化。

## 颜色

- **主操作**：`sel` / `sel-fg`，取方案的主色实底（中性方案为近黑配近白字，深色下反转）。用于主按钮、激活分段、激活的结果标签胶囊。一个视图里最多出现一处。
- **强调**：`accent`，用于激活图标、链接、选中文字，只作实色的文字和图标，不作底色。与主操作同色，只在对比度不足时加深或提亮。
- **选中**：`row-selected` / `row-focused` 是 `surface` 混入方案主色色相的低饱和染底：钴蓝、雾蓝为蓝调，鼠尾草为青绿，琥珀为暖米；中性、石墨的主色是灰阶，没有可用色相，退回蓝色。灰色染底在深色下受语义色对比度约束、无法再提亮，选中感只能靠色相拉开。文字保持原色，状态色和 method 色在选中行里照常可读。当前焦点行用 `row-focused`，左侧加 3px `row-indicator` 竖条（同一色相，与选中底和 surface 都 ≥3:1）；单选列表（侧栏、消息列表、设置导航）的当前项同样用 `row-selected` 加这条竖条（`.wb-row-current`）。选中行和选中项不用描边：描边会把行框成一个盒子。选择器卡片（如动作类型选择）的已选项用 `row-selected`、键盘或指针所在项用 `row-focused`，不加竖条；框选矩形用 `row-indicator` 描边加同色浅底。选中项里的图标和对勾用 `row-indicator`。选中、焦点、悬停这类状态底色只用 `row-*` 与 `hover`，不用 `accent` 加透明度：中性、石墨方案的 `accent` 是灰阶，叠成浅底后与底面几乎无差。
- **语义色**：`ok` 对应 2xx，`warn` 对应 3xx，`info` 对应 4xx，`danger` 对应 5xx 和错误，`violet` 对应 PATCH（青绿），亮色取 700–800 档，深色取 400 档。作文字用时，在 surface / base / inset / hover / 选中行 / 焦点行和 12–16% 自色染底上都 ≥4.5:1。
- **实底语义色**：语义色作实底（计数徽章、实心状态块）时，文字用对应的 `ok-fg` / `warn-fg` / `danger-fg` / `info-fg`，不写死白字。亮色取白，深色取深底色（400 档实底上白字不足 3:1）；各方案下 ≥6.4:1。
- **类型色**：`type-*` 用于 JSON 树的数字、字符串、布尔、null、键名，在 surface、hover 和选中行上 ≥4.5:1，`type-null` 直接取 `fg-faint`（斜体区分），随方案变化。
- **分类色**：按名称哈希分配颜色的标签（如 SSE 事件名）只用固定色相：语义色与标记色。不用 `accent`，它在灰阶方案下会与未分类标签的灰色撞色。
- **焦点**：所有可聚焦元素统一用 `--wb-focus-ring`（1px `ring` 实线压在 3px `ring/50` 外环上），Tailwind 写 `focus-visible:shadow-focus outline-none`。

## 字体

| 角色 | 字号 / 行高 | 字重 | 用在 |
| --- | --- | --- | --- |
| 标题 | 15px / 22px | 600 | 对话框标题、设置页标题 |
| 正文 | 13px / 19px | 400 | 默认 |
| 强调 | 13px / 19px | 500 | 按钮、激活标签、表头 |
| 次要 | 12px / 16px | 400 | 工具条标签、说明、状态栏 |
| 徽章 | 11px / 14px | 500 | 状态徽章、计数 |
| 数据 | 12px / 18px mono | 400 | URL、头部、body、数值列 |

- 无衬线字体用 Geist Variable（`@fontsource-variable/geist`），中文回退 PingFang SC / 微软雅黑。
- 等宽字体继续用 JetBrains Mono Variable（已在依赖中）。
- 字体平滑（`antialiased`）默认关闭，作为可选偏好提供。

## 尺寸与圆角

- 控件高度分四档：xs 24px、sm 28px、md 32px（默认）、lg 36px。图标按钮是同尺寸的正方形，图标 16px（xs 下 12px）。
- 圆角：面板和控件 4px，浮层 6px，徽章用胶囊形。`data-corner` 支持切换 `large`（6/10px）和 `none`（0）。
- 间距按 4px 栅格：控件内边距左右 10px，工具条内控件间距 4px，分组之间放 1px × 16px 的竖线。

## 组件

| 组件 | 规格 |
| --- | --- |
| 主按钮 | `h-control-md` · px-2.5 · `bg-sel text-sel-fg` · 悬停 `sel-hover` · 圆角 control |
| 描边按钮 | 透明底 + `border-line` · 悬停 `hover` · 深色主题下底为 `elevated/30` |
| ghost 按钮 | 无边框 · `text-fg-muted` · 悬停 `bg-hover text-fg` · 工具条默认用这一种 |
| 破坏性按钮 | `danger/10` 底 + `text-danger` · 悬停 `danger/20` |
| 工具条按钮 | 16px 图标 + 12px 文字，ghost 样式，`h-control-sm`；激活时 `bg-hover text-fg` |
| 输入框 | `h-control-md` · 透明底 + `border-line` · 占位 `fg-faint` · 聚焦 `shadow-focus` |
| 开关 | 轨道开 `sel` / 关 `line-strong` · 圆点开 `sel-fg` / 关 `fg-muted`（各方案下 ≥4.4:1，满足非文本 ≥3:1）· 不写死白色圆点 |
| 状态徽章 | h-5 · 胶囊 · 11px/500 · 底 `<语义色>/12`（深色 `/16`），字用语义色 |
| method 标签 | 纯文字 · mono 11px/600 · method 色 |
| 计数徽章 | h-4 · 胶囊 · `bg-danger text-danger-fg` · 只用于未处理的断点和错误数 |
| 编辑区标签 | 高 36px · 非激活为 `text-fg-muted` 加右侧发丝线 · 激活为 `bg-surface text-fg`，底边与内容区连通 · 标签栏底边线用伪元素绘制，激活标签抬高层级盖住它；不用负外边距越出标签栏，标签容器可横向滚动，越界会撑出竖向滚动条 |
| 详情标签栏右侧 | 请求区只放 method、复制 cURL、关闭；HTTP 版本与状态码只出现在响应区，版本在前、状态码紧随其后 |
| 结果分段 | 流量筛选栏（`--wb-filterbar-h`）上的筛选项 · h-5 · px-2 · 12px/500 · 项间距 2px · 激活项用 `sel` 实底胶囊，其余为 ghost 文字，计数用 `fg-faint` · 同一行的图标按钮用 `control-xs` |
| 菜单 | `bg-popover` · `border-line` · 圆角 lg · `shadow-wb` · 项高 `control-sm` · 悬停 `hover` · 快捷键用 `fg-faint` |
| 对话框 | `bg-popover` · 圆角 lg · `shadow-dialog` · 遮罩 `bg-overlay`（`--wb-overlay`）· 标题 15/600 · 底部按钮右对齐，主按钮在最右 |
| 流量表 | 表头 `inset` · 12px/500 `fg-muted` · 行高 `--wb-row-h` · 行间无分隔线 · 当前行左侧 3px `row-indicator` 竖条，标记色条存在时让位给标记色；左边缘只留给当前行与标记，「已修改」用序号前的 6px `info` 圆点 · 已读变灰不作用于选中行 · 已读行只把 URL / host 主文本从 `fg` 降为 `fg-muted`（≥5:1，选中行上 ≥6:1）；状态码、method、耗时、大小、进程等数据列保持原色，不使用整行透明度 |
| 状态栏 | 高 24px · `base` · 12px `fg-muted` · 指标与指标之间加 12px 间距 |

## 配色方案

配色方案决定整套界面色：分层、发丝线、文字灰阶、主色、选中行。它与明暗主题正交，每套方案都有亮、暗两版，经 `<html data-palette="…">` 切换。语义色和 method 色不随方案改变色相，因为它们是抓包读数，换了方案也要一眼认得出；只有在某套方案的底色上对比度不足时，才微调明度（tokens.css 中标注「对比度修正」的行）。

| 方案 | `data-palette` | 亮色 chrome / 主色 | 深色 chrome / 主色 | 气质 |
| --- | --- | --- | --- | --- |
| 中性（默认） | 缺省或 `neutral` | #F5F5F5 / #171717 | #1B1B1E / #D0D0D6 | 纯灰阶，单色主操作 |
| 雾蓝 | `mist` | #ECF0F5 / #2E5FA6 | #272B31 / #9DB1CD | 冷灰底，低饱和蓝 |
| 石墨 | `graphite` | #E8ECF2 / #48505D | #26292F / #AEB5C0 | 蓝灰底，石板主色 |
| 钴蓝 | `cobalt` | #E4ECF7 / #2B63B7 | #202836 / #89ADDF | 蓝调底，饱和蓝主色 |
| 鼠尾草 | `sage` | #E7EFEB / #3A7B6A | #222D29 / #8AB8A9 | 灰绿底，墨绿主色 |
| 琥珀 | `amber` | #F4E9CB / #955F1C | #302B21 / #D5A94B | 暖纸底，铜褐主色 |
| 自定义 | `custom` | 由 5 个种子色推导 | 同左，亮暗各一套 | — |

下文「所有底面」指 base、inset、surface、popover、elevated、hover、选中行、焦点行八种。各预设方案中，`fg` 在所有底面上 ≥6.5:1，`fg-muted` 在选中行上 ≥6:1、所有底面上 ≥5:1，`fg-faint` 在所有底面上 ≥4.6:1；同一底面上始终 `fg` > `fg-muted` > `fg-faint`。

### 自定义方案

用户在亮、暗两套里各选 5 个种子色：背景 `bg`、前景 `fg`、主色 `primary`、描边 `border`、侧栏 `sidebar`。其余 token 由运行时按下列步骤推导并写入内联样式。记号：`mix(a, b, w)` 表示从 a 向 b 线性插值 w；「端点」亮色为 #000000，暗色为 #FFFFFF，「反端点」相反；「向 X 推进到 P」指 w 以 0.01 为步长从 0 增加，取第一个满足 P 的值。

**1. 输入约束与调整**

种子色可以任意选择，推导前先校正到可用范围。持久化的是用户原始输入；取色器旁对被调整的种子显示实际生效色和「已调整以保证可读」提示。

- 1）极性：亮色要求 `fg` 比 `bg` 暗，暗色要求 `fg` 比 `bg` 亮；
- 2）可读性：`fg` 在由 `bg`、`fg` 派生出的全部底面（下表 surface 到 row-focused）上都 ≥7:1；
- 3）不满足 1 或 2 时，先把 `fg` 向端点推进；`fg` 到达端点仍不满足（比如 `bg` 是中灰，或 `bg === fg`），再把 `bg` 向反端点推进。两者都到端点时必然满足，所以这一步总有解；
- 4）`sidebar` 也要满足极性和 ≥7:1，不满足就向 `bg` 推进（`bg` 本身已满足）。

满足这两条约束后，下表每一步的目标都可达，推导不会失败。

**2. 推导**

| token | 推导 | 亮 / 暗权重 |
| --- | --- | --- |
| `surface` | `bg` | — |
| `base` | `mix(bg, fg, w)` | 0.04 / 0.04 |
| `inset` | `sidebar` | — |
| `popover` | `mix(bg, fg, w)` | 0 / 0.06 |
| `elevated` | `mix(bg, fg, w)` | 0.06 / 0.12 |
| `hover` | `mix(bg, fg, w)` | 0.06 / 0.14 |
| `row-selected` / `row-focused` / `row-indicator` | `deriveSelection`（见下） | — |
| `line` | `border` | — |
| `line-strong` | `mix(border, fg, 0.08)` | — |
| `ring` | `mix(fg, bg, w)` | 0.62 / 0.42 |
| `fg-muted` | `mix(fg, bg, 0.4)`，再向 `fg` 推进到在 `row-selected` 上 ≥6:1 且所有底面上 ≥5:1 | — |
| `fg-faint` | 从 `fg-muted` 向 `inset` 后退，取所有底面上仍 ≥4.6:1 的最后一步 | — |
| `accent` | `primary`，再向 `fg` 推进到在 `surface` / `base` / `inset` 上 ≥4.6:1 | — |
| `accent-hover` | `accent` 向端点推进到与 `accent` ≥1.2:1；主色已贴近端点、推进不到时，改为 `mix(accent, bg, 0.2)`，并在 `surface` / `base` / `inset` 上 <4.6:1 时减小权重 | — |
| `accent-fg` | #FFFFFF 或 #000000，取对 `accent` 对比度高者（必 ≥4.58:1） | — |
| `sel` / `sel-hover` / `sel-fg` | 先取 `sel = primary`、`sel-hover = mix(sel, bg, w)`；`sel-fg` 在 #FFFFFF / #000000 中取 `min(对 sel, 对 sel-hover)` 较大者。该值 <4.5:1 时把 `sel` 向远离 `sel-fg` 的一端推进，并重算 `sel-hover` 与 `sel-fg`，直到达标 | `sel-hover`：0.12 / 0.10 |
| 语义色 / method 色 / `type-*` | 取预设默认值；在 surface / base / inset / hover / 选中行 / 焦点行或自色染底（12% / 16%）上 <4.5:1 时向端点推进到达标。`type-null` 取 `fg-faint` | — |
| `ok-fg` / `warn-fg` / `danger-fg` / `info-fg` | #FFFFFF 或 #000000，取对推导后语义色对比度高者 | — |

`deriveSelection`（预设方案与自定义方案共用，预设的三个值由它生成后写进 tokens.css，测试保证两者一致）：

- 色相：取 `primary` 的 HSL 色相；饱和度 <0.15 时退回 217（蓝）。染色 = `hsl(色相, 80%, 45%)`（亮）/ `hsl(色相, 80%, 68%)`（暗）。
- 比例：`row-focused` / `row-selected` 取 `mix(bg, 染色, w)` 中第一个与 `bg` 对比度达到 1.22 / 1.1（亮）或 1.4 / 1.22（暗）的 w，按与 surface 的对比度求解而非固定权重，否则黄绿色相天生更亮，暖色方案的选中底会几乎看不见。随后若有文字在其上低于下限（`fg` 6.5、`fg-muted` 焦点 5 / 选中 6、`fg-faint` 4.6、语义色 4.5），w 逐档回退。自定义方案只做第一步，文字色在随后的推导中自行达标。
- 竖条：染色向端点推进到与 `row-focused` 和 `bg` 都 ≥3:1。

极性正确且 `fg` 在所有底面上 ≥7:1 时，端点色在这些底面和染底上都 ≥4.5:1，所以语义色这一步同样不会落空。

用中性方案的种子色代入这套公式，结果和默认 token 一致（误差在 ±8 以内，而且也不会触发任何调整）。`fg-muted` / `fg-faint`（及取自 `fg-muted` 的 `method-other`）例外：推导取刚好达标的灰度，预设值留有余量，两者最多相差 12，但都满足上文全部对比度阈值。设置页需要提供：5 个取色器（亮/暗切换编辑）、实时预览，以及「从当前预设复制」作为起点。

**3. 应用与切换**

- `applyPalette` 每次运行都会先 `removeProperty` 删掉自定义方案可能写过的全部内联变量，再设置 `data-palette`。清除列表就是上表的所有 token，外加旧 `applyAccent` 写的 `--c-accent*`、`--c-sel*`、`--c-row-selected`、`--c-row-focused`、`--wb-selection`，否则内联变量会继续覆盖预设。`--wb-font-size` 也走内联，不在清除列表里。
- 选 `custom` 时设 `data-palette="custom"`，只写入当前明暗那一套的推导结果。
- 切换明暗时重新运行 `applyPalette`，`custom` 用另一套种子重新推导。明暗以实际生效的主题为准，包括 `?theme=` 的 URL 覆盖。
- `palette`、`customPalette` 和下文的 `corner`、`fontSmoothing` 加入 `prefs.ts` 的跨窗口同步键（`GLOBAL_KEYS`），独立设置窗口里的修改实时同步到主窗口。

## 迁移清单

1. 把 `tokens.css` 里的变量替换进 `web/src/workbench/theme/tokens.css`（保留网格背景、滚动条、查找高亮、动画的工具类），去掉写死的 `-webkit-font-smoothing: antialiased`（改由 `data-font-smoothing` 控制）、`.wb-row-read` 的整行 `opacity` 规则和 `--wb-read-opacity`。
2. `tailwind.config.js`：
   - 新增颜色映射：`popover` / `hover` / `ring` / `row-selected` / `row-focused` / `row-indicator` / `type-*` / `overlay`（`rgb(var(--wb-overlay))`），缺 `row-selected` 映射时 `bg-row-selected` 不会生成；
   - 语义色改为带 `fg` 的对象：`ok` / `warn` / `danger` / `info` 各加 `fg: rgb(var(--c-*-fg) / <alpha-value>)`（现有 `warn.fg` 从 `--c-base` 改指 `--c-warn-fg`）；
   - 尺寸：`height` / `width` / `minHeight` 新增 `control-xs` / `control-sm` / `control-md` / `control-lg` / `row`，分别映射到 `--wb-control-*` / `--wb-row-h`，紧凑模式改这些变量即可整体生效；
   - `rounded-lg` 映射到 `--wb-radius-lg`，`shadow-dialog` 映射到 `--wb-shadow-dialog`，`shadow-focus` 映射到 `--wb-focus-ring`；
   - `fontFamily.sans` 改为 `var(--font-sans)`；
   - 删掉未使用的 `primary` / `gray` / `success` / `warning` / `error` 调色板，以及 `index.css` 里引用它们的 `.btn-*` / `.card` / `.input`。
3. 控件尺寸（`ui/controls.tsx` / `ui/primitives.tsx`）：
   - `Button` 的 `md` 从 `h-7` 改为 `h-control-md`，`sm` 从 `h-6` 改为 `h-control-sm`；
   - `IconButton` 同理，改为 `h-control-md w-control-md` / `h-control-sm w-control-sm`；
   - `TextInput` / `Select` 从 `h-7` 改为 `h-control-md`；
   - `SegTabs` 分段项从 `h-6` 改为 `h-control-xs`；
   - 工具条按钮、菜单项用 `h-control-sm`。
4. `Toggle`：圆点从 `bg-white` 改为开 `bg-sel-fg`、关 `bg-fg-muted`，中性深色的 `sel` 是近白色，白圆点在上面只有 1.54:1。
5. 计数徽章：`CountBadge` 改为组件表中的 `bg-danger text-danger-fg`，其他压在实底语义色上的文字一律换成对应的 `*-fg`。
6. 浮层与焦点：
   - 菜单、提示、下拉框（`Menu` / `SelectMenu` / `Tooltip`）的 `bg-elevated` 改为 `bg-popover` 加上 `rounded-lg shadow-wb`，悬停态的 `hover:bg-elevated` 改为 `hover:bg-hover`；
   - 对话框（`ConfirmDialog` / `InfoDialog` / `PasswordDialog` / `ThrottleDialog` / `UpdateDialog` / `CurlImportDialog` / `BreakpointEditor`）的遮罩 `bg-black/40`、`bg-black/50` 改为 `bg-overlay`，面板改为 `bg-popover rounded-lg shadow-dialog`；
   - 现有的 `focus-visible:ring-1 focus-visible:ring-accent`、`ring-sel-fg/80`、`ring-inset` 统一换成 `focus-visible:shadow-focus outline-none`。
7. 删掉 `shadow-raise` / `shadow-press` / `shadow-well` 的用法（token 已是 `none`，之后可以删掉对应的映射）。
8. 列表选中和已读：
   - 去掉 `.wb-row-selected` 强制白字的规则（含其中对开关的特殊处理），选中行用 `bg-row-selected`，焦点行用 `bg-row-focused` 加左侧 3px `row-indicator` 竖条，保留原来的文字色；
   - `TrafficTable` 的 `isRead` 不加整行类，只把 URL / host 单元格的文字色换成 `text-fg-muted`。
9. `prefs.ts`：设置项「强调色」由「配色方案」取代，`accent` / `accentCustom` 偏好替换为 `palette` / `customPalette`（亮暗各 5 色），`applyAccent` 替换为按上文「应用与切换」实现的 `applyPalette`；`usePrefsBridge` 的 effect 依赖 `[palette, customPalette, theme]`，`applyPrefsToDocument` 用实际生效的主题调用；`GLOBAL_KEYS` 删掉 `accent` / `accentCustom`，加入 `palette` / `customPalette` / `corner` / `fontSmoothing`。持久化版本号加一，旧偏好按下表迁移：

    | 旧强调色 | 新方案 |
    | --- | --- |
    | sky / prussian | cobalt |
    | teal | sage |
    | brass | amber |
    | slate | graphite |
    | vermilion | neutral |
    | custom（hex） | custom，以中性方案为底，`primary` 取原来的 hex |
10. 新偏好的设置入口：`SettingsView` 外观分组加入「配色方案」（6 个预设 + 自定义编辑器）、「圆角」（`default` / `large` / `none`，写 `data-corner`，`default` 时移除该属性）、「字体平滑」（开关，写 `data-font-smoothing="antialiased"`，关闭时移除），三种语言的 `i18n/locales/*.json` 加对应文案。
11. 字体：
    - 安装 `@fontsource-variable/geist`，在 `main.tsx` 中把 `@fontsource-variable/inter/wght.css` 改为 `@fontsource-variable/geist/wght.css`，并从 `package.json` 移除 `@fontsource-variable/inter`；
    - `licenses/components.json` 的 `web` 列表同步用 `@fontsource-variable/geist` 顶替 `@fontsource-variable/inter`；
    - 在 `web/`、`site/` 都已 `npm ci`、Go 模块已下载完毕的前提下，运行 `node scripts/generate-third-party-notices.mjs` 重新生成 `internal/notices/THIRD_PARTY_NOTICES.txt` 和 `licenses/notices-lock.json`，再以 `--check` 核对。`npm run build` 的 prebuild 会通过 `--check-inputs` 比对 `web/package-lock.json` 与 `licenses/components.json` 的 hash，漏掉这个步骤会直接构建失败。字体依赖、组件清单、声明文件放在同一个 commit 里。
