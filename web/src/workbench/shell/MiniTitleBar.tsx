import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useState } from 'react'
import { Copy, Minus, Radar, Square, X } from 'lucide-react'
import { Window } from '@wailsio/runtime'
import { detectPlatform } from '@/lib/platform'
import { cx } from '../ui/primitives'

const DRAG = { ['--wails-draggable' as string]: 'drag' } as CSSProperties
const NO_DRAG = { ['--wails-draggable' as string]: 'no-drag' } as CSSProperties

function WindowControls() {
  const [maximised, setMaximised] = useState(false)
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
  const btn = 'flex h-8 w-11 items-center justify-center text-fg-muted transition-colors'
  return (
    <div className="ml-1 flex items-stretch self-stretch" style={NO_DRAG} data-no-drag>
      <button className={cx(btn, 'hover:bg-inset hover:text-fg')} onClick={() => void Window.Minimise()} aria-label="最小化">
        <Minus className="h-4 w-4" />
      </button>
      <button
        className={cx(btn, 'hover:bg-inset hover:text-fg')}
        onClick={() => void Window.ToggleMaximise()}
        aria-label={maximised ? '向下还原' : '最大化'}
      >
        {maximised ? <Copy className="h-3.5 w-3.5 -scale-x-100" /> : <Square className="h-3.5 w-3.5" />}
      </button>
      {/* 子窗口关闭 = 仅关闭本窗口（不退出整个应用） */}
      <button className={cx(btn, 'hover:bg-rose-500 hover:text-white')} onClick={() => void Window.Close()} aria-label="关闭">
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}

/**
 * 独立子窗口的精简标题栏：图标 + 标题 +（Windows）自绘窗口按钮。
 * **mac 不渲染此组件**（StandaloneWindow 已按平台门控）——那里用系统原生标题栏。
 */
export function MiniTitleBar({ title }: { title: string }) {
  const selfDrawn = detectPlatform() === 'windows'

  const onDoubleClick = useCallback(
    (e: ReactMouseEvent) => {
      if (!selfDrawn) return
      if ((e.target as HTMLElement).closest('[data-no-drag]')) return
      void Window.ToggleMaximise()
    },
    [selfDrawn],
  )

  // ESC 关闭子窗口（工具/关于这类「轻」窗口的常见习惯）。
  // 仅当焦点不在任何交互控件上时才关闭——否则按 ESC 取消下拉/输入会误关整窗。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      const el = document.activeElement as HTMLElement | null
      const interactive =
        !!el &&
        (el.tagName === 'INPUT' ||
          el.tagName === 'TEXTAREA' ||
          el.tagName === 'SELECT' ||
          el.tagName === 'BUTTON' ||
          el.isContentEditable ||
          el.getAttribute('role') === 'switch')
      if (!interactive) void Window.Close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  return (
    <header
      className={cx('flex h-8 shrink-0 items-center border-b border-line bg-surface pl-2.5 select-none')}
      style={selfDrawn ? DRAG : undefined}
      onDoubleClick={onDoubleClick}
    >
      <span className="flex items-center gap-1.5">
        <span className="flex h-4 w-4 items-center justify-center rounded-wb-sm bg-accent text-accent-fg">
          <Radar className="h-3 w-3" />
        </span>
        <span className="text-[12.5px] font-medium text-fg">{title}</span>
      </span>
      <div className="flex-1 self-stretch" />
      {selfDrawn && <WindowControls />}
    </header>
  )
}
