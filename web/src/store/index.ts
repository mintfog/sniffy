import { create } from 'zustand'
import { devtools, persist } from 'zustand/middleware'
import { HttpSession, WebSocketSession, StreamSession, Filter, UIState, Statistics } from '@/types'
import type { PausedFlow } from '@/workbench/views/breakpoints/model'

// 主应用状态接口
interface AppState {
  // UI 状态
  ui: UIState
  
  // 数据状态
  sessions: HttpSession[]
  webSocketSessions: WebSocketSession[]
  streamSessions: StreamSession[]
  /**
   * 命中断点、正被按住等待处置的 flow。
   *
   * 刻意不进 persist 的 partialize：暂停是进程内的瞬态，重启后后端一条都不剩，
   * 存下来只会显示一批点不动的僵尸行。也刻意不进 clearAllData：清空流量表不该
   * 让人以为断点被解除了——那些请求其实还被按在那里。
   */
  pausedFlows: PausedFlow[]
  /** 全局“断在请求 / 响应”开关，供各处常驻组件显示状态。 */
  globalBreak: { onRequest: boolean; onResponse: boolean }
  /**
   * 每条离开断点的 flow 的去向（resumed / aborted / expired），按 id 记账。
   * 超时是失败开放：请求已经原样发出去了。编辑器只靠列表里少了一行来感知这件事的话，
   * 用户看到的就是"我正在改的东西凭空消失了"。
   *
   * 不能只留最后一条：同一批命中的断点会一起超时，后到的那几条会把用户正编辑的那条
   * 挤掉，编辑器就会把"已超时"说成"被别的窗口处置了"。
   */
  resolutions: Record<string, string>
  /**
   * 正在编辑的暂停项 id。编辑器是应用级模态、只挂一份，各处入口（断点页、流量表
   * 右键菜单与双击、详情面板横幅）都经这个字段打开它。
   */
  editingBreakpointId?: string
  selectedSessionId?: string
  filter: Filter
  searchTerm: string
  
  // 统计数据
  statistics: Statistics
  
  // 系统状态
  isRecording: boolean
  isConnected: boolean
  
  // Actions
  setUIState: (ui: Partial<UIState>) => void
  setSessions: (sessions: HttpSession[]) => void
  addSession: (session: HttpSession) => void
  updateSession: (id: string, session: Partial<HttpSession>) => void
  removeSession: (id: string) => void
  setSelectedSession: (id?: string) => void
  
  setWebSocketSessions: (sessions: WebSocketSession[]) => void
  addWebSocketSession: (session: WebSocketSession) => void
  updateWebSocketSession: (id: string, session: Partial<WebSocketSession>) => void
  removeWebSocketSession: (id: string) => void

  setStreamSessions: (sessions: StreamSession[]) => void
  addStreamSession: (session: StreamSession) => void
  updateStreamSession: (id: string, session: Partial<StreamSession>) => void
  removeStreamSession: (id: string) => void
  
  setPausedFlows: (flows: PausedFlow[]) => void
  upsertPausedFlow: (flow: PausedFlow) => void
  removePausedFlow: (id: string) => void
  setGlobalBreak: (state: { onRequest: boolean; onResponse: boolean }) => void
  noteResolved: (id: string, resolution: string) => void
  setEditingBreakpoint: (id?: string) => void

  setFilter: (filter: Partial<Filter>) => void
  clearFilter: () => void
  setSearchTerm: (term: string) => void
  
  setStatistics: (stats: Statistics) => void
  setRecording: (recording: boolean) => void
  setConnected: (connected: boolean) => void
  
  // 清除所有数据
  clearAllData: () => void
}

const initialUIState: UIState = {
  sidebarCollapsed: false,
  darkMode: false,
  filterPanelOpen: false,
  currentView: 'dashboard',
}

const initialFilter: Filter = {}

const initialStatistics: Statistics = {
  totalRequests: 0,
  totalSessions: 0,
  totalBytes: 0,
  requestsPerSecond: 0,
  averageResponseTime: 0,
  statusCodeDistribution: {},
  methodDistribution: {},
  topHosts: [],
}

