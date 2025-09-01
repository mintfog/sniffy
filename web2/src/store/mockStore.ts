import { useAppStore } from './index'
import { mockHttpSessions, mockWebSocketSessions, mockStatistics } from '@/services/mockData'

/**
 * 初始化模拟数据到状态管理中
 */
export function initializeMockData() {
  const store = useAppStore.getState()
  
  // 设置模拟的HTTP会话数据
  store.setSessions(mockHttpSessions)
  
  // 设置模拟的WebSocket会话数据
  store.setWebSocketSessions(mockWebSocketSessions)
  
  // 设置模拟的统计数据
  store.setStatistics(mockStatistics)
  
  // 设置连接状态
  store.setConnected(true)
  store.setRecording(true)
  
  console.log('Mock data initialized:', {
    httpSessions: mockHttpSessions.length,
    webSocketSessions: mockWebSocketSessions.length,
    statistics: mockStatistics
  })
}

/**
 * 模拟实时数据更新
 */
export function startMockDataSimulation() {
  // 每5秒模拟新的请求
  const addNewRequestInterval = setInterval(() => {
    const store = useAppStore.getState()
    const newSession = createMockSession()
    store.addSession(newSession)
  }, 5000)

  // 每10秒更新统计数据
  const updateStatsInterval = setInterval(() => {
    const store = useAppStore.getState()
    const currentStats = store.statistics
    const updatedStats = {
      ...currentStats,
      totalRequests: currentStats.totalRequests + Math.floor(Math.random() * 10),
      requestsPerSecond: Math.random() * 20,
      averageResponseTime: 150 + Math.random() * 200
    }
    store.setStatistics(updatedStats)
  }, 10000)

  // 返回清理函数
  return () => {
    clearInterval(addNewRequestInterval)
    clearInterval(updateStatsInterval)
  }
}

// 创建模拟会话的辅助函数
function createMockSession() {
  const methods = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH']
  const hosts = ['api.example.com', 'api.github.com', 'httpbin.org', 'jsonplaceholder.typicode.com']
  const statuses = [200, 201, 400, 401, 404, 500]
  
  const method = methods[Math.floor(Math.random() * methods.length)]
  const host = hosts[Math.floor(Math.random() * hosts.length)]
  const status = statuses[Math.floor(Math.random() * statuses.length)]
  const path = `/api/v1/resource/${Math.floor(Math.random() * 1000)}`
  
  return {
    id: `session-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`,
    request: {
      id: `req-${Date.now()}`,
      method,
      url: `https://${host}${path}`,
      headers: {
        'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
        'Accept': 'application/json',
        'Content-Type': 'application/json'
      },
      timestamp: new Date().toISOString(),
      clientIP: `192.168.1.${Math.floor(Math.random() * 255)}`,
      host,
      path,
      protocol: 'HTTPS/1.1'
    },
    response: {
      id: `res-${Date.now()}`,
      requestId: `req-${Date.now()}`,
      status,
      statusText: getStatusText(status),
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Math.floor(Math.random() * 5000).toString()
      },
      body: JSON.stringify({ message: 'Mock response data' }),
      timestamp: new Date().toISOString(),
      size: Math.floor(Math.random() * 5000),
      responseTime: Math.floor(Math.random() * 1000)
    },
    duration: Math.floor(Math.random() * 1000),
    status: 'completed' as const
  }
}

function getStatusText(status: number): string {
  const statusTexts: Record<number, string> = {
    200: 'OK',
    201: 'Created',
    400: 'Bad Request',
    401: 'Unauthorized',
    404: 'Not Found',
    500: 'Internal Server Error'
  }
  return statusTexts[status] || 'Unknown'
}
