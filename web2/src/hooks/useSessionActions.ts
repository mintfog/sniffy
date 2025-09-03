import { useState } from 'react'
import { useAppStore } from '@/store'
import { HttpSession, WebSocketSession } from '@/types'
import { generateCurlCommand, exportSessionData } from '@/utils/sessionUtils'

type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

export function useSessionActions() {
  const { selectedSessionId, setSelectedSession, removeSession } = useAppStore()
  const [actionFeedback, setActionFeedback] = useState<string | null>(null)

  // 复制到剪贴板
  const copyToClipboard = async (text: string, successMessage: string = '已复制到剪贴板') => {
    try {
      await navigator.clipboard.writeText(text)
      setActionFeedback(successMessage)
      setTimeout(() => setActionFeedback(null), 2000)
    } catch (error) {
      console.error('复制失败:', error)
      setActionFeedback('复制失败')
      setTimeout(() => setActionFeedback(null), 2000)
    }
  }

  // 处理会话操作
  const handleSessionAction = async (action: string, session: UnifiedSession) => {
    switch (action) {
      case 'copy-url':
        const url = session.sessionType === 'http' 
          ? (session as HttpSession & { sessionType: 'http' }).request.url
          : (session as WebSocketSession & { sessionType: 'websocket' }).url
        await copyToClipboard(url, 'URL已复制到剪贴板')
        break
        
      case 'copy-curl':
        if (session.sessionType === 'http') {
          const curl = generateCurlCommand(session as HttpSession & { sessionType: 'http' })
          await copyToClipboard(curl, 'cURL命令已复制到剪贴板')
        }
        break
        
      case 'export':
        exportSessionData(session)
        setActionFeedback('会话已导出')
        setTimeout(() => setActionFeedback(null), 2000)
        break
        
      case 'delete':
        if (session.sessionType === 'http') {
          removeSession(session.id)
          if (selectedSessionId === session.id) {
            setSelectedSession(undefined)
          }
        }
        break
        
      case 'repeat':
        console.log('重新发送请求:', session.id)
        break
        
      case 'share':
        console.log('分享会话:', session.id)
        break
    }
  }

  return {
    actionFeedback,
    handleSessionAction,
    copyToClipboard
  }
}
