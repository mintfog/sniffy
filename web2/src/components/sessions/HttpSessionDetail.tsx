import React, { useState } from 'react'
import { ArrowUp, ArrowDown, Clock, Copy, Check } from 'lucide-react'
import { HttpSession } from '@/types'
import { ExpandableCell } from '@/components/ui'
import { formatSize, generateRawRequest, generateRawResponse, getContentType, canPreview, formatJson } from '@/utils/sessionUtils'
import clsx from 'clsx'

interface HttpSessionDetailProps {
  session: HttpSession
}

export function HttpSessionDetail({ session }: HttpSessionDetailProps) {
  const [requestTab, setRequestTab] = useState<'headers' | 'body' | 'raw'>('headers')
  const [responseTab, setResponseTab] = useState<'headers' | 'body' | 'raw' | 'preview'>('headers')
  const [copiedItem, setCopiedItem] = useState<string | null>(null)

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
                    <CopyButton content={generateRawRequest(session)} itemId="request-raw" />
                  </div>
                  <div className="bg-gray-900 text-green-400 p-3 rounded border font-mono text-sm">
                    <pre className="whitespace-pre-wrap overflow-auto">
                      {generateRawRequest(session)}
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
                        <CopyButton content={generateRawResponse(session)} itemId="response-raw" />
                      </div>
                      <div className="bg-gray-900 text-green-400 p-3 rounded border font-mono text-sm">
                        <pre className="whitespace-pre-wrap overflow-auto">
                          {generateRawResponse(session)}
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
