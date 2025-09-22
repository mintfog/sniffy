import { useState, useMemo } from 'react'
import { HttpSession, WebSocketSession } from '@/types'

type SessionType = 'all' | 'http' | 'websocket'
type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

export function useSessionFilters(
  sessions: HttpSession[], 
  webSocketSessions: WebSocketSession[]
) {
  const [sessionType, setSessionType] = useState<SessionType>('all')
  const [selectedProcesses, setSelectedProcesses] = useState<string[]>([])
  const [showProcessFilter, setShowProcessFilter] = useState(false)

  // 合并HTTP和WebSocket会话
  const unifiedSessions: UnifiedSession[] = useMemo(() => [
    ...sessions.map(s => ({ ...s, sessionType: 'http' as const })),
    ...webSocketSessions.map(s => ({ ...s, sessionType: 'websocket' as const }))
  ], [sessions, webSocketSessions])

  // 获取所有唯一的进程名称
  const availableProcesses = useMemo(() => {
    const processSet = new Set<string>()
    unifiedSessions.forEach(session => {
      if (session.processName) {
        processSet.add(session.processName)
      }
    })
    return Array.from(processSet).sort()
  }, [unifiedSessions])

  // 根据类型和进程过滤并排序会话
  const filteredAndSortedSessions = useMemo(() => {
    // 过滤
    const filtered = unifiedSessions.filter(session => {
      // 按类型过滤
      if (sessionType !== 'all' && session.sessionType !== sessionType) {
        return false
      }
      
      // 按进程过滤
      if (selectedProcesses.length > 0) {
        if (!session.processName || !selectedProcesses.includes(session.processName)) {
          return false
        }
      }
      
      return true
    })

    // 排序
    return [...filtered].sort((a, b) => {
      const aTime = a.sessionType === 'http' ? a.request.timestamp : a.startTime
      const bTime = b.sessionType === 'http' ? b.request.timestamp : b.startTime
      return new Date(bTime).getTime() - new Date(aTime).getTime()
    })
  }, [unifiedSessions, sessionType, selectedProcesses])

  // 处理进程过滤器选择
  const toggleProcess = (processName: string) => {
    setSelectedProcesses(prev => 
      prev.includes(processName) 
        ? prev.filter(p => p !== processName)
        : [...prev, processName]
    )
  }

  // 清除进程过滤器
  const clearProcessFilter = () => {
    setSelectedProcesses([])
  }

  return {
    sessionType,
    setSessionType,
    selectedProcesses,
    setSelectedProcesses,
    showProcessFilter,
    setShowProcessFilter,
    availableProcesses,
    toggleProcess,
    clearProcessFilter,
    unifiedSessions,
    filteredAndSortedSessions
  }
}
