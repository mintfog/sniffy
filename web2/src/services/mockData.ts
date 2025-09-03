import { 
  HttpSession, 
  WebSocketSession, 
  Statistics, 
  SniffyConfig,
  InterceptRule,
  InterceptStats,
  InterceptHistory
} from '@/types'

// 模拟 HTTP 会话数据
export const mockHttpSessions: HttpSession[] = [
  {
    id: 'session-1',
    request: {
      id: 'req-1',
      method: 'GET',
      url: 'https://api.github.com/user',
      headers: {
        'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
        'Accept': 'application/json',
        'Authorization': 'Bearer token123'
      },
      timestamp: new Date(Date.now() - 5000).toISOString(),
      clientIP: '192.168.1.100',
      host: 'api.github.com',
      path: '/user',
      protocol: 'HTTPS/1.1',
      userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
      serverIP: '140.82.114.4',
      serverPort: 443
    },
    response: {
      id: 'res-1',
      requestId: 'req-1',
      status: 200,
      statusText: 'OK',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': '1024',
        'Cache-Control': 'max-age=300'
      },
      body: JSON.stringify({
        login: 'testuser',
        id: 12345,
        avatar_url: 'https://avatars.githubusercontent.com/u/12345',
        name: 'Test User'
      }),
      timestamp: new Date(Date.now() - 4800).toISOString(),
      size: 1024,
      responseTime: 200
    },
    duration: 200,
    status: 'completed'
  },
  {
    id: 'session-2',
    request: {
      id: 'req-2',
      method: 'POST',
      url: 'https://api.example.com/login',
      headers: {
        'Content-Type': 'application/json',
        'User-Agent': 'Chrome/91.0.4472.124'
      },
      body: JSON.stringify({ username: 'user', password: 'pass' }),
      timestamp: new Date(Date.now() - 3000).toISOString(),
      clientIP: '192.168.1.101',
      host: 'api.example.com',
      path: '/login',
      protocol: 'HTTPS/1.1',
      serverIP: '203.0.113.1',
      serverPort: 443
    },
    response: {
      id: 'res-2',
      requestId: 'req-2',
      status: 401,
      statusText: 'Unauthorized',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': '64'
      },
      body: JSON.stringify({ error: 'Invalid credentials' }),
      timestamp: new Date(Date.now() - 2900).toISOString(),
      size: 64,
      responseTime: 100
    },
    duration: 100,
    status: 'completed'
  },
  {
    id: 'session-3',
    request: {
      id: 'req-3',
      method: 'GET',
      url: 'https://jsonplaceholder.typicode.com/posts/1',
      headers: {
        'Accept': 'application/json',
        'User-Agent': 'fetch/1.0'
      },
      timestamp: new Date(Date.now() - 2000).toISOString(),
      clientIP: '192.168.1.102',
      host: 'jsonplaceholder.typicode.com',
      path: '/posts/1',
      protocol: 'HTTPS/1.1',
      serverIP: '104.16.88.129',
      serverPort: 443
    },
    response: {
      id: 'res-3',
      requestId: 'req-3',
      status: 200,
      statusText: 'OK',
      headers: {
        'Content-Type': 'application/json; charset=utf-8',
        'Content-Length': '292'
      },
      body: JSON.stringify({
        userId: 1,
        id: 1,
        title: 'sunt aut facere repellat provident',
        body: 'quia et suscipit suscipit recusandae...'
      }),
      timestamp: new Date(Date.now() - 1850).toISOString(),
      size: 292,
      responseTime: 150
    },
    duration: 150,
    status: 'completed'
  },
  {
    id: 'session-4',
    request: {
      id: 'req-4',
      method: 'PUT',
      url: 'https://api.example.com/users/123',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer abc123'
      },
      body: JSON.stringify({ name: 'Updated Name', email: 'new@example.com' }),
      timestamp: new Date(Date.now() - 1000).toISOString(),
      clientIP: '192.168.1.103',
      host: 'api.example.com',
      path: '/users/123',
      protocol: 'HTTPS/1.1',
      serverIP: '203.0.113.1',
      serverPort: 443
    },
    response: {
      id: 'res-4',
      requestId: 'req-4',
      status: 500,
      statusText: 'Internal Server Error',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': '128'
      },
      body: JSON.stringify({ error: 'Database connection failed' }),
      timestamp: new Date(Date.now() - 500).toISOString(),
      size: 128,
      responseTime: 500
    },
    duration: 500,
    status: 'completed'
  },
  {
    id: 'session-5',
    request: {
      id: 'req-5',
      method: 'DELETE',
      url: 'https://api.example.com/posts/456',
      headers: {
        'Authorization': 'Bearer xyz789',
        'User-Agent': 'MyApp/1.0'
      },
      timestamp: new Date(Date.now() - 500).toISOString(),
      clientIP: '192.168.1.104',
      host: 'api.example.com',
      path: '/posts/456',
      protocol: 'HTTPS/1.1',
      serverIP: '203.0.113.1',
      serverPort: 443
    },
    status: 'pending'
  }
]

