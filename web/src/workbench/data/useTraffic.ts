import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useAppStore, useSessions, useWebSocketSessions } from '@/store'
import { toRowFromHttp, toRowFromWs } from '../lib/format'
import type { TrafficRow } from '../lib/types'
import { makeDemoRowFactory, makeDemoRows } from './demo'

const MAX_DEMO_ROWS = 3000

/**
 * 流量数据源（工作台专用）：
 *  - 若 store 已有真实会话（WS 推送 / 轮询填充），优先使用真实数据；
 *  - 否则使用 demo 数据，并支持“实时”追加，便于无后端时演示与压测。
 * 行模型统一为 TrafficRow（oldest-first，新请求追加到列表底部）。
 */
export function useTraffic() {
  const httpSessions = useSessions()
  const wsSessions = useWebSocketSessions()

  const realRows = useMemo<TrafficRow[]>(() => {
    if (httpSessions.length === 0 && wsSessions.length === 0) return []
    const total = httpSessions.length
    const http = httpSessions.map((s, i) => toRowFromHttp(s, total - i))
    const ws = wsSessions.map((s, i) => toRowFromWs(s, wsSessions.length - i))
    // store 是 newest-first；合并后按时间正序稳定排序（最新的在底部）
    return [...http, ...ws].sort((a, b) => a.startedAt - b.startedAt)
  }, [httpSessions, wsSessions])

  // 一旦出现过真实数据就不再回退 demo：把真实流量逐行删空/清空后应展示空列表，
  // 而不是凭空冒出演示行（demo 仅用于尚未接入后端时的演示）
  const seenRealRef = useRef(false)
  if (realRows.length > 0) seenRealRef.current = true
  const isDemo = realRows.length === 0 && !seenRealRef.current

  // ── demo 状态 ──
  const [demoRows, setDemoRows] = useState<TrafficRow[]>(() => makeDemoRows(60))
  // 默认「捕获中」：端口监听 + 持续记录新流量（演示态下表现为实时追加）
  const [live, setLive] = useState(true)
  const factoryRef = useRef(makeDemoRowFactory(60))
  const timerRef = useRef<ReturnType<typeof setInterval>>()

  useEffect(() => {
    if (!live || !isDemo) {
      if (timerRef.current) clearInterval(timerRef.current)
      return
    }
    timerRef.current = setInterval(() => {
      setDemoRows((prev) => {
        const burst = 1 + Math.floor(Math.random() * 2)
        const fresh: TrafficRow[] = []
        for (let i = 0; i < burst; i++) fresh.push(factoryRef.current())
        // oldest-first 尾插，超过上限从头部裁掉最旧的
        const next = [...prev, ...fresh]
        return next.length > MAX_DEMO_ROWS ? next.slice(next.length - MAX_DEMO_ROWS) : next
      })
    }, 700)
    return () => {
      if (timerRef.current) clearInterval(timerRef.current)
    }
  }, [live, isDemo])

  const clearDemo = useCallback(() => {
    setDemoRows([])
    factoryRef.current = makeDemoRowFactory(0)
  }, [])

  const seedDemo = useCallback(() => {
    setDemoRows(makeDemoRows(60))
    factoryRef.current = makeDemoRowFactory(60)
  }, [])

  /** 按 id 删除若干行（demo 直接过滤；真实数据按 kind 分发到 store） */
  const removeRows = useCallback(
    (ids: ReadonlySet<string>) => {
      if (ids.size === 0) return
      if (isDemo) {
        setDemoRows((prev) => prev.filter((r) => !ids.has(r.id)))
        return
      }
      const { removeSession, removeWebSocketSession } = useAppStore.getState()
      for (const row of realRows) {
        if (!ids.has(row.id)) continue
        if (row.kind === 'ws') removeWebSocketSession(row.id)
        else removeSession(row.id)
      }
    },
    [isDemo, realRows],
  )

  return {
    rows: isDemo ? demoRows : realRows,
    isDemo,
    live,
    setLive,
    clearDemo,
    seedDemo,
    removeRows,
  }
}
