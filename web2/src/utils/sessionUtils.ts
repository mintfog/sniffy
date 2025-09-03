import { HttpSession, WebSocketSession } from '@/types'

type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

export const formatDuration = (duration?: number) => {
  if (!duration) return '-'
  if (duration < 1000) return `${duration}ms`
  return `${(duration / 1000).toFixed(2)}s`
}

export const formatSize = (size: number) => {
  if (size < 1024) return `${size}B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)}KB`
  return `${(size / (1024 * 1024)).toFixed(1)}MB`
}

export const getStatusColor = (session: UnifiedSession) => {
  if (session.sessionType === 'websocket') {
    const wsSession = session as WebSocketSession & { sessionType: 'websocket' }
    switch (wsSession.status) {
      case 'connecting': return 'text-yellow-600 bg-yellow-50'
      case 'connected': return 'text-green-600 bg-green-50'
      case 'disconnected': return 'text-gray-600 bg-gray-50'
      case 'error': return 'text-red-600 bg-red-50'
      default: return 'text-gray-600 bg-gray-50'
    }
  } else {
    const httpSession = session as HttpSession & { sessionType: 'http' }
    if (httpSession.status === 'pending') return 'text-yellow-600 bg-yellow-50'
    if (httpSession.status === 'error') return 'text-red-600 bg-red-50'
    if (!httpSession.response) return 'text-gray-600 bg-gray-50'
    
    const status = httpSession.response.status
    if (status >= 200 && status < 300) return 'text-green-600 bg-green-50'
    if (status >= 300 && status < 400) return 'text-blue-600 bg-blue-50'
    if (status >= 400 && status < 500) return 'text-orange-600 bg-orange-50'
    return 'text-red-600 bg-red-50'
  }
}

export const getMethodColor = (method: string) => {
  switch (method) {
    case 'GET': return 'text-green-700 bg-green-100'
    case 'POST': return 'text-blue-700 bg-blue-100'
    case 'PUT': return 'text-orange-700 bg-orange-100'
    case 'DELETE': return 'text-red-700 bg-red-100'
    case 'PATCH': return 'text-purple-700 bg-purple-100'
    default: return 'text-gray-700 bg-gray-100'
  }
}

export const generateCurlCommand = (session: HttpSession & { sessionType: 'http' }) => {
  let curl = `curl -X ${session.request.method}`
  
  // 添加头部
  Object.entries(session.request.headers).forEach(([key, value]) => {
    curl += ` \\\n  -H "${key}: ${value}"`
  })
  
  // 添加请求体
  if (session.request.body) {
    curl += ` \\\n  -d '${session.request.body}'`
  }
  
  // 添加URL
  curl += ` \\\n  "${session.request.url}"`
  
  return curl
}

export const exportSessionData = (session: UnifiedSession) => {
  const data = JSON.stringify(session, null, 2)
  const blob = new Blob([data], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `session-${session.id}.json`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

export const generateRawRequest = (session: HttpSession) => {
  let raw = `${session.request.method} ${session.request.path} ${session.request.protocol}\r\n`
  
  if (session.request.serverIP && session.request.serverPort) {
    raw += `# Remote Address: ${session.request.serverIP}:${session.request.serverPort}\r\n`
  }
  
  Object.entries(session.request.headers).forEach(([key, value]) => {
    raw += `${key}: ${value}\r\n`
  })
  raw += '\r\n'
  if (session.request.body) {
    raw += session.request.body
  }
  return raw
}

export const generateRawResponse = (session: HttpSession) => {
  if (!session.response) return ''
  let raw = `${session.request.protocol} ${session.response.status} ${session.response.statusText}\r\n`
  Object.entries(session.response.headers).forEach(([key, value]) => {
    raw += `${key}: ${value}\r\n`
  })
  raw += '\r\n'
  if (session.response.body) {
    raw += session.response.body
  }
  return raw
}

export const getContentType = (headers: Record<string, string>) => {
  return headers['content-type'] || headers['Content-Type'] || ''
}

export const canPreview = (contentType: string) => {
  return contentType.includes('text/html') || 
         contentType.includes('application/json') || 
         contentType.includes('application/xml') || 
         contentType.includes('text/xml') ||
         contentType.includes('image/')
}

export const formatJson = (content: string) => {
  try {
    const parsed = JSON.parse(content)
    return JSON.stringify(parsed, null, 2)
  } catch {
    return content
  }
}
