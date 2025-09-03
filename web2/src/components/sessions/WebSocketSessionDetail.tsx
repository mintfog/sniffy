import React, { useState } from 'react'
import { ArrowUp, ArrowDown, Clock, Zap, MessageSquare } from 'lucide-react'
import { WebSocketSession } from '@/types'
import { formatSize } from '@/utils/sessionUtils'
import clsx from 'clsx'
import dayjs from 'dayjs'

interface WebSocketSessionDetailProps {
  session: WebSocketSession
}

export function WebSocketSessionDetail({ session }: WebSocketSessionDetailProps) {
  const [filter, setFilter] = useState<'all' | 'inbound' | 'outbound'>('all')

  const filteredMessages = session.messages.filter(message => {
    if (filter === 'all') return true
    return message.direction === filter
  })

  return (
    <div className="h-full flex flex-col">
      {/* 连接统计 */}
      <div className="border-b border-gray-200 px-4 py-3 bg-gray-50 flex-shrink-0">
        <div className="grid grid-cols-4 gap-4 text-sm">
          <div>
            <span className="text-gray-500">状态:</span>
            <span className="ml-1 font-medium text-gray-900">{session.status}</span>
          </div>
          <div>
            <span className="text-gray-500">消息数:</span>
            <span className="ml-1 font-medium text-gray-900">{session.messageCount}</span>
          </div>
          <div>
            <span className="text-gray-500">数据量:</span>
            <span className="ml-1 font-medium text-gray-900">{formatSize(session.totalSize)}</span>
          </div>
          <div>
            <span className="text-gray-500">时长:</span>
            <span className="ml-1 font-medium text-gray-900">
              {session.endTime ? 
                dayjs(session.endTime).diff(dayjs(session.startTime), 'second') + 's' : 
                dayjs().diff(dayjs(session.startTime), 'second') + 's'
              }
            </span>
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
