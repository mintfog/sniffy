<p align="center">
  <img src="web/src/assets/sniffy-mark.png" width="112" alt="Sniffy">
</p>

<h1 align="center">Sniffy</h1>

<p align="center">
  <strong>中文</strong> · <a href="README.en.md">English</a>
</p>

<p align="center">
  <strong>开源、跨平台的抓包与调试工具</strong><br>
  看清流量，掌控请求。
</p>

<p align="center">
  <a href="https://github.com/mintfog/sniffy/releases"><img src="https://img.shields.io/github/v/release/mintfog/sniffy?include_prereleases&amp;style=flat-square&amp;label=release&amp;color=087F8C" alt="最新版本"></a>
  <a href="https://github.com/mintfog/sniffy/actions/workflows/test.yml?query=branch%3Amain"><img src="https://img.shields.io/github/actions/workflow/status/mintfog/sniffy/test.yml?branch=main&amp;style=flat-square&amp;label=tests" alt="测试状态"></a>
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-334155?style=flat-square" alt="Windows / macOS / Linux">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-334155?style=flat-square" alt="Apache License 2.0"></a>
</p>

<p align="center">
  <a href="https://github.com/mintfog/sniffy/releases"><strong>下载 Sniffy</strong></a> ·
  <a href="#features">功能特性</a> ·
  <a href="#getting-started">快速开始</a> ·
  <a href="#plugins">插件扩展</a> ·
  <a href="#documentation">使用文档</a> ·
  <a href="https://github.com/mintfog/sniffy/issues">问题反馈</a>
</p>

---

Sniffy 是面向开发与测试的网络调试工具，支持 Windows、macOS 和 Linux。你可以在同一个工作台中检查 HTTP / HTTPS 流量、重发请求、改写响应，并通过断点逐步排查接口问题。

Sniffy 的特色是**应用内 JavaScript 插件**：从一次性的 Mock 到可复用的业务调试规则，直接编写、保存并观察效果。

## 下载与安装

