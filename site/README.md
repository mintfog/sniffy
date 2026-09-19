# Sniffy 官网

使用 Astro 构建产品首页，使用 Starlight 维护文档。依赖与构建产物位于 `site/`。

中文在根路径，英文在 `/en/` 下：首页为 `/` 与 `/en/`，文档为 `/docs/` 与 `/en/docs/`。

## 本地开发

需要 Node.js 22.12+。

```bash
cd site
npm ci
npm run dev
```

默认访问 `http://localhost:4321`。首页工作台按应用本体的界面复刻，示例会话可按类型筛选、逐条选中，请求与响应两侧的页签以及 Tree / Raw / Hex 均可切换；下载入口指向对象存储上的安装包。

## 检查与构建

```bash
npm run check
npm run build
npm run preview
```

静态文件输出到 `dist/`。当前路由以域名根路径部署为前提，`astro.config.mjs` 的 `site` 决定 canonical 链接与 sitemap 中的绝对地址。

## 内容位置

- `src/i18n/locales/*.json`：首页与工作台演示的全部文案，一种语言一个文件。工作台用词与应用本体的 `web/src/i18n/locales/` 保持一致；功能卡片与下载选项使用固定键名关联展示数据。
- `src/i18n/types.ts`：文案结构与语言表；`src/i18n/ui.ts` 把 JSON 按 `Record<Lang, SiteCopy>` 装配，漏字段会在 `npm run check` 报错并指出具体路径。
- `src/layouts/HomePage.astro`：首页 HTML 外壳、元信息与区块编排；`src/pages/index.astro` 与 `src/pages/en/index.astro` 只负责传入语言。
- `src/components/*Section.astro`、`SiteHeader.astro`、`ProtocolStrip.astro`、`SiteFooter.astro`：各区块按 `lang` 读取文案，模板、作用域样式与交互脚本由组件管理。
- `src/components/BrandMark.astro`、`PlatformList.astro`、`SectionHeading.astro`、`EditionCard.astro`：站标、平台列表、区块标题与版本卡片的共享组件。
- `src/components/Lines.astro`：把文案数组渲染成多行，行间补 `<br>` 与空格。
- `src/components/WorkbenchPreview.astro`：工作台演示的模板、样式与筛选、会话选择、页签交互。
- `src/data/workbench.ts`：示例会话及其展示数据，在构建时生成参数、请求头、响应头、Raw / Tree / Hex 内容与浏览器使用的会话摘要。摘要类型从生成结果推导；示例流量使用 `example.com` 等保留域名。
- `src/styles/base.css`：字体、设计变量、重置与基础工具类；`src/styles/ui.css`：按钮、链接和标题等共享样式。各区块与工作台的样式及响应式规则位于对应组件的 `<style>` 中，作用域使用 `where` 策略保留基础样式的覆盖关系。
- `src/content/docs/docs/`：中文文档；`src/content/docs/en/docs/`：英文文档，同名文件一一对应。管理 API 单独成一组，位于两侧的 `docs/api/` 下，一个资源一页。侧边栏分组与各页标题在 `astro.config.mjs` 的 `sidebar` 中定义，英文标题写进 `translations`。
- `src/components/docs/Endpoint.astro`：API 端点标题，渲染动词徽章与路径，`{id}` 段单独着色。多个动词写成 `method="PUT / POST"`。
- `src/components/docs/Screenshot.astro`：截图槽位，未登记的文件名渲染占位框并标出待补的截图。
- `src/components/docs/Downloads.astro`：安装页的下载表，按形态（desktop / headless）列出清单里的产物。
- `src/data/release.json`：发布清单，由 `scripts/release-manifest.sh` 生成，见下节。
- `public/sniffy-mark.png`：产品标志，来源于桌面应用素材。

用到组件的页面必须是 `.mdx`，且 import 与正文之间空一行。

## 发布清单

`src/data/release.json` 是首页下载区、安装页下载表与客户端「检查更新」共用的数据源，经 `src/pages/release.json.ts` 发布到 `/release.json`。客户端以它为主源、以对象存储上的同名文件为兜底源（见 `internal/update`），全程不经过 GitHub。

发版时在仓库根目录执行：

```bash
bash scripts/release-manifest.sh --version v1.2.3 --dir dist
```

脚本按文件名解析出系统、架构与形态，体积和 SHA256 一律从目录里的实际文件现算，不读取同目录的 `SHA256SUMS`。因此必须拿**即将上传的那批文件**生成：清单里的校验值对不上，客户端下完会判定失败并删掉安装包。

随后把同一批产物传到 `https://cdn.gosniffy.com/releases/<tag>/`，`release.json` 也传一份到 `https://cdn.gosniffy.com/release.json` 作为兜底源，最后部署官网。

## 文档截图

截图由 CDN 提供，不进仓库。中英文两侧引用同一个文件名。

```mdx
import Screenshot from "@/components/docs/Screenshot.astro";

<Screenshot src="workbench-traffic-list.png" alt="工作台的流量列表" />
```

文件名登记在 `Screenshot.astro` 的 `SIZES` 中，未登记的渲染占位框并标出待补的截图，引用它的页面不需要改动。新截图由维护者上传并登记。

英文界面另拍的一份传到 `screenshots/v1/en/`，文件名登记在同文件的 `EN_SHOTS` 中。英文页只在登记后才切过去，未登记的继续用中文那张，因此可以一张一张补——补一张登记一行，文档页不动。英文图与中文图同数据、同视口渲染，尺寸一致，故沿用 `SIZES`；若某张英文图尺寸不同，`EN_SHOTS` 要改回独立的尺寸表。

命名用小写短横线，前缀是所属界面：`workbench-`、`certs-`、`rules-`、`breakpoints-`、`plugins-`、`compose-`、`settings-`、`install-`。

截图前请清掉真实域名、令牌与个人信息，示例流量使用 `example.com` 一类保留域名。

## 新增语言

1. 复制 `src/i18n/locales/` 下任一 JSON 翻译成目标语言。
2. 在 `src/i18n/types.ts` 的 `Lang` 与 `languages` 中登记，`base` 决定该语言的路径前缀。
3. 在 `src/i18n/ui.ts` 的 `siteCopy` 表里引入这份 JSON，缺字段此时会报错。
4. 新建 `src/pages/<lang>/index.astro`，内容只有一行 `<HomePage lang="<lang>" />`。
5. 文档侧在 `astro.config.mjs` 的 `locales` 中加入该语言，侧边栏标题写进 `translations`。

## 补充翻译

未翻译的页面由 Starlight 回退到中文原文并提示读者，补齐方式是在 `src/content/docs/en/docs/` 下新建同名文件。新增文档页时两侧同时新建，并在 `astro.config.mjs` 的 `sidebar` 中登记。

文档内的站内链接写绝对路径，中文指向 `/docs/…`、英文指向 `/en/docs/…`，锚点按各自语言的标题生成。

多行文案按数组书写，行数可以随语言不同——中文分两行的句子，英文写成三行也不必改模板。
