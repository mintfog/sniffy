import React, { useState, useMemo, useCallback } from 'react'
import { Globe, MessageSquare, Filter, MoreHorizontal, Clock, Zap } from 'lucide-react'
import { HttpSession, WebSocketSession } from '@/types'
import { ExpandableCell, VirtualList } from '@/components/ui'
import { SessionActionMenu } from './SessionActionMenu'
import { useSessionFilters } from '@/hooks/useSessionFilters'
import { formatDuration, formatSize, getStatusColor, getMethodColor } from '@/utils/sessionUtils'
import clsx from 'clsx'
import dayjs from 'dayjs'

type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

interface SessionListProps {
  sessions: HttpSession[]
  webSocketSessions: WebSocketSession[]
  selectedSessionId?: string
  onSessionSelect: (sessionId: string) => void
  isLoading: boolean
}

export function SessionList({ 
  sessions, 
  webSocketSessions, 
  selectedSessionId, 
  onSessionSelect, 
  isLoading 
}: SessionListProps) {
  const [openDropdownId, setOpenDropdownId] = useState<string | null>(null)
  
  const {
    sessionType,
    setSessionType,
    filteredAndSortedSessions
  } = useSessionFilters(sessions, webSocketSessions)

  return (
    <div className="bg-white border-r border-gray-200 flex flex-col h-full">
      {/* 列表头部 */}
      <div className="border-b border-gray-200 px-6 py-4 flex-shrink-0">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold text-gray-900">网络会话</h2>
          <div className="flex items-center space-x-2">
            <span className="text-sm text-gray-500">
              共 {filteredAndSortedSessions.length} 个会话
            </span>
            <button className="p-2 hover:bg-gray-100 rounded-md">
              <Filter className="h-4 w-4 text-gray-400" />
            </button>
          </div>
        </div>
        
        {/* 会话类型过滤 */}
        <div className="flex space-x-2">
          {[
            { value: 'all', label: '全部', icon: Globe },
            { value: 'http', label: 'HTTP', icon: Globe },
            { value: 'websocket', label: 'WebSocket', icon: MessageSquare },
          ].map((option) => {
            const Icon = option.icon
            return (
              <button
                key={option.value}
                onClick={() => setSessionType(option.value as any)}
                className={clsx(
                  'flex items-center px-3 py-2 text-sm rounded-md transition-colors',
                  sessionType === option.value
                    ? 'bg-primary-100 text-primary-700'
                    : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                )}
              >
                <Icon className="h-4 w-4 mr-2" />
                {option.label}
              </button>
            )
          })}
        </div>
      </div>

      {/* 会话列表 */}
      <div className="flex-1 overflow-hidden">
        {isLoading ? (
          <div className="flex items-center justify-center h-32">
            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
          </div>
        ) : filteredAndSortedSessions.length === 0 ? (
          <div className="flex items-center justify-center h-32 text-gray-500">
            暂无会话数据
          </div>
        ) : (
          <VirtualizedSessionList
            sessions={filteredAndSortedSessions}
            selectedSessionId={selectedSessionId}
            onSessionSelect={onSessionSelect}
            openDropdownId={openDropdownId}
            setOpenDropdownId={setOpenDropdownId}
          />
        )}
      </div>
    </div>
  )
}

// 虚拟化会话列表组件
interface VirtualizedSessionListProps {
  sessions: UnifiedSession[]
  selectedSessionId?: string
  onSessionSelect: (sessionId: string) => void
  openDropdownId: string | null
  setOpenDropdownId: (id: string | null) => void
}

function VirtualizedSessionList({ 
  sessions, 
  selectedSessionId, 
  onSessionSelect, 
  openDropdownId, 
  setOpenDropdownId 
}: VirtualizedSessionListProps) {
  const ITEM_HEIGHT = 120 // 每个会话项的固定高度
  const CONTAINER_HEIGHT = 600 // 容器的最大高度

  // 创建渲染项目的回调函数
  const renderSessionItem = useCallback((session: UnifiedSession, index: number) => (
    <div className="border-b border-gray-100 last:border-b-0">
      <SessionListItem
        key={session.id}
        session={session}
        isSelected={selectedSessionId === session.id}
        onSelect={() => onSessionSelect(session.id)}
        openDropdownId={openDropdownId}
        setOpenDropdownId={setOpenDropdownId}
      />
    </div>
  ), [selectedSessionId, onSessionSelect, openDropdownId, setOpenDropdownId])

  return (
    <VirtualList
      items={sessions}
      itemHeight={ITEM_HEIGHT}
      containerHeight={Math.min(CONTAINER_HEIGHT, sessions.length * ITEM_HEIGHT)}
      renderItem={renderSessionItem}
      className="h-full"
      overscan={3}
    />
  )
}

interface SessionListItemProps {
  session: UnifiedSession
  isSelected: boolean
  onSelect: () => void
  openDropdownId: string | null
  setOpenDropdownId: (id: string | null) => void
}

