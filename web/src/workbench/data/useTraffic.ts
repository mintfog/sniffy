import { useCallback, useMemo } from 'react'
import { useAppStore, useSessions, useWebSocketSessions } from '@/store'
import { Bridge } from '@/lib/bridge'
import { toRowFromHttp, toRowFromWs } from '../lib/format'
import type { TrafficRow } from '../lib/types'

/**
 * 流量数据源（工作台专用）：把 store 里的真实会话（WS 推送 / 轮询填充）
 * 统一成 TrafficRow（oldest-first，新请求追加到列表底部）。
 */
export function useTraffic() {
  const httpSessions = useSessions()
  const wsSessions = useWebSocketSessions()

  const rows = useMemo<TrafficRow[]>(() => {
    if (httpSessions.length === 0 && wsSessions.length === 0) return []
    const total = httpSessions.length
    const http = httpSessions.map((s, i) => toRowFromHttp(s, total - i))
    const ws = wsSessions.map((s, i) => toRowFromWs(s, wsSessions.length - i))
    // store 是 newest-first；合并后按时间正序稳定排序（最新的在底部）
    return [...http, ...ws].sort((a, b) => a.startedAt - b.startedAt)
  }, [httpSessions, wsSessions])

  /** 按 id 删除若干行（按 kind 分发到 store） */
  const removeRows = useCallback(
    (ids: ReadonlySet<string>) => {
      if (ids.size === 0) return
      const { removeSession, removeWebSocketSession } = useAppStore.getState()
      for (const row of rows) {
        if (!ids.has(row.id)) continue
        if (row.kind === 'ws') {
          removeWebSocketSession(row.id)
        } else {
          removeSession(row.id)
          // 同步删除后端会话(否则下次回填会重新出现)；WS 暂无后端删除接口,仅本地移除。
          Bridge.deleteSession(row.id).catch(() => {})
        }
      }
    },
    [rows],
  )

  return { rows, removeRows }
}
