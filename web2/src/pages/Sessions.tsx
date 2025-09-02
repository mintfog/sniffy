import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Clock, Globe, Zap, Filter, MoreHorizontal, MessageSquare, ArrowUp, ArrowDown } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { useAppStore } from '@/store'
import { HttpSession, WebSocketSession } from '@/types'
import { ExpandableCell } from '@/components/ui'
import clsx from 'clsx'
import dayjs from 'dayjs'
import { formatDuration } from '@/utils'

type SessionType = 'all' | 'http' | 'websocket'
type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

export function Sessions() {
  const { sessions, webSocketSessions, selectedSessionId, setSelectedSession } = useAppStore()
  const [page] = useState(1)
  const [pageSize] = useState(50)
  const [sessionType, setSessionType] = useState<SessionType>('all')

  // 获取会话列表
  const { isLoading } = useQuery({
    queryKey: ['sessions', page, pageSize],
    queryFn: () => sniffyApi.getSessions({ page, pageSize }),
    refetchInterval: 2000, // 每2秒刷新
  })

  // 合并HTTP和WebSocket会话
  const unifiedSessions: UnifiedSession[] = [
    ...sessions.map(s => ({ ...s, sessionType: 'http' as const })),
    ...webSocketSessions.map(s => ({ ...s, sessionType: 'websocket' as const }))
  ]

  // 根据类型过滤会话
  const filteredSessions = unifiedSessions.filter(session => {
    if (sessionType === 'all') return true
    return session.sessionType === sessionType
  })

  // 按时间排序
  const sortedSessions = [...filteredSessions].sort((a, b) => {
    const aTime = a.sessionType === 'http' ? a.request.timestamp : a.startTime
    const bTime = b.sessionType === 'http' ? b.request.timestamp : b.startTime
    return new Date(bTime).getTime() - new Date(aTime).getTime()
  })

  const getStatusColor = (session: UnifiedSession) => {
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

  const getMethodColor = (method: string) => {
    switch (method) {
      case 'GET': return 'text-green-700 bg-green-100'
      case 'POST': return 'text-blue-700 bg-blue-100'
      case 'PUT': return 'text-orange-700 bg-orange-100'
      case 'DELETE': return 'text-red-700 bg-red-100'
      case 'PATCH': return 'text-purple-700 bg-purple-100'
      default: return 'text-gray-700 bg-gray-100'
    }
  }

  const formatDuration = (duration?: number) => {
    if (!duration) return '-'
    if (duration < 1000) return `${duration}ms`
    return `${(duration / 1000).toFixed(2)}s`
  }

  const formatSize = (size: number) => {
    if (size < 1024) return `${size}B`
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)}KB`
    return `${(size / (1024 * 1024)).toFixed(1)}MB`
  }

  return (
    <div className="flex h-[calc(100vh-8rem)] rounded-lg overflow-hidden border border-gray-200">
      {/* 会话列表 */}
      <div className="w-2/3 bg-white border-r border-gray-200 flex flex-col">
        {/* 列表头部 */}
        <div className="border-b border-gray-200 px-6 py-4 flex-shrink-0">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-semibold text-gray-900">网络会话</h2>
            <div className="flex items-center space-x-2">
              <span className="text-sm text-gray-500">
                共 {sortedSessions.length} 个会话
              </span>
              <button className="p-2 hover:bg-gray-100 rounded-md">
                <Filter className="h-4 w-4 text-gray-400" />
              </button>
            </div>
          </div>
          
          {/* 会话类型过滤 */}
          <div className="flex space-x-2">
            {[
              { value: 'all', label: '全部', icon: Globe },
              { value: 'http', label: 'HTTP', icon: Globe },
              { value: 'websocket', label: 'WebSocket', icon: MessageSquare },
            ].map((option) => {
              const Icon = option.icon
              return (
                <button
                  key={option.value}
                  onClick={() => setSessionType(option.value as SessionType)}
                  className={clsx(
                    'flex items-center px-3 py-2 text-sm rounded-md transition-colors',
                    sessionType === option.value
                      ? 'bg-primary-100 text-primary-700'
                      : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                  )}
                >
                  <Icon className="h-4 w-4 mr-2" />
                  {option.label}
                </button>
              )
            })}
          </div>
        </div>

        {/* 会话列表 */}
        <div className="flex-1 overflow-auto">
          {isLoading ? (
            <div className="flex items-center justify-center h-32">
              <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
            </div>
          ) : (
            <div className="divide-y divide-gray-100">
              {sortedSessions.map((session) => (
                <div
                  key={session.id}
                  onClick={() => setSelectedSession(session.id)}
                  className={clsx(
                    'px-6 py-4 hover:bg-gray-50 cursor-pointer transition-colors',
                    selectedSessionId === session.id && 'bg-primary-50 border-r-2 border-primary-500'
                  )}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center space-x-3">
                      {/* 会话类型标识 */}
                      {session.sessionType === 'http' ? (
                        <>
                          {/* HTTP 方法 */}
                          <span className={clsx(
                            'px-2 py-1 text-xs font-medium rounded',
                            getMethodColor((session as HttpSession & { sessionType: 'http' }).request.method)
                          )}>
                            {(session as HttpSession & { sessionType: 'http' }).request.method}
                          </span>

                          {/* 状态码 */}
                          <span className={clsx(
                            'px-2 py-1 text-xs font-medium rounded',
                            getStatusColor(session)
                          )}>
                            {(session as HttpSession & { sessionType: 'http' }).response?.status || (session as HttpSession & { sessionType: 'http' }).status}
                          </span>
                        </>
                      ) : (
                        <>
                          {/* WebSocket 类型 */}
                          <span className="px-2 py-1 text-xs font-medium rounded bg-purple-100 text-purple-700">
                            WebSocket
                          </span>

                          {/* WebSocket 状态 */}
                          <span className={clsx(
                            'px-2 py-1 text-xs font-medium rounded',
                            getStatusColor(session)
                          )}>
                            {(session as WebSocketSession & { sessionType: 'websocket' }).status === 'connecting' ? '连接中' :
                             (session as WebSocketSession & { sessionType: 'websocket' }).status === 'connected' ? '已连接' :
                             (session as WebSocketSession & { sessionType: 'websocket' }).status === 'disconnected' ? '已断开' : '错误'}
                          </span>
                        </>
                      )}
                    </div>

                    <div className="flex items-center space-x-4 text-sm text-gray-500">
                      {session.sessionType === 'http' ? (
                        <>
                          {/* HTTP 响应时间 */}
                          <div className="flex items-center">
                            <Clock className="h-4 w-4 mr-1" />
                            {formatDuration((session as HttpSession & { sessionType: 'http' }).duration)}
                          </div>

                          {/* HTTP 响应大小 */}
                          {(session as HttpSession & { sessionType: 'http' }).response && (
                            <div className="flex items-center">
                              <Zap className="h-4 w-4 mr-1" />
                              {formatSize((session as HttpSession & { sessionType: 'http' }).response!.size)}
                            </div>
                          )}
                        </>
                      ) : (
                        <>
                          {/* WebSocket 消息数量 */}
                          <div className="flex items-center">
                            <MessageSquare className="h-4 w-4 mr-1" />
                            {(session as WebSocketSession & { sessionType: 'websocket' }).messageCount} 条消息
                          </div>

                          {/* WebSocket 总大小 */}
                          <div className="flex items-center">
                            <Zap className="h-4 w-4 mr-1" />
                            {formatSize((session as WebSocketSession & { sessionType: 'websocket' }).totalSize)}
                          </div>
                        </>
                      )}

                      <button className="p-1 hover:bg-gray-200 rounded">
                        <MoreHorizontal className="h-4 w-4" />
                      </button>
                    </div>
                  </div>

                  <div className="mt-2">
                    <div className="flex items-start text-sm">
                      <Globe className="h-4 w-4 mr-2 text-gray-400 mt-0.5 flex-shrink-0" />
                      <div className="flex-1 min-w-0">
                        {session.sessionType === 'http' ? (
                          <>
                            <div className="font-medium text-gray-900 mb-1">
                              {(session as HttpSession & { sessionType: 'http' }).request.host}
                            </div>
                            <ExpandableCell 
                              content={(session as HttpSession & { sessionType: 'http' }).request.url} 
                              maxLength={80} 
                              showCopy={true}
                              className="text-gray-500"
                            />
                          </>
                        ) : (
                          <>
                            <div className="font-medium text-gray-900 mb-1">
                              {new URL((session as WebSocketSession & { sessionType: 'websocket' }).url).hostname}
                            </div>
                            <ExpandableCell 
                              content={(session as WebSocketSession & { sessionType: 'websocket' }).url} 
                              maxLength={80} 
                              showCopy={true}
                              className="text-gray-500"
                            />
                          </>
                        )}
                      </div>
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                      {session.sessionType === 'http' 
                        ? dayjs((session as HttpSession & { sessionType: 'http' }).request.timestamp).format('HH:mm:ss.SSS')
                        : dayjs((session as WebSocketSession & { sessionType: 'websocket' }).startTime).format('HH:mm:ss.SSS')
                      }
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 会话详情 */}
      <div className="w-1/3 bg-white flex flex-col">
        {selectedSessionId ? (
          <UnifiedSessionDetail sessionId={selectedSessionId} />
        ) : (
          <div className="flex items-center justify-center h-full text-gray-500">
            <div className="text-center">
              <Globe className="h-12 w-12 mx-auto mb-4 text-gray-300" />
              <p>选择一个会话查看详情</p>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

// 统一会话详情组件
function UnifiedSessionDetail({ sessionId }: { sessionId: string }) {
  const { sessions, webSocketSessions } = useAppStore()
  
  // 尝试在HTTP会话中查找
  const httpSession = sessions.find(s => s.id === sessionId)
  // 尝试在WebSocket会话中查找
  const wsSession = webSocketSessions.find(s => s.id === sessionId)
  
  const session = httpSession || wsSession
  const sessionType = httpSession ? 'http' : 'websocket'

  if (!session) {
    return (
      <div className="flex items-center justify-center h-full text-gray-500">
        <p>会话不存在</p>
      </div>
    )
  }

  if (sessionType === 'websocket') {
    return <WebSocketDetailView session={wsSession as WebSocketSession} />
  } else {
    return <HttpDetailView session={httpSession as HttpSession} />
  }
}

// HTTP 会话详情视图
function HttpDetailView({ session }: { session: HttpSession }) {
  const tabs = [
    { id: 'request', label: '请求' },
    { id: 'response', label: '响应' },
    { id: 'headers', label: '头部' },
    { id: 'timing', label: '时序' },
  ]

  const [activeTab, setActiveTab] = useState('request')

  return (
    <div className="h-full flex flex-col">
      {/* 详情头部 */}
      <div className="border-b border-gray-200 px-6 py-4 flex-shrink-0">
        <h3 className="text-lg font-semibold text-gray-900">会话详情</h3>
        <div className="mt-1">
          <ExpandableCell 
            content={session.request.url} 
            maxLength={60} 
            showCopy={true}
            className="text-sm text-gray-500"
          />
        </div>
      </div>

      {/* 标签页 */}
      <div className="border-b border-gray-200 flex-shrink-0">
        <nav className="flex space-x-8 px-6">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={clsx(
                'py-4 px-1 border-b-2 font-medium text-sm',
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              )}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* 标签页内容 */}
      <div className="flex-1 overflow-auto p-6">
        {activeTab === 'request' && (
          <div className="space-y-4">
            <div>
              <h4 className="font-medium text-gray-900 mb-2">请求行</h4>
              <code className="block p-3 bg-gray-50 rounded text-sm">
                {session.request.method} {session.request.path} {session.request.protocol}
              </code>
            </div>
            {session.request.body && (
              <div>
                <h4 className="font-medium text-gray-900 mb-2">请求体</h4>
                <div className="p-3 bg-gray-50 rounded">
                  <ExpandableCell 
                    content={session.request.body} 
                    maxLength={200} 
                    showCopy={true}
                    className="text-sm font-mono"
                  />
                </div>
              </div>
            )}
          </div>
        )}

        {activeTab === 'response' && session.response && (
          <div className="space-y-4">
            <div>
              <h4 className="font-medium text-gray-900 mb-2">状态行</h4>
              <code className="block p-3 bg-gray-50 rounded text-sm">
                {session.response.status} {session.response.statusText}
              </code>
            </div>
            {session.response.body && (
              <div>
                <h4 className="font-medium text-gray-900 mb-2">响应体</h4>
                <div className="p-3 bg-gray-50 rounded">
                  <ExpandableCell 
                    content={session.response.body} 
                    maxLength={200} 
                    showCopy={true}
                    className="text-sm font-mono"
                  />
                </div>
              </div>
            )}
          </div>
        )}

        {activeTab === 'headers' && (
          <div className="space-y-4">
            <div>
              <h4 className="font-medium text-gray-900 mb-2">请求头</h4>
              <div className="space-y-1">
                {Object.entries(session.request.headers).map(([key, value]) => (
                  <div key={key} className="flex">
                    <span className="font-medium text-sm text-gray-600 w-1/3">{key}:</span>
                    <span className="text-sm text-gray-900 w-2/3">{value}</span>
                  </div>
                ))}
              </div>
            </div>

            {session.response && (
              <div>
                <h4 className="font-medium text-gray-900 mb-2">响应头</h4>
                <div className="space-y-1">
                  {Object.entries(session.response.headers).map(([key, value]) => (
                    <div key={key} className="flex">
                      <span className="font-medium text-sm text-gray-600 w-1/3">{key}:</span>
                      <span className="text-sm text-gray-900 w-2/3">{value}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {activeTab === 'timing' && (
          <div className="space-y-4">
            <div>
              <h4 className="font-medium text-gray-900 mb-2">时序信息</h4>
              <div className="space-y-2">
                <div className="flex justify-between">
                  <span className="text-sm text-gray-600">开始时间:</span>
                  <span className="text-sm text-gray-900">
                    {dayjs(session.request.timestamp).format('YYYY-MM-DD HH:mm:ss.SSS')}
                  </span>
                </div>
                {session.response && (
                  <div className="flex justify-between">
                    <span className="text-sm text-gray-600">结束时间:</span>
                    <span className="text-sm text-gray-900">
                      {dayjs(session.response.timestamp).format('YYYY-MM-DD HH:mm:ss.SSS')}
                    </span>
                  </div>
                )}
                {session.duration && (
                  <div className="flex justify-between">
                    <span className="text-sm text-gray-600">总耗时:</span>
                    <span className="text-sm text-gray-900">{formatDuration(session.duration)}</span>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

// WebSocket 会话详情视图
function WebSocketDetailView({ session }: { session: WebSocketSession }) {
  const [filter, setFilter] = useState<'all' | 'inbound' | 'outbound'>('all')

  const filteredMessages = session.messages.filter(message => {
    if (filter === 'all') return true
    return message.direction === filter
  })

  const formatSize = (size: number) => {
    if (size < 1024) return `${size}B`
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)}KB`
    return `${(size / (1024 * 1024)).toFixed(1)}MB`
  }

  const getStatusText = (status: WebSocketSession['status']) => {
    switch (status) {
      case 'connecting': return '连接中'
      case 'connected': return '已连接'
      case 'disconnected': return '已断开'
      case 'error': return '错误'
      default: return '未知'
    }
  }

  const getStatusColor = (status: WebSocketSession['status']) => {
    switch (status) {
      case 'connecting': return 'text-yellow-700 bg-yellow-100'
      case 'connected': return 'text-green-700 bg-green-100'
      case 'disconnected': return 'text-gray-700 bg-gray-100'
      case 'error': return 'text-red-700 bg-red-100'
      default: return 'text-gray-700 bg-gray-100'
    }
  }

  return (
    <div className="h-full flex flex-col">
      {/* 详情头部 */}
      <div className="border-b border-gray-200 px-6 py-4 flex-shrink-0">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-lg font-semibold text-gray-900">WebSocket 详情</h3>
            <p className="text-sm text-gray-500 mt-1">{session.url}</p>
          </div>
          
          <span className={clsx(
            'px-3 py-1 text-sm font-medium rounded',
            getStatusColor(session.status)
          )}>
            {getStatusText(session.status)}
          </span>
        </div>

        {/* 连接统计 */}
        <div className="grid grid-cols-3 gap-4 mt-4">
          <div className="text-center">
            <div className="text-lg font-semibold text-gray-900">{session.messageCount}</div>
            <div className="text-xs text-gray-500">总消息数</div>
          </div>
          <div className="text-center">
            <div className="text-lg font-semibold text-gray-900">{formatSize(session.totalSize)}</div>
            <div className="text-xs text-gray-500">总数据量</div>
          </div>
          <div className="text-center">
            <div className="text-lg font-semibold text-gray-900">
              {session.endTime ? 
                dayjs(session.endTime).diff(dayjs(session.startTime), 'second') + 's' : 
                dayjs().diff(dayjs(session.startTime), 'second') + 's'
              }
            </div>
            <div className="text-xs text-gray-500">连接时长</div>
          </div>
        </div>
      </div>

      {/* 消息过滤 */}
      <div className="border-b border-gray-200 px-6 py-3 flex-shrink-0">
        <div className="flex items-center space-x-4">
          <span className="text-sm font-medium text-gray-700">消息类型:</span>
          <div className="flex space-x-2">
            {[
              { value: 'all', label: '全部' },
              { value: 'inbound', label: '接收' },
              { value: 'outbound', label: '发送' },
            ].map((option) => (
              <button
                key={option.value}
                onClick={() => setFilter(option.value as any)}
                className={clsx(
                  'px-3 py-1 text-sm rounded-md transition-colors',
                  filter === option.value
                    ? 'bg-primary-100 text-primary-700'
                    : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                )}
              >
                {option.label}
              </button>
            ))}
          </div>
          
          <div className="flex-1"></div>
          
          <span className="text-sm text-gray-500">
            显示 {filteredMessages.length} / {session.messages.length} 条消息
          </span>
        </div>
      </div>

      {/* 消息列表 */}
      <div className="flex-1 overflow-auto p-6">
        {filteredMessages.length === 0 ? (
          <div className="flex items-center justify-center h-full">
            <p className="text-gray-500">暂无消息</p>
          </div>
        ) : (
          <div className="space-y-3">
            {filteredMessages.map((message) => (
              <div
                key={message.id}
                className={clsx(
                  'p-4 rounded-lg border',
                  message.direction === 'inbound' 
                    ? 'bg-green-50 border-green-200' 
                    : 'bg-blue-50 border-blue-200'
                )}
              >
                <div className="flex items-center justify-between mb-2">
                  <div className="flex items-center space-x-2">
                    {message.direction === 'inbound' ? (
                      <ArrowDown className="h-4 w-4 text-green-600" />
                    ) : (
                      <ArrowUp className="h-4 w-4 text-blue-600" />
                    )}
                    <span className={clsx(
                      'text-sm font-medium',
                      message.direction === 'inbound' ? 'text-green-700' : 'text-blue-700'
                    )}>
                      {message.direction === 'inbound' ? '接收' : '发送'}
                    </span>
                    <span className={clsx(
                      'px-2 py-1 text-xs rounded',
                      message.type === 'text' 
                        ? 'bg-gray-100 text-gray-700' 
                        : 'bg-orange-100 text-orange-700'
                    )}>
                      {message.type === 'text' ? '文本' : '二进制'}
                    </span>
                  </div>

                  <div className="flex items-center space-x-3 text-sm text-gray-500">
                    <div className="flex items-center">
                      <Zap className="h-3 w-3 mr-1" />
                      {formatSize(message.size)}
                    </div>
                    <div className="flex items-center">
                      <Clock className="h-3 w-3 mr-1" />
                      {dayjs(message.timestamp).format('HH:mm:ss.SSS')}
                    </div>
                  </div>
                </div>

                <div className="bg-white p-3 rounded border">
                  {message.type === 'text' ? (
                    <pre className="text-sm text-gray-900 whitespace-pre-wrap overflow-auto max-h-40">
                      {message.data}
                    </pre>
                  ) : (
                    <div className="text-sm text-gray-500">
                      二进制数据 ({formatSize(message.size)})
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
