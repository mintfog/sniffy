import { useEffect } from 'react'
import { Events } from '@wailsio/runtime'
import { useAppStore } from '@/store'
import { Bridge } from '@/lib/bridge'
import type { HttpSession, WebSocketSession } from '@/types'

/**
 * 后端实时同步（Wails v3）。
 *
 * 挂载时：
 *   1. 初次拉取已有会话回填 store（GetSessions），成功即标记 isConnected=true；
 *      失败（非 Wails 环境，如浏览器预览）则保持未连接 → 工作台展示空表。
 *   2. 读取录制开关状态。
 *   3. 订阅引擎事件总线转发来的 Wails 事件，按 id upsert 会话：
 *        - flow_started / flow_updated → 完整 HTTPSessionDTO（flow_completed 已被 flow_updated 覆盖，忽略）
 *        - ws_message                  → 完整 WSSessionDTO
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

    // 2. 录制状态
    Bridge.isRecording()
      .then((r) => alive && store.setRecording(r))
      .catch(() => {})

    // 3. 订阅事件（非 Wails 环境下 Events.On 不会触发，无害）
    const offs: Array<() => void> = []
    try {
      offs.push(Events.On('flow_started', (e) => upsertHttp(e.data as HttpSession)))
      offs.push(Events.On('flow_updated', (e) => upsertHttp(e.data as HttpSession)))
      offs.push(Events.On('ws_message', (e) => upsertWs(e.data as WebSocketSession)))
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
