import { useState } from 'react'
import { Trash2, ChevronDown, ChevronRight, Info, Plus, Minus } from 'lucide-react'
import { InterceptAction, ActionType } from '@/types'
import clsx from 'clsx'

interface ActionEditorProps {
  action: InterceptAction
  index: number
  onUpdate: (updates: Partial<InterceptAction>) => void
  onRemove: () => void
  errors: Record<string, string>
}

export function ActionEditor({ action, index, onUpdate, onRemove, errors }: ActionEditorProps) {
  const [expanded, setExpanded] = useState(true)

  // 动作类型选项
  const actionTypeOptions = [
    { group: '请求控制', options: [
      { value: 'block', label: '阻止请求' },
      { value: 'allow', label: '允许请求' },
      { value: 'redirect', label: '重定向' },
      { value: 'auto_respond', label: '自动响应' }
    ]},
    { group: '请求修改', options: [
      { value: 'modify_url', label: '修改URL' },
      { value: 'modify_method', label: '修改HTTP方法' },
      { value: 'modify_headers', label: '修改请求头' },
      { value: 'modify_body', label: '修改请求体' }
    ]},
    { group: '响应修改', options: [
      { value: 'modify_status', label: '修改状态码' },
      { value: 'modify_response_headers', label: '修改响应头' },
      { value: 'modify_response_body', label: '修改响应体' }
    ]},
    { group: '流量控制', options: [
      { value: 'delay', label: '添加延迟' },
      { value: 'timeout', label: '设置超时' },
      { value: 'bandwidth_limit', label: '限制带宽' }
    ]},
    { group: '调试相关', options: [
      { value: 'breakpoint', label: '设置断点' },
      { value: 'log', label: '记录日志' }
    ]}
  ]

  // 获取动作类型的颜色
  const getActionTypeColor = (type: ActionType) => {
    if (['block', 'allow'].includes(type)) return 'bg-red-50 border-red-200 text-red-800'
    if (['redirect', 'auto_respond'].includes(type)) return 'bg-blue-50 border-blue-200 text-blue-800'
    if (type.startsWith('modify_')) return 'bg-purple-50 border-purple-200 text-purple-800'
    if (['delay', 'timeout', 'bandwidth_limit'].includes(type)) return 'bg-orange-50 border-orange-200 text-orange-800'
    return 'bg-gray-50 border-gray-200 text-gray-800'
  }

  const renderActionParameters = () => {
    switch (action.type) {
      case 'block':
        return (
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                阻止消息
              </label>
              <input
                type="text"
                value={action.parameters.message || ''}
                onChange={(e) => onUpdate({
                  parameters: { ...action.parameters, message: e.target.value }
                })}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                placeholder="请求已被阻止"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                返回状态码
              </label>
              <select
                value={action.parameters.statusCode || 403}
                onChange={(e) => onUpdate({
                  parameters: { ...action.parameters, statusCode: parseInt(e.target.value) }
                })}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
              >
                <option value={403}>403 Forbidden</option>
                <option value={404}>404 Not Found</option>
                <option value={418}>418 I'm a teapot</option>
                <option value={429}>429 Too Many Requests</option>
              </select>
            </div>
          </div>
        )

      case 'redirect':
        return (
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                重定向URL *
              </label>
              <input
                type="url"
                value={action.parameters.url || ''}
                onChange={(e) => onUpdate({
                  parameters: { ...action.parameters, url: e.target.value }
                })}
                className={clsx(
                  'w-full px-3 py-2 border rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500',
                  errors[`action_${index}_url`] ? 'border-red-300' : 'border-gray-300'
                )}
                placeholder="https://example.com"
              />
              {errors[`action_${index}_url`] && (
                <p className="mt-1 text-sm text-red-600">{errors[`action_${index}_url`]}</p>
              )}
            </div>
            <div>
              <label className="flex items-center">
                <input
                  type="checkbox"
                  checked={action.parameters.preserveQuery || false}
                  onChange={(e) => onUpdate({
                    parameters: { ...action.parameters, preserveQuery: e.target.checked }
                  })}
                  className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                />
                <span className="ml-2 text-sm text-gray-700">保留查询参数</span>
              </label>
            </div>
          </div>
        )

      case 'auto_respond':
        return (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-2">
                  状态码 *
                </label>
                <input
                  type="number"
                  min="100"
                  max="599"
                  value={action.parameters.response?.status || 200}
                  onChange={(e) => onUpdate({
                    parameters: {
                      ...action.parameters,
                      response: {
                        ...action.parameters.response,
                        status: parseInt(e.target.value) || 200
                      }
                    }
                  })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-2">
                  内容类型
                </label>
                <select
                  value={action.parameters.response?.contentType || 'application/json'}
                  onChange={(e) => onUpdate({
                    parameters: {
                      ...action.parameters,
                      response: {
                        ...action.parameters.response,
                        contentType: e.target.value
                      }
                    }
                  })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                >
                  <option value="application/json">application/json</option>
                  <option value="text/html">text/html</option>
                  <option value="text/plain">text/plain</option>
                  <option value="application/xml">application/xml</option>
                </select>
              </div>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                响应体
              </label>
              <textarea
                value={action.parameters.response?.body || ''}
                onChange={(e) => onUpdate({
                  parameters: {
                    ...action.parameters,
                    response: {
                      ...action.parameters.response,
                      body: e.target.value
                    }
                  }
                })}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                rows={4}
                placeholder="响应内容"
              />
            </div>
          </div>
        )

      case 'delay':
        return (
          <div className="space-y-4">
            <div>
              <label className="flex items-center mb-3">
                <input
                  type="checkbox"
                  checked={action.parameters.randomDelay || false}
                  onChange={(e) => onUpdate({
                    parameters: { ...action.parameters, randomDelay: e.target.checked }
                  })}
                  className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                />
                <span className="ml-2 text-sm text-gray-700">随机延迟</span>
              </label>
            </div>
            
            {action.parameters.randomDelay ? (
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    最小延迟（毫秒）
                  </label>
                  <input
                    type="number"
                    min="0"
                    value={action.parameters.minDelay || 0}
                    onChange={(e) => onUpdate({
                      parameters: { ...action.parameters, minDelay: parseInt(e.target.value) || 0 }
                    })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    最大延迟（毫秒）
                  </label>
                  <input
                    type="number"
                    min="0"
                    value={action.parameters.maxDelay || 1000}
                    onChange={(e) => onUpdate({
                      parameters: { ...action.parameters, maxDelay: parseInt(e.target.value) || 1000 }
                    })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                  />
                </div>
              </div>
            ) : (
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-2">
                  延迟时间（毫秒） *
                </label>
                <input
                  type="number"
                  min="0"
                  value={action.parameters.milliseconds || ''}
                  onChange={(e) => onUpdate({
                    parameters: { ...action.parameters, milliseconds: parseInt(e.target.value) || 0 }
                  })}
                  className={clsx(
                    'w-full px-3 py-2 border rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500',
                    errors[`action_${index}_delay`] ? 'border-red-300' : 'border-gray-300'
                  )}
                  placeholder="1000"
                />
                {errors[`action_${index}_delay`] && (
                  <p className="mt-1 text-sm text-red-600">{errors[`action_${index}_delay`]}</p>
                )}
              </div>
            )}
          </div>
        )

      case 'modify_headers':
      case 'modify_response_headers':
        return (
          <HeadersEditor 
            headers={action.parameters.headers || action.parameters.responseHeaders || {}}
            onChange={(headers) => {
              const key = action.type === 'modify_headers' ? 'headers' : 'responseHeaders'
              onUpdate({
                parameters: { ...action.parameters, [key]: headers }
              })
            }}
          />
        )

      case 'modify_body':
      case 'modify_response_body':
        return (
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-2">
                新的{action.type === 'modify_body' ? '请求' : '响应'}体内容
              </label>
              <textarea
                value={action.parameters.body || action.parameters.responseBody || ''}
                onChange={(e) => {
                  const key = action.type === 'modify_body' ? 'body' : 'responseBody'
                  onUpdate({
                    parameters: { ...action.parameters, [key]: e.target.value }
                  })
                }}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                rows={6}
                placeholder="输入新的内容"
              />
            </div>
          </div>
        )

      default:
        return (
          <div className="p-4 bg-gray-50 border border-gray-200 rounded-md">
            <p className="text-sm text-gray-600">
              该动作类型的配置选项正在开发中...
            </p>
          </div>
        )
    }
  }

  return (
    <div className={clsx('border rounded-lg overflow-hidden', getActionTypeColor(action.type))}>
      {/* 动作头部 */}
      <div 
        className="p-4 cursor-pointer hover:bg-opacity-80"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center space-x-3">
            <div className="flex items-center">
              {expanded ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
            </div>
            
            <div className="flex-1">
              <select
                value={action.type}
                onChange={(e) => {
                  const newType = e.target.value as ActionType
                  onUpdate({ 
                    type: newType,
                    parameters: {},
                    enabled: true
                  })
                }}
                className="bg-transparent border-none text-sm font-medium focus:ring-0 focus:outline-none"
                onClick={(e) => e.stopPropagation()}
              >
                {actionTypeOptions.map(group => (
                  <optgroup key={group.group} label={group.group}>
                    {group.options.map(option => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </div>
            
            {action.description && (
              <span className="text-xs text-gray-600 italic">
                {action.description}
              </span>
            )}
          </div>

          <div className="flex items-center space-x-2">
            <label className="flex items-center">
              <input
                type="checkbox"
                checked={action.enabled !== false}
                onChange={(e) => onUpdate({ enabled: e.target.checked })}
                className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                onClick={(e) => e.stopPropagation()}
              />
              <span className="ml-1 text-xs">启用</span>
            </label>
            
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation()
                onRemove()
              }}
              className="p-1 text-red-600 hover:text-red-800 hover:bg-red-100 rounded"
              title="删除动作"
            >
              <Trash2 className="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>

      {/* 动作参数 */}
      {expanded && (
        <div className="px-4 pb-4 bg-white">
          {/* 描述输入 */}
          <div className="mb-4">
            <label className="block text-sm font-medium text-gray-700 mb-2">
              描述（可选）
            </label>
            <input
              type="text"
              value={action.description || ''}
              onChange={(e) => onUpdate({ description: e.target.value })}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
              placeholder="描述这个动作的用途"
            />
          </div>

          {/* 动作特定参数 */}
          {renderActionParameters()}
        </div>
      )}
    </div>
  )
}

// 头部编辑器组件
interface HeadersEditorProps {
  headers: {
    add?: Record<string, string>
    modify?: Record<string, string>
    remove?: string[]
  }
  onChange: (headers: {
    add?: Record<string, string>
    modify?: Record<string, string>
    remove?: string[]
  }) => void
}

function HeadersEditor({ headers, onChange }: HeadersEditorProps) {
  const [activeTab, setActiveTab] = useState<'add' | 'modify' | 'remove'>('add')

  const addHeader = (type: 'add' | 'modify') => {
    const newHeaders = { ...headers }
    if (!newHeaders[type]) newHeaders[type] = {}
    newHeaders[type]![''] = ''
    onChange(newHeaders)
  }

  const updateHeader = (type: 'add' | 'modify', oldKey: string, newKey: string, value: string) => {
    const newHeaders = { ...headers }
    if (!newHeaders[type]) newHeaders[type] = {}
    
    if (oldKey !== newKey && oldKey !== '') {
      delete newHeaders[type]![oldKey]
    }
    
    if (newKey !== '') {
      newHeaders[type]![newKey] = value
    }
    
    onChange(newHeaders)
  }

  const removeHeader = (type: 'add' | 'modify', key: string) => {
    const newHeaders = { ...headers }
    if (newHeaders[type]) {
      delete newHeaders[type]![key]
    }
    onChange(newHeaders)
  }

  const addRemoveHeader = () => {
    const newHeaders = { ...headers }
    if (!newHeaders.remove) newHeaders.remove = []
    newHeaders.remove.push('')
    onChange(newHeaders)
  }

  const updateRemoveHeader = (index: number, value: string) => {
    const newHeaders = { ...headers }
    if (!newHeaders.remove) newHeaders.remove = []
    newHeaders.remove[index] = value
    onChange(newHeaders)
  }

  const deleteRemoveHeader = (index: number) => {
    const newHeaders = { ...headers }
    if (newHeaders.remove) {
      newHeaders.remove.splice(index, 1)
    }
    onChange(newHeaders)
  }

  return (
    <div className="space-y-4">
      {/* 标签页 */}
      <div className="flex space-x-1 border-b border-gray-200">
        {(['add', 'modify', 'remove'] as const).map((tab) => (
          <button
            key={tab}
            type="button"
            onClick={() => setActiveTab(tab)}
            className={clsx(
              'px-3 py-2 text-sm font-medium rounded-t-md border-b-2 transition-colors',
              activeTab === tab
                ? 'border-blue-500 text-blue-600 bg-blue-50'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:bg-gray-50'
            )}
          >
            {tab === 'add' ? '添加' : tab === 'modify' ? '修改' : '删除'}
          </button>
        ))}
      </div>

      {/* 内容区域 */}
      <div className="min-h-[100px]">
        {activeTab === 'add' && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <h4 className="text-sm font-medium text-gray-700">添加请求头</h4>
              <button
                type="button"
                onClick={() => addHeader('add')}
                className="flex items-center px-2 py-1 text-xs bg-blue-600 text-white rounded hover:bg-blue-700"
              >
                <Plus className="h-3 w-3 mr-1" />
                添加
              </button>
            </div>
            {Object.entries(headers.add || {}).map(([key, value], index) => (
              <div key={index} className="flex gap-2">
                <input
                  type="text"
                  placeholder="Header Name"
                  value={key}
                  onChange={(e) => updateHeader('add', key, e.target.value, value)}
                  className="flex-1 px-2 py-1 text-sm border border-gray-300 rounded"
                />
                <input
                  type="text"
                  placeholder="Header Value"
                  value={value}
                  onChange={(e) => updateHeader('add', key, key, e.target.value)}
                  className="flex-1 px-2 py-1 text-sm border border-gray-300 rounded"
                />
                <button
                  type="button"
                  onClick={() => removeHeader('add', key)}
                  className="p-1 text-red-600 hover:text-red-800"
                >
                  <Minus className="h-4 w-4" />
                </button>
              </div>
            ))}
          </div>
        )}

        {activeTab === 'modify' && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <h4 className="text-sm font-medium text-gray-700">修改请求头</h4>
              <button
                type="button"
                onClick={() => addHeader('modify')}
                className="flex items-center px-2 py-1 text-xs bg-blue-600 text-white rounded hover:bg-blue-700"
              >
                <Plus className="h-3 w-3 mr-1" />
                添加
              </button>
            </div>
            {Object.entries(headers.modify || {}).map(([key, value], index) => (
              <div key={index} className="flex gap-2">
                <input
                  type="text"
                  placeholder="Header Name"
                  value={key}
                  onChange={(e) => updateHeader('modify', key, e.target.value, value)}
                  className="flex-1 px-2 py-1 text-sm border border-gray-300 rounded"
                />
                <input
                  type="text"
                  placeholder="New Value"
                  value={value}
                  onChange={(e) => updateHeader('modify', key, key, e.target.value)}
                  className="flex-1 px-2 py-1 text-sm border border-gray-300 rounded"
                />
                <button
                  type="button"
                  onClick={() => removeHeader('modify', key)}
                  className="p-1 text-red-600 hover:text-red-800"
                >
                  <Minus className="h-4 w-4" />
                </button>
              </div>
            ))}
          </div>
        )}

        {activeTab === 'remove' && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <h4 className="text-sm font-medium text-gray-700">删除请求头</h4>
              <button
                type="button"
                onClick={addRemoveHeader}
                className="flex items-center px-2 py-1 text-xs bg-blue-600 text-white rounded hover:bg-blue-700"
              >
                <Plus className="h-3 w-3 mr-1" />
                添加
              </button>
            </div>
            {(headers.remove || []).map((headerName, index) => (
              <div key={index} className="flex gap-2">
                <input
                  type="text"
                  placeholder="Header Name to Remove"
                  value={headerName}
                  onChange={(e) => updateRemoveHeader(index, e.target.value)}
                  className="flex-1 px-2 py-1 text-sm border border-gray-300 rounded"
                />
                <button
                  type="button"
                  onClick={() => deleteRemoveHeader(index)}
                  className="p-1 text-red-600 hover:text-red-800"
                >
                  <Minus className="h-4 w-4" />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
