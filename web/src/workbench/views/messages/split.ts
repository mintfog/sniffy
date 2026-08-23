import { type PointerEvent as ReactPointerEvent, useCallback } from 'react'
import { useElementSize } from '../../lib/useElementSize'

/**
 * 消息列表(上)/详情(下) 的垂直分栏换算与拖拽。
 *
 * 占比由调用方保管——主窗口持久化到 usePrefs（所有窗口共享），构造器窗口用本窗口的
 * state，两者不能串在一起，否则拖构造器的分隔条会静默改掉主窗口布局。
 */
export function useVerticalSplit(topFrac: number, onTopFracChange: (frac: number) => void) {
  const { ref, height } = useElementSize<HTMLDivElement>()
  // 容器还没被量到（首帧）时按 700px 估一个高度，免得上半区先塌成一条再弹开。
  const topH = height > 280 ? Math.min(height - 140, Math.max(120, Math.round(topFrac * height))) : Math.round(topFrac * 700)

  const startResize = useCallback(
    (e: ReactPointerEvent) => {
      e.preventDefault()
      const rect = ref.current?.getBoundingClientRect()
      if (!rect || rect.height <= 0) return
      const onMove = (ev: PointerEvent) => {
        const px = Math.min(rect.height - 140, Math.max(120, ev.clientY - rect.top))
        onTopFracChange(px / rect.height)
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }
      document.body.style.cursor = 'row-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [ref, onTopFracChange],
  )

  return { containerRef: ref, topH, startResize }
}
