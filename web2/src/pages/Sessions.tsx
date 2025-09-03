import { useState, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Check } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { useAppStore } from '@/store'
import { SessionList, SessionDetail } from '@/components/sessions'
import { useResizePanel } from '@/hooks/useResizePanel'
import { useSessionActions } from '@/hooks/useSessionActions'
import clsx from 'clsx'

export function Sessions() {
  const { sessions, webSocketSessions, selectedSessionId, setSelectedSession } = useAppStore()
  const [page] = useState(1)
  const [pageSize] = useState(50)

  const { detailWidth, isResizing, handleMouseDown } = useResizePanel(60)
  const { actionFeedback } = useSessionActions()

  // 获取会话列表
  const { isLoading } = useQuery({
    queryKey: ['sessions', page, pageSize],
    queryFn: () => sniffyApi.getSessions({ page, pageSize }),
    refetchInterval: 2000,
  })

  // 点击外部关闭下拉菜单
  useEffect(() => {
    const handleClickOutside = () => {
      // 这个逻辑现在由SessionList内部处理
    }

    document.addEventListener('click', handleClickOutside)
    return () => document.removeEventListener('click', handleClickOutside)
  }, [])

  return (
    <div className="relative">
      {/* 操作反馈提示 */}
      {actionFeedback && (
        <div className="fixed top-4 right-4 z-50 bg-green-500 text-white px-4 py-2 rounded-md shadow-lg transition-all duration-300">
          <div className="flex items-center">
            <Check className="h-4 w-4 mr-2" />
            {actionFeedback}
          </div>
        </div>
      )}
      
      <div className="sessions-container flex h-[calc(100vh-8rem)] rounded-lg overflow-hidden border border-gray-200">
        {/* 会话列表 */}
        <div 
          className={clsx(
            "transition-all duration-300",
            selectedSessionId ? "" : "w-full"
          )}
          style={selectedSessionId ? { width: `${100 - detailWidth}%` } : {}}
        >
          <SessionList
            sessions={sessions}
            webSocketSessions={webSocketSessions}
            selectedSessionId={selectedSessionId}
            onSessionSelect={setSelectedSession}
            isLoading={isLoading}
          />
        </div>

        {/* 可拖拽的分隔条 */}
        {selectedSessionId && (
          <div
            className={clsx(
              "bg-gray-300 hover:bg-gray-400 cursor-col-resize flex-shrink-0 transition-colors",
              isResizing ? "bg-gray-400" : ""
            )}
            style={{ width: '4px' }}
            onMouseDown={handleMouseDown}
          />
        )}

        {/* 会话详情 */}
        {selectedSessionId && (
          <div 
            className="h-full bg-white flex flex-col animate-in slide-in-from-right duration-300"
            style={{ width: `${detailWidth}%` }}
          >
            <SessionDetail sessionId={selectedSessionId} />
          </div>
        )}
      </div>
    </div>
  )
}