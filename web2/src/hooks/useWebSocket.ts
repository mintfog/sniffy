import { useEffect, useRef, useState, useCallback } from 'react'
import { useAppStore } from '@/store'

interface UseWebSocketOptions {
  onOpen?: (event: Event) => void
  onMessage?: (data: any) => void
  onError?: (event: Event) => void
  onClose?: (event: CloseEvent) => void
  reconnectAttempts?: number
  reconnectInterval?: number
}

export function useWebSocket(options: UseWebSocketOptions = {}) {
  const {
    onOpen,
    onMessage,
    onError,
    onClose,
    reconnectAttempts = 5,
    reconnectInterval = 3000,
  } = options

  const [isConnected, setIsConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectCountRef = useRef(0)
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout>>()
  
  const { setConnected, addSession, addWebSocketSession, updateSession } = useAppStore()

  const connect = useCallback(() => {
    try {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const wsUrl = `${protocol}//${window.location.host}/api/ws`
      
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws

      ws.onopen = (event) => {
        console.log('WebSocket connected')
        setIsConnected(true)
        setConnected(true)
        setError(null)
        reconnectCountRef.current = 0
        onOpen?.(event)
      }

      ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data)
          
          // 处理不同类型的消息
          switch (data.type) {
            case 'http_request':
              addSession(data.payload)
              break
              
            case 'http_response':
              updateSession(data.payload.requestId, {
                response: data.payload,
                status: 'completed',
                duration: data.payload.responseTime,
              })
              break
              
            case 'websocket_session':
              addWebSocketSession(data.payload)
              break
              
            case 'error':
              console.error('Server error:', data.payload)
              setError(data.payload.message)
              break
              
            default:
              console.log('Unknown message type:', data.type)
          }
          
          onMessage?.(data)
        } catch (error) {
          console.error('Failed to parse WebSocket message:', error)
        }
      }

      ws.onerror = (event) => {
        console.error('WebSocket error:', event)
        setError('WebSocket connection error')
        onError?.(event)
      }

      ws.onclose = (event) => {
        console.log('WebSocket disconnected')
        setIsConnected(false)
        setConnected(false)
        wsRef.current = null
        onClose?.(event)

        // 自动重连
        if (reconnectCountRef.current < reconnectAttempts) {
          reconnectCountRef.current++
          console.log(`Attempting to reconnect... (${reconnectCountRef.current}/${reconnectAttempts})`)
          
          reconnectTimeoutRef.current = setTimeout(() => {
            connect()
          }, reconnectInterval)
        } else {
          setError('Failed to connect after multiple attempts')
        }
      }
    } catch (error) {
      console.error('Failed to create WebSocket connection:', error)
      setError('Failed to create WebSocket connection')
    }
  }, [onOpen, onMessage, onError, onClose, reconnectAttempts, reconnectInterval, setConnected, addSession, addWebSocketSession, updateSession])

  const disconnect = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current)
    }
    
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }
    
    setIsConnected(false)
    setConnected(false)
  }, [setConnected])

  const sendMessage = useCallback((message: any) => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(message))
      return true
    }
    return false
  }, [])

  // 组件挂载时自动连接
  useEffect(() => {
    connect()

    // 组件卸载时断开连接
    return () => {
      disconnect()
    }
  }, [connect, disconnect])

  // 监听页面可见性变化
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.hidden) {
        // 页面隐藏时可以选择断开连接以节省资源
        // disconnect()
      } else {
        // 页面可见时重新连接
        if (!isConnected && reconnectCountRef.current < reconnectAttempts) {
          connect()
        }
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [isConnected, connect, reconnectAttempts])

  return {
    isConnected,
    error,
    connect,
    disconnect,
    sendMessage,
  }
}
