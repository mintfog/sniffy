import React, { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Clock, Globe, Zap, Filter, MoreHorizontal, MessageSquare, ArrowUp, ArrowDown, X, Copy, Check, Share2, Trash2, RefreshCw, Download, Link, Terminal } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { useAppStore } from '@/store'
import { HttpSession, WebSocketSession } from '@/types'
import { ExpandableCell } from '@/components/ui'
import clsx from 'clsx'
import dayjs from 'dayjs'

type SessionType = 'all' | 'http' | 'websocket'
type UnifiedSession = (HttpSession & { sessionType: 'http' }) | (WebSocketSession & { sessionType: 'websocket' })

export function Sessions() {
  const { sessions, webSocketSessions, selectedSessionId, setSelectedSession, removeSession } = useAppStore()
  const [page] = useState(1)
  const [pageSize] = useState(50)
  const [sessionType, setSessionType] = useState<SessionType>('all')
  const [detailWidth, setDetailWidth] = useState(60)
  const [isResizing, setIsResizing] = useState(false)
  const [openDropdownId, setOpenDropdownId] = useState<string | null>(null)
  const [actionFeedback, setActionFeedback] = useState<string | null>(null)

  // 获取会话列表
  const { isLoading } = useQuery({
    queryKey: ['sessions', page, pageSize],
    queryFn: () => sniffyApi.getSessions({ page, pageSize }),
    refetchInterval: 2000,
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

  // 处理拖拽调整宽度
  const handleMouseDown = (e: React.MouseEvent) => {
    e.preventDefault()
    setIsResizing(true)
  }

  const handleMouseMove = (e: MouseEvent) => {
    if (!isResizing) return
    
    const container = document.querySelector('.sessions-container') as HTMLElement
    if (!container) return
    
    const containerRect = container.getBoundingClientRect()
    const mouseX = e.clientX - containerRect.left
    const newDetailWidth = Math.max(30, Math.min(80, (containerRect.width - mouseX) / containerRect.width * 100))
    
    setDetailWidth(newDetailWidth)
  }

  const handleMouseUp = () => {
    setIsResizing(false)
  }

  // 添加全局鼠标事件监听
  React.useEffect(() => {
    if (isResizing) {
      document.addEventListener('mousemove', handleMouseMove)
      document.addEventListener('mouseup', handleMouseUp)
      document.body.style.userSelect = 'none'
      document.body.style.cursor = 'col-resize'
      
      return () => {
        document.removeEventListener('mousemove', handleMouseMove)
        document.removeEventListener('mouseup', handleMouseUp)
        document.body.style.userSelect = ''
        document.body.style.cursor = ''
      }
    }
  }, [isResizing])

  // 点击外部关闭下拉菜单
  React.useEffect(() => {
    const handleClickOutside = () => {
      if (openDropdownId) {
        setOpenDropdownId(null)
      }
    }

    if (openDropdownId) {
      document.addEventListener('click', handleClickOutside)
      return () => document.removeEventListener('click', handleClickOutside)
    }
  }, [openDropdownId])

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

  // 复制到剪贴板
  const copyToClipboard = async (text: string, successMessage: string = '已复制到剪贴板') => {
    try {
      await navigator.clipboard.writeText(text)
      setActionFeedback(successMessage)
      setTimeout(() => setActionFeedback(null), 2000)
    } catch (error) {
      console.error('复制失败:', error)
      setActionFeedback('复制失败')
      setTimeout(() => setActionFeedback(null), 2000)
    }
  }

  // 生成cURL命令
  const generateCurlCommand = (session: HttpSession & { sessionType: 'http' }) => {
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

  // 导出会话数据
  const exportSession = (session: UnifiedSession) => {
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

  // 处理会话操作
  const handleSessionAction = async (action: string, session: UnifiedSession) => {
    setOpenDropdownId(null)
    
    switch (action) {
      case 'copy-url':
        const url = session.sessionType === 'http' 
          ? (session as HttpSession & { sessionType: 'http' }).request.url
          : (session as WebSocketSession & { sessionType: 'websocket' }).url
        await copyToClipboard(url, 'URL已复制到剪贴板')
        break
        
      case 'copy-curl':
        if (session.sessionType === 'http') {
          const curl = generateCurlCommand(session as HttpSession & { sessionType: 'http' })
          await copyToClipboard(curl, 'cURL命令已复制到剪贴板')
        }
        break
        
      case 'export':
        exportSession(session)
        setActionFeedback('会话已导出')
        setTimeout(() => setActionFeedback(null), 2000)
        break
        
      case 'delete':
        if (session.sessionType === 'http') {
          removeSession(session.id)
          if (selectedSessionId === session.id) {
            setSelectedSession(undefined)
          }
        }
        break
        
      case 'repeat':
        console.log('重新发送请求:', session.id)
        break
        
      case 'share':
        console.log('分享会话:', session.id)
        break
    }
  }

  return (
    <div className="relative">
      {/* 操作反馈提示 */}
      {actionFeedback && (
        <div className="fixed top-4 right-4 z-50 bg-green-500 text-white px-4 py-2 rounded-md shadow-lg transition-all duration-300">
          <div className="flex items-center">
            <Check className="h-4 w-4 mr-2" />
            {actionFeedback}
          </div>
        </div>
      )}
      
      <div className="sessions-container flex h-[calc(100vh-8rem)] rounded-lg overflow-hidden border border-gray-200">
        {/* 会话列表 */}
        <div 
          className={clsx(
            "bg-white border-r border-gray-200 flex flex-col transition-all duration-300",
            selectedSessionId ? "" : "w-full"
          )}
          style={selectedSessionId ? { width: `${100 - detailWidth}%` } : {}}
        >
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

                        <div className="relative">
                          <button 
                            className="p-1 hover:bg-gray-200 rounded transition-colors"
                            onClick={(e) => {
                              e.stopPropagation()
                              setOpenDropdownId(openDropdownId === session.id ? null : session.id)
                            }}
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </button>
                          
                          {/* 下拉菜单 */}
                          {openDropdownId === session.id && (
                            <div className="absolute right-0 top-full mt-1 w-48 bg-white border border-gray-200 rounded-md shadow-lg z-50">
                              <div className="py-1">
                                <button
                                  className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
                                  onClick={() => handleSessionAction('copy-url', session)}
                                >
                                  <Link className="h-4 w-4 mr-3" />
                                  复制URL
                                </button>
                                
                                {session.sessionType === 'http' && (
                                  <button
                                    className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
                                    onClick={() => handleSessionAction('copy-curl', session)}
                                  >
                                    <Terminal className="h-4 w-4 mr-3" />
                                    复制为cURL
                                  </button>
                                )}
                                
                                <button
                                  className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
                                  onClick={() => handleSessionAction('export', session)}
                                >
                                  <Download className="h-4 w-4 mr-3" />
                                  导出会话
                                </button>
                                
                                {session.sessionType === 'http' && (
                                  <button
                                    className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
                                    onClick={() => handleSessionAction('repeat', session)}
                                  >
                                    <RefreshCw className="h-4 w-4 mr-3" />
                                    重新发送
                                  </button>
                                )}
                                
                                <button
                                  className="w-full flex items-center px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 transition-colors"
                                  onClick={() => handleSessionAction('share', session)}
                                >
                                  <Share2 className="h-4 w-4 mr-3" />
                                  分享会话
                                </button>
                                
                                <div className="border-t border-gray-100 my-1"></div>
                                
                                {session.sessionType === 'http' && (
                                  <button
                                    className="w-full flex items-center px-4 py-2 text-sm text-red-600 hover:bg-red-50 transition-colors"
                                    onClick={() => handleSessionAction('delete', session)}
                                  >
                                    <Trash2 className="h-4 w-4 mr-3" />
                                    删除会话
                                  </button>
                                )}
                              </div>
                            </div>
                          )}
                        </div>
                      </div>
                    </div>

                    <div className="mt-2">
                      <div className="flex items-start text-sm">
                        <Globe className="h-4 w-4 mr-2 text-gray-400 mt-0.5 flex-shrink-0" />
                        <div className="flex-1 min-w-0">
                          {session.sessionType === 'http' ? (
                            <ExpandableCell 
                              content={(session as HttpSession & { sessionType: 'http' }).request.url} 
                              maxLength={80} 
                              showCopy={true}
                              className="text-gray-900 font-medium"
                            />
                          ) : (
                            <ExpandableCell 
                              content={(session as WebSocketSession & { sessionType: 'websocket' }).url} 
                              maxLength={80} 
                              showCopy={true}
                              className="text-gray-900 font-medium"
                            />
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

        {/* 可拖拽的分隔条 */}
        {selectedSessionId && (
          <div
            className={clsx(
              "bg-gray-300 hover:bg-gray-400 cursor-col-resize flex-shrink-0 transition-colors",
              isResizing ? "bg-gray-400" : ""
            )}
            style={{ width: '4px' }}
            onMouseDown={handleMouseDown}
          />
        )}

        {/* 会话详情 */}
        {selectedSessionId && (
          <div 
            className="h-full bg-white flex flex-col animate-in slide-in-from-right duration-300"
            style={{ width: `${detailWidth}%` }}
          >
            <UnifiedSessionDetail sessionId={selectedSessionId} />
          </div>
        )}
      </div>
    </div>
  )
}

// 统一会话详情组件
function UnifiedSessionDetail({ sessionId }: { sessionId: string }) {
  const { sessions, webSocketSessions, setSelectedSession } = useAppStore()
  
  const httpSession = sessions.find(s => s.id === sessionId)
  const wsSession = webSocketSessions.find(s => s.id === sessionId)
  
  const session = httpSession || wsSession
  const sessionType = httpSession ? 'http' : 'websocket'

  const handleClose = () => {
    setSelectedSession(undefined)
  }

  if (!session) {
    return (
      <div className="flex items-center justify-center h-full text-gray-500">
        <p>会话不存在</p>
      </div>
    )
  }

  const detailHeader = (
    <div className="border-b border-gray-200 px-6 py-4 flex-shrink-0">
      <div className="flex items-center justify-between">
        <div className="flex items-center space-x-3">
          <h3 className="text-lg font-semibold text-gray-900">
            {sessionType === 'websocket' ? 'WebSocket 详情' : 'HTTP 详情'}
          </h3>
          <span className={clsx(
            'px-2 py-1 text-xs font-medium rounded',
            sessionType === 'websocket' ? 'bg-purple-100 text-purple-700' : 'bg-blue-100 text-blue-700'
          )}>
            {sessionType.toUpperCase()}
          </span>
        </div>
        <div className="flex items-center space-x-2">
          <button
            onClick={handleClose}
            className="p-1 hover:bg-gray-100 rounded transition-colors"
            title="关闭详情"
          >
            <X className="h-5 w-5 text-gray-500" />
          </button>
        </div>
      </div>
      
      <div className="mt-3">
        <ExpandableCell 
          content={sessionType === 'http' ? 
            (session as HttpSession).request.url : 
            (session as WebSocketSession).url
          } 
          maxLength={80} 
          showCopy={true}
          className="text-sm text-gray-600"
        />
      </div>
    </div>
  )

  if (sessionType === 'websocket') {
    return (
      <div className="flex flex-col h-full">
        {detailHeader}
        <div className="flex-1 overflow-hidden">
          <WebSocketDetailContent session={wsSession as WebSocketSession} />
        </div>
      </div>
    )
  } else {
    return (
      <div className="flex flex-col h-full">
        {detailHeader}
        <div className="flex-1 overflow-hidden">
          <HttpDetailContent session={httpSession as HttpSession} />
        </div>
      </div>
    )
  }
}

// HTTP 会话详情内容
function HttpDetailContent({ session }: { session: HttpSession }) {
  const [requestTab, setRequestTab] = useState<'headers' | 'body' | 'raw'>('headers')
  const [responseTab, setResponseTab] = useState<'headers' | 'body' | 'raw' | 'preview'>('headers')
  const [copiedItem, setCopiedItem] = useState<string | null>(null)

  const formatSize = (size: number) => {
    if (size < 1024) return `${size}B`
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)}KB`
    return `${(size / (1024 * 1024)).toFixed(1)}MB`
  }

  // 生成原始请求消息
  const generateRawRequest = () => {
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

  // 生成原始响应消息
  const generateRawResponse = () => {
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

  // 检测内容类型
  const getContentType = (headers: Record<string, string>) => {
    return headers['content-type'] || headers['Content-Type'] || ''
  }

  // 判断是否可以预览
  const canPreview = (contentType: string) => {
    return contentType.includes('text/html') || 
           contentType.includes('application/json') || 
           contentType.includes('application/xml') || 
           contentType.includes('text/xml') ||
           contentType.includes('image/')
  }

  // 格式化JSON
  const formatJson = (content: string) => {
    try {
      const parsed = JSON.parse(content)
      return JSON.stringify(parsed, null, 2)
    } catch {
      return content
    }
  }

  // 复制到剪贴板
  const handleCopy = async (content: string, itemId: string) => {
    try {
      await navigator.clipboard.writeText(content)
      setCopiedItem(itemId)
      setTimeout(() => setCopiedItem(null), 2000)
    } catch (error) {
      console.error('复制失败:', error)
    }
  }

  // 复制按钮组件
  const CopyButton = ({ content, itemId, className = "" }: { content: string, itemId: string, className?: string }) => (
    <button
      onClick={() => handleCopy(content, itemId)}
      className={clsx(
        'flex items-center px-2 py-1 text-xs bg-gray-100 hover:bg-gray-200 border rounded transition-colors',
        className
      )}
      title="复制到剪贴板"
    >
      {copiedItem === itemId ? (
        <>
          <Check className="h-3 w-3 mr-1 text-green-600" />
          <span className="text-green-600">已复制</span>
        </>
      ) : (
        <>
          <Copy className="h-3 w-3 mr-1 text-gray-600" />
          <span className="text-gray-600">复制</span>
        </>
      )}
    </button>
  )

  return (
    <div className="h-full flex flex-col">
      {/* 概览信息 */}
      <div className="border-b border-gray-200 px-4 py-3 bg-gray-50 flex-shrink-0">
        <div className="grid grid-cols-2 gap-4 text-sm">
          {/* 第一行 */}
          <div className="flex items-center space-x-4">
            <div className="flex items-center">
              <span className="text-gray-500 text-xs">方法:</span>
              <span className={clsx(
                'ml-1 px-2 py-0.5 text-xs font-medium rounded',
                session.request.method === 'GET' ? 'text-green-700 bg-green-100' :
                session.request.method === 'POST' ? 'text-blue-700 bg-blue-100' :
                session.request.method === 'PUT' ? 'text-orange-700 bg-orange-100' :
                session.request.method === 'DELETE' ? 'text-red-700 bg-red-100' :
                'text-gray-700 bg-gray-100'
              )}>
                {session.request.method}
              </span>
            </div>
            <div className="flex items-center">
              <span className="text-gray-500 text-xs">状态:</span>
              <span className={clsx(
                'ml-1 px-2 py-0.5 text-xs font-medium rounded',
                session.response?.status && session.response.status >= 200 && session.response.status < 300 ? 'text-green-700 bg-green-100' :
                session.response?.status && session.response.status >= 400 ? 'text-red-700 bg-red-100' :
                'text-yellow-700 bg-yellow-100'
              )}>
                {session.response?.status || '进行中'}
              </span>
            </div>
            <div className="flex items-center">
              <span className="text-gray-500 text-xs">耗时:</span>
              <span className="ml-1 font-medium text-gray-900 text-xs">
                {session.duration ? `${session.duration}ms` : '-'}
              </span>
            </div>
          </div>
          
          {/* 第二行 */}
          <div className="flex items-center space-x-4">
            <div className="flex items-center">
              <span className="text-gray-500 text-xs">远程IP:</span>
              <span className="ml-1 font-medium text-gray-900 font-mono text-xs">
                {session.request.serverIP && session.request.serverPort 
                  ? `${session.request.serverIP}:${session.request.serverPort}`
                  : '-'
                }
              </span>
            </div>
            <div className="flex items-center">
              <span className="text-gray-500 text-xs">大小:</span>
              <span className="ml-1 font-medium text-gray-900 text-xs">
                {session.response ? formatSize(session.response.size) : '-'}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* 主要内容区域 - 整体滚动 */}
      <div className="flex-1 overflow-auto p-6">
        {/* 请求部分 */}
        <div className="mb-6">
          <div className="border border-gray-200 rounded-lg">
            <div className="border-b border-gray-200 bg-blue-50 px-4 py-2 flex items-center justify-between">
              <div className="flex items-center space-x-2">
                <ArrowUp className="h-4 w-4 text-blue-600" />
                <span className="font-medium text-blue-900">请求</span>
              </div>
              <div className="flex space-x-2">
                <button
                  onClick={() => setRequestTab('headers')}
                  className={clsx(
                    'px-3 py-1 text-xs rounded transition-colors',
                    requestTab === 'headers' 
                      ? 'bg-blue-200 text-blue-900' 
                      : 'text-blue-700 hover:bg-blue-100'
                  )}
                >
                  请求头
                </button>
                {session.request.body && (
                  <button
                    onClick={() => setRequestTab('body')}
                    className={clsx(
                      'px-3 py-1 text-xs rounded transition-colors',
                      requestTab === 'body' 
                        ? 'bg-blue-200 text-blue-900' 
                        : 'text-blue-700 hover:bg-blue-100'
                    )}
                  >
                    请求体
                  </button>
                )}
                <button
                  onClick={() => setRequestTab('raw')}
                  className={clsx(
                    'px-3 py-1 text-xs rounded transition-colors',
                    requestTab === 'raw' 
                      ? 'bg-blue-200 text-blue-900' 
                      : 'text-blue-700 hover:bg-blue-100'
                  )}
                >
                  Raw
                </button>
              </div>
            </div>
            
            <div className="p-4">
              {requestTab === 'headers' ? (
                <div className="space-y-2">
                  <div className="text-xs font-medium text-gray-700 mb-2">
                    {session.request.method} {session.request.path} {session.request.protocol}
                  </div>
                  {session.request.serverIP && session.request.serverPort && (
                    <div className="flex text-sm border-b border-gray-100 py-1 bg-blue-50">
                      <span className="font-medium text-blue-700 w-1/3 break-words">远程地址:</span>
                      <span className="text-blue-900 w-2/3 break-words font-mono text-xs">
                        {session.request.serverIP}:{session.request.serverPort}
                      </span>
                    </div>
                  )}
                  {Object.entries(session.request.headers).map(([key, value]) => (
                    <div key={key} className="flex text-sm border-b border-gray-100 py-1">
                      <span className="font-medium text-gray-600 w-1/3 break-words">{key}:</span>
                      <span className="text-gray-900 w-2/3 break-words">{value}</span>
                    </div>
                  ))}
                </div>
              ) : requestTab === 'body' ? (
                <div>
                  <div className="flex items-center justify-between mb-2">
                    <div className="text-xs text-gray-500">请求体内容</div>
                    <CopyButton content={session.request.body || ''} itemId="request-body" />
                  </div>
                  <div className="bg-gray-50 p-3 rounded border">
                    <ExpandableCell 
                      content={session.request.body || ''} 
                      maxLength={500} 
                      showCopy={false}
                      className="text-sm font-mono text-gray-900"
                    />
                  </div>
                </div>
              ) : (
                <div>
                  <div className="flex items-center justify-between mb-2">
                    <div className="text-xs text-gray-500">原始请求消息</div>
                    <CopyButton content={generateRawRequest()} itemId="request-raw" />
                  </div>
                  <div className="bg-gray-900 text-green-400 p-3 rounded border font-mono text-sm">
                    <pre className="whitespace-pre-wrap overflow-auto">
                      {generateRawRequest()}
                    </pre>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* 响应部分 */}
        <div className="mb-6">
          <div className="border border-gray-200 rounded-lg">
            <div className="border-b border-gray-200 bg-green-50 px-4 py-2 flex items-center justify-between">
              <div className="flex items-center space-x-2">
                <ArrowDown className="h-4 w-4 text-green-600" />
                <span className="font-medium text-green-900">响应</span>
              </div>
              {session.response && (
                <div className="flex space-x-2">
                  <button
                    onClick={() => setResponseTab('headers')}
                    className={clsx(
                      'px-3 py-1 text-xs rounded transition-colors',
                      responseTab === 'headers' 
                        ? 'bg-green-200 text-green-900' 
                        : 'text-green-700 hover:bg-green-100'
                    )}
                  >
                    响应头
                  </button>
                  {session.response.body && (
                    <button
                      onClick={() => setResponseTab('body')}
                      className={clsx(
                        'px-3 py-1 text-xs rounded transition-colors',
                        responseTab === 'body' 
                          ? 'bg-green-200 text-green-900' 
                          : 'text-green-700 hover:bg-green-100'
                      )}
                    >
                      响应体
                    </button>
                  )}
                  <button
                    onClick={() => setResponseTab('raw')}
                    className={clsx(
                      'px-3 py-1 text-xs rounded transition-colors',
                      responseTab === 'raw' 
                        ? 'bg-green-200 text-green-900' 
                        : 'text-green-700 hover:bg-green-100'
                    )}
                  >
                    Raw
                  </button>
                  {session.response.body && canPreview(getContentType(session.response.headers)) && (
                    <button
                      onClick={() => setResponseTab('preview')}
                      className={clsx(
                        'px-3 py-1 text-xs rounded transition-colors',
                        responseTab === 'preview' 
                          ? 'bg-green-200 text-green-900' 
                          : 'text-green-700 hover:bg-green-100'
                      )}
                    >
                      预览
                    </button>
                  )}
                </div>
              )}
            </div>
            
            <div className="p-4">
              {!session.response ? (
                <div className="flex items-center justify-center py-12 text-gray-500">
                  <div className="text-center">
                    <Clock className="h-8 w-8 mx-auto mb-2 text-gray-300" />
                    <p>等待响应...</p>
                  </div>
                </div>
              ) : (
                <>
                  {responseTab === 'headers' ? (
                    <div className="space-y-2">
                      <div className="text-xs font-medium text-gray-700 mb-2">
                        {session.response.status} {session.response.statusText}
                      </div>
                      {Object.entries(session.response.headers).map(([key, value]) => (
                        <div key={key} className="flex text-sm border-b border-gray-100 py-1">
                          <span className="font-medium text-gray-600 w-1/3 break-words">{key}:</span>
                          <span className="text-gray-900 w-2/3 break-words">{value}</span>
                        </div>
                      ))}
                    </div>
                  ) : responseTab === 'body' ? (
                    <div>
                      <div className="flex items-center justify-between mb-2">
                        <div className="text-xs text-gray-500">响应体内容</div>
                        <CopyButton 
                          content={getContentType(session.response.headers).includes('application/json') ? 
                            formatJson(session.response.body || '') : 
                            session.response.body || ''
                          } 
                          itemId="response-body" 
                        />
                      </div>
                      <div className="bg-gray-50 p-3 rounded border">
                        {getContentType(session.response.headers).includes('application/json') ? (
                          <pre className="text-sm font-mono text-gray-900 whitespace-pre-wrap overflow-auto">
                            {formatJson(session.response.body || '')}
                          </pre>
                        ) : (
                          <ExpandableCell 
                            content={session.response.body || ''} 
                            maxLength={500} 
                            showCopy={false}
                            className="text-sm font-mono text-gray-900"
                          />
                        )}
                      </div>
                    </div>
                  ) : responseTab === 'raw' ? (
                    <div>
                      <div className="flex items-center justify-between mb-2">
                        <div className="text-xs text-gray-500">原始响应消息</div>
                        <CopyButton content={generateRawResponse()} itemId="response-raw" />
                      </div>
                      <div className="bg-gray-900 text-green-400 p-3 rounded border font-mono text-sm">
                        <pre className="whitespace-pre-wrap overflow-auto">
                          {generateRawResponse()}
                        </pre>
                      </div>
                    </div>
                  ) : responseTab === 'preview' ? (
                    <div>
                      {(() => {
                        const contentType = getContentType(session.response.headers)
                        const responseBody = session.response.body || ''
                        
                        if (contentType.includes('text/html')) {
                          return (
                            <>
                              <div className="flex items-center justify-between mb-2">
                                <div className="text-xs text-gray-500">HTML预览</div>
                                <CopyButton content={responseBody} itemId="preview-html" />
                              </div>
                              <div className="border rounded">
                                <iframe
                                  srcDoc={responseBody}
                                  className="w-full h-60 border-0"
                                  sandbox="allow-same-origin"
                                  title="HTML Preview"
                                />
                              </div>
                            </>
                          )
                        } else if (contentType.includes('application/json')) {
                          const formattedJson = formatJson(responseBody)
                          return (
                            <>
                              <div className="flex items-center justify-between mb-2">
                                <div className="text-xs text-gray-500">JSON预览</div>
                                <CopyButton content={formattedJson} itemId="preview-json" />
                              </div>
                              <div className="bg-gray-50 p-3 rounded border">
                                <pre className="text-sm font-mono text-gray-900 whitespace-pre-wrap overflow-auto">
                                  {formattedJson}
                                </pre>
                              </div>
                            </>
                          )
                        } else {
                          return (
                            <>
                              <div className="text-xs text-gray-500 mb-2">内容预览</div>
                              <div className="text-center py-8 text-gray-500">
                                <p>无法预览此类型的内容</p>
                                <p className="text-xs mt-1">Content-Type: {contentType}</p>
                              </div>
                            </>
                          )
                        }
                      })()}
                    </div>
                  ) : null}
                </>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// WebSocket 会话详情内容
function WebSocketDetailContent({ session }: { session: WebSocketSession }) {
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