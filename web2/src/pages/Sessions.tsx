import { Check } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { useAppStore } from '@/store'
import { SessionList, SessionDetail } from '@/components/sessions'
import { useResizePanel } from '@/hooks/useResizePanel'
import { useSessionActions } from '@/hooks/useSessionActions'
import { useSmartRefresh } from '@/hooks'
import clsx from 'clsx'

export function Sessions() {
  const { sessions, webSocketSessions, selectedSessionId, setSelectedSession, setSessions } = useAppStore()

  const { detailWidth, isResizing, handleMouseDown } = useResizePanel(60)
  const { actionFeedback } = useSessionActions()

  // 获取会话列表 - 智能刷新策略
  const { isLoading } = useSmartRefresh({
    queryKey: ['sessions'],
    queryFn: async () => {
      const response = await sniffyApi.getSessions({})
      // 更新store中的会话数据
      if (response.data) {
        setSessions(response.data)
      }
      return response
    },
    enabled: true,
    interval: 3000,
    maxRetries: 3,
    staleTime: 1000
  })

  // 点击外部关闭下拉菜单的逻辑现在由 SessionList 内部处理，移除了全局监听器

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