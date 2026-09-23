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
import type {
  HttpSession,
  StreamDelta,
  StreamSession,
  WebSocketSession,
  WsDelta,
} from '@/types'
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
 * 尚未确认终态的出站会话多久对账一次。
 *
 * flow_started / flow_updated 各只发一次，而事件总线对慢订阅者是直接丢弃的
 * （core.EventBus 把「订阅者自行重新拉取对账」写成了契约）。终态那条一旦被丢，
 * 页签就永远停在「发送中」：主按钮与 Ctrl+Enter 一起锁死，只能关掉重开。
 * 长连接靠 messageCount 跳号发现消息缺口；关闭通知没有后续消息可对账，须结合 HTTP 终态补取快照。
 */
const PENDING_POLL_MS = 2000
const STREAM_STATUS_POLL_MS = 10000

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
export function connOf(
  draft: Draft,
  session?: WebSocketSession
): WebSocketPart['conn'] {
  const part = wsOf(draft)
  if (part.conn === 'connecting') return 'connecting'
  return session ? session.status : part.conn
}

function prune<T>(
  map: Record<string, T>,
  live: ReadonlySet<string>
): Record<string, T> {
  const next: Record<string, T> = {}
  let dropped = false
  for (const [id, value] of Object.entries(map)) {
    if (live.has(id)) next[id] = value
    else dropped = true
  }
  return dropped ? next : map
}

function hasEventStreamResponse(s?: HttpSession): boolean {
  return Object.entries(s?.response?.headers ?? {}).some(
    ([name, value]) =>
      name.toLowerCase() === 'content-type' &&
      value.split(';', 1)[0].trim().toLowerCase() === 'text/event-stream'
  )
}

function needsStreamSnapshot(
  http?: HttpSession,
  stream?: StreamSession
): boolean {
  if (!stream) return hasEventStreamResponse(http)
  return stream.status === 'open' && !!http && http.status !== 'pending'
}

