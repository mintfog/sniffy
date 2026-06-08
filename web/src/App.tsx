import { useEffect } from 'react'
import { Routes, Route } from 'react-router-dom'
import { NotFound } from '@/pages/NotFound'
import Workbench from '@/workbench/Workbench'
import { isDesktop } from '@/lib/platform'

function App() {
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

  return (
    <Routes>
      {/* 默认进入工作台 */}
      <Route path="/" element={<Workbench />} />
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default App