const SessionListItem = React.memo(function SessionListItem({ 
  session, 
  isSelected, 
  onSelect, 
  openDropdownId, 
  setOpenDropdownId 
}: SessionListItemProps) {
  // 使用useCallback优化点击处理函数
  const handleMenuToggle = useCallback((e: React.MouseEvent) => {
    e.stopPropagation()
    setOpenDropdownId(openDropdownId === session.id ? null : session.id)
  }, [openDropdownId, session.id, setOpenDropdownId])

  const handleMenuClose = useCallback(() => {
    setOpenDropdownId(null)
  }, [setOpenDropdownId])

  // 缓存计算值
  const sessionUrl = useMemo(() => {
    return session.sessionType === 'http' 
      ? (session as HttpSession & { sessionType: 'http' }).request.url
      : (session as WebSocketSession & { sessionType: 'websocket' }).url
  }, [session])

  const sessionTime = useMemo(() => {
    return session.sessionType === 'http'
      ? dayjs((session as HttpSession & { sessionType: 'http' }).request.timestamp).format('HH:mm:ss.SSS')
      : dayjs((session as WebSocketSession & { sessionType: 'websocket' }).startTime).format('HH:mm:ss.SSS')
  }, [session])

  return (
    <div
      onClick={onSelect}
      className={clsx(
        'px-6 py-4 hover:bg-gray-50 cursor-pointer transition-colors',
        isSelected && 'bg-primary-50 border-r-2 border-primary-500'
      )}
    >
      <div className="flex items-center justify-between">
        <div className="flex items-center space-x-3">
          {/* 会话类型标识 */}
          {session.sessionType === 'http' ? (
            <>
              {/* HTTP 方法 */}
              <span className={clsx(
                'px-2 py-1 text-xs font-medium rounded',
                getMethodColor((session as HttpSession & { sessionType: 'http' }).request.method)
              )}>
                {(session as HttpSession & { sessionType: 'http' }).request.method}
              </span>

              {/* 状态码 */}
              <span className={clsx(
                'px-2 py-1 text-xs font-medium rounded',
                getStatusColor(session)
              )}>
                {(session as HttpSession & { sessionType: 'http' }).response?.status || (session as HttpSession & { sessionType: 'http' }).status}
              </span>
            </>
          ) : (
            <>
              {/* WebSocket 类型 */}
              <span className="px-2 py-1 text-xs font-medium rounded bg-purple-100 text-purple-700">
                WebSocket
              </span>

              {/* WebSocket 状态 */}
              <span className={clsx(
                'px-2 py-1 text-xs font-medium rounded',
                getStatusColor(session)
              )}>
                {(session as WebSocketSession & { sessionType: 'websocket' }).status === 'connecting' ? '连接中' :
                 (session as WebSocketSession & { sessionType: 'websocket' }).status === 'connected' ? '已连接' :
                 (session as WebSocketSession & { sessionType: 'websocket' }).status === 'disconnected' ? '已断开' : '错误'}
              </span>
            </>
          )}
        </div>

        <div className="flex items-center space-x-4 text-sm text-gray-500">
          {session.sessionType === 'http' ? (
            <>
              {/* HTTP 响应时间 */}
              <div className="flex items-center">
                <Clock className="h-4 w-4 mr-1" />
                {formatDuration((session as HttpSession & { sessionType: 'http' }).duration)}
              </div>

              {/* HTTP 响应大小 */}
              {(session as HttpSession & { sessionType: 'http' }).response && (
                <div className="flex items-center">
                  <Zap className="h-4 w-4 mr-1" />
                  {formatSize((session as HttpSession & { sessionType: 'http' }).response!.size)}
                </div>
              )}
            </>
          ) : (
            <>
              {/* WebSocket 消息数量 */}
              <div className="flex items-center">
                <MessageSquare className="h-4 w-4 mr-1" />
                {(session as WebSocketSession & { sessionType: 'websocket' }).messageCount} 条消息
              </div>

              {/* WebSocket 总大小 */}
              <div className="flex items-center">
                <Zap className="h-4 w-4 mr-1" />
                {formatSize((session as WebSocketSession & { sessionType: 'websocket' }).totalSize)}
              </div>
            </>
          )}

          <div className="relative">
            <button 
              className="p-1 hover:bg-gray-200 rounded transition-colors"
              onClick={handleMenuToggle}
            >
              <MoreHorizontal className="h-4 w-4" />
            </button>
            
            {/* 操作菜单 */}
            {openDropdownId === session.id && (
              <SessionActionMenu 
                session={session} 
                onClose={handleMenuClose} 
              />
            )}
          </div>
        </div>
      </div>

      <div className="mt-2">
        <div className="flex items-start text-sm">
          <Globe className="h-4 w-4 mr-2 text-gray-400 mt-0.5 flex-shrink-0" />
          <div className="flex-1 min-w-0">
            <ExpandableCell 
              content={sessionUrl} 
              maxLength={80} 
              showCopy={true}
              className="text-gray-900 font-medium"
            />
          </div>
        </div>
        <div className="text-xs text-gray-500 mt-1">
          {sessionTime}
        </div>
      </div>
    </div>
  )
})
