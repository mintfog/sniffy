<p align="center">
  <img src="web/src/assets/sniffy-mark.png" width="112" alt="Sniffy">
</p>

<h1 align="center">Sniffy</h1>

<p align="center">
  <strong>中文</strong> · <a href="README.en.md">English</a>
</p>

<p align="center">
  <strong>开源、跨平台的抓包与调试工具</strong><br>
  支持 HTTP / HTTPS、WebSocket、gRPC、SSE
</p>

<p align="center">
  <a href="https://gosniffy.com/download/"><img src="https://img.shields.io/github/v/release/mintfog/sniffy?include_prereleases&amp;style=flat-square&amp;label=release&amp;color=087F8C" alt="最新版本"></a>
  <a href="https://github.com/mintfog/sniffy/actions/workflows/test.yml?query=branch%3Amain"><img src="https://img.shields.io/github/actions/workflow/status/mintfog/sniffy/test.yml?branch=main&amp;style=flat-square&amp;label=tests" alt="测试状态"></a>
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-334155?style=flat-square" alt="Windows / macOS / Linux">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-334155?style=flat-square" alt="Apache License 2.0"></a>
</p>

<p align="center">
  <a href="https://gosniffy.com/">官网</a> ·
  <a href="https://gosniffy.com/download/"><strong>下载 Sniffy</strong></a> ·
  <a href="#features">功能特性</a> ·
  <a href="https://gosniffy.com/docs/desktop/">快速开始</a> ·
  <a href="https://gosniffy.com/docs/plugins/">插件扩展</a> ·
  <a href="https://gosniffy.com/docs/">使用文档</a> ·
  <a href="https://github.com/mintfog/sniffy/issues">问题反馈</a>
</p>

---

Sniffy 运行在 Windows、macOS 和 Linux 上，用来抓取和查看 HTTP / HTTPS 请求与响应、WebSocket 双向消息，以及 gRPC、SSE 等流式会话。

排查 HTTP 接口问题时，可以重发请求来复现问题，或返回 Mock 响应检查客户端的处理结果。也可以在请求发出或响应返回前打断点，修改内容后手动放行。

## 下载与安装

