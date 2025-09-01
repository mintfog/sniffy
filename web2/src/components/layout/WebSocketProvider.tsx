import { useEffect } from 'react'
import { useWebSocket } from '@/hooks/useWebSocket'
import { useAppStore } from '@/store'

/**
 * WebSocket 提供者组件，负责管理实时连接
 */
export function WebSocketProvider({ children }: { children: React.ReactNode }) {
  const { addSession, updateSession, addWebSocketSession, setStatistics } = useAppStore()

  const { isConnected, error } = useWebSocket({
    onMessage: (data) => {
      console.log('Received WebSocket message:', data)
      
      // 处理不同类型的实时数据
      switch (data.type) {
        case 'http_request':
          // 新的 HTTP 请求
          addSession({
            id: data.payload.id,
            request: data.payload,
            status: 'pending'
          })
          break
          
        case 'http_response':
          // HTTP 响应完成
          updateSession(data.payload.requestId, {
            response: data.payload,
            status: 'completed',
            duration: data.payload.responseTime
          })
          break
          
        case 'websocket_session':
          // WebSocket 会话更新
          addWebSocketSession(data.payload)
          break
          
        case 'statistics_update':
          // 统计数据更新
          setStatistics(data.payload)
          break
          
        case 'session_update':
          // 会话状态更新
          updateSession(data.payload.id, data.payload.updates)
          break
          
        case 'error':
          console.error('Server error:', data.payload)
          break
          
        default:
          console.log('Unknown message type:', data.type)
      }
    },
    
    onError: (error) => {
      console.error('WebSocket connection error:', error)
    },
    
    onOpen: () => {
      console.log('WebSocket connection established')
    },
    
    onClose: (event) => {
      console.log('WebSocket connection closed:', event.code, event.reason)
    }
  })

  // 在控制台显示连接状态（仅开发环境）
  useEffect(() => {
    if (process.env.NODE_ENV === 'development') {
      console.log('WebSocket connection status:', isConnected ? 'Connected' : 'Disconnected')
      if (error) {
        console.error('WebSocket error:', error)
      }
    }
  }, [isConnected, error])

  return <>{children}</>
}
