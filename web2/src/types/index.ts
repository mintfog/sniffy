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
  description?: string
  enabled: boolean
  conditions: InterceptCondition[]
  actions: InterceptAction[]
  priority: number
  logicOperator: 'AND' | 'OR' // 条件之间的逻辑关系
  tags?: string[]
  createdAt: string
  updatedAt: string
}

// 匹配条件类型
export type ConditionType = 
  // URL相关
  | 'url' | 'url_host' | 'url_path' | 'url_query' | 'url_fragment'
  // HTTP相关
  | 'method' | 'scheme' | 'port'
  // 请求相关
  | 'request_header' | 'request_body' | 'request_size' | 'content_type'
  // 响应相关  
  | 'response_status' | 'response_header' | 'response_body' | 'response_size'
  // 文件类型
  | 'file_extension' | 'mime_type'
  // 时间相关
  | 'time_of_day' | 'day_of_week'
  // 其他
  | 'client_ip' | 'server_ip' | 'user_agent'

// 操作符类型
export type ConditionOperator = 
  // 文本匹配
  | 'equals' | 'not_equals' | 'contains' | 'not_contains'
  | 'starts_with' | 'ends_with' | 'regex' | 'not_regex'
  // 数值比较
  | 'greater_than' | 'less_than' | 'between'
  // 存在性检查
  | 'exists' | 'not_exists' | 'is_empty' | 'not_empty'
  // 列表匹配
  | 'in_list' | 'not_in_list'

export interface InterceptCondition {
  type: ConditionType
  operator: ConditionOperator
  value: string | number | string[]
  value2?: string | number // 用于 between 操作符
  caseSensitive?: boolean
  negate?: boolean // 取反
  headerName?: string // 当type为header时，指定header名称
}

// 动作类型
export type ActionType = 
  // 请求控制
  | 'block' | 'allow' | 'redirect' | 'auto_respond'
  // 请求修改
  | 'modify_url' | 'modify_method' | 'modify_headers' | 'modify_body'
  // 响应修改
  | 'modify_status' | 'modify_response_headers' | 'modify_response_body'
  // 流量控制
  | 'delay' | 'timeout' | 'bandwidth_limit'
  // 调试相关
  | 'breakpoint' | 'log'

export interface InterceptAction {
  type: ActionType
  parameters: ActionParameters
  enabled?: boolean
  description?: string
}

// 动作参数类型
export interface ActionParameters {
  // 阻止/允许
  message?: string
  statusCode?: number
  
  // 重定向
  url?: string
  preserveQuery?: boolean
  
  // 自动响应
  response?: {
    status: number
    headers?: Record<string, string>
    body?: string
    contentType?: string
  }
  
  // URL修改
  newUrl?: string
  urlPattern?: string
  replacement?: string
  
  // 方法修改
  newMethod?: string
  
  // 头部修改
  headers?: {
    add?: Record<string, string>
    modify?: Record<string, string>
    remove?: string[]
  }
  
  // 请求体修改
  body?: string
  bodyPattern?: string
  bodyReplacement?: string
  
  // 响应修改
  responseHeaders?: {
    add?: Record<string, string>
    modify?: Record<string, string>
    remove?: string[]
  }
  responseBody?: string
  responseBodyPattern?: string
  responseBodyReplacement?: string
  
  // 延迟
  milliseconds?: number
  randomDelay?: boolean
  minDelay?: number
  maxDelay?: number
  
  // 超时
  timeoutMs?: number
  
  // 带宽限制
  bytesPerSecond?: number
  
  // 断点
  breakOnRequest?: boolean
  breakOnResponse?: boolean
  
  // 日志
  logLevel?: 'info' | 'warn' | 'error'
  logMessage?: string
  
  // 其他
  [key: string]: any
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
