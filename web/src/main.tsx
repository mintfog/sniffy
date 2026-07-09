import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App.tsx'
import { installFrontendLog } from './lib/frontendLog'
import { applyPrefsToDocument } from './workbench/prefs'
// 自托管字体（打包进 dist，运行时零外链——此前用 Google Fonts 外链曾致 mac 窗口卡约 30 秒）。
// 仅引入权重轴(wght)版；包内各 @font-face 带 unicode-range，运行时只会按需加载 latin 子集。
import '@fontsource-variable/inter/wght.css'
import '@fontsource-variable/jetbrains-mono/wght.css'
import './i18n'
import './index.css'

// 尽早安装前端报错捕获，转发到后端日志文件。
installFrontendLog()

// 在首帧前同步应用主题/强调色/密度/字号，避免闪烁。
applyPrefsToDocument()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>
)
