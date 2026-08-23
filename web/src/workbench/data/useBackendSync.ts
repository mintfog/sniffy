import { useEffect } from 'react'
import { Events } from '@wailsio/runtime'
import { useAppStore } from '@/store'
import { Bridge } from '@/lib/bridge'
import type { HttpSession, WebSocketSession, StreamSession, WsDelta, StreamDelta } from '@/types'
import { acceptsRefetch, createRefetcher, mergeStreamDelta, mergeWsDelta } from './delta'

/**
 * 后端实时同步（Wails v3）。
 *
 * 挂载时：
 *   1. 初次拉取已有会话回填 store（GetSessions），成功即标记 isConnected=true；
 *      失败（非 Wails 环境，如浏览器预览）则保持未连接 → 工作台展示空表。
 *   2. 读取录制开关状态。
 *   3. 订阅引擎事件总线转发来的 Wails 事件，按 id upsert 会话：
 *        - flow_started / flow_updated       → 完整 HTTPSessionDTO（flow_completed 已被 flow_updated 覆盖，忽略）
 *        - ws_message / stream_message       → 增量（见 ./delta），只带新增的那条消息
 *
 * store 约定 newest-first；新会话 prepend，已存在的 patch。useTraffic 再按时间正序展示。
 */
export function useBackendSync() {
  useEffect(() => {
    let alive = true
    const store = useAppStore.getState()

    const upsertHttp = (s: HttpSession) => {
      if (!s || !s.id) return
      const st = useAppStore.getState()
      if (st.sessions.some((x) => x.id === s.id)) st.updateSession(s.id, s)
      else st.addSession(s)
    }
    const upsertWs = (s: WebSocketSession) => {
      if (!s || !s.id) return
      const st = useAppStore.getState()
      if (st.webSocketSessions.some((x) => x.id === s.id)) st.updateWebSocketSession(s.id, s)
      else st.addWebSocketSession(s)
    }
    const upsertStream = (s: StreamSession) => {
      if (!s || !s.id) return
      const st = useAppStore.getState()
      if (st.streamSessions.some((x) => x.id === s.id)) st.updateStreamSession(s.id, s)
      else st.addStreamSession(s)
    }

    // 丢帧后整条重拉（后端始终留着完整会话）。
    const refetchWs = createRefetcher(Bridge.getWSSession)
    const refetchStream = createRefetcher(Bridge.getStreamSession)

    // 重拉的结果落地之前可能已经有更新的增量合进去了，覆盖前先比一次（见 acceptsRefetch）：
    // 直接盖会把刚收到的帧丢掉，还会把 messageCount 拨回去，让下一条增量再判一次跳号。
    const applyRefetchedWs = (s: WebSocketSession) => {
      if (acceptsRefetch(useAppStore.getState().webSocketSessions.find((x) => x.id === s.id), s)) upsertWs(s)
    }
    const applyRefetchedStream = (s: StreamSession) => {
      if (acceptsRefetch(useAppStore.getState().streamSessions.find((x) => x.id === s.id), s)) upsertStream(s)
    }

    const applyWsDelta = (d: WsDelta) => {
      if (!d?.session?.id) return
      const prev = useAppStore.getState().webSocketSessions.find((x) => x.id === d.session.id)
      const { session, gap } = mergeWsDelta(prev, d)
      upsertWs(session)
      if (gap) refetchWs(session.id, applyRefetchedWs)
    }
    const applyStreamDelta = (d: StreamDelta) => {
      if (!d?.session?.id) return
      const prev = useAppStore.getState().streamSessions.find((x) => x.id === d.session.id)
      const { session, gap } = mergeStreamDelta(prev, d)
      upsertStream(session)
      if (gap) refetchStream(session.id, applyRefetchedStream)
    }

    // 1. 初次回填 + 标记连接
    Bridge.getSessions(1, 2000)
      .then((page) => {
        if (!alive) return
        if (page?.data) store.setSessions(page.data)
        store.setConnected(true)
      })
      .catch(() => {
        // 非 Wails 环境：保持未连接，工作台展示空表。
      })

    // 1b. 回填已捕获的 WebSocket 会话（实时帧另经 ws_message 增量推送）
    Bridge.getWSSessions(1, 2000)
      .then((page) => {
        if (alive && page?.data) store.setWebSocketSessions(page.data)
      })
      .catch(() => {})

    // 1c. 回填已捕获的流式会话（SSE / gRPC / 分块流；实时消息另经 stream_message 增量推送）
    Bridge.getStreamSessions(1, 2000)
      .then((page) => {
        if (alive && page?.data) store.setStreamSessions(page.data)
      })
      .catch(() => {})

    // 2. 录制状态
    Bridge.isRecording()
      .then((r) => alive && store.setRecording(r))
      .catch(() => {})

    // 3. 订阅事件（非 Wails 环境下 Events.On 不会触发，无害）
    const offs: Array<() => void> = []
    try {
      offs.push(Events.On('flow_started', (e) => upsertHttp(e.data as HttpSession)))
      offs.push(Events.On('flow_updated', (e) => upsertHttp(e.data as HttpSession)))
      offs.push(Events.On('ws_message', (e) => applyWsDelta(e.data as WsDelta)))
      offs.push(Events.On('stream_message', (e) => applyStreamDelta(e.data as StreamDelta)))
    } catch {
      // ignore: runtime 不可用
    }

    return () => {
      alive = false
      for (const off of offs) {
        try {
          off()
        } catch {
          /* ignore */
        }
      }
    }
  }, [])
}
