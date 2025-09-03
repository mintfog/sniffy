import { useState, useMemo } from 'react'
import { HttpSession, WebSocketSession } from '@/types'

type SessionType = 'all' | 'http' | 'websocket'
type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

export function useSessionFilters(
  sessions: HttpSession[], 
  webSocketSessions: WebSocketSession[]
) {
  const [sessionType, setSessionType] = useState<SessionType>('all')

  // 合并HTTP和WebSocket会话
  const unifiedSessions: UnifiedSession[] = useMemo(() => [
    ...sessions.map(s => ({ ...s, sessionType: 'http' as const })),
    ...webSocketSessions.map(s => ({ ...s, sessionType: 'websocket' as const }))
  ], [sessions, webSocketSessions])

  // 根据类型过滤并排序会话
  const filteredAndSortedSessions = useMemo(() => {
    // 过滤
    const filtered = unifiedSessions.filter(session => {
      if (sessionType === 'all') return true
      return session.sessionType === sessionType
    })

    // 排序
    return [...filtered].sort((a, b) => {
      const aTime = a.sessionType === 'http' ? a.request.timestamp : a.startTime
      const bTime = b.sessionType === 'http' ? b.request.timestamp : b.startTime
      return new Date(bTime).getTime() - new Date(aTime).getTime()
    })
  }, [unifiedSessions, sessionType])

  return {
    sessionType,
    setSessionType,
    unifiedSessions,
    filteredAndSortedSessions
  }
}
