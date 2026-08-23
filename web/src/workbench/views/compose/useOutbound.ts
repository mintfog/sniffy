/**
 * 认领本窗口发出的会话。
 *
 * 构造器不是流量列表：全量订阅会把整条代理热路径搬进这个窗口，所以 flow_started /
 * flow_updated / ws_message / stream_message 四个事件共用一张 id 白名单，只留下自己发出去的。
 *
 * HTTP 事件的 payload 是整个会话 DTO，存最后一份即可。长连接（ws_message /
 * stream_message）是增量推送，必须自己累加——合并规则与流量列表共用 data/delta，
 * 两个界面才不会把同一条流显示成两样。累加起来的时间线攒在这里，关掉页签不回收
 * 就是实打实的泄漏。
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { Bridge } from '@/lib/bridge'
import type { HttpSession, StreamDelta, StreamSession, WebSocketSession, WsDelta } from '@/types'
import {
  acceptsHttpRefetch,
  acceptsRefetch,
  createRefetcher,
  mergeStreamDelta,
  mergeWsDelta,
} from '../../data/delta'
import { wsOf } from './model'
import type { Draft, DraftKind, WebSocketPart } from './model'

/**
 * 仍是 pending 的一次性往返多久对账一次。
 *
 * flow_started / flow_updated 各只发一次，而事件总线对慢订阅者是直接丢弃的
 * （core.EventBus 把「订阅者自行重新拉取对账」写成了契约）。终态那条一旦被丢，
 * 页签就永远停在「发送中」：主按钮与 Ctrl+Enter 一起锁死，只能关掉重开。
 * 长连接靠 messageCount 跳号发现丢帧，一次性往返没有序号可对，只能按 pending 轮询。
 */
const PENDING_POLL_MS = 2000

export interface OutboundSessions {
  http: Record<string, HttpSession>
  ws: Record<string, WebSocketSession>
  stream: Record<string, StreamSession>
  /** 认领一条刚发出的会话，并立刻回填一次：第一帧可能早于发送 Promise 兑现。 */
  track: (id: string, kind: DraftKind) => void
  /** 传入仍被页签引用的 id 集合，回收其余会话。 */
  retain: (live: ReadonlySet<string>) => void
}

/**
 * 出站连接状态的唯一判读：会话 DTO 一到就以它为准。
 * 例外是重连——此时 sentFlowId 还指着上一条已关闭的会话，乐观值必须压过它。
 */
export function connOf(draft: Draft, session?: WebSocketSession): WebSocketPart['conn'] {
  const part = wsOf(draft)
  if (part.conn === 'connecting') return 'connecting'
  return session ? session.status : part.conn
}

function prune<T>(map: Record<string, T>, live: ReadonlySet<string>): Record<string, T> {
  const next: Record<string, T> = {}
  let dropped = false
  for (const [id, value] of Object.entries(map)) {
    if (live.has(id)) next[id] = value
    else dropped = true
  }
  return dropped ? next : map
}