export function useOutboundSessions(): OutboundSessions {
  const [http, setHttp] = useState<Record<string, HttpSession>>({})
  const [ws, setWs] = useState<Record<string, WebSocketSession>>({})
  const [stream, setStream] = useState<Record<string, StreamSession>>({})
  // 出站 WebSocket 不产生 HTTP flow，回填时须按请求类型选择接口。
  const tracked = useRef<Map<string, DraftKind>>(new Map())
  const missingStreams = useRef(new Set<string>())

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
      if (s?.id && tracked.current.has(s.id))
        commitHttp({ ...live.current.http, [s.id]: s })
    },
    [commitHttp]
  )

  // 以下三个是「整条重拉」的落地口（发送后的首次回填、丢帧重拉、pending 轮询共用），
  // 一律带陈旧判据：拉取是异步的，结果落地时手上可能已经有更新的一份了（见 acceptsRefetch）。
  const reconcileHttp = useCallback(
    (s?: HttpSession) => {
      if (!s?.id || !tracked.current.has(s.id)) return
      if (!acceptsHttpRefetch(live.current.http[s.id], s)) return
      commitHttp({ ...live.current.http, [s.id]: s })
    },
    [commitHttp]
  )
  const keepWs = useCallback(
    (s?: WebSocketSession) => {
      if (!s?.id || !tracked.current.has(s.id)) return
      if (!acceptsRefetch(live.current.ws[s.id], s)) return
      commitWs({ ...live.current.ws, [s.id]: s })
    },
    [commitWs]
  )
  const keepStream = useCallback(
    (s?: StreamSession) => {
      if (!s?.id || !tracked.current.has(s.id)) return
      const previous = live.current.stream[s.id]
      missingStreams.current.delete(s.id)
      // 关闭只更新元数据，消息数可能不变；在途的 open 快照不能覆盖已收到的终态。
      if (previous?.status === 'closed' && s.status === 'open') return
      if (!acceptsRefetch(previous, s)) return
      commitStream({ ...live.current.stream, [s.id]: s })
    },
    [commitStream]
  )

  // 丢帧后整条重拉。ref 而非 useMemo：闸门里的在途集合必须跨渲染存活。
  const refetch = useRef({
    http: createRefetcher(Bridge.getSession),
    ws: createRefetcher(Bridge.getWSSession),
    stream: createRefetcher(Bridge.getStreamSession),
  })

  const refetchStream = useCallback(
    (id: string) => {
      if (missingStreams.current.has(id)) return
      const s = live.current.http[id]
      const terminal = !!s && s.status !== 'pending'
      refetch.current.stream(id, keepStream, () => {
        // 终态前发出的查询可能早于流创建，只有终态后的空快照才能确认不存在。
        if (terminal && tracked.current.has(id) && !live.current.stream[id])
          missingStreams.current.add(id)
      })
    },
    [keepStream]
  )

  // 响应头用于补回缺失的流；HTTP 终态用于核对已建立的流是否关闭。
  useEffect(() => {
    for (const s of Object.values(http)) {
      if (needsStreamSnapshot(s, live.current.stream[s.id])) refetchStream(s.id)
    }
  }, [http, refetchStream])

  const applyWsDelta = useCallback(
    (d?: WsDelta) => {
      const id = d?.session?.id
      if (!id || !tracked.current.has(id)) return
      const { session, gap } = mergeWsDelta(live.current.ws[id], d)
      commitWs({ ...live.current.ws, [id]: session })
      if (gap) refetch.current.ws(id, keepWs)
    },
    [commitWs, keepWs]
  )
  const applyStreamDelta = useCallback(
    (d?: StreamDelta) => {
      const id = d?.session?.id
      if (!id || !tracked.current.has(id)) return
      const { session, gap } = mergeStreamDelta(live.current.stream[id], d)
      missingStreams.current.delete(id)
      commitStream({ ...live.current.stream, [id]: session })
      if (gap) refetchStream(id)
    },
    [commitStream, refetchStream]
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

  // 回填失败或被在途请求合并时，定期重试待确认的 HTTP 响应与 SSE 终态。
  useEffect(() => {
    let polls = 0
    const timer = window.setInterval(() => {
      const pollActiveStreams =
        ++polls % (STREAM_STATUS_POLL_MS / PENDING_POLL_MS) === 0
      for (const [id, kind] of tracked.current) {
        // 出站 WebSocket 不产生 HTTP flow（见 internal/app/compose_ws.go）。
        if (kind === 'ws') continue
        const s = live.current.http[id]
        const currentStream = live.current.stream[id]
        const pending = !s || s.status === 'pending'
        // 关闭与 HTTP 终态可能同时丢失，活跃流也要低频核对 HTTP 状态。
        if (pending && (currentStream?.status !== 'open' || pollActiveStreams))
          refetch.current.http(id, reconcileHttp)
        // 首轮回填可能先读到空流快照、后读到 HTTP 终态；此时仍须继续补回事件。
        if (
          (!currentStream && pending) ||
          needsStreamSnapshot(s, currentStream)
        ) {
          refetchStream(id)
        }
      }
    }, PENDING_POLL_MS)
    return () => window.clearInterval(timer)
  }, [refetchStream, reconcileHttp])

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
      refetchStream(id)
    },
    [refetchStream, keepWs, reconcileHttp]
  )

  const retain = useCallback(
    (keep: ReadonlySet<string>) => {
      for (const id of tracked.current.keys()) {
        if (!keep.has(id)) {
          tracked.current.delete(id)
          missingStreams.current.delete(id)
        }
      }
      commitHttp(prune(live.current.http, keep))
      commitWs(prune(live.current.ws, keep))
      commitStream(prune(live.current.stream, keep))
    },
    [commitHttp, commitStream, commitWs]
  )

  return { http, ws, stream, track, retain }
}
