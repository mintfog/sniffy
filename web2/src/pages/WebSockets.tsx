import { useState } from 'react'
import { MessageSquare, Clock, ArrowUp, ArrowDown, Zap } from 'lucide-react'
import { useAppStore } from '@/store'
import { WebSocketSession } from '@/types'
import clsx from 'clsx'
import dayjs from 'dayjs'

export function WebSockets() {
  const { webSocketSessions } = useAppStore()
  const [selectedSessionId, setSelectedSessionId] = useState<string>()

  const selectedSession = webSocketSessions.find(s => s.id === selectedSessionId)

  const getStatusColor = (status: WebSocketSession['status']) => {
    switch (status) {
      case 'connecting': return 'text-yellow-700 bg-yellow-100'
      case 'connected': return 'text-green-700 bg-green-100'
      case 'disconnected': return 'text-gray-700 bg-gray-100'
      case 'error': return 'text-red-700 bg-red-100'
      default: return 'text-gray-700 bg-gray-100'
    }
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

  const formatSize = (size: number) => {
    if (size < 1024) return `${size}B`
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)}KB`
    return `${(size / (1024 * 1024)).toFixed(1)}MB`
  }

  return (
    <div className="space-y-6">
      {/* 页面标题 */}
      <div>
        <h1 className="text-3xl font-bold text-gray-900">WebSocket 连接</h1>
        <p className="mt-2 text-gray-600">监控和分析 WebSocket 连接及消息流</p>
      </div>

      <div className="flex h-[calc(100vh-200px)] space-x-6">
        {/* 连接列表 */}
        <div className="w-1/3 bg-white rounded-lg border border-gray-200">
          <div className="border-b border-gray-200 px-6 py-4">
            <h3 className="text-lg font-semibold text-gray-900">WebSocket 连接</h3>
            <p className="text-sm text-gray-500 mt-1">共 {webSocketSessions.length} 个连接</p>
          </div>

          <div className="flex-1 overflow-auto">
            {webSocketSessions.length === 0 ? (
              <div className="flex items-center justify-center h-32">
                <div className="text-center">
                  <MessageSquare className="h-12 w-12 mx-auto mb-4 text-gray-300" />
                  <p className="text-gray-500">暂无 WebSocket 连接</p>
                </div>
              </div>
            ) : (
              <div className="divide-y divide-gray-100">
                {webSocketSessions.map((session) => (
                  <div
                    key={session.id}
                    onClick={() => setSelectedSessionId(session.id)}
                    className={clsx(
                      'px-6 py-4 hover:bg-gray-50 cursor-pointer transition-colors',
                      selectedSessionId === session.id && 'bg-primary-50 border-r-2 border-primary-500'
                    )}
                  >
                    <div className="flex items-center justify-between mb-2">
                      <span className={clsx(
                        'px-2 py-1 text-xs font-medium rounded',
                        getStatusColor(session.status)
                      )}>
                        {getStatusText(session.status)}
                      </span>
                      
                      <div className="flex items-center text-xs text-gray-500">
                        <MessageSquare className="h-3 w-3 mr-1" />
                        {session.messageCount}
                      </div>
                    </div>

                    <div className="text-sm text-gray-900 font-medium truncate">
                      {new URL(session.url).hostname}
                    </div>
                    
                    <div className="text-xs text-gray-500 mt-1">
                      {dayjs(session.startTime).format('HH:mm:ss')}
                    </div>

                    <div className="flex items-center justify-between mt-2 text-xs text-gray-500">
                      <span>消息: {session.messageCount}</span>
                      <span>大小: {formatSize(session.totalSize)}</span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* 消息详情 */}
        <div className="flex-1 bg-white rounded-lg border border-gray-200">
          {selectedSession ? (
            <WebSocketDetail session={selectedSession} />
          ) : (
            <div className="flex items-center justify-center h-full">
              <div className="text-center">
                <MessageSquare className="h-12 w-12 mx-auto mb-4 text-gray-300" />
                <p className="text-gray-500">选择一个连接查看消息详情</p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// WebSocket 详情组件
function WebSocketDetail({ session }: { session: WebSocketSession }) {
  const [filter, setFilter] = useState<'all' | 'inbound' | 'outbound'>('all')

  const filteredMessages = session.messages.filter(message => {
    if (filter === 'all') return true
    return message.direction === filter
  })

  return (
    <div className="h-full flex flex-col">
      {/* 详情头部 */}
      <div className="border-b border-gray-200 px-6 py-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-lg font-semibold text-gray-900">连接详情</h3>
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
      <div className="border-b border-gray-200 px-6 py-3">
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
