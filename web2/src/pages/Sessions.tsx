import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Clock, Globe, Zap, Filter, MoreHorizontal } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { useAppStore } from '@/store'
import { HttpSession } from '@/types'
import { ExpandableCell } from '@/components/ui'
import clsx from 'clsx'
import dayjs from 'dayjs'
import { formatDuration } from '@/utils'

export function Sessions() {
  const { sessions, selectedSessionId, setSelectedSession } = useAppStore()
  const [page] = useState(1)
  const [pageSize] = useState(50)

  // 获取会话列表
  const { data: sessionsData, isLoading } = useQuery({
    queryKey: ['sessions', page, pageSize],
    queryFn: () => sniffyApi.getSessions({ page, pageSize }),
    refetchInterval: 2000, // 每2秒刷新
  })

  const getStatusColor = (session: HttpSession) => {
    if (session.status === 'pending') return 'text-yellow-600 bg-yellow-50'
    if (session.status === 'error') return 'text-red-600 bg-red-50'
    if (!session.response) return 'text-gray-600 bg-gray-50'
    
    const status = session.response.status
    if (status >= 200 && status < 300) return 'text-green-600 bg-green-50'
    if (status >= 300 && status < 400) return 'text-blue-600 bg-blue-50'
    if (status >= 400 && status < 500) return 'text-orange-600 bg-orange-50'
    return 'text-red-600 bg-red-50'
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
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-gray-900">HTTP 会话</h2>
            <div className="flex items-center space-x-2">
              <span className="text-sm text-gray-500">
                共 {sessionsData?.total || 0} 个会话
              </span>
              <button className="p-2 hover:bg-gray-100 rounded-md">
                <Filter className="h-4 w-4 text-gray-400" />
              </button>
            </div>
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
              {sessions.map((session) => (
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
                      {/* HTTP 方法 */}
                      <span className={clsx(
                        'px-2 py-1 text-xs font-medium rounded',
                        getMethodColor(session.request.method)
                      )}>
                        {session.request.method}
                      </span>

                      {/* 状态码 */}
                      <span className={clsx(
                        'px-2 py-1 text-xs font-medium rounded',
                        getStatusColor(session)
                      )}>
                        {session.response?.status || session.status}
                      </span>
                    </div>

                    <div className="flex items-center space-x-4 text-sm text-gray-500">
                      {/* 响应时间 */}
                      <div className="flex items-center">
                        <Clock className="h-4 w-4 mr-1" />
                        {formatDuration(session.duration)}
                      </div>

                      {/* 响应大小 */}
                      {session.response && (
                        <div className="flex items-center">
                          <Zap className="h-4 w-4 mr-1" />
                          {formatSize(session.response.size)}
                        </div>
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
                        <div className="font-medium text-gray-900 mb-1">{session.request.host}</div>
                        <ExpandableCell 
                          content={session.request.url} 
                          maxLength={80} 
                          showCopy={true}
                          className="text-gray-500"
                        />
                      </div>
                    </div>
                    <div className="text-xs text-gray-500 mt-1">
                      {dayjs(session.request.timestamp).format('HH:mm:ss.SSS')}
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
          <SessionDetail sessionId={selectedSessionId} />
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

// 会话详情组件
function SessionDetail({ sessionId }: { sessionId: string }) {
  const { sessions } = useAppStore()
  const session = sessions.find(s => s.id === sessionId)

  if (!session) {
    return (
      <div className="flex items-center justify-center h-full text-gray-500">
        <p>会话不存在</p>
      </div>
    )
  }

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
