import { useEffect } from 'react'
import { Routes, Route } from 'react-router-dom'
import { NotFound } from '@/pages/NotFound'
import Workbench from '@/workbench/Workbench'
import StandaloneWindow, { isStandaloneKind } from '@/workbench/StandaloneWindow'
import { usePrefsBridge } from '@/workbench/prefs'
import { isDesktop } from '@/lib/platform'

function App() {
  // 全局偏好：应用 CSS 变量（主题/强调色/密度/字号）并与其它窗口同步。
  usePrefsBridge()

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
    <Routes>
      {/* 默认进入工作台 */}
      <Route path="/" element={<Workbench />} />
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default App
