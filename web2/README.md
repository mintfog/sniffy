# Sniffy WebUI

一个现代化的网络流量分析工具 Web 界面，类似于 Fiddler Everywhere 的功能体验。

## ✨ 特性

- 🎯 **实时监控**: 实时捕获和显示 HTTP/HTTPS 请求
- 🔌 **WebSocket 支持**: 监控 WebSocket 连接和消息流
- 📊 **数据分析**: 丰富的统计图表和数据可视化
- 🎨 **现代界面**: 基于 React 18 + TypeScript 的现代化 UI
- ⚡ **高性能**: 使用 Vite 构建，支持热重载和快速开发
- 🔧 **可配置**: 灵活的配置选项和插件系统
- 📱 **响应式**: 适配桌面和移动设备

## 🛠️ 技术栈

- **前端框架**: React 18 + TypeScript
- **构建工具**: Vite
- **样式框架**: Tailwind CSS
- **状态管理**: Zustand
- **数据获取**: React Query + Axios
- **路由**: React Router v6
- **图标**: Lucide React
- **代码规范**: ESLint + Prettier

## 🚀 快速开始

### 环境要求

- Node.js >= 16.0.0
- npm >= 8.0.0 或 pnpm >= 7.0.0

### 安装依赖

```bash
cd web
npm install
# 或者
pnpm install
```

### 环境配置

复制环境变量模板：

```bash
cp .env.example .env.local
```

根据需要修改 `.env.local` 中的配置。

### 启动开发服务器

```bash
npm run dev
# 或者
pnpm dev
```

访问 http://localhost:3000 查看应用。

### 构建生产版本

```bash
npm run build
# 或者
pnpm build
```

### 预览生产构建

```bash
npm run preview
# 或者
pnpm preview
```

## 📝 可用脚本

- `dev`: 启动开发服务器
- `build`: 构建生产版本
- `preview`: 预览生产构建
- `lint`: 运行 ESLint 检查
- `lint:fix`: 自动修复 ESLint 错误
- `format`: 使用 Prettier 格式化代码
- `type-check`: 运行 TypeScript 类型检查

## 🏗️ 项目结构

```
web/
├── public/                 # 静态资源
├── src/
│   ├── components/         # React 组件
│   │   ├── ui/            # 基础 UI 组件
│   │   └── layout/        # 布局组件
│   ├── pages/             # 页面组件
│   ├── hooks/             # 自定义 Hooks
│   ├── store/             # 状态管理
│   ├── services/          # API 服务
│   ├── types/             # TypeScript 类型定义
│   ├── utils/             # 工具函数
│   └── assets/            # 资源文件
├── index.html             # HTML 模板
├── vite.config.ts         # Vite 配置
├── tailwind.config.js     # Tailwind CSS 配置
├── tsconfig.json          # TypeScript 配置
└── package.json           # 项目依赖
```

## 🔧 主要功能

### 仪表板
- 实时统计数据
- 请求趋势图表
- HTTP 状态码分布
- 热门主机排行

### 会话列表
- 实时显示 HTTP 请求/响应
- 请求方法、状态码、URL 预览
- 响应时间和大小统计
- 点击查看详细信息

### 请求详情
- 完整的请求/响应信息
- 头部信息查看
- 请求体和响应体展示
- 时序信息分析

### WebSocket 监控
- 连接状态监控
- 消息流实时显示
- 消息类型分类（文本/二进制）
- 连接统计信息

### 设置页面
- 代理服务器配置
- HTTPS 证书管理
- 插件系统管理
- 过滤器设置

## 🔌 API 集成

WebUI 通过以下 API 端点与 Sniffy 后端通信：

- `GET /api/status` - 获取系统状态
- `GET /api/sessions` - 获取 HTTP 会话列表
- `GET /api/websockets` - 获取 WebSocket 会话
- `GET /api/statistics` - 获取统计数据
- `GET /api/config` - 获取/更新配置
- `WebSocket /api/ws` - 实时数据推送

## 🎨 自定义主题

项目使用 Tailwind CSS，可以通过修改 `tailwind.config.js` 来自定义主题：

```javascript
module.exports = {
  theme: {
    extend: {
      colors: {
        primary: {
          // 自定义主色调
        }
      }
    }
  }
}
```

## 🐛 故障排除

### 开发服务器无法启动
- 检查 Node.js 版本是否符合要求
- 清除 node_modules 并重新安装依赖
- 检查端口 3000 是否被占用

### API 请求失败
- 检查后端服务是否正常运行
- 验证 `.env.local` 中的 API 地址配置
- 检查网络连接和防火墙设置

### WebSocket 连接失败
- 确认后端 WebSocket 服务已启用
- 检查浏览器是否阻止 WebSocket 连接
- 验证 WebSocket URL 配置

## 📄 许可证

本项目采用 MIT 许可证。详见 [LICENSE](../LICENSE) 文件。

## 🤝 贡献指南

欢迎提交 Issue 和 Pull Request！

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交变更 (`git commit -m 'Add some amazing feature'`)
4. 推送分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

## 📞 联系方式

如有问题或建议，请通过以下方式联系：

- 创建 [GitHub Issue](https://github.com/your-repo/sniffy/issues)
- 发送邮件到项目维护者