**[前往 Releases 下载 Sniffy →](https://github.com/mintfog/sniffy/releases)**

在版本页面的 **Assets** 中找到下表对应的文件，下载并安装即可使用。

| 操作系统 | 处理器架构 | 下载文件 |
| --- | --- | --- |
| Windows | x64 | `sniffy-desktop-windows-amd64-installer.exe` |
| macOS | Apple 芯片 | `sniffy-desktop-darwin-arm64-installer.dmg` |
| macOS | Intel | `sniffy-desktop-darwin-amd64-installer.dmg` |
| Linux | x64 | `sniffy-desktop-linux-amd64-installer.deb` |

<a id="features"></a>

## 功能特性

| 功能 | 用途 |
| --- | --- |
| **流量检查** | 查看 HTTP / HTTPS 请求与响应，检查头部和正文，通过搜索、筛选与标记定位目标会话。 |
| **请求调试** | 构造请求、直接重发或编辑后重发，支持导入与复制 cURL，复现接口行为。 |
| **规则改写** | 按匹配条件修改请求与响应，配置重定向、阻断和 Mock，验证不同业务场景。 |
| **断点调试** | 在请求或响应阶段暂停，检查并调整数据后手动放行。 |
| **实时消息** | 查看 WebSocket 消息与 SSE、gRPC 等流式会话，排查长连接中的交互问题。 |
| **插件扩展** | 在应用内编写 JavaScript，支持脚本热重载、作用范围配置与实时日志。 |
| **会话导出** | 将 HTTP 会话导出为 HAR，或将流量信息导出为 JSON，便于留存与进一步分析。 |

<a id="getting-started"></a>

## 快速开始

1. **接入流量**：打开 Sniffy，开启「系统代理」，或将目标应用的 HTTP / HTTPS 代理设为 `127.0.0.1:8080`。如果修改过监听端口，请使用设置中的实际值。
2. **配置 HTTPS**：打开「证书管理」，按应用内的平台引导安装并信任 Sniffy 根证书。抓取其他设备的 HTTPS 流量时，也需要在对应设备上完成信任配置。
3. **查看会话**：在目标应用中发起请求，回到流量列表查看头部、正文、状态码与耗时，再按域名或关键字筛选。
4. **复现与验证**：选择会话重发，或使用规则、断点和插件调整请求与响应，观察客户端行为。

<a id="plugins"></a>

## 在应用内编写插件

自动注入鉴权信息、为接口返回 Mock 数据、按业务条件触发断点，或处理 WebSocket 与流式消息，都可以用 JavaScript 实现。新建插件时可选择内置模板，快速开始编写。

**打开「插件」页面 → 新建插件并选择模板 → 编辑脚本 → 保存并启用。** 已启用的插件支持保存后热重载，可在同一页面查看日志、调整配置与作用范围。

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

插件可在请求、响应、WebSocket 消息和流式消息四个阶段执行，并提供编码、哈希、签名与持久化存储等 API。详见[插件助手函数参考](docs/plugins-helpers.md)。

<a id="documentation"></a>

## 使用文档

- **代理与证书配置**：参阅上方[快速开始](#getting-started)及应用内「证书管理」的平台安装引导。
- **[插件助手函数参考](docs/plugins-helpers.md)**：钩子运行约束、文本与二进制载荷、编码、签名及存储 API。
- **[出站 TLS 证书校验](docs/outbound-tls.md)**：源站证书信任、内部测试环境配置与 API 示例。

## Headless 模式

Sniffy 同时提供 Headless 模式，支持通过 REST API 管理会话、规则与插件，通过 WebSocket 订阅实时事件，适合服务器部署与自动化调试。

<details>
<summary>启动与 API 接入</summary>

从 [Releases](https://github.com/mintfog/sniffy/releases) 下载对应平台的 Headless 可执行文件运行，或使用 Go 1.26+ 从源码启动：

```bash
go run ./cmd/sniffy
```

默认代理端口为 `8080`，管理 API 地址为 `http://127.0.0.1:8888`。代理地址与端口可通过 `-addr`、`-port` 指定，管理 API 使用 `-api-addr`、`-api-port`。

管理 API 的所有请求均需 Bearer token。启动时可通过 `SNIFFY_API_TOKEN` 设置；未设置时自动生成并保存到用户配置目录的 `sniffy/api_token` 文件，具体路径见启动日志。将实际 token 设为当前终端的 `SNIFFY_API_TOKEN` 后，即可查询会话：

```bash
curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

WebSocket 事件入口为 `/api/ws`。管理 API 绑定非回环地址时，需通过 `-api-tls-cert` 与 `-api-tls-key` 配置 HTTPS；由 TLS 反向代理或 VPN 保护的环境可通过 `-allow-insecure-api` 显式允许 HTTP。

桌面界面通过 Wails 连接本地服务。

</details>

## 从源码开发

Sniffy 使用 **Go + Wails v3 + React + TypeScript**，桌面与 Headless 模式共享同一套抓包、规则和插件逻辑。

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

应用内更新的测试命令、覆盖率门槛和平台验证范围见 [更新功能测试](docs/update-testing.md)。

版本标签、发布说明与自动构建流程见[版本发布指南](docs/releasing.md)。

</details>

## 参与贡献

欢迎通过 [Issues](https://github.com/mintfog/sniffy/issues) 反馈问题、提出功能建议，或通过 [Pull Requests](https://github.com/mintfog/sniffy/pulls) 改进代码与文档。实用的插件和调试案例同样欢迎分享。

反馈问题时，请附上 Sniffy 版本、操作系统、复现步骤及必要的脱敏信息。涉及较大改动时，建议先通过 Issue 讨论。

## 开源许可

Sniffy 基于 [Apache License 2.0](LICENSE) 开源。
