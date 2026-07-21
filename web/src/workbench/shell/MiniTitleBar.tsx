import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Minus, Square, X } from 'lucide-react'
import { Window } from '@wailsio/runtime'
import { detectPlatform } from '@/lib/platform'
import { cx, SniffyMark } from '../ui/primitives'

const DRAG = { ['--wails-draggable' as string]: 'drag' } as CSSProperties
const NO_DRAG = { ['--wails-draggable' as string]: 'no-drag' } as CSSProperties

function WindowControls() {
  const { t } = useTranslation()
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
      <button className={cx(btn, 'hover:bg-inset hover:text-fg')} onClick={() => void Window.Minimise()} aria-label={t('miniTitleBar.window.minimize')}>
        <Minus className="h-4 w-4" />
      </button>
      <button
        className={cx(btn, 'hover:bg-inset hover:text-fg')}
        onClick={() => void Window.ToggleMaximise()}
        aria-label={maximised ? t('miniTitleBar.window.restore') : t('miniTitleBar.window.maximize')}
      >
        {maximised ? <Copy className="h-3.5 w-3.5 -scale-x-100" /> : <Square className="h-3.5 w-3.5" />}
      </button>
      {/* 子窗口关闭 = 仅关闭本窗口（不退出整个应用） */}
      <button className={cx(btn, 'hover:bg-[#E81123] hover:text-white')} onClick={() => void Window.Close()} aria-label={t('miniTitleBar.window.close')}>
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}

/**
 * 独立子窗口的精简标题栏：图标 + 标题 +（Windows）自绘窗口按钮。
 * mac 用透明原生标题栏（HiddenInset，系统标题已隐藏）：本条以 mac 模式渲染为
 * 托住红绿灯的主题色拖拽条并展示标题，保证标题栏颜色与窗口内容一致。
 */
export function MiniTitleBar({ title }: { title: string }) {
  const platform = detectPlatform()
  const selfDrawn = platform === 'windows'
  const isMac = platform === 'mac'

  const onDoubleClick = useCallback(
    (e: ReactMouseEvent) => {
      if (!selfDrawn && !isMac) return
      if ((e.target as HTMLElement).closest('[data-no-drag]')) return
      void Window.ToggleMaximise()
    },
    [selfDrawn, isMac],
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
      className={cx(
        'flex shrink-0 items-center border-b border-line bg-surface select-none',
        // mac：高度托住 HiddenInset 红绿灯，左侧 80px 是其悬浮位
        isMac ? 'h-11 pl-20' : 'h-8 pl-2.5',
      )}
      style={selfDrawn || isMac ? DRAG : undefined}
      onDoubleClick={onDoubleClick}
    >
      <span className="flex items-center gap-1.5">
        <SniffyMark className="h-4 w-4 text-accent" />
        <span className="text-[12.5px] font-medium text-fg">{title}</span>
      </span>
      <div className="flex-1 self-stretch" />
      {selfDrawn && <WindowControls />}
    </header>
  )
}
