// 模拟网络请求数据
export const mockRequests = [
  {
    id: '1',
    method: 'GET',
    url: 'https://api.example.com/users',
    domain: 'api.example.com',
    path: '/users',
    status: 200,
    statusText: 'OK',
    type: 'xhr',
    size: '2.1 kB',
    time: '156ms',
    timestamp: new Date(Date.now() - 1000 * 60 * 5),
    headers: {
      request: {
        'Accept': 'application/json',
        'Authorization': 'Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...',
        'Content-Type': 'application/json',
        'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)',
        'X-Requested-With': 'XMLHttpRequest'
      },
      response: {
        'Content-Type': 'application/json; charset=utf-8',
        'Access-Control-Allow-Origin': '*',
        'Cache-Control': 'no-cache',
        'Content-Length': '2147',
        'Date': 'Thu, 15 Nov 2023 10:30:00 GMT',
        'Server': 'nginx/1.18.0'
      }
    },
    requestBody: null,
    responseBody: JSON.stringify({
      users: [
        { id: 1, name: '张三', email: 'zhangsan@example.com' },
        { id: 2, name: '李四', email: 'lisi@example.com' }
      ],
      total: 2
    }, null, 2),
    intercepted: false
  },
  {
    id: '2',
    method: 'POST',
    url: 'https://api.example.com/users',
    domain: 'api.example.com',
    path: '/users',
    status: 201,
    statusText: 'Created',
    type: 'xhr',
    size: '345 B',
    time: '89ms',
    timestamp: new Date(Date.now() - 1000 * 60 * 3),
    headers: {
      request: {
        'Accept': 'application/json',
        'Authorization': 'Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...',
        'Content-Type': 'application/json',
        'Content-Length': '67'
      },
      response: {
        'Content-Type': 'application/json; charset=utf-8',
        'Location': '/users/3',
        'Content-Length': '345'
      }
    },
    requestBody: JSON.stringify({
      name: '王五',
      email: 'wangwu@example.com'
    }, null, 2),
    responseBody: JSON.stringify({
      id: 3,
      name: '王五',
      email: 'wangwu@example.com',
      created_at: '2023-11-15T10:32:00Z'
    }, null, 2),
    intercepted: true
  },
  {
    id: '3',
    method: 'GET',
    url: 'https://cdn.example.com/assets/style.css',
    domain: 'cdn.example.com',
    path: '/assets/style.css',
    status: 200,
    statusText: 'OK',
    type: 'stylesheet',
    size: '45.2 kB',
    time: '234ms',
    timestamp: new Date(Date.now() - 1000 * 60 * 8),
    headers: {
      request: {
        'Accept': 'text/css,*/*;q=0.1',
        'Accept-Encoding': 'gzip, deflate, br',
        'Cache-Control': 'max-age=0'
      },
      response: {
        'Content-Type': 'text/css',
        'Content-Encoding': 'gzip',
        'Cache-Control': 'public, max-age=31536000',
        'ETag': '"abc123def456"'
      }
    },
    requestBody: null,
    responseBody: '/* CSS content */\nbody { margin: 0; padding: 0; }',
    intercepted: false
  },
  {
    id: '4',
    method: 'GET',
    url: 'https://api.example.com/orders/123',
    domain: 'api.example.com',
    path: '/orders/123',
    status: 404,
    statusText: 'Not Found',
    type: 'xhr',
    size: '156 B',
    time: '45ms',
    timestamp: new Date(Date.now() - 1000 * 60),
    headers: {
      request: {
        'Accept': 'application/json',
        'Authorization': 'Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...'
      },
      response: {
        'Content-Type': 'application/json',
        'Content-Length': '156'
      }
    },
    requestBody: null,
    responseBody: JSON.stringify({
      error: 'Order not found',
      code: 'ORDER_NOT_FOUND'
    }, null, 2),
    intercepted: false
  },
  {
    id: '5',
    method: 'PUT',
    url: 'https://api.example.com/users/2',
    domain: 'api.example.com',
    path: '/users/2',
    status: 500,
    statusText: 'Internal Server Error',
    type: 'xhr',
    size: '234 B',
    time: '1.2s',
    timestamp: new Date(Date.now() - 1000 * 30),
    headers: {
      request: {
        'Accept': 'application/json',
        'Content-Type': 'application/json',
        'Authorization': 'Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...'
      },
      response: {
        'Content-Type': 'application/json',
        'Content-Length': '234'
      }
    },
    requestBody: JSON.stringify({
      name: '李四修改',
      email: 'lisi_new@example.com'
    }, null, 2),
    responseBody: JSON.stringify({
      error: 'Database connection failed',
      code: 'DB_CONNECTION_ERROR',
      timestamp: '2023-11-15T10:35:00Z'
    }, null, 2),
    intercepted: false
  }
];

// 拦截器规则
export const mockInterceptRules = [
  {
    id: '1',
    name: '用户API拦截',
    enabled: true,
    condition: {
      url: '**/users**',
      method: 'POST'
    },
    action: {
      type: 'modify_response',
      statusCode: 200,
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        id: 999,
        name: '拦截测试用户',
        email: 'intercepted@example.com'
      })
    },
    created: new Date(Date.now() - 1000 * 60 * 60 * 24)
  },
  {
    id: '2',
    name: '延迟测试',
    enabled: false,
    condition: {
      url: '**/api/**',
      method: '*'
    },
    action: {
      type: 'delay',
      delay: 2000
    },
    created: new Date(Date.now() - 1000 * 60 * 60 * 12)
  }
];

// 请求类型图标映射
export const getRequestTypeIcon = (type) => {
  const icons = {
    'xhr': '📡',
    'fetch': '📡',
    'document': '📄',
    'stylesheet': '🎨',
    'script': '⚙️',
    'image': '🖼️',
    'font': '🔤',
    'websocket': '🔌',
    'other': '📦'
  };
  return icons[type] || icons.other;
};

// 状态码颜色类名
export const getStatusColorClass = (status) => {
  if (status >= 200 && status < 300) return 'status-200';
  if (status >= 300 && status < 400) return 'status-300';
  if (status >= 400 && status < 500) return 'status-400';
  if (status >= 500) return 'status-500';
  return '';
};