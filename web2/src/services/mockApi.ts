import { 
  ApiResponse, 
  PaginatedResponse,
  HttpSession, 
  WebSocketSession, 
  Statistics, 
  SniffyConfig 
} from '@/types'
import { 
  mockHttpSessions, 
  mockWebSocketSessions, 
  mockStatistics, 
  mockConfig 
} from './mockData'

// 模拟延迟
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms))

// 创建成功响应
const createSuccessResponse = <T>(data: T): ApiResponse<T> => ({
  data,
  success: true,
  timestamp: new Date().toISOString()
})

// 创建分页响应
const createPaginatedResponse = <T>(
  data: T[], 
  page: number = 1, 
  pageSize: number = 50
): PaginatedResponse<T> => {
  const start = (page - 1) * pageSize
  const end = start + pageSize
  const paginatedData = data.slice(start, end)
  
  return {
    data: paginatedData,
    total: data.length,
    page,
    pageSize,
    hasNext: end < data.length,
    hasPrev: page > 1
  }
}

// 模拟 API 服务
export const mockApi = {
  // 系统状态
  getStatus: async () => {
    await delay(100)
    return createSuccessResponse({
      status: 'running',
      version: '1.0.0',
      uptime: 86400
    })
  },

  // 会话管理
  getSessions: async (params?: {
    page?: number
    pageSize?: number
    filter?: string
  }) => {
    await delay(200)
    const { page = 1, pageSize = 50 } = params || {}
    return createPaginatedResponse(mockHttpSessions, page, pageSize)
  },

  getSession: async (id: string) => {
    await delay(100)
    const session = mockHttpSessions.find(s => s.id === id)
    if (!session) {
      throw new Error('Session not found')
    }
    return createSuccessResponse(session)
  },

  deleteSession: async (id: string) => {
    await delay(150)
    return createSuccessResponse(undefined)
  },

  clearSessions: async () => {
    await delay(200)
    return createSuccessResponse(undefined)
  },

  // WebSocket 会话
  getWebSocketSessions: async (params?: {
    page?: number
    pageSize?: number
  }) => {
    await delay(150)
    const { page = 1, pageSize = 50 } = params || {}
    return createPaginatedResponse(mockWebSocketSessions, page, pageSize)
  },

  getWebSocketSession: async (id: string) => {
    await delay(100)
    const session = mockWebSocketSessions.find(s => s.id === id)
    if (!session) {
      throw new Error('WebSocket session not found')
    }
    return createSuccessResponse(session)
  },

  // 统计数据
  getStatistics: async () => {
    await delay(100)
    return createSuccessResponse(mockStatistics)
  },

  // 配置管理
  getConfig: async () => {
    await delay(100)
    return createSuccessResponse(mockConfig)
  },

  updateConfig: async (config: Partial<SniffyConfig>) => {
    await delay(200)
    const updatedConfig = { ...mockConfig, ...config }
    return createSuccessResponse(updatedConfig)
  },

  // 录制控制
  startRecording: async () => {
    await delay(100)
    return createSuccessResponse(undefined)
  },

  stopRecording: async () => {
    await delay(100)
    return createSuccessResponse(undefined)
  },

  getRecordingStatus: async () => {
    await delay(50)
    return createSuccessResponse({ recording: mockConfig.recording })
  },

  // 插件管理
  getPlugins: async () => {
    await delay(100)
    return createSuccessResponse(mockConfig.plugins)
  },

  enablePlugin: async (name: string) => {
    await delay(100)
    return createSuccessResponse(undefined)
  },

  disablePlugin: async (name: string) => {
    await delay(100)
    return createSuccessResponse(undefined)
  },

  updatePluginConfig: async (name: string, config: any) => {
    await delay(150)
    return createSuccessResponse(undefined)
  },

  // 导出功能
  exportSessions: async (config: any) => {
    await delay(500)
    // 模拟返回一个 Blob
    const data = JSON.stringify(mockHttpSessions, null, 2)
    return new Blob([data], { type: 'application/json' })
  },

  // 证书管理
  getCACertificate: async () => {
    await delay(200)
    const certData = '-----BEGIN CERTIFICATE-----\nMOCK CERTIFICATE DATA\n-----END CERTIFICATE-----'
    return new Blob([certData], { type: 'application/x-pem-file' })
  },

  regenerateCACertificate: async () => {
    await delay(1000)
    return createSuccessResponse(undefined)
  }
}
