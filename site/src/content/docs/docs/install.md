---
title: 安装
description: 下载桌面安装包或 headless 可执行文件，或从源码构建 Sniffy。
---

## 选择形态

| 形态                    | 适合                                   | 交互方式                     |
| ----------------------- | -------------------------------------- | ---------------------------- |
| 桌面版                  | 日常抓包、界面化改写与调试             | 原生窗口                     |
| 无界面版（headless）    | 服务器、容器、CI，以及脚本化的自动调试 | REST + WebSocket             |

两者共用同一套抓包引擎、规则引擎与插件系统，配置与规则也存放在同一个目录，可以在同一台机器上交替使用。

## 桌面版

在 [GitHub Releases](https://github.com/mintfog/sniffy/releases) 中选择对应平台的安装包。

| 操作系统 | 架构       | 安装包                                       |
| -------- | ---------- | -------------------------------------------- |
| Windows  | x64        | `sniffy-desktop-windows-amd64-installer.exe` |
| macOS    | Apple 芯片 | `sniffy-desktop-darwin-arm64-installer.dmg`  |
| macOS    | Intel      | `sniffy-desktop-darwin-amd64-installer.dmg`  |
| Linux    | x64        | `sniffy-desktop-linux-amd64-installer.deb`   |

### Windows

运行 `.exe` 安装程序，按向导完成安装。若 SmartScreen 弹出「已保护你的电脑」提示，点击「更多信息 - 仍要运行」。

Windows 桌面版需要 Microsoft Edge WebView2 Runtime。若系统尚未安装或启动后窗口空白，请安装 [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)。

### macOS

打开 `.dmg`，把 Sniffy 拖进「应用程序」。安装包使用 ad-hoc 签名，暂时没有 Apple 开发者证书与公证，首次打开时 Gatekeeper 会拦下：

- 在「应用程序」中右键点击 Sniffy，选择「打开」，在对话框中再次确认；或
- 在「系统设置 - 隐私与安全性」中，对被拦截的应用点击「仍要打开」。

如果提示应用「已损坏」，请先确认安装包来自本项目的 Releases 页面且下载完整。确认是隔离属性导致的拦截后，可执行：

```bash
xattr -dr com.apple.quarantine /Applications/Sniffy.app
```

### Linux

`.deb` 适用于 Debian、Ubuntu 及其衍生版：

```bash
sudo apt install ./sniffy-desktop-linux-amd64-installer.deb
```

包内会安装可执行文件 `/usr/bin/sniffy`、桌面入口与图标，依赖由 `dpkg-shlibdeps` 在构建时解析，主要是系统 WebKitGTK 组件。安装后可从应用菜单启动，也可直接运行 `sniffy`。

其他发行版请使用下面的源码构建，或在容器里运行无界面版。

## 无界面版

Releases 中的 headless 可执行文件按 `sniffy-<os>-<arch>` 命名，覆盖 `linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`、`windows/amd64`、`windows/arm64`。它是纯静态的单文件，没有运行时依赖。

```bash
chmod +x sniffy-linux-amd64
./sniffy-linux-amd64 -version
./sniffy-linux-amd64
```

默认代理监听 `0.0.0.0:8080`，管理 API 监听 `127.0.0.1:8888`。详见[无界面版](/docs/headless/)。

## 从源码构建

需要 Go 1.26+。前端与桌面版还需要 Node.js 22.12+。

```bash
git clone https://github.com/mintfog/sniffy.git
cd sniffy
```

### 无界面版

```bash
go build -o sniffy ./cmd/sniffy
```

交叉编译到 `dist/`：

```bash
bash scripts/build.sh headless linux/amd64 darwin/arm64 windows/amd64
```

headless 不依赖 CGO，可以在任意一台机器上为所有目标平台交叉编译。

### 桌面版

桌面版基于 Wails v3，需要 `desktop` 构建标签，并且必须先构建前端：

```bash
cd web && npm ci && npm run build && cd ..
go run -tags desktop .
```

生产构建：

```bash
bash scripts/build.sh desktop
```

构建脚本是 bash，Windows 下请在 Git Bash 中运行。

平台依赖：

- **Windows** 使用 WebView2，不依赖 CGO，支持交叉编译：`task desktop:windows`，打包 NSIS 安装程序用 `task desktop:windows:installer`。
- **macOS / Linux** 需要 CGO 与系统 webview 开发包（Linux 上为 WebKitGTK 及其开发头文件）。

不带 `desktop` 标签时，仓库根目录的入口是一个 stub，因此 `go build ./...` 在没有 webview 依赖的环境里也能通过。

## 数据存放位置

两种形态共用同一份配置与规则。

| 用途   | 位置                                                               |
| ------ | ------------------------------------------------------------------ |
| 配置区 | Linux `~/.config/sniffy`、macOS `~/Library/Application Support/sniffy`、Windows `%AppData%\sniffy` |
| 缓存区 | Linux `~/.cache/sniffy`、macOS `~/Library/Caches/sniffy`、Windows `%LocalAppData%\sniffy` |

配置区存放 `config.json`、`rules.json`、日志、用户插件与证书；缓存区只存大体积响应体的落盘副本，随时可以删除。完整说明见[配置参考](/docs/configuration/)。

## 升级与卸载

升级时覆盖安装即可，配置区不会被安装程序改动，规则、插件与根证书都会保留。

卸载走各平台的常规方式（Windows 的「应用和功能」、macOS 直接删除应用、Linux `sudo apt remove sniffy`）。卸载不会删除配置区与缓存区，也不会移除已经装进系统信任库的根证书——如果不再使用 Sniffy，请手动删除这两个目录，并在系统信任库中删除 Sniffy Root CA，见 [HTTPS 解密与根证书](/docs/https/#移除根证书)。
