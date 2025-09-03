import React from 'react'
import { X } from 'lucide-react'
import { useAppStore } from '@/store'
import { HttpSession, WebSocketSession } from '@/types'
import { ExpandableCell } from '@/components/ui'
import { HttpSessionDetail } from './HttpSessionDetail'
import { WebSocketSessionDetail } from './WebSocketSessionDetail'
import clsx from 'clsx'

interface SessionDetailProps {
  sessionId: string
}

export function SessionDetail({ sessionId }: SessionDetailProps) {
  const { sessions, webSocketSessions, setSelectedSession } = useAppStore()
  
  const httpSession = sessions.find(s => s.id === sessionId)
  const wsSession = webSocketSessions.find(s => s.id === sessionId)
  
  const session = httpSession || wsSession
  const sessionType = httpSession ? 'http' : 'websocket'

  const handleClose = () => {
    setSelectedSession(undefined)
  }

  if (!session) {
    return (
      <div className="flex items-center justify-center h-full text-gray-500">
        <p>会话不存在</p>
      </div>
    )
  }

  const detailHeader = (
    <div className="border-b border-gray-200 px-6 py-4 flex-shrink-0">
      <div className="flex items-center justify-between">
        <div className="flex items-center space-x-3">
          <h3 className="text-lg font-semibold text-gray-900">
            {sessionType === 'websocket' ? 'WebSocket 详情' : 'HTTP 详情'}
          </h3>
          <span className={clsx(
            'px-2 py-1 text-xs font-medium rounded',
            sessionType === 'websocket' ? 'bg-purple-100 text-purple-700' : 'bg-blue-100 text-blue-700'
          )}>
            {sessionType.toUpperCase()}
          </span>
        </div>
        <div className="flex items-center space-x-2">
          <button
            onClick={handleClose}
            className="p-1 hover:bg-gray-100 rounded transition-colors"
            title="关闭详情"
          >
            <X className="h-5 w-5 text-gray-500" />
          </button>
        </div>
      </div>
      
      <div className="mt-3">
        <ExpandableCell 
          content={sessionType === 'http' ? 
            (session as HttpSession).request.url : 
            (session as WebSocketSession).url
          } 
          maxLength={80} 
          showCopy={true}
          className="text-sm text-gray-600"
        />
      </div>
    </div>
  )

  return (
    <div className="flex flex-col h-full">
      {detailHeader}
      <div className="flex-1 overflow-hidden">
        {sessionType === 'websocket' ? (
          <WebSocketSessionDetail session={wsSession as WebSocketSession} />
        ) : (
          <HttpSessionDetail session={httpSession as HttpSession} />
        )}
      </div>
    </div>
  )
}
