import React, { useMemo, useState, useCallback, useEffect, useRef } from 'react'

export interface VirtualListProps<T> {
  items: T[]
  itemHeight: number
  containerHeight: number
  renderItem: (item: T, index: number) => React.ReactNode
  className?: string
  overscan?: number // 额外渲染的项目数量，防止快速滚动时出现空白
  preserveScrollPosition?: boolean // 当新数据插入时是否自动补偿滚动位置以保持当前查看内容的相对位置
}

export function VirtualList<T>({
  items,
  itemHeight,
  containerHeight,
  renderItem,
  className = '',
  overscan = 5,
  preserveScrollPosition = false
}: VirtualListProps<T>) {
  const [scrollTop, setScrollTop] = useState(0)
  const scrollElementRef = useRef<HTMLDivElement>(null)
  const previousItemCountRef = useRef<number>(items.length)
  const lastScrollTopRef = useRef<number>(0)

  // 计算可见项目的范围
  const visibleRange = useMemo(() => {
    const startIndex = Math.floor(scrollTop / itemHeight)
    const endIndex = Math.min(
      startIndex + Math.ceil(containerHeight / itemHeight),
      items.length - 1
    )

    return {
      startIndex: Math.max(0, startIndex - overscan),
      endIndex: Math.min(items.length - 1, endIndex + overscan)
    }
  }, [scrollTop, itemHeight, containerHeight, items.length, overscan])

  // 计算总高度
  const totalHeight = items.length * itemHeight

  // 获取可见项目
  const visibleItems = useMemo(() => {
    return items.slice(visibleRange.startIndex, visibleRange.endIndex + 1)
  }, [items, visibleRange.startIndex, visibleRange.endIndex])

  // 处理滚动事件
  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const newScrollTop = e.currentTarget.scrollTop
    setScrollTop(newScrollTop)
    lastScrollTopRef.current = newScrollTop
  }, [])

  // 当数据更新时，检测是否有新项目插入并补偿滚动位置
  useEffect(() => {
    if (!preserveScrollPosition || !scrollElementRef.current) return

    const currentItemCount = items.length
    const previousItemCount = previousItemCountRef.current
    
    // 如果有新项目插入到列表顶部
    if (currentItemCount > previousItemCount) {
      const newItemsCount = currentItemCount - previousItemCount
      const heightCompensation = newItemsCount * itemHeight
      const currentScrollTop = scrollElementRef.current.scrollTop
      
      // 调整滚动位置以补偿新增的内容高度
      const newScrollTop = currentScrollTop + heightCompensation
      scrollElementRef.current.scrollTop = newScrollTop
      setScrollTop(newScrollTop)
      lastScrollTopRef.current = newScrollTop
    }
    
    // 更新记录的项目数量
    previousItemCountRef.current = currentItemCount
  }, [items.length, itemHeight, preserveScrollPosition])

  // 计算偏移量
  const offsetY = visibleRange.startIndex * itemHeight

  return (
    <div
      ref={scrollElementRef}
      className={`overflow-auto ${className}`}
      style={{ height: containerHeight }}
      onScroll={handleScroll}
    >
      <div style={{ height: totalHeight, position: 'relative' }}>
        <div
          style={{
            transform: `translateY(${offsetY}px)`,
            position: 'absolute',
            top: 0,
            left: 0,
            right: 0
          }}
        >
          {visibleItems.map((item, index) => (
            <div
              key={visibleRange.startIndex + index}
              style={{ height: itemHeight }}
            >
              {renderItem(item, visibleRange.startIndex + index)}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

// 使用 IntersectionObserver 的虚拟滚动Hook
export function useVirtualScrolling<T>(
  items: T[],
  itemHeight: number,
  containerHeight: number,
  overscan: number = 5
) {
  const [scrollTop, setScrollTop] = useState(0)
  const [isScrolling, setIsScrolling] = useState(false)
  const scrollTimeoutRef = useRef<NodeJS.Timeout>()

  const visibleRange = useMemo(() => {
    const startIndex = Math.floor(scrollTop / itemHeight)
    const endIndex = Math.min(
      startIndex + Math.ceil(containerHeight / itemHeight),
      items.length - 1
    )

    return {
      startIndex: Math.max(0, startIndex - overscan),
      endIndex: Math.min(items.length - 1, endIndex + overscan)
    }
  }, [scrollTop, itemHeight, containerHeight, items.length, overscan])

  const handleScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    setScrollTop(e.currentTarget.scrollTop)
    setIsScrolling(true)

    // 清除之前的定时器
    if (scrollTimeoutRef.current) {
      clearTimeout(scrollTimeoutRef.current)
    }

    // 设置滚动结束标志
    scrollTimeoutRef.current = setTimeout(() => {
      setIsScrolling(false)
    }, 150)
  }, [])

  // 清理定时器
  useEffect(() => {
    return () => {
      if (scrollTimeoutRef.current) {
        clearTimeout(scrollTimeoutRef.current)
      }
    }
  }, [])

  return {
    visibleRange,
    handleScroll,
    isScrolling,
    totalHeight: items.length * itemHeight,
    offsetY: visibleRange.startIndex * itemHeight
  }
}
