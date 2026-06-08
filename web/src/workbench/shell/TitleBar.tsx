import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useState } from 'react'
import { Copy, Minus, Moon, Radar, Square, Sun, X } from 'lucide-react'
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

type WailsRuntime = {
  WindowMinimise(): void
  WindowToggleMaximise(): void
  WindowIsMaximised(): Promise<boolean>
  Quit(): void
}
function wailsRuntime(): WailsRuntime | undefined {
  const rt = (window as unknown as { runtime?: Partial<WailsRuntime> }).runtime
  return rt && typeof rt.WindowMinimise === 'function' ? (rt as WailsRuntime) : undefined
}

/** Windows 自绘窗口按钮（最小化/最大化/关闭）。mac 用系统红绿灯、Linux 用原生装饰，均不渲染此组件。 */
function WindowControls() {
  const rt = wailsRuntime()
  const [maximised, setMaximised] = useState(false)

  // 窗口尺寸变化（拖拽贴边 / Win+方向键等）时同步最大化态，保证图标正确
  useEffect(() => {
    if (!rt) return
    let alive = true
    const sync = () => void rt.WindowIsMaximised().then((m) => alive && setMaximised(m))
    sync()
    window.addEventListener('resize', sync)
    return () => {
      alive = false
      window.removeEventListener('resize', sync)
    }
  }, [rt])

  if (!rt) return null
  const btn = 'flex h-9 w-12 items-center justify-center text-fg-muted transition-colors'
  return (
    <div className="ml-1 flex items-stretch self-stretch" style={NO_DRAG} data-no-drag>
      <button className={cx(btn, 'hover:bg-inset hover:text-fg')} onClick={() => rt.WindowMinimise()} aria-label="最小化">
        <Minus className="h-4 w-4" />
      </button>
      <button
        className={cx(btn, 'hover:bg-inset hover:text-fg')}
        onClick={() => rt.WindowToggleMaximise()}
        aria-label={maximised ? '向下还原' : '最大化'}
      >
        {maximised ? <Copy className="h-3.5 w-3.5 -scale-x-100" /> : <Square className="h-3.5 w-3.5" />}
      </button>
      <button className={cx(btn, 'hover:bg-rose-500 hover:text-white')} onClick={() => rt.Quit()} aria-label="关闭">
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}

/**
 * 应用标题栏，按平台分流（与后端 ApplyPlatformChrome 配套）：
 *   - windows：无边框 → 整条可拖拽 + 双击最大化 + 右侧自绘窗口按钮。
 *   - mac：原生集成标题栏 → 整条可拖拽，左侧留白避开系统红绿灯；窗口按钮/双击由系统接管。
 *   - linux / web：原生装饰或浏览器 → 仅作普通菜单栏，不拖拽、不自绘。
 */
export function TitleBar({ menus, isDark, onToggleTheme, connected, isDemo }: TitleBarProps) {
  const platform = detectPlatform()
  const selfDrawn = platform === 'windows' // 自绘窗口按钮 + 整条拖拽 + 双击最大化
  const macInset = platform === 'mac' // 原生红绿灯，左侧留白
  const draggable = selfDrawn || macInset // frameless / inset 两种才需要 --wails-draggable

  // 双击标题栏空白处最大化/还原（仅 Windows；mac 双击由系统按偏好处理，避免双触发）
  const onDoubleClick = useCallback(
    (e: ReactMouseEvent) => {
      if (!selfDrawn) return
      if ((e.target as HTMLElement).closest('[data-no-drag]')) return
      wailsRuntime()?.WindowToggleMaximise()
    },
    [selfDrawn],
  )

  return (
    <header
      className={cx(
        'flex h-9 items-center gap-1 border-b border-line bg-surface select-none',
        // mac：给系统红绿灯（左上角约 70px）让位
        macInset ? 'pl-[78px]' : 'pl-2',
      )}
      style={draggable ? DRAG : undefined}
      onDoubleClick={onDoubleClick}
    >
      {/* 品牌标记（拖拽区） */}
      <div className="flex items-center gap-1.5 pr-1.5 pl-1">
        <span className="flex h-5 w-5 items-center justify-center rounded-wb-sm bg-accent text-accent-fg">
          <Radar className="h-3.5 w-3.5" />
        </span>
        <span className="text-[13px] font-semibold tracking-tight text-fg">Sniffy</span>
      </div>

      <span className="mx-1 h-4 w-px bg-line" />

      {/* 菜单栏（可点击，标 no-drag） */}
      <div className="flex items-center" style={NO_DRAG} data-no-drag>
        <MenuBar menus={menus} />
      </div>

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
          {isDemo ? '演示数据' : connected ? '已连接' : '未连接'}
        </div>

        <Tooltip label={isDark ? '切换到亮色' : '切换到深色'} placement="bottom">
          <IconButton onClick={onToggleTheme} aria-label="切换主题">
            {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </IconButton>
        </Tooltip>
      </div>

      {/* 窗口按钮：仅 Windows 自绘，紧贴右上角 */}
      {selfDrawn && <WindowControls />}
    </header>
  )
}
