# sniffy

跨平台抓包 / 代理工具,核心差异化是**可脚本化的插件系统**:任何人都能用 JavaScript 写插件,实时改请求/改响应、mock、断点。

## 架构(分层)

```
internal/core      抓包引擎:监听 / 协议处理 / TLS MITM / 运行插件管道,发事件总线
internal/flow      统一契约:Flow / Decision / 编解码(引擎↔脚本↔UI↔存储共用)
internal/pipeline  插件管道:按优先级编排、应用 Decision、断点管理
internal/plugin    两层插件:js(goja 脚本)+ native(Go 原生);plugin.json + 脚本
internal/service   唯一真相源:会话/统计/规则/录制/配置/证书/插件/断点
internal/api       headless 传输:HTTP REST + gorilla WebSocket 实时推送
internal/desktop   桌面传输:Wails v3 Service 绑定 + 事件桥(// +build desktop)
internal/app       装配:engine + service + pipeline + plugins
cmd/sniffy         headless 服务器入口
cmd/sniffy-desktop 桌面入口(Wails v3)
web                前端(React + Vite + TS + Tailwind)
capture/ ca/ pkg/process  抓包核心 / CA / 进程检测(沿用)
```

引擎只管抓包并广播事件;service 是唯一真相源;headless 与桌面两种 transport 都是 service 的薄壳,共享同一套逻辑。

## 运行

### headless 服务器模式
```bash
go run ./cmd/sniffy            # 代理 :8080,管理 API+WS :8888
# 浏览器把 HTTP 代理设为 127.0.0.1:8080,前端开发: cd web && npm run dev
```

### 桌面模式(Wails v3)
需安装各平台 webview 依赖(Windows: WebView2,纯 Go 无需 CGO;macOS: 自带;Linux: libwebkit2gtk-4.1-dev)。
```bash
scripts/build.sh desktop      # 构建前端 + 编译桌面二进制(-tags desktop)
# 或开发:cd web && npm run build && go run -tags desktop .
```

### 数据目录
配置与日志均落盘在用户配置目录 `<UserConfigDir>/sniffy/`(Windows: `%AppData%\sniffy`,macOS: `~/Library/Application Support/sniffy`,Linux: `~/.config/sniffy`):
- `config.json` / `rules.json` —— 应用配置(监听地址/端口、上游代理等,启动时读回)与重写规则;
- `logs/sniffy-<日期>.log` —— 运行日志,按天滚动,保留 7 天;
- `plugins/` —— 用户插件。

headless 模式下命令行显式指定的 `-addr`/`-port` 优先于 `config.json`。

## 构建
```bash
scripts/build.sh headless                       # 当前平台
scripts/build.sh headless linux/amd64 darwin/arm64 windows/amd64   # 交叉编译(纯 Go)
scripts/build.sh frontend                        # 仅前端
scripts/build.sh desktop                         # 桌面二进制
# 也可用 Taskfile:task build / task build:all / task desktop / task test
```

## 写一个插件

插件放在用户配置目录 `<UserConfigDir>/sniffy/plugins/<id>/`,含 `plugin.json` + 脚本:

```js
// index.js — 可用钩子:onRequest / onResponse / onWebSocketMessage
function onResponse(flow) {
  flow.response.headers['X-Sniffy'] = 'hello'        // 改响应头
  flow.response.body = flow.response.body.replace('foo', 'bar')  // 改响应体
}
function onRequest(flow) {
  if (flow.url.includes('/blocked')) abort({ status: 403, reason: 'no' }) // 阻断
  if (flow.url.includes('/mock')) mock({ status: 200, body: 'mocked' })   // mock,不打上游
  if (flow.url.includes('/debug')) setBreakpoint()                         // 断点,UI 手动放行
}
```
`flow` 字段:`id, method, url, host, path, headers{}, body, response{status,statusText,headers,body}`。
宿主 API:`console.*`、`store.get/set`、`settings`、`notify`。也可在桌面 App 的"插件"页用 Monaco 编辑器在线写、保存即热重载。

## License

Licensed under the Apache License 2.0. Copyright 2025 goSniffy authors. See [LICENSE](./LICENSE).
