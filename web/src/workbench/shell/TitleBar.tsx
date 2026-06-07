import { Moon, Radar, Sun } from 'lucide-react'
import { MenuBar, type TopMenu } from '../ui/Menu'
import { cx, IconButton, Tooltip } from '../ui/primitives'

interface TitleBarProps {
  menus: TopMenu[]
  isDark: boolean
  onToggleTheme: () => void
  connected: boolean
  isDemo: boolean
}

export function TitleBar({ menus, isDark, onToggleTheme, connected, isDemo }: TitleBarProps) {
  return (
    <header className="flex h-9 items-center gap-1 border-b border-line bg-surface px-2 select-none">
      {/* 品牌标记 */}
      <div className="flex items-center gap-1.5 pr-1.5 pl-1">
        <span className="flex h-5 w-5 items-center justify-center rounded-wb-sm bg-accent text-accent-fg">
          <Radar className="h-3.5 w-3.5" />
        </span>
        <span className="text-[13px] font-semibold tracking-tight text-fg">Sniffy</span>
      </div>

      <span className="mx-1 h-4 w-px bg-line" />

      {/* 原生风格菜单栏 */}
      <MenuBar menus={menus} />

      <div className="flex-1" />

      {/* 右侧：连接状态 + 主题切换 */}
      <div className="flex items-center gap-1.5">
        <div
          className={cx(
            'flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[10px] font-medium',
            isDemo
              ? 'border-warn/30 bg-warn/10 text-warn'
              : connected
                ? 'border-ok/30 bg-ok/10 text-ok'
                : 'border-line bg-inset text-fg-faint',
          )}
        >
          <span className={cx('h-1.5 w-1.5 rounded-full', isDemo ? 'bg-warn' : connected ? 'bg-ok' : 'bg-fg-faint')} />
          {isDemo ? '演示数据' : connected ? '已连接' : '未连接'}
        </div>

        <Tooltip label={isDark ? '切换到亮色' : '切换到深色'} placement="bottom">
          <IconButton onClick={onToggleTheme} aria-label="切换主题">
            {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </IconButton>
        </Tooltip>
      </div>
    </header>
  )
}
