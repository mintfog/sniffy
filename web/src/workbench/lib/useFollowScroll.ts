import { type RefObject, useCallback, useEffect, useRef, useState } from 'react'

interface FollowScrollOptions {
  /** 列表条目数；变化即视为有新数据到达。 */
  count: number
  follow: boolean
  focusedId?: string
  /** 距底部多少像素以内算「贴底」。 */
  bottomSlack: number
  /** 为 true 时暂停自动滚动（如框选拖拽中）。 */
  pausedRef?: RefObject<boolean>
}

/**
 * 虚拟列表的「跟随最新」：显式开启时立刻跳到底部；之后仅当本就贴底、且未聚焦某行查看详情时，
 * 新数据才自动滚到底部——上滚查看历史或聚焦某行即暂停，回到底部（或取消聚焦）即恢复。
 */
export function useFollowScroll(
  scrollRef: RefObject<HTMLElement>,
  { count, follow, focusedId, bottomSlack, pausedRef }: FollowScrollOptions,
) {
  const [scrollTop, setScrollTop] = useState(0)
  const [atBottom, setAtBottom] = useState(true)
  const atBottomRef = useRef(true)

  /** 把元素当前滚动位置同步给虚拟化计算与贴底判定；程序化修改 scrollTop 后也要调用。 */
  const syncScroll = useCallback(
    (el: HTMLElement) => {
      setScrollTop(el.scrollTop)
      const nearBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - bottomSlack
      atBottomRef.current = nearBottom
      setAtBottom(nearBottom)
    },
    [bottomSlack],
  )

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    syncScroll(el)
  }, [scrollRef, syncScroll])

  useEffect(() => {
    if (follow) scrollToBottom()
  }, [follow, scrollToBottom])

  useEffect(() => {
    if (follow && atBottomRef.current && !focusedId && !pausedRef?.current) scrollToBottom()
  }, [count, follow, focusedId, pausedRef, scrollToBottom])

  return { scrollTop, atBottom, syncScroll, scrollToBottom }
}
