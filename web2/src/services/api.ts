import { mockApi } from './mockApi'
import { 
  ApiResponse, 
  HttpSession, 
  WebSocketSession, 
  Statistics, 
  SniffyConfig,
  PaginatedResponse,
  ExportConfig,
  InterceptRule,
  InterceptStats,
  InterceptHistory
} from '@/types'

// 直接使用真实API（不使用模拟数据）
const USE_MOCK_API = false

// API 基础URL (Web API 插件默认运行在 8888 端口，代理在 8080 端口)
const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8888'

console.log('🔧 API Configuration:', {
  useMockAPI: USE_MOCK_API,
  apiBaseUrl: API_BASE_URL,
  mode: import.meta.env.MODE
})

// 通用请求方法
const request = async <T>(
  endpoint: string,
  options?: RequestInit
): Promise<T> => {
  const url = `${API_BASE_URL}${endpoint}`
  
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
  })

  if (!response.ok) {
    throw new Error(`API request failed: ${response.statusText}`)
  }

  return response.json()
}

// API 方法
export const sniffyApi = {
  // 系统状态
  getStatus: (): Promise<ApiResponse<{ status: string; version: string; uptime: number }>> =>
    USE_MOCK_API ? mockApi.getStatus() : request('/api/status'),

  // 会话管理
  getSessions: (params?: {
    page?: number
    pageSize?: number
    filter?: string
  }): Promise<PaginatedResponse<HttpSession>> => {
    if (USE_MOCK_API) return mockApi.getSessions(params)
    
    const query = new URLSearchParams()
    if (params?.page) query.set('page', params.page.toString())
    if (params?.pageSize) query.set('pageSize', params.pageSize.toString())
    if (params?.filter) query.set('filter', params.filter)
    
    return request(`/api/sessions?${query.toString()}`)
  },

  getSession: (id: string): Promise<ApiResponse<HttpSession>> =>
    USE_MOCK_API ? mockApi.getSession(id) : request(`/api/sessions/${id}`),

  deleteSession: (id: string): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.deleteSession(id) : request(`/api/sessions/${id}`, { method: 'DELETE' }),

  clearSessions: (): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.clearSessions() : request('/api/sessions/clear', { method: 'POST' }),

  // WebSocket 会话
  getWebSocketSessions: (params?: {
    page?: number
    pageSize?: number
  }): Promise<PaginatedResponse<WebSocketSession>> => {
    if (USE_MOCK_API) return mockApi.getWebSocketSessions(params)
    
    const query = new URLSearchParams()
    if (params?.page) query.set('page', params.page.toString())
    if (params?.pageSize) query.set('pageSize', params.pageSize.toString())
    
    return request(`/api/websocket-sessions?${query.toString()}`)
  },

  getWebSocketSession: (id: string): Promise<ApiResponse<WebSocketSession>> =>
    USE_MOCK_API ? mockApi.getWebSocketSession(id) : request(`/api/websocket-sessions/${id}`),

  // 统计数据
  getStatistics: (): Promise<ApiResponse<Statistics>> =>
    USE_MOCK_API ? mockApi.getStatistics() : request('/api/statistics'),

  // 配置管理
  getConfig: (): Promise<ApiResponse<SniffyConfig>> =>
    USE_MOCK_API ? mockApi.getConfig() : request('/api/config'),

  updateConfig: (config: Partial<SniffyConfig>): Promise<ApiResponse<SniffyConfig>> =>
    USE_MOCK_API ? mockApi.updateConfig(config) : request('/api/config', {
      method: 'PUT',
      body: JSON.stringify(config),
    }),

  // 录制控制
  startRecording: (): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.startRecording() : request('/api/recording/start', { method: 'POST' }),

  stopRecording: (): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.stopRecording() : request('/api/recording/stop', { method: 'POST' }),

  getRecordingStatus: (): Promise<ApiResponse<{ recording: boolean }>> =>
    USE_MOCK_API ? mockApi.getRecordingStatus() : request('/api/recording/status'),

  // 插件管理
  getPlugins: (): Promise<ApiResponse<any[]>> =>
    USE_MOCK_API ? mockApi.getPlugins() : request('/api/plugins'),

  enablePlugin: (name: string): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.enablePlugin(name) : request(`/api/plugins/${name}/enable`, { method: 'POST' }),

  disablePlugin: (name: string): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.disablePlugin(name) : request(`/api/plugins/${name}/disable`, { method: 'POST' }),

  updatePluginConfig: (name: string, config: any): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.updatePluginConfig(name, config) : request(`/api/plugins/${name}/config`, {
      method: 'PUT',
      body: JSON.stringify(config),
    }),

  // 导出功能
  exportSessions: async (config: ExportConfig): Promise<Blob> => {
    if (USE_MOCK_API) return mockApi.exportSessions(config)
    
    const response = await fetch(`${API_BASE_URL}/api/export`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config),
    })
    
    if (!response.ok) {
      throw new Error('Export failed')
    }
    
    return response.blob()
  },

  // 证书管理
  getCACertificate: async (): Promise<Blob> => {
    if (USE_MOCK_API) return mockApi.getCACertificate()
    
    const response = await fetch(`${API_BASE_URL}/api/certificate/ca`)
    if (!response.ok) {
      throw new Error('Failed to get certificate')
    }
    
    return response.blob()
  },

  regenerateCACertificate: (): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.regenerateCACertificate() : request('/api/certificate/regenerate', { method: 'POST' }),

  // 拦截器管理
  getInterceptRules: (params?: {
    page?: number
    pageSize?: number
    enabled?: boolean
  }): Promise<PaginatedResponse<InterceptRule>> => {
    if (USE_MOCK_API) return mockApi.getInterceptRules(params)
    
    const query = new URLSearchParams()
    if (params?.page) query.set('page', params.page.toString())
    if (params?.pageSize) query.set('pageSize', params.pageSize.toString())
    if (params?.enabled !== undefined) query.set('enabled', params.enabled.toString())
    
    return request(`/api/intercept/rules?${query.toString()}`)
  },

  getInterceptRule: (id: string): Promise<ApiResponse<InterceptRule>> =>
    USE_MOCK_API ? mockApi.getInterceptRule(id) : request(`/api/intercept/rules/${id}`),

  createInterceptRule: (rule: Omit<InterceptRule, 'id' | 'createdAt' | 'updatedAt'>): Promise<ApiResponse<InterceptRule>> =>
    USE_MOCK_API ? mockApi.createInterceptRule(rule) : request('/api/intercept/rules', {
      method: 'POST',
      body: JSON.stringify(rule),
    }),

  updateInterceptRule: (id: string, rule: Partial<Omit<InterceptRule, 'id' | 'createdAt' | 'updatedAt'>>): Promise<ApiResponse<InterceptRule>> =>
    USE_MOCK_API ? mockApi.updateInterceptRule(id, rule) : request(`/api/intercept/rules/${id}`, {
      method: 'PUT',
      body: JSON.stringify(rule),
    }),

  deleteInterceptRule: (id: string): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.deleteInterceptRule(id) : request(`/api/intercept/rules/${id}`, { method: 'DELETE' }),

  toggleInterceptRule: (id: string, enabled: boolean): Promise<ApiResponse<InterceptRule>> =>
    USE_MOCK_API ? mockApi.toggleInterceptRule(id, enabled) : request(`/api/intercept/rules/${id}/toggle`, {
      method: 'POST',
      body: JSON.stringify({ enabled }),
    }),

  // 拦截统计
  getInterceptStats: (): Promise<ApiResponse<InterceptStats>> =>
    USE_MOCK_API ? mockApi.getInterceptStats() : request('/api/intercept/stats'),

  // 拦截历史
  getInterceptHistory: (params?: {
    page?: number
    pageSize?: number
    ruleId?: string
    sessionId?: string
  }): Promise<PaginatedResponse<InterceptHistory>> => {
    if (USE_MOCK_API) return mockApi.getInterceptHistory(params)
    
    const query = new URLSearchParams()
    if (params?.page) query.set('page', params.page.toString())
    if (params?.pageSize) query.set('pageSize', params.pageSize.toString())
    if (params?.ruleId) query.set('ruleId', params.ruleId)
    if (params?.sessionId) query.set('sessionId', params.sessionId)
    
    return request(`/api/intercept/history?${query.toString()}`)
  },

  clearInterceptHistory: (): Promise<ApiResponse<void>> =>
    USE_MOCK_API ? mockApi.clearInterceptHistory() : request('/api/intercept/history/clear', { method: 'POST' }),

  // 实时数据 (WebSocket)
  connectWebSocket: (onMessage: (data: any) => void, onError?: (error: Event) => void) => {
    if (USE_MOCK_API) {
      console.log('WebSocket connection disabled when using mock API')
      return null
    }
    
    // 从API_BASE_URL构造WebSocket URL
    const apiUrl = new URL(API_BASE_URL)
    const protocol = apiUrl.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${apiUrl.host}/api/ws`
    
    const ws = new WebSocket(wsUrl)
    
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        onMessage(data)
      } catch (error) {
        console.error('Failed to parse WebSocket message:', error)
      }
    }
    
    ws.onerror = (error) => {
      console.error('WebSocket error:', error)
      onError?.(error)
    }
    
    return ws
  },
}
