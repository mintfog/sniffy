import { type PointerEvent as ReactPointerEvent, useEffect, useState } from 'react'
import { cx } from './primitives'

/**
 * ResizeHandle 是面板间的拖拽分隔条，col 为左右分栏的竖线，row 为上下分栏的横线。
 *
 * 版面只占 1px，命中区向两侧外扩并压在相邻面板上，故需 z-20。悬停高亮带延迟，
 * 指针扫过时不闪；拖动中指针常离开命中区，高亮改由 dragging 维持到松开。
 * grip 把手与高亮条等粗且位于其下，亮起时被整条覆盖。
 */
export function ResizeHandle({
  orientation,
  onPointerDown,
  grip,
  className,
}: {
  orientation: 'col' | 'row'
  onPointerDown: (e: ReactPointerEvent) => void
  grip?: boolean
  className?: string
}) {
  const [dragging, setDragging] = useState(false)

  useEffect(() => {
    if (!dragging) return
    const end = () => setDragging(false)
    window.addEventListener('pointerup', end)
    window.addEventListener('pointercancel', end)
    return () => {
      window.removeEventListener('pointerup', end)
      window.removeEventListener('pointercancel', end)
    }
  }, [dragging])

  const col = orientation === 'col'
  return (
    <div
      role="separator"
      aria-orientation={col ? 'vertical' : 'horizontal'}
      className={cx('group/rh relative z-20 shrink-0 bg-line', col ? 'w-px' : 'h-px', className)}
    >
      <div
        onPointerDown={(e) => {
          if (e.button !== 0) return
          onPointerDown(e)
          setDragging(true)
        }}
        className={cx('absolute', col ? '-inset-x-[3px] inset-y-0 cursor-col-resize' : '-inset-y-[3px] inset-x-0 cursor-row-resize')}
      />
      {grip && (
        <div
          className={cx(
            'pointer-events-none absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-line-strong',
            col ? 'h-8 w-[3px]' : 'h-[3px] w-8',
          )}
        />
      )}
      <div
        className={cx(
          'pointer-events-none absolute bg-row-indicator transition-opacity duration-150',
          col ? 'inset-y-0 left-1/2 w-[3px] -translate-x-1/2' : 'inset-x-0 top-1/2 h-[3px] -translate-y-1/2',
          dragging ? 'opacity-100' : 'opacity-0 group-hover/rh:opacity-100 group-hover/rh:delay-150',
        )}
      />
    </div>
  )
}
