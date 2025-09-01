import { mockApi } from './mockApi'
import { 
  ApiResponse, 
  HttpSession, 
  WebSocketSession, 
  Statistics, 
  SniffyConfig,
  PaginatedResponse,
  ExportConfig
} from '@/types'

// 开发模式使用模拟API，生产模式使用真实API
const isDevelopment = import.meta.env.DEV || process.env.NODE_ENV === 'development'

// API 方法
export const sniffyApi = {
  // 系统状态
  getStatus: (): Promise<ApiResponse<{ status: string; version: string; uptime: number }>> =>
    isDevelopment ? mockApi.getStatus() : Promise.reject('API not implemented'),

  // 会话管理
  getSessions: (params?: {
    page?: number
    pageSize?: number
    filter?: string
  }): Promise<PaginatedResponse<HttpSession>> =>
    isDevelopment ? mockApi.getSessions(params) : Promise.reject('API not implemented'),

  getSession: (id: string): Promise<ApiResponse<HttpSession>> =>
    isDevelopment ? mockApi.getSession(id) : Promise.reject('API not implemented'),

  deleteSession: (id: string): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.deleteSession(id) : Promise.reject('API not implemented'),

  clearSessions: (): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.clearSessions() : Promise.reject('API not implemented'),

  // WebSocket 会话
  getWebSocketSessions: (params?: {
    page?: number
    pageSize?: number
  }): Promise<PaginatedResponse<WebSocketSession>> =>
    isDevelopment ? mockApi.getWebSocketSessions(params) : Promise.reject('API not implemented'),

  getWebSocketSession: (id: string): Promise<ApiResponse<WebSocketSession>> =>
    isDevelopment ? mockApi.getWebSocketSession(id) : Promise.reject('API not implemented'),

  // 统计数据
  getStatistics: (): Promise<ApiResponse<Statistics>> =>
    isDevelopment ? mockApi.getStatistics() : Promise.reject('API not implemented'),

  // 配置管理
  getConfig: (): Promise<ApiResponse<SniffyConfig>> =>
    isDevelopment ? mockApi.getConfig() : Promise.reject('API not implemented'),

  updateConfig: (config: Partial<SniffyConfig>): Promise<ApiResponse<SniffyConfig>> =>
    isDevelopment ? mockApi.updateConfig(config) : Promise.reject('API not implemented'),

  // 录制控制
  startRecording: (): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.startRecording() : Promise.reject('API not implemented'),

  stopRecording: (): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.stopRecording() : Promise.reject('API not implemented'),

  getRecordingStatus: (): Promise<ApiResponse<{ recording: boolean }>> =>
    isDevelopment ? mockApi.getRecordingStatus() : Promise.reject('API not implemented'),

  // 插件管理
  getPlugins: (): Promise<ApiResponse<any[]>> =>
    isDevelopment ? mockApi.getPlugins() : Promise.reject('API not implemented'),

  enablePlugin: (name: string): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.enablePlugin(name) : Promise.reject('API not implemented'),

  disablePlugin: (name: string): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.disablePlugin(name) : Promise.reject('API not implemented'),

  updatePluginConfig: (name: string, config: any): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.updatePluginConfig(name, config) : Promise.reject('API not implemented'),

  // 导出功能
  exportSessions: (config: ExportConfig): Promise<Blob> =>
    isDevelopment ? mockApi.exportSessions(config) : Promise.reject('API not implemented'),

  // 证书管理
  getCACertificate: (): Promise<Blob> =>
    isDevelopment ? mockApi.getCACertificate() : Promise.reject('API not implemented'),

  regenerateCACertificate: (): Promise<ApiResponse<void>> =>
    isDevelopment ? mockApi.regenerateCACertificate() : Promise.reject('API not implemented'),

  // 实时数据 (WebSocket) - 开发模式下暂不启用
  connectWebSocket: (onMessage: (data: any) => void, onError?: (error: Event) => void) => {
    if (isDevelopment) {
      console.log('WebSocket connection disabled in development mode')
      return null
    }
    
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/api/ws`
    
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