// 模拟 WebSocket 会话数据
export const mockWebSocketSessions: WebSocketSession[] = [
  {
    id: 'ws-1',
    url: 'wss://echo.websocket.org',
    status: 'connected',
    startTime: new Date(Date.now() - 30000).toISOString(),
    messageCount: 5,
    totalSize: 1024,
    messages: [
      {
        id: 'msg-1',
        sessionId: 'ws-1',
        direction: 'outbound',
        type: 'text',
        data: 'Hello WebSocket!',
        timestamp: new Date(Date.now() - 25000).toISOString(),
        size: 16
      },
      {
        id: 'msg-2',
        sessionId: 'ws-1',
        direction: 'inbound',
        type: 'text',
        data: 'Hello WebSocket!',
        timestamp: new Date(Date.now() - 24500).toISOString(),
        size: 16
      },
      {
        id: 'msg-3',
        sessionId: 'ws-1',
        direction: 'outbound',
        type: 'text',
        data: JSON.stringify({ type: 'ping', timestamp: Date.now() }),
        timestamp: new Date(Date.now() - 20000).toISOString(),
        size: 45
      },
      {
        id: 'msg-4',
        sessionId: 'ws-1',
        direction: 'inbound',
        type: 'text',
        data: JSON.stringify({ type: 'pong', timestamp: Date.now() }),
        timestamp: new Date(Date.now() - 19500).toISOString(),
        size: 45
      },
      {
        id: 'msg-5',
        sessionId: 'ws-1',
        direction: 'outbound',
        type: 'binary',
        data: '[Binary Data]',
        timestamp: new Date(Date.now() - 10000).toISOString(),
        size: 902
      }
    ]
  },
  {
    id: 'ws-2',
    url: 'wss://api.example.com/realtime',
    status: 'disconnected',
    startTime: new Date(Date.now() - 60000).toISOString(),
    endTime: new Date(Date.now() - 5000).toISOString(),
    messageCount: 12,
    totalSize: 2048,
    messages: []
  },
  {
    id: 'ws-3',
    url: 'wss://stream.example.com/data',
    status: 'connecting',
    startTime: new Date(Date.now() - 1000).toISOString(),
    messageCount: 0,
    totalSize: 0,
    messages: []
  }
]

// 模拟统计数据
export const mockStatistics: Statistics = {
  totalRequests: 1234,
  totalSessions: 567,
  totalBytes: 15728640, // 15MB
  requestsPerSecond: 12.5,
  averageResponseTime: 234,
  statusCodeDistribution: {
    200: 856,
    301: 45,
    404: 123,
    401: 67,
    500: 89,
    502: 23,
    503: 31
  },
  methodDistribution: {
    GET: 789,
    POST: 234,
    PUT: 89,
    DELETE: 45,
    PATCH: 34,
    OPTIONS: 43
  },
  topHosts: [
    { host: 'api.github.com', count: 345 },
    { host: 'api.example.com', count: 234 },
    { host: 'jsonplaceholder.typicode.com', count: 189 },
    { host: 'httpbin.org', count: 156 },
    { host: 'api.openai.com', count: 123 }
  ]
}

// 模拟配置数据
export const mockConfig: SniffyConfig = {
  port: 8080,
  host: '0.0.0.0',
  enableHTTPS: true,
  caCertPath: '/path/to/ca.crt',
  recording: true,
  filters: {
    method: ['GET', 'POST'],
    status: [200, 404, 500]
  },
  plugins: [
    {
      name: 'logger',
      enabled: true,
      config: {
        logLevel: 'info',
        logFile: '/var/log/sniffy.log'
      }
    },
    {
      name: 'connection_monitor',
      enabled: true,
      config: {
        maxConnections: 1000,
        timeout: 30000
      }
    },
    {
      name: 'request_modifier',
      enabled: false,
      config: {
        modifyHeaders: true,
        blockPatterns: ['*.ads.com']
      }
    },
    {
      name: 'websocket_logger',
      enabled: true,
      config: {
        logMessages: true,
        maxMessageSize: 1024
      }
    }
  ]
}