**[下载 Sniffy →](https://gosniffy.com/download/)** · [安装指南](https://gosniffy.com/docs/install/)

<a id="features"></a>

## 功能特性

| 功能 | 用途 |
| --- | --- |
| **流量检查** | 查看 HTTP / HTTPS 请求与响应的头部和正文，按条件搜索、筛选或标记会话。 |
| **请求调试** | 构造新请求，将已捕获的请求直接重发或编辑后重发；可导入和复制 cURL 命令。 |
| **规则改写** | 命中条件时改写请求或响应内容，执行重定向、阻断请求或返回 Mock 响应。 |
| **断点调试** | 在请求发出或响应返回前暂停流量，检查并修改数据后手动放行。 |
| **WebSocket** | 查看客户端与服务端之间收发的消息。 |
| **gRPC / SSE** | 逐条查看 gRPC 消息、SSE 事件等流式数据。 |
| **插件扩展** | 在应用内编写 JavaScript 脚本，设置作用范围并查看日志；已启用的脚本保存后自动重载。 |
| **会话导出** | 将 HTTP 会话导出为 HAR 文件，或将流量数据导出为 JSON。 |

<a id="getting-started"></a>

## 快速开始

完整步骤见[桌面版快速开始](https://gosniffy.com/docs/desktop/)与[HTTPS 解密与根证书](https://gosniffy.com/docs/https/)。

1. **接入流量**：启动 Sniffy 后打开「系统代理」，或把目标应用的 HTTP / HTTPS 代理指向 `127.0.0.1:8080`。若改过监听端口，请替换为设置中的实际端口。
2. **配置 HTTPS**：进入「证书管理」，按当前系统的提示安装并信任 Sniffy 根证书。如果要抓手机或其他设备的 HTTPS 流量，同样需要在对应设备上安装并信任这张证书。
3. **查看会话**：在目标应用中发起请求，在流量列表中查看头部、正文、状态码与耗时，可按域名或关键字筛选。
4. **复现与修改**：选择会话重发，或配置规则、断点来调整请求与响应，验证客户端行为。

<a id="plugins"></a>

## 插件扩展

当内置规则不够用时，可以在应用内编写 JavaScript 插件。常见用途包括自动注入鉴权头、按请求内容动态生成 Mock 响应、按业务条件触发断点，或修改 WebSocket 与流式消息。

在「插件」页面可从内置模板新建脚本，编辑后保存并启用。已启用的插件在保存后会自动热重载，同一页面提供日志输出、配置项与 URL 作用范围设置。

<details>
<summary>示例：为接口返回 Mock 数据</summary>

以下脚本为 `/api/demo/profile` 返回固定的 JSON 响应，可通过插件白名单限定生效的目标域名。

```js
function onRequest(flow) {
  if (flow.path !== '/api/demo/profile') return;

  mock({
    status: 200,
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
    body: JSON.stringify({ name: 'Sniffy', plan: 'pro' }),
  });
}
```

</details>

插件可在请求、响应、WebSocket 消息和流式消息四个阶段执行，内置编码、哈希、签名与持久化存储等 API。详见[插件开发文档](https://gosniffy.com/docs/plugins/)。

<a id="documentation"></a>

## 使用文档

- 想把浏览器、命令行或移动端的流量接进来，可先看[接入流量](https://gosniffy.com/docs/capture/)与[HTTPS 解密与根证书](https://gosniffy.com/docs/https/)。
- 需要用脚本按业务条件处理流量时，见[插件开发文档](https://gosniffy.com/docs/plugins/)中关于钩子限制、文本与二进制载荷、编码转换、哈希签名和本地存储的说明。
- 测试环境使用自签名证书或内部 CA 时，可在[出站 TLS 证书校验](https://gosniffy.com/docs/https/#出站方向的证书校验)查看源站证书的信任配置。
- 在服务器上运行 Sniffy 时，[Headless 部署](https://gosniffy.com/docs/headless/)与[管理 API](https://gosniffy.com/docs/api/)介绍了启动方式、认证、远程访问和接口用法。

## Headless 模式

在服务器环境或自动化测试中，可以运行无界面的 [Headless 模式](https://gosniffy.com/docs/headless/)，调用 REST API 管理会话、规则与插件，并通过 WebSocket 接收实时事件。

<details>
<summary>启动与 API 接入</summary>

下载对应平台的 [Headless 可执行文件](https://gosniffy.com/docs/install/#无界面版)直接运行，或使用 Go 1.26+ 从源码启动：

```bash
go run ./cmd/sniffy
```

代理默认监听 `:8080`，可用 `-port` 修改端口；`-addr` 只接受通配监听地址（空值、`*`、`0.0.0.0` 或 `::`）。管理 API 默认监听 `http://127.0.0.1:8888`，可用 `-api-addr` 和 `-api-port` 调整。

调用管理 API 需要携带 Bearer token。进程启动时会先读取环境变量 `SNIFFY_API_TOKEN`；若未设置，则读取用户配置目录下的 `sniffy/api_token` 文件。该文件不存在时会自动生成 token 并写入该位置，同时在日志中打印文件路径。

把实际 token 导出到当前终端的 `SNIFFY_API_TOKEN` 后，即可查询会话：

```bash
curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

WebSocket 事件入口为 `/api/ws`。管理 API 绑定非回环地址时，需通过 `-api-tls-cert` 与 `-api-tls-key` 配置 HTTPS；由 TLS 反向代理或 VPN 保护的环境可通过 `-allow-insecure-api` 显式允许 HTTP。

桌面界面通过 Wails 连接本地服务。

</details>

## 从源码开发

Sniffy 使用 Go、Wails v3、React 和 TypeScript 构建，桌面端与 Headless 模式共用同一套抓包、规则和插件运行时。

<details>
<summary>开发环境与构建命令</summary>

准备 Go 1.26+、Node.js 22+ 与 npm，以及对应平台的桌面依赖：

| 平台 | 桌面依赖 |
| --- | --- |
| Windows | WebView2；Bash 脚本通过 Git Bash 运行 |
| macOS | 系统 WebView、Xcode Command Line Tools，启用 CGO |
| Linux | C 编译工具链、GTK 4、WebKitGTK 6.0，启用 CGO |

克隆仓库并启动桌面开发环境：

```bash
git clone https://github.com/mintfog/sniffy.git
cd sniffy
bash scripts/build.sh frontend
go run -tags desktop .
```

在仓库根目录执行以下命令，构建产物输出到 `dist/`：

```bash
# 构建当前平台的桌面程序
bash scripts/build.sh desktop

# 构建 Headless 程序，支持交叉编译
bash scripts/build.sh headless
bash scripts/build.sh headless linux/amd64 darwin/arm64 windows/amd64

# 运行测试与前端检查
go test ./...
npm --prefix web test
npm --prefix web run type-check
npm --prefix web run lint
```

也可使用 [Taskfile.yml](Taskfile.yml) 中的任务：`task dev`、`task desktop`、`task build:all` 与 `task test`。

</details>

## 参与贡献

欢迎通过 [Issues](https://github.com/mintfog/sniffy/issues) 反馈问题或提出功能建议，也欢迎提交 [Pull Requests](https://github.com/mintfog/sniffy/pulls) 改进代码、文档，或分享插件与调试案例。

反馈问题时，请附上 Sniffy 版本、操作系统、复现步骤，并移除日志或抓包文件中的敏感信息。涉及较大改动时，建议先通过 Issue 讨论。

## 致谢

感谢以下开源项目与社区：

- [Wails](https://github.com/wailsapp/wails) — 跨平台桌面应用框架。
- [goja](https://github.com/dop251/goja) — Go 实现的 JavaScript 引擎，为插件系统提供运行时。
- [LINUX DO](https://linux.do/) — 新的理想型社区。
- [V2EX](https://www.v2ex.com/) — 创意工作者们的社区。

## 开源许可

Sniffy 基于 [Apache License 2.0](LICENSE) 开源。

第三方组件的许可与源码获取方式见[第三方依赖与许可证](https://gosniffy.com/docs/third-party/)，完整声明随软件分发。
