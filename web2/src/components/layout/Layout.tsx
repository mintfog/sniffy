import { Outlet } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { Header } from './Header'
import { useUIState } from '@/store'

export function Layout() {
  const { sidebarCollapsed } = useUIState()

  return (
    <div className="flex h-screen bg-gray-50">
      {/* 侧边栏 */}
      <Sidebar />
      
      {/* 主内容区域 */}
      <div className={`flex-1 flex flex-col ${sidebarCollapsed ? 'ml-16' : 'ml-64'} transition-all duration-200`}>
        {/* 顶部导航栏 */}
        <Header />
        
        {/* 页面内容 */}
        <main className="flex-1 overflow-hidden">
          <div className="h-full p-6">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
