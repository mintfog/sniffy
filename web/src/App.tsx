import { Suspense, lazy, useEffect } from 'react'
import { Routes, Route } from 'react-router-dom'
import { NotFound } from '@/pages/NotFound'
import StandaloneWindow, { isStandaloneKind } from '@/workbench/StandaloneWindow'
import { usePrefsBridge } from '@/workbench/prefs'
import { useLangBridge } from '@/i18n/bridge'
import { isDesktop } from '@/lib/platform'

// 工作台是主窗口专属、体量最大的视图。懒加载后，独立子窗口（设置/插件/规则等）
// 不再连它一起解析执行，明显缩短子窗口的冷启动。
const Workbench = lazy(() => import('@/workbench/Workbench'))

// 主题底色在首帧前已由 applyPrefsToDocument 应用到 <html>/<body>，
// 懒加载切块间隙用同底色填满即可，避免白屏闪烁。
function WindowBootFallback() {
  return <div className="h-screen w-screen bg-base" />
}

function App() {
  // 全局偏好：应用 CSS 变量（主题/强调色/密度/字号）并与其它窗口同步。
  usePrefsBridge()
  // 语言：同步 <html lang>/文档标题，并跨窗口同步语言切换。
  useLangBridge()

  // 桌面端屏蔽 WebView 默认右键菜单。
  // 浏览器/headless 形态不拦截，方便开发调试。
  useEffect(() => {
    if (!isDesktop()) return
    const onContextMenu = (e: MouseEvent) => {
      const t = e.target as HTMLElement | null
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
      e.preventDefault()
    }
    window.addEventListener('contextmenu', onContextMenu)
    return () => window.removeEventListener('contextmenu', onContextMenu)
  }, [])

  // 独立子窗口：?w=settings|tools|about → 渲染精简外壳承载单一页面。
  const w = new URLSearchParams(window.location.search).get('w')
  if (isStandaloneKind(w)) {
    return <StandaloneWindow kind={w} />
  }

  return (
    <Suspense fallback={<WindowBootFallback />}>
      <Routes>
        {/* 默认进入工作台 */}
        <Route path="/" element={<Workbench />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </Suspense>
  )
}

export default App
