// HTTP 请求/响应类型
export interface HttpRequest {
  id: string
  method: string
  url: string
  headers: Record<string, string>
  body?: string
  timestamp: string
  clientIP: string
  host: string
  path: string
  protocol: string
  userAgent?: string
  serverIP?: string
  serverPort?: number
}

export interface HttpResponse {
  id: string
  requestId: string
  status: number
  statusText: string
  headers: Record<string, string>
  body?: string
  timestamp: string
  size: number
  responseTime: number
}

export interface HttpSession {
  id: string
  request: HttpRequest
  response?: HttpResponse
  duration?: number
  status: 'pending' | 'completed' | 'error'
  blocked?: boolean
  modified?: boolean
}

// WebSocket 类型
export interface WebSocketMessage {
  id: string
  sessionId: string
  direction: 'inbound' | 'outbound'
  type: 'text' | 'binary'
  data: string
  timestamp: string
  size: number
}

export interface WebSocketSession {
  id: string
  url: string
  status: 'connecting' | 'connected' | 'disconnected' | 'error'
  startTime: string
  endTime?: string
  messageCount: number
  totalSize: number
  messages: WebSocketMessage[]
}

// 连接类型
export interface Connection {
  id: string
  clientIP: string
  serverIP: string
  clientPort: number
  serverPort: number
  protocol: 'HTTP' | 'HTTPS' | 'WebSocket' | 'TCP'
  startTime: string
  endTime?: string
  status: 'active' | 'closed' | 'error'
  bytesIn: number
  bytesOut: number
  duration?: number
}

// 过滤器和搜索类型
export interface Filter {
  method?: string[]
  status?: number[]
  host?: string[]
  contentType?: string[]
  protocol?: string[]
  timeRange?: {
    start: string
    end: string
  }
}

export interface SearchQuery {
  term: string
  field?: 'url' | 'headers' | 'body' | 'all'
  caseSensitive?: boolean
  regex?: boolean
}

// API 响应类型
export interface ApiResponse<T> {
  data: T
  success: boolean
  message?: string
  timestamp: string
}

export interface PaginatedResponse<T> {
  data: T[]
  total: number
  page: number
  pageSize: number
  hasNext: boolean
  hasPrev: boolean
}

// 配置类型
export interface SniffyConfig {
  port: number
  host: string
  enableHTTPS: boolean
  caCertPath?: string
  plugins: PluginConfig[]
  filters: Filter
  recording: boolean
}

export interface PluginConfig {
  name: string
  enabled: boolean
  config: Record<string, any>
}

// 统计数据类型
export interface Statistics {
  totalRequests: number
  totalSessions: number
  totalBytes: number
  requestsPerSecond: number
  averageResponseTime: number
  statusCodeDistribution: Record<number, number>
  methodDistribution: Record<string, number>
  topHosts: Array<{ host: string; count: number }>
}

// UI 状态类型
export interface UIState {
  sidebarCollapsed: boolean
  darkMode: boolean
  selectedSession?: string
  filterPanelOpen: boolean
  currentView: 'sessions' | 'requests' | 'websockets' | 'dashboard'
}

// 拦截器类型
export interface InterceptRule {
  id: string
  name: string
  enabled: boolean
  conditions: InterceptCondition[]
  actions: InterceptAction[]
  priority: number
  createdAt: string
  updatedAt: string
}

export interface InterceptCondition {
  type: 'url' | 'method' | 'header' | 'body' | 'status'
  operator: 'equals' | 'contains' | 'regex' | 'starts_with' | 'ends_with'
  value: string
  caseSensitive?: boolean
}

export interface InterceptAction {
  type: 'block' | 'modify_request' | 'modify_response' | 'delay' | 'redirect'
  parameters: Record<string, any>
}

export interface InterceptStats {
  totalRules: number
  activeRules: number
  totalInterceptions: number
  blockedRequests: number
  modifiedRequests: number
  modifiedResponses: number
}

// 拦截历史记录
export interface InterceptHistory {
  id: string
  sessionId: string
  ruleId: string
  ruleName: string
  action: string
  timestamp: string
  details: Record<string, any>
}

// 导出类型
export interface ExportConfig {
  format: 'json' | 'csv' | 'har'
  includeRequestBody: boolean
  includeResponseBody: boolean
  selectedOnly: boolean
  timeRange?: {
    start: string
    end: string
  }
}
