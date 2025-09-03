import { useState, useEffect } from 'react'

export function useResizePanel(initialWidth: number = 60) {
  const [detailWidth, setDetailWidth] = useState(initialWidth)
  const [isResizing, setIsResizing] = useState(false)

  // 处理拖拽调整宽度
  const handleMouseDown = (e: React.MouseEvent) => {
    e.preventDefault()
    setIsResizing(true)
  }

  const handleMouseMove = (e: MouseEvent) => {
    if (!isResizing) return
    
    const container = document.querySelector('.sessions-container') as HTMLElement
    if (!container) return
    
    const containerRect = container.getBoundingClientRect()
    const mouseX = e.clientX - containerRect.left
    const newDetailWidth = Math.max(30, Math.min(80, (containerRect.width - mouseX) / containerRect.width * 100))
    
    setDetailWidth(newDetailWidth)
  }

  const handleMouseUp = () => {
    setIsResizing(false)
  }

  // 添加全局鼠标事件监听
  useEffect(() => {
    if (isResizing) {
      document.addEventListener('mousemove', handleMouseMove)
      document.addEventListener('mouseup', handleMouseUp)
      document.body.style.userSelect = 'none'
      document.body.style.cursor = 'col-resize'
      
      return () => {
        document.removeEventListener('mousemove', handleMouseMove)
        document.removeEventListener('mouseup', handleMouseUp)
        document.body.style.userSelect = ''
        document.body.style.cursor = ''
      }
    }
  }, [isResizing])

  return {
    detailWidth,
    isResizing,
    handleMouseDown
  }
}
