import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useAppStore, useSessions, useWebSocketSessions } from '@/store'
import { Bridge } from '@/lib/bridge'
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
  // 是否已连上 Wails 后端（useBackendSync 回填成功后置 true）。
  const isConnected = useAppStore((s) => s.isConnected)

  const realRows = useMemo<TrafficRow[]>(() => {
    if (httpSessions.length === 0 && wsSessions.length === 0) return []
    const total = httpSessions.length
    const http = httpSessions.map((s, i) => toRowFromHttp(s, total - i))
    const ws = wsSessions.map((s, i) => toRowFromWs(s, wsSessions.length - i))
    // store 是 newest-first；合并后按时间正序稳定排序（最新的在底部）
    return [...http, ...ws].sort((a, b) => a.startedAt - b.startedAt)
  }, [httpSessions, wsSessions])

  // 演示数据兜底：**仅在未连接后端时**(纯浏览器预览 / UI 开发)才自动展示 demo。
  // 一旦连上 Wails 后端(桌面运行时),即使尚无抓到流量也展示空表,而不是凭空冒出演示行
  // forceDemo：用户从「工具 → 重新填充演示数据」显式召出;连后端后也能手动展示。
  const [forceDemo, setForceDemo] = useState(false)
  const seenRealRef = useRef(false)
  if (realRows.length > 0) seenRealRef.current = true
  const isDemo = forceDemo || (!isConnected && realRows.length === 0 && !seenRealRef.current)

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
    setForceDemo(false) // 清空演示 = 退出演示态，回到真实数据/空表
  }, [])

  const seedDemo = useCallback(() => {
    setDemoRows(makeDemoRows(60))
    factoryRef.current = makeDemoRowFactory(60)
    setForceDemo(true) // 显式召出演示数据，连后端后也强制展示
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
        if (row.kind === 'ws') {
          removeWebSocketSession(row.id)
        } else {
          removeSession(row.id)
          // 同步删除后端会话(否则下次回填会重新出现)；WS 暂无后端删除接口,仅本地移除。
          Bridge.deleteSession(row.id).catch(() => {})
        }
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
