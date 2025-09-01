import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { HttpSession } from '@/types'

dayjs.extend(relativeTime)

/**
 * 格式化文件大小
 */
export function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B'
  
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

/**
 * 格式化持续时间
 */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(2)}s`
  if (ms < 3600000) return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
  
  const hours = Math.floor(ms / 3600000)
  const minutes = Math.floor((ms % 3600000) / 60000)
  const seconds = Math.floor((ms % 60000) / 1000)
  
  return `${hours}h ${minutes}m ${seconds}s`
}

/**
 * 格式化时间
 */
export function formatTime(timestamp: string, format = 'HH:mm:ss.SSS'): string {
  return dayjs(timestamp).format(format)
}

/**
 * 格式化相对时间
 */
export function formatRelativeTime(timestamp: string): string {
  return dayjs(timestamp).fromNow()
}

/**
 * 获取状态码颜色类名
 */
export function getStatusCodeColor(status: number): string {
  if (status >= 200 && status < 300) return 'text-green-700 bg-green-100'
  if (status >= 300 && status < 400) return 'text-blue-700 bg-blue-100'  
  if (status >= 400 && status < 500) return 'text-orange-700 bg-orange-100'
  if (status >= 500) return 'text-red-700 bg-red-100'
  return 'text-gray-700 bg-gray-100'
}

/**
 * 获取 HTTP 方法颜色类名
 */
export function getMethodColor(method: string): string {
  switch (method.toUpperCase()) {
    case 'GET': return 'text-green-700 bg-green-100'
    case 'POST': return 'text-blue-700 bg-blue-100'
    case 'PUT': return 'text-orange-700 bg-orange-100'
    case 'DELETE': return 'text-red-700 bg-red-100'
    case 'PATCH': return 'text-purple-700 bg-purple-100'
    case 'OPTIONS': return 'text-gray-700 bg-gray-100'
    case 'HEAD': return 'text-indigo-700 bg-indigo-100'
    default: return 'text-gray-700 bg-gray-100'
  }
}

/**
 * 获取内容类型图标
 */
export function getContentTypeIcon(contentType: string): string {
  if (contentType.includes('json')) return '🔧'
  if (contentType.includes('html')) return '🌐'
  if (contentType.includes('css')) return '🎨'
  if (contentType.includes('javascript')) return '⚡'
  if (contentType.includes('image')) return '🖼️'
  if (contentType.includes('video')) return '🎥'
  if (contentType.includes('audio')) return '🔊'
  if (contentType.includes('pdf')) return '📄'
  if (contentType.includes('xml')) return '📋'
  return '📄'
}

/**
 * 解析 URL
 */
export function parseUrl(url: string) {
  try {
    const urlObj = new URL(url)
    return {
      protocol: urlObj.protocol,
      hostname: urlObj.hostname,
      port: urlObj.port,
      pathname: urlObj.pathname,
      search: urlObj.search,
      hash: urlObj.hash,
      origin: urlObj.origin,
    }
  } catch {
    return null
  }
}

/**
 * 深度克隆对象
 */
export function deepClone<T>(obj: T): T {
  if (obj === null || typeof obj !== 'object') return obj
  if (obj instanceof Date) return new Date(obj.getTime()) as unknown as T
  if (obj instanceof Array) return obj.map(item => deepClone(item)) as unknown as T
  if (typeof obj === 'object') {
    const clonedObj = {} as T
    Object.keys(obj).forEach(key => {
      (clonedObj as any)[key] = deepClone((obj as any)[key])
    })
    return clonedObj
  }
  return obj
}

/**
 * 防抖函数
 */
export function debounce<T extends (...args: any[]) => any>(
  func: T,
  wait: number
): (...args: Parameters<T>) => void {
  let timeout: ReturnType<typeof setTimeout>
  return function(...args: Parameters<T>) {
    clearTimeout(timeout)
    timeout = setTimeout(() => func.apply(null, args), wait)
  }
}

/**
 * 节流函数
 */
export function throttle<T extends (...args: any[]) => any>(
  func: T,
  limit: number
): (...args: Parameters<T>) => void {
  let inThrottle: boolean
  return function(...args: Parameters<T>) {
    if (!inThrottle) {
      func.apply(null, args)
      inThrottle = true
      setTimeout(() => (inThrottle = false), limit)
    }
  }
}

/**
 * 生成随机 ID
 */
export function generateId(): string {
  return Math.random().toString(36).substr(2, 9)
}

/**
 * 检查是否为有效的 JSON
 */
export function isValidJson(str: string): boolean {
  try {
    JSON.parse(str)
    return true
  } catch {
    return false
  }
}

/**
 * 美化 JSON 字符串
 */
export function prettifyJson(str: string): string {
  try {
    return JSON.stringify(JSON.parse(str), null, 2)
  } catch {
    return str
  }
}

/**
 * 过滤会话数据
 */
export function filterSessions(
  sessions: HttpSession[],
  filters: {
    searchTerm?: string
    method?: string[]
    status?: number[]
    host?: string[]
    contentType?: string[]
  }
): HttpSession[] {
  return sessions.filter(session => {
    // 搜索词过滤
    if (filters.searchTerm) {
      const term = filters.searchTerm.toLowerCase()
      const matchUrl = session.request.url.toLowerCase().includes(term)
      const matchMethod = session.request.method.toLowerCase().includes(term)
      const matchHost = session.request.host.toLowerCase().includes(term)
      const matchHeaders = Object.values(session.request.headers).some(header =>
        header.toLowerCase().includes(term)
      )
      
      if (!matchUrl && !matchMethod && !matchHost && !matchHeaders) {
        return false
      }
    }

    // HTTP 方法过滤
    if (filters.method && filters.method.length > 0) {
      if (!filters.method.includes(session.request.method)) {
        return false
      }
    }

    // 状态码过滤
    if (filters.status && filters.status.length > 0) {
      if (!session.response || !filters.status.includes(session.response.status)) {
        return false
      }
    }

    // 主机过滤
    if (filters.host && filters.host.length > 0) {
      if (!filters.host.includes(session.request.host)) {
        return false
      }
    }

    // 内容类型过滤
    if (filters.contentType && filters.contentType.length > 0) {
      const responseContentType = session.response?.headers['content-type'] || ''
      const hasMatchingContentType = filters.contentType.some(type =>
        responseContentType.toLowerCase().includes(type.toLowerCase())
      )
      if (!hasMatchingContentType) {
        return false
      }
    }

    return true
  })
}

/**
 * 导出会话为 HAR 格式
 */
export function exportToHAR(sessions: HttpSession[]): string {
  const har = {
    log: {
      version: '1.2',
      creator: {
        name: 'Sniffy',
        version: '1.0.0',
      },
      entries: sessions.map(session => ({
        startedDateTime: session.request.timestamp,
        time: session.duration || 0,
        request: {
          method: session.request.method,
          url: session.request.url,
          httpVersion: session.request.protocol || 'HTTP/1.1',
          headers: Object.entries(session.request.headers).map(([name, value]) => ({
            name,
            value,
          })),
          queryString: [],
          postData: session.request.body
            ? {
                mimeType: session.request.headers['content-type'] || 'text/plain',
                text: session.request.body,
              }
            : undefined,
          headersSize: -1,
          bodySize: session.request.body?.length || 0,
        },
        response: session.response
          ? {
              status: session.response.status,
              statusText: session.response.statusText,
              httpVersion: 'HTTP/1.1',
              headers: Object.entries(session.response.headers).map(([name, value]) => ({
                name,
                value,
              })),
              content: {
                size: session.response.size,
                mimeType: session.response.headers['content-type'] || 'text/plain',
                text: session.response.body || '',
              },
              headersSize: -1,
              bodySize: session.response.size,
            }
          : {
              status: 0,
              statusText: '',
              httpVersion: 'HTTP/1.1',
              headers: [],
              content: { size: 0, mimeType: '', text: '' },
              headersSize: -1,
              bodySize: 0,
            },
        cache: {},
        timings: {
          send: 0,
          wait: session.duration || 0,
          receive: 0,
        },
      })),
    },
  }

  return JSON.stringify(har, null, 2)
}