// 模拟拦截规则数据
export const mockInterceptRules: InterceptRule[] = [
  {
    id: 'rule-1',
    name: '阻止广告请求',
    enabled: true,
    priority: 1,
    conditions: [
      {
        type: 'url',
        operator: 'contains',
        value: 'ads.',
        caseSensitive: false
      },
      {
        type: 'url',
        operator: 'contains', 
        value: 'analytics',
        caseSensitive: false
      }
    ],
    actions: [
      {
        type: 'block',
        parameters: {
          message: '广告请求已被阻止'
        }
      }
    ],
    createdAt: new Date(Date.now() - 86400000).toISOString(),
    updatedAt: new Date(Date.now() - 3600000).toISOString()
  },
  {
    id: 'rule-2',
    name: '修改User-Agent',
    enabled: true,
    priority: 2,
    conditions: [
      {
        type: 'url',
        operator: 'starts_with',
        value: 'https://api.github.com'
      }
    ],
    actions: [
      {
        type: 'modify_request',
        parameters: {
          headers: {
            'User-Agent': 'Sniffy-Proxy/1.0'
          }
        }
      }
    ],
    createdAt: new Date(Date.now() - 172800000).toISOString(),
    updatedAt: new Date(Date.now() - 7200000).toISOString()
  },
  {
    id: 'rule-3',
    name: '慢网络模拟',
    enabled: false,
    priority: 3,
    conditions: [
      {
        type: 'url',
        operator: 'contains',
        value: 'example.com'
      }
    ],
    actions: [
      {
        type: 'delay',
        parameters: {
          milliseconds: 2000
        }
      }
    ],
    createdAt: new Date(Date.now() - 259200000).toISOString(),
    updatedAt: new Date(Date.now() - 259200000).toISOString()
  },
  {
    id: 'rule-4',
    name: '重定向测试请求',
    enabled: true,
    priority: 4,
    conditions: [
      {
        type: 'url',
        operator: 'equals',
        value: 'https://httpbin.org/get'
      }
    ],
    actions: [
      {
        type: 'redirect',
        parameters: {
          url: 'https://jsonplaceholder.typicode.com/posts/1'
        }
      }
    ],
    createdAt: new Date(Date.now() - 345600000).toISOString(),
    updatedAt: new Date(Date.now() - 14400000).toISOString()
  },
  {
    id: 'rule-5',
    name: '修改API响应',
    enabled: false,
    priority: 5,
    conditions: [
      {
        type: 'url',
        operator: 'regex',
        value: 'api\\.example\\.com/users/\\d+'
      },
      {
        type: 'method',
        operator: 'equals',
        value: 'GET'
      }
    ],
    actions: [
      {
        type: 'modify_response',
        parameters: {
          headers: {
            'X-Modified-By': 'Sniffy'
          },
          body: JSON.stringify({
            id: 123,
            name: 'Modified User',
            email: 'modified@example.com'
          })
        }
      }
    ],
    createdAt: new Date(Date.now() - 432000000).toISOString(),
    updatedAt: new Date(Date.now() - 21600000).toISOString()
  }
]

// 模拟拦截统计数据
export const mockInterceptStats: InterceptStats = {
  totalRules: 5,
  activeRules: 3,
  totalInterceptions: 247,
  blockedRequests: 89,
  modifiedRequests: 134,
  modifiedResponses: 24
}

// 模拟拦截历史数据
export const mockInterceptHistory: InterceptHistory[] = [
  {
    id: 'history-1',
    sessionId: 'session-1',
    ruleId: 'rule-2',
    ruleName: '修改User-Agent',
    action: 'modify_request',
    timestamp: new Date(Date.now() - 5000).toISOString(),
    details: {
      originalHeaders: {
        'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
      },
      modifiedHeaders: {
        'User-Agent': 'Sniffy-Proxy/1.0'
      }
    }
  },
  {
    id: 'history-2',
    sessionId: 'session-6',
    ruleId: 'rule-1',
    ruleName: '阻止广告请求',
    action: 'block',
    timestamp: new Date(Date.now() - 15000).toISOString(),
    details: {
      url: 'https://ads.example.com/banner.js',
      reason: '广告请求已被阻止'
    }
  },
  {
    id: 'history-3',
    sessionId: 'session-7',
    ruleId: 'rule-4',
    ruleName: '重定向测试请求',
    action: 'redirect',
    timestamp: new Date(Date.now() - 25000).toISOString(),
    details: {
      originalUrl: 'https://httpbin.org/get',
      redirectUrl: 'https://jsonplaceholder.typicode.com/posts/1'
    }
  },
  {
    id: 'history-4',
    sessionId: 'session-8',
    ruleId: 'rule-3',
    ruleName: '慢网络模拟',
    action: 'delay',
    timestamp: new Date(Date.now() - 35000).toISOString(),
    details: {
      delayMs: 2000,
      url: 'https://api.example.com/data'
    }
  },
  {
    id: 'history-5',
    sessionId: 'session-9',
    ruleId: 'rule-5',
    ruleName: '修改API响应',
    action: 'modify_response',
    timestamp: new Date(Date.now() - 45000).toISOString(),
    details: {
      originalStatus: 200,
      modifiedStatus: 200,
      modifiedHeaders: {
        'X-Modified-By': 'Sniffy'
      },
      bodyModified: true
    }
  }
]