export function useOutboundSessions(): OutboundSessions {
  const [http, setHttp] = useState<Record<string, HttpSession>>({})
  const [ws, setWs] = useState<Record<string, WebSocketSession>>({})
  const [stream, setStream] = useState<Record<string, StreamSession>>({})
  // 记 kind 而不只是 id：对账只对一次性往返有意义，而出站 WebSocket 根本不产生 HTTP flow。
  const tracked = useRef<Map<string, DraftKind>>(new Map())

  // 增量合并要先读到上一版会话才能续上时间线，而事件订阅不能挂在会话状态上（每来一帧
  // 就重订阅）。故以 ref 为准、setState 只负责渲染，两者同一个 commit 点更新，不会漂。
  const live = useRef({
    http: {} as Record<string, HttpSession>,
    ws: {} as Record<string, WebSocketSession>,
    stream: {} as Record<string, StreamSession>,
  })

  const commitHttp = useCallback((next: Record<string, HttpSession>) => {
    live.current.http = next
    setHttp(next)
  }, [])
  const commitWs = useCallback((next: Record<string, WebSocketSession>) => {
    live.current.ws = next
    setWs(next)
  }, [])
  const commitStream = useCallback((next: Record<string, StreamSession>) => {
    live.current.stream = next
    setStream(next)
  }, [])

  /** 事件送来的 HTTP 会话：事件按发布顺序到达，后一条一定更新，直接存。 */
  const keepHttp = useCallback(
    (s?: HttpSession) => {
      if (s?.id && tracked.current.has(s.id)) commitHttp({ ...live.current.http, [s.id]: s })
    },
    [commitHttp],
  )

  // 以下三个是「整条重拉」的落地口（发送后的首次回填、丢帧重拉、pending 轮询共用），
  // 一律带陈旧判据：拉取是异步的，结果落地时手上可能已经有更新的一份了（见 acceptsRefetch）。
  const reconcileHttp = useCallback(
    (s?: HttpSession) => {
      if (!s?.id || !tracked.current.has(s.id)) return
      if (!acceptsHttpRefetch(live.current.http[s.id], s)) return
      commitHttp({ ...live.current.http, [s.id]: s })
    },
    [commitHttp],
  )
  const keepWs = useCallback(
    (s?: WebSocketSession) => {
      if (!s?.id || !tracked.current.has(s.id)) return
      if (!acceptsRefetch(live.current.ws[s.id], s)) return
      commitWs({ ...live.current.ws, [s.id]: s })
    },
    [commitWs],
  )
  const keepStream = useCallback(
    (s?: StreamSession) => {
      if (!s?.id || !tracked.current.has(s.id)) return
      if (!acceptsRefetch(live.current.stream[s.id], s)) return
      commitStream({ ...live.current.stream, [s.id]: s })
    },
    [commitStream],
  )

  // 丢帧后整条重拉。ref 而非 useMemo：闸门里的在途集合必须跨渲染存活。
  const refetch = useRef({
    http: createRefetcher(Bridge.getSession),
    ws: createRefetcher(Bridge.getWSSession),
    stream: createRefetcher(Bridge.getStreamSession),
  })

  const applyWsDelta = useCallback(
    (d?: WsDelta) => {
      const id = d?.session?.id
      if (!id || !tracked.current.has(id)) return
      const { session, gap } = mergeWsDelta(live.current.ws[id], d)
      commitWs({ ...live.current.ws, [id]: session })
      if (gap) refetch.current.ws(id, keepWs)
    },
    [commitWs, keepWs],
  )
  const applyStreamDelta = useCallback(
    (d?: StreamDelta) => {
      const id = d?.session?.id
      if (!id || !tracked.current.has(id)) return
      const { session, gap } = mergeStreamDelta(live.current.stream[id], d)
      commitStream({ ...live.current.stream, [id]: session })
      if (gap) refetch.current.stream(id, keepStream)
    },
    [commitStream, keepStream],
  )

  useEffect(() => {
    const offs: Array<() => void> = []
    const on = <T>(name: string, keep: (s: T) => void) => {
      offs.push(Events.On(name, (e: { data?: unknown }) => keep(e?.data as T)))
    }
    try {
      on<HttpSession>('flow_started', keepHttp)
      on<HttpSession>('flow_updated', keepHttp)
      on<WsDelta>('ws_message', applyWsDelta)
      on<StreamDelta>('stream_message', applyStreamDelta)
    } catch {
      /* 非 Wails 环境 */
    }
    return () => {
      for (const off of offs) {
        try {
          off()
        } catch {
          /* 卸载期取消订阅失败无从补救 */
        }
      }
    }
  }, [applyStreamDelta, applyWsDelta, keepHttp])

  // 兜底对账：仍是 pending 的一次性往返定期重拉一次，理由见 PENDING_POLL_MS。
  useEffect(() => {
    const timer = window.setInterval(() => {
      for (const [id, kind] of tracked.current) {
        // 出站 WebSocket 不产生 HTTP flow（见 internal/app/compose_ws.go）。
        if (kind === 'ws') continue
        // SSE 的 flow 在流关闭前一直停在 pending（runComposeSSE 刻意为之），而流会话一旦
        // 建起来界面就不再等这条 flow 了 —— 再轮询就是给整条流按 2s 一次白拉。
        if (kind === 'sse' && live.current.stream[id]) continue
        const s = live.current.http[id]
        if (s && s.status !== 'pending') continue
        refetch.current.http(id, reconcileHttp)
      }
    }, PENDING_POLL_MS)
    return () => window.clearInterval(timer)
  }, [reconcileHttp])

  const track = useCallback(
    (id: string, kind: DraftKind) => {
      if (!id) return
      tracked.current.set(id, kind)
      // 回填走与丢帧重拉同一个闸门，免得它和紧随其后的轮询各拉一遍。
      // 出站 WebSocket 不产生 HTTP flow，只有会话可拉。
      if (kind === 'ws') {
        refetch.current.ws(id, keepWs)
        return
      }
      refetch.current.http(id, reconcileHttp)
      if (kind === 'sse') refetch.current.stream(id, keepStream)
    },
    [keepStream, keepWs, reconcileHttp],
  )

  const retain = useCallback(
    (keep: ReadonlySet<string>) => {
      for (const id of tracked.current.keys()) {
        if (!keep.has(id)) tracked.current.delete(id)
      }
      commitHttp(prune(live.current.http, keep))
      commitWs(prune(live.current.ws, keep))
      commitStream(prune(live.current.stream, keep))
    },
    [commitHttp, commitStream, commitWs],
  )

  return { http, ws, stream, track, retain }
}