export const useAppStore = create<AppState>()(
  devtools(
    persist(
      (set) => ({
        // 初始状态
        ui: initialUIState,
        sessions: [],
        webSocketSessions: [],
        streamSessions: [],
        pausedFlows: [],
        globalBreak: { onRequest: false, onResponse: false },
        resolutions: {},
        editingBreakpointId: undefined,
        selectedSessionId: undefined,
        filter: initialFilter,
        searchTerm: '',
        statistics: initialStatistics,
        isRecording: false,
        isConnected: false,

        // UI Actions
        setUIState: (ui) =>
          set((state) => ({
            ui: { ...state.ui, ...ui },
          })),

        // Session Actions
        setSessions: (sessions) => set({ sessions }),
        
        addSession: (session) =>
          set((state) => ({
            sessions: [session, ...state.sessions],
          })),
          
        updateSession: (id, sessionUpdate) =>
          set((state) => ({
            sessions: state.sessions.map((session) =>
              session.id === id ? { ...session, ...sessionUpdate } : session
            ),
          })),
          
        removeSession: (id) =>
          set((state) => ({
            sessions: state.sessions.filter((session) => session.id !== id),
          })),
          
        setSelectedSession: (id) => set({ selectedSessionId: id }),

        // WebSocket Actions
        setWebSocketSessions: (webSocketSessions) => set({ webSocketSessions }),
        
        addWebSocketSession: (session) =>
          set((state) => ({
            webSocketSessions: [session, ...state.webSocketSessions],
          })),
          
        updateWebSocketSession: (id, sessionUpdate) =>
          set((state) => ({
            webSocketSessions: state.webSocketSessions.map((session) =>
              session.id === id ? { ...session, ...sessionUpdate } : session
            ),
          })),

        removeWebSocketSession: (id) =>
          set((state) => ({
            webSocketSessions: state.webSocketSessions.filter((session) => session.id !== id),
          })),

        // Stream Actions(SSE / gRPC / 分块流)
        setStreamSessions: (streamSessions) => set({ streamSessions }),

        addStreamSession: (session) =>
          set((state) => ({
            streamSessions: [session, ...state.streamSessions],
          })),

        updateStreamSession: (id, sessionUpdate) =>
          set((state) => ({
            streamSessions: state.streamSessions.map((session) =>
              session.id === id ? { ...session, ...sessionUpdate } : session
            ),
          })),

        removeStreamSession: (id) =>
          set((state) => ({
            streamSessions: state.streamSessions.filter((session) => session.id !== id),
          })),

        // 断点 Actions
        setPausedFlows: (pausedFlows) => set({ pausedFlows }),
        // 已在列表里的 id 就地替换（续期会带着新的截止时刻重发一次命中），新 id 追加到
        // 末尾——按首次见到的先后排，列表不会在每次事件之后重排。
        upsertPausedFlow: (flow) =>
          set((state) => ({
            pausedFlows: state.pausedFlows.some((p) => p.id === flow.id)
              ? state.pausedFlows.map((p) => (p.id === flow.id ? flow : p))
              : [...state.pausedFlows, flow],
          })),
        removePausedFlow: (id) =>
          set((state) => ({ pausedFlows: state.pausedFlows.filter((p) => p.id !== id) })),
        noteResolved: (id, resolution) =>
          set((state) => {
            // 只留最近一批，避免长时间运行后无限增长。
            const keys = Object.keys(state.resolutions)
            const kept = keys.length >= 256 ? keys.slice(-128) : keys
            const next: Record<string, string> = {}
            for (const k of kept) next[k] = state.resolutions[k]
            next[id] = resolution
            return { resolutions: next }
          }),
        setGlobalBreak: (globalBreak) => set({ globalBreak }),
        setEditingBreakpoint: (editingBreakpointId) => set({ editingBreakpointId }),

        // Filter Actions
        setFilter: (filterUpdate) =>
          set((state) => ({
            filter: { ...state.filter, ...filterUpdate },
          })),
          
        clearFilter: () => set({ filter: initialFilter }),
        
        setSearchTerm: (searchTerm) => set({ searchTerm }),

        // Statistics Actions
        setStatistics: (statistics) => set({ statistics }),

        // System Actions
        setRecording: (isRecording) => set({ isRecording }),
        setConnected: (isConnected) => set({ isConnected }),

        // Clear all data
        clearAllData: () =>
          set({
            sessions: [],
            webSocketSessions: [],
            streamSessions: [],
            selectedSessionId: undefined,
            searchTerm: '',
            statistics: initialStatistics,
          }),
      }),
      {
        name: 'sniffy-storage',
        partialize: (state) => ({
          ui: state.ui,
          filter: state.filter,
        }),
      }
    ),
    {
      name: 'sniffy-store',
    }
  )
)

// 选择器 Hooks
export const useUIState = () => useAppStore((state) => state.ui)
export const useSessions = () => useAppStore((state) => state.sessions)
export const useWebSocketSessions = () => useAppStore((state) => state.webSocketSessions)
export const useStreamSessions = () => useAppStore((state) => state.streamSessions)
export const useSelectedSession = () => {
  const selectedId = useAppStore((state) => state.selectedSessionId)
  const sessions = useAppStore((state) => state.sessions)
  return sessions.find((session) => session.id === selectedId)
}
export const useFilter = () => useAppStore((state) => state.filter)
export const useStatistics = () => useAppStore((state) => state.statistics)
// 注意：分别订阅两个原子字段，避免每次返回新对象导致无关 store 更新也触发重渲染
export const usePausedFlows = () => useAppStore((state) => state.pausedFlows)
// 角标只关心数量：订阅整个数组会让任何一条内容变化（含续期）都拖着整棵树重渲染。
export const usePausedCount = () => useAppStore((state) => state.pausedFlows.length)
export const useGlobalBreak = () => useAppStore((state) => state.globalBreak)
export const useResolutions = () => useAppStore((state) => state.resolutions)
export const useEditingBreakpointId = () => useAppStore((state) => state.editingBreakpointId)
/** 按 id 取暂停项；流量表的行要据此判断"这一条能不能就地编辑"。 */
export const usePausedFlow = (id?: string) =>
  useAppStore((state) => (id ? state.pausedFlows.find((p) => p.id === id) : undefined))
export const useSystemStatus = () => {
  const isRecording = useAppStore((state) => state.isRecording)
  const isConnected = useAppStore((state) => state.isConnected)
  return { isRecording, isConnected }
}
