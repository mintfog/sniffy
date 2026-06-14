import { type CSSProperties, type MouseEvent as ReactMouseEvent, memo, useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Minus, Moon, Radar, Square, Sun, X } from 'lucide-react'
import { Application, Window } from '@wailsio/runtime'
import { detectPlatform } from '@/lib/platform'
import { MenuBar, type TopMenu } from '../ui/Menu'
import { cx, IconButton, Tooltip } from '../ui/primitives'

interface TitleBarProps {
  menus: TopMenu[]
  isDark: boolean
  onToggleTheme: () => void
  connected: boolean
  isDemo: boolean
}

// Wails 无边框/集成窗口的拖拽标记：drag 区域可拖动窗口；交互控件标 no-drag 才能点击。
const DRAG = { ['--wails-draggable' as string]: 'drag' } as CSSProperties
const NO_DRAG = { ['--wails-draggable' as string]: 'no-drag' } as CSSProperties

/** Windows 自绘窗口按钮（最小化/最大化/关闭）。mac 用系统红绿灯、Linux 用原生装饰，均不渲染此组件。 */
function WindowControls() {
  const { t } = useTranslation()
  const [maximised, setMaximised] = useState(false)

  // 窗口尺寸变化（拖拽贴边 / Win+方向键等）时同步最大化态，保证图标正确
  useEffect(() => {
    let alive = true
    const sync = () => void Window.IsMaximised().then((m) => alive && setMaximised(m)).catch(() => {})
    sync()
    window.addEventListener('resize', sync)
    return () => {
      alive = false
      window.removeEventListener('resize', sync)
    }
  }, [])

  const btn = 'flex h-9 w-12 items-center justify-center text-fg-muted transition-colors'
  return (
    <div className="ml-1 flex items-stretch self-stretch" style={NO_DRAG} data-no-drag>
      <button className={cx(btn, 'hover:bg-inset hover:text-fg')} onClick={() => void Window.Minimise()} aria-label={t('titleBar.window.minimize')}>
        <Minus className="h-4 w-4" />
      </button>
      <button
        className={cx(btn, 'hover:bg-inset hover:text-fg')}
        onClick={() => void Window.ToggleMaximise()}
        aria-label={maximised ? t('titleBar.window.restore') : t('titleBar.window.maximize')}
      >
        {maximised ? <Copy className="h-3.5 w-3.5 -scale-x-100" /> : <Square className="h-3.5 w-3.5" />}
      </button>
      <button className={cx(btn, 'hover:bg-rose-500 hover:text-white')} onClick={() => void Application.Quit()} aria-label={t('titleBar.window.close')}>
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}

/**
 * 应用标题栏，按平台分流（与后端 ApplyPlatformChrome 配套）：
 *   - windows：无边框 → 整条可拖拽 + 双击最大化 + 右侧自绘窗口按钮。
 *   - mac：原生标题栏透明化（HiddenInset）→ 本条就是标题栏底色（永远跟随应用主题），
 *     系统红绿灯悬浮在左上，左侧留出其位置；菜单不在条内（在顶部系统菜单栏，见 nativeMenu.ts），
 *     整条可拖拽 + 双击缩放。
 *   - linux：原生装饰 → 仅作普通菜单栏，不拖拽、不自绘。
 */
// memo：流量持续刷新时 Workbench 频繁重渲染，但只要 props（尤其 menus 引用）不变，
// 标题栏与下拉菜单就不重渲染——保证开着的菜单不被数据刷新打断。
export const TitleBar = memo(function TitleBar({ menus, isDark, onToggleTheme, connected, isDemo }: TitleBarProps) {
  const { t } = useTranslation()
  const platform = detectPlatform()
  const selfDrawn = platform === 'windows' // 自绘窗口按钮 + 整条拖拽 + 双击最大化
  const isMac = platform === 'mac' // 透明标题栏下的拖拽条：留红绿灯位、无菜单、无自绘窗口按钮

  // 双击标题栏空白处：Windows 最大化/还原，mac 缩放（Linux 交给原生装饰，不处理）
  const onDoubleClick = useCallback(
    (e: ReactMouseEvent) => {
      if (!selfDrawn && !isMac) return
      if ((e.target as HTMLElement).closest('[data-no-drag]')) return
      void Window.ToggleMaximise()
    },
    [selfDrawn, isMac],
  )

  return (
    <header
      className={cx(
        'flex items-center gap-1 border-b border-line bg-surface select-none',
        // mac：高度托住 HiddenInset 红绿灯（垂直居中约 22px 处），左侧 80px 是其悬浮位
        isMac ? 'h-11 pl-20' : 'h-9 pl-2',
      )}
      style={selfDrawn || isMac ? DRAG : undefined}
      onDoubleClick={onDoubleClick}
    >
      {/* 品牌标记（拖拽区） */}
      <div className="flex items-center gap-1.5 pr-1.5 pl-1">
        <span className="flex h-5 w-5 items-center justify-center rounded-wb-sm bg-accent text-accent-fg">
          <Radar className="h-3.5 w-3.5" />
        </span>
        <span className="text-[13px] font-semibold tracking-tight text-fg">Sniffy</span>
      </div>

      {/* 菜单栏（可点击，标 no-drag）。mac 的菜单在系统菜单栏，不在条内。 */}
      {!isMac && (
        <>
          <span className="mx-1 h-4 w-px bg-line" />
          <div className="flex items-center" style={NO_DRAG} data-no-drag>
            <MenuBar menus={menus} />
          </div>
        </>
      )}

      {/* 中间空白：拖拽区 */}
      <div className="flex-1 self-stretch" />

      {/* 右侧：连接状态 + 主题切换（可点击，标 no-drag）。
          Windows 下窗口按钮要贴右上角，故右内边距仅在非自绘平台保留。 */}
      <div className={cx('flex items-center gap-1.5', selfDrawn ? '' : 'pr-2')} style={NO_DRAG} data-no-drag>
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
          {isDemo ? t('titleBar.status.demo') : connected ? t('titleBar.status.connected') : t('titleBar.status.disconnected')}
        </div>

        <Tooltip label={isDark ? t('titleBar.theme.toLight') : t('titleBar.theme.toDark')} placement="bottom">
          <IconButton onClick={onToggleTheme} aria-label={t('titleBar.theme.toggle')}>
            {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </IconButton>
        </Tooltip>
      </div>

      {/* 窗口按钮：仅 Windows 自绘，紧贴右上角 */}
      {selfDrawn && <WindowControls />}
    </header>
  )
})
