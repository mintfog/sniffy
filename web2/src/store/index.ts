import { create } from 'zustand'
import { devtools, persist } from 'zustand/middleware'
import { HttpSession, WebSocketSession, Filter, UIState, Statistics } from '@/types'

// 主应用状态接口
interface AppState {
  // UI 状态
  ui: UIState
  
  // 数据状态
  sessions: HttpSession[]
  webSocketSessions: WebSocketSession[]
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
export const useSelectedSession = () => {
  const selectedId = useAppStore((state) => state.selectedSessionId)
  const sessions = useAppStore((state) => state.sessions)
  return sessions.find((session) => session.id === selectedId)
}
export const useFilter = () => useAppStore((state) => state.filter)
export const useStatistics = () => useAppStore((state) => state.statistics)
export const useSystemStatus = () => useAppStore((state) => ({
  isRecording: state.isRecording,
  isConnected: state.isConnected,
}))
