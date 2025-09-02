import { NavLink } from 'react-router-dom'
import { 
  Home,
  List,
  Settings,
  ChevronLeft,
  ChevronRight,
  Activity,
  Filter
} from 'lucide-react'
import { useAppStore } from '@/store'
import clsx from 'clsx'

const navigationItems = [
  { name: '仪表板', href: '/', icon: Home },
  { name: '网络会话', href: '/sessions', icon: List },

  { name: '请求拦截器', href: '/interceptors', icon: Filter },

  { name: '设置', href: '/settings', icon: Settings },
]

export function Sidebar() {
  const { ui, setUIState, isRecording, isConnected } = useAppStore()
  const { sidebarCollapsed } = ui

  const toggleSidebar = () => {
    setUIState({ sidebarCollapsed: !sidebarCollapsed })
  }

  return (
    <div className={clsx(
      'fixed left-0 top-0 h-full bg-white border-r border-gray-200 z-40 transition-all duration-200',
      sidebarCollapsed ? 'w-16' : 'w-64'
    )}>
      {/* Logo 区域 */}
      <div className="flex items-center justify-between p-4 border-b border-gray-200">
        {!sidebarCollapsed && (
          <div className="flex items-center">
            <Activity className="h-8 w-8 text-primary-600" />
            <span className="ml-2 text-xl font-bold text-gray-900">Sniffy</span>
          </div>
        )}
        <button
          onClick={toggleSidebar}
          className="p-1 rounded-md hover:bg-gray-100 transition-colors"
        >
          {sidebarCollapsed ? (
            <ChevronRight className="h-5 w-5 text-gray-500" />
          ) : (
            <ChevronLeft className="h-5 w-5 text-gray-500" />
          )}
        </button>
      </div>

      {/* 状态指示器 */}
      <div className="p-4 border-b border-gray-200">
        <div className="flex items-center space-x-2">
          <div className={clsx(
            'w-3 h-3 rounded-full',
            isConnected ? 'bg-green-500' : 'bg-red-500'
          )} />
          {!sidebarCollapsed && (
            <span className="text-sm text-gray-600">
              {isConnected ? '已连接' : '未连接'}
            </span>
          )}
        </div>
        
        {!sidebarCollapsed && (
          <div className="mt-2 flex items-center space-x-2">
            <div className={clsx(
              'w-3 h-3 rounded-full',
              isRecording ? 'bg-red-500 animate-pulse' : 'bg-gray-400'
            )} />
            <span className="text-sm text-gray-600">
              {isRecording ? '录制中' : '已停止'}
            </span>
          </div>
        )}
      </div>

      {/* 导航菜单 */}
      <nav className="flex-1 p-4">
        <ul className="space-y-2">
          {navigationItems.map((item) => {
            const Icon = item.icon
            return (
              <li key={item.name}>
                <NavLink
                  to={item.href}
                  className={({ isActive }) =>
                    clsx(
                      'flex items-center px-3 py-2 rounded-md text-sm font-medium transition-colors',
                      isActive
                        ? 'bg-primary-100 text-primary-700'
                        : 'text-gray-700 hover:bg-gray-100 hover:text-gray-900'
                    )
                  }
                >
                  <Icon className="h-5 w-5 flex-shrink-0" />
                  {!sidebarCollapsed && (
                    <span className="ml-3">{item.name}</span>
                  )}
                </NavLink>
              </li>
            )
          })}
        </ul>
      </nav>

      {/* 底部信息 */}
      {!sidebarCollapsed && (
        <div className="p-4 border-t border-gray-200">
          <div className="text-xs text-gray-500">
            版本 1.0.0
          </div>
        </div>
      )}
    </div>
  )
}
