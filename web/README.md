# Sniffy 桌面前端

Sniffy 桌面端的前端界面（现代抓包工作台）。它**不是独立网页应用**：生产形态下被构建进 `web/dist` 并嵌入 Go 二进制，由 **Wails v3** 的资源服务器在 WebView 内提供，经 `@wailsio/runtime` 的原生绑定/事件直连 Go 后端（不再走 HTTP/REST）。

> HTTP/REST + `/api/ws` 仅属于独立的 **headless** 模式（`cmd/sniffy` + `internal/api`），与本前端无关。

## 🛠️ 技术栈

- React 18 + TypeScript + Vite
- Tailwind CSS
- 状态管理：Zustand
- 后端通信：`@wailsio/runtime`（`Call.ByName` 调用 + `Events.On` 事件），版本与 Go 侧锁定 `3.0.0-alpha.79`
- 路由：React Router v6
- 图标：Lucide React

## 🚀 开发与构建

```bash
cd web
npm install

npm run dev            # 浏览器预览（无 Wails 后端 → 自动落入演示数据，仅看 UI）
npm run build          # 生产构建（纯 vite，不跑 tsc）→ web/dist
npm run build:typecheck # tsc 类型检查 + 构建
```

真实运行请构建桌面二进制（前端会被嵌入）：

```bash
# 仓库根目录
cd web && npm run build && cd ..
CGO_ENABLED=0 go build -tags desktop -o sniffy-desktop.exe .   # 或 scripts/build.sh desktop
go run -tags desktop .                                          # 直接运行
```

## 🔌 后端集成（Wails v3 原生绑定）

前端经桥接层 `src/lib/bridge.ts` 调用 Go 侧 `internal/desktop.Bridge` 的导出方法：

```ts
import { Call, Events } from '@wailsio/runtime'

// 调用后端方法：FQN = 包导入路径 + .Bridge. + 方法名
Call.ByName('github.com/mintfog/sniffy/internal/desktop.Bridge.GetSessions', 1, 2000)

// 订阅引擎事件总线转发的实时事件
Events.On('flow_started', (e) => /* e.data: HTTPSessionDTO */ {})
Events.On('flow_updated', (e) => {})
Events.On('ws_message',   (e) => /* e.data: WSSessionDTO */ {})
```

实时同步在 `src/workbench/data/useBackendSync.ts`：挂载时回填会话并订阅事件，成功即置 `isConnected=true`；未连接（如浏览器预览）时回退演示数据。

窗口控制（仅 Windows 自绘标题栏）用 `@wailsio/runtime` 的 `Window.*` / `Application.Quit`，拖拽用 CSS 变量 `--wails-draggable`。

## 🏗️ 目录结构

```
web/src/
├── lib/            # bridge.ts(后端桥接) + platform.ts(平台检测)
├── workbench/      # 工作台 UI（根组件 Workbench.tsx）
│   ├── shell/      # TitleBar / ProxyBar / Toolbar / StatusBar / IconRail
│   ├── views/      # 流量表 / 详情面板 / 规则 / 断点 / 插件 / 证书 / 设置
│   ├── data/       # useTraffic（行模型）/ useBackendSync（实时同步）/ demo
│   ├── ui/         # 菜单 / 基础控件 / primitives
│   ├── lib/        # 格式化 / 类型 / 剪贴板
│   └── theme/      # 主题 token + useTheme
├── store/          # zustand 全局 store（会话/录制/连接状态）
├── types/          # 与 internal/service/view.go 的 DTO 对齐的前端类型
├── pages/          # NotFound
├── App.tsx
└── main.tsx
```

## 📝 可用脚本

- `dev` 开发服务器（浏览器预览，演示数据）
- `build` 生产构建（纯 vite）
- `build:typecheck` tsc 类型检查 + 构建
- `lint` / `lint:fix` ESLint
- `format` Prettier
- `type-check` 仅类型检查

## 🐛 故障排除

- **开发服务器无法启动**：检查 Node ≥ 16；清 `node_modules` 重装；确认端口 3000 未被占用。
- **预览里只有演示数据**：`npm run dev` 是纯浏览器预览，没有 Wails 后端 → 这是预期行为；真实流量请运行桌面二进制。
- **桌面端表格为空**：确认代理端口（默认 :8080）已被客户端使用、录制开关为开（默认开）。

## 📄 许可证

随主仓库采用 Apache License 2.0，详见根目录 [LICENSE](../LICENSE)。
