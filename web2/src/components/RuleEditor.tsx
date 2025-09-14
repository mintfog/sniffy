import { useState, useEffect } from 'react'
import { X, Plus, Trash2, Info, ChevronDown, ChevronRight } from 'lucide-react'
import { 
  InterceptRule, 
  InterceptCondition, 
  InterceptAction, 
  ConditionType, 
  ConditionOperator, 
  ActionType 
} from '@/types'
import { ActionEditor } from './ActionEditor'
import clsx from 'clsx'

interface RuleEditorProps {
  rule?: InterceptRule | null
  isOpen: boolean
  onClose: () => void
  onSave: (rule: Omit<InterceptRule, 'id' | 'createdAt' | 'updatedAt'>) => void
}

export function RuleEditor({ rule, isOpen, onClose, onSave }: RuleEditorProps) {
  const [formData, setFormData] = useState({
    name: '',
    description: '',
    enabled: true,
    priority: 1,
    logicOperator: 'AND' as 'AND' | 'OR',
    tags: [] as string[],
    conditions: [] as InterceptCondition[],
    actions: [] as InterceptAction[]
  })
  
  const [expandedSections, setExpandedSections] = useState({
    conditions: true,
    actions: true,
    advanced: false
  })
  
  const [tagInput, setTagInput] = useState('')
  const [errors, setErrors] = useState<Record<string, string>>({})

  // 条件类型选项
  const conditionTypeOptions = [
    { group: 'URL相关', options: [
      { value: 'url', label: '完整URL' },
      { value: 'url_host', label: '主机名' },
      { value: 'url_path', label: '路径' },
      { value: 'url_query', label: '查询参数' },
      { value: 'url_fragment', label: 'URL片段' }
    ]},
    { group: 'HTTP相关', options: [
      { value: 'method', label: 'HTTP方法' },
      { value: 'scheme', label: '协议方案' },
      { value: 'port', label: '端口' }
    ]},
    { group: '请求相关', options: [
      { value: 'request_header', label: '请求头' },
      { value: 'request_body', label: '请求体' },
      { value: 'request_size', label: '请求大小' },
      { value: 'content_type', label: '内容类型' }
    ]},
    { group: '响应相关', options: [
      { value: 'response_status', label: '响应状态码' },
      { value: 'response_header', label: '响应头' },
      { value: 'response_body', label: '响应体' },
      { value: 'response_size', label: '响应大小' }
    ]},
    { group: '文件类型', options: [
      { value: 'file_extension', label: '文件扩展名' },
      { value: 'mime_type', label: 'MIME类型' }
    ]},
    { group: '其他', options: [
      { value: 'client_ip', label: '客户端IP' },
      { value: 'server_ip', label: '服务器IP' },
      { value: 'user_agent', label: '用户代理' }
    ]}
  ]

  // 操作符选项
  const getOperatorOptions = (conditionType: ConditionType) => {
    const textOperators = [
      { value: 'equals', label: '等于' },
      { value: 'not_equals', label: '不等于' },
      { value: 'contains', label: '包含' },
      { value: 'not_contains', label: '不包含' },
      { value: 'starts_with', label: '开头为' },
      { value: 'ends_with', label: '结尾为' },
      { value: 'regex', label: '正则表达式' },
      { value: 'not_regex', label: '不匹配正则' }
    ]
    
    const numericOperators = [
      { value: 'equals', label: '等于' },
      { value: 'not_equals', label: '不等于' },
      { value: 'greater_than', label: '大于' },
      { value: 'less_than', label: '小于' },
      { value: 'between', label: '在范围内' }
    ]
    
    const existenceOperators = [
      { value: 'exists', label: '存在' },
      { value: 'not_exists', label: '不存在' },
      { value: 'is_empty', label: '为空' },
      { value: 'not_empty', label: '不为空' }
    ]
    
    const listOperators = [
      { value: 'in_list', label: '在列表中' },
      { value: 'not_in_list', label: '不在列表中' }
    ]

    // 根据条件类型返回合适的操作符
    if (['request_size', 'response_size', 'port'].includes(conditionType)) {
      return [...numericOperators, ...existenceOperators]
    }
    
    if (['request_header', 'response_header'].includes(conditionType)) {
      return [...textOperators, ...existenceOperators]
    }
    
    if (['method', 'file_extension'].includes(conditionType)) {
      return [...textOperators, ...listOperators]
    }
    
    return textOperators
  }

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

  // 初始化表单数据
  useEffect(() => {
    if (rule) {
      setFormData({
        name: rule.name,
        description: rule.description || '',
        enabled: rule.enabled,
        priority: rule.priority,
        logicOperator: rule.logicOperator,
        tags: rule.tags || [],
        conditions: [...rule.conditions],
        actions: [...rule.actions]
      })
    } else {
      setFormData({
        name: '',
        description: '',
        enabled: true,
        priority: 1,
        logicOperator: 'AND',
        tags: [],
        conditions: [],
        actions: []
      })
    }
    setErrors({})
    setTagInput('')
  }, [rule, isOpen])

  // 验证表单
  const validateForm = () => {
    const newErrors: Record<string, string> = {}

    if (!formData.name.trim()) {
      newErrors.name = '规则名称不能为空'
    }

    if (formData.conditions.length === 0) {
      newErrors.conditions = '至少需要一个匹配条件'
    }

    if (formData.actions.length === 0) {
      newErrors.actions = '至少需要一个执行动作'
    }

    // 验证条件
    formData.conditions.forEach((condition, index) => {
      if (!condition.value.trim()) {
        newErrors[`condition_${index}_value`] = '条件值不能为空'
      }
    })

    // 验证动作
    formData.actions.forEach((action, index) => {
      if (action.type === 'redirect' && !action.parameters.url) {
        newErrors[`action_${index}_url`] = '重定向URL不能为空'
      }
      if (action.type === 'delay' && (!action.parameters.milliseconds || action.parameters.milliseconds < 0)) {
        newErrors[`action_${index}_delay`] = '延迟时间必须大于0'
      }
    })

    setErrors(newErrors)
    return Object.keys(newErrors).length === 0
  }

  // 处理保存
  const handleSave = () => {
    if (validateForm()) {
      onSave(formData)
      onClose()
    }
  }

  // 标签相关函数
  const addTag = () => {
    if (tagInput.trim() && !formData.tags.includes(tagInput.trim())) {
      setFormData(prev => ({
        ...prev,
        tags: [...prev.tags, tagInput.trim()]
      }))
      setTagInput('')
    }
  }

  const removeTag = (tagToRemove: string) => {
    setFormData(prev => ({
      ...prev,
      tags: prev.tags.filter(tag => tag !== tagToRemove)
    }))
  }

  // 添加条件
  const addCondition = () => {
    setFormData(prev => ({
      ...prev,
      conditions: [...prev.conditions, {
        type: 'url' as ConditionType,
        operator: 'contains' as ConditionOperator,
        value: '',
        caseSensitive: false
      }]
    }))
  }

  // 更新条件
  const updateCondition = (index: number, updates: Partial<InterceptCondition>) => {
    setFormData(prev => ({
      ...prev,
      conditions: prev.conditions.map((condition, i) => 
        i === index ? { ...condition, ...updates } : condition
      )
    }))
  }

  // 删除条件
  const removeCondition = (index: number) => {
    setFormData(prev => ({
      ...prev,
      conditions: prev.conditions.filter((_, i) => i !== index)
    }))
  }

  // 添加动作
  const addAction = () => {
    setFormData(prev => ({
      ...prev,
      actions: [...prev.actions, {
        type: 'block' as ActionType,
        enabled: true,
        description: '',
        parameters: {}
      }]
    }))
  }

  // 更新动作
  const updateAction = (index: number, updates: Partial<InterceptAction>) => {
    setFormData(prev => ({
      ...prev,
      actions: prev.actions.map((action, i) => 
        i === index ? { ...action, ...updates } : action
      )
    }))
  }

  // 删除动作
  const removeAction = (index: number) => {
    setFormData(prev => ({
      ...prev,
      actions: prev.actions.filter((_, i) => i !== index)
    }))
  }

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-6xl w-full max-h-[95vh] overflow-hidden flex flex-col">
        {/* 头部 */}
        <div className="flex items-center justify-between p-6 border-b border-gray-200 bg-gray-50">
          <div>
            <h2 className="text-xl font-semibold text-gray-900">
              {rule ? '编辑拦截规则' : '新建拦截规则'}
            </h2>
            <p className="text-sm text-gray-600 mt-1">
              创建强大的请求拦截和修改规则
            </p>
          </div>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-600 transition-colors p-2 hover:bg-gray-100 rounded-md"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* 内容区域 */}
        <div className="flex-1 overflow-y-auto">
          <div className="space-y-0">
            {/* 基本信息区域 */}
            <div className="p-6 border-b border-gray-200">
              <h3 className="text-lg font-medium text-gray-900 mb-4">基本信息</h3>
              <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                <div className="lg:col-span-2">
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    规则名称 *
                  </label>
                  <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData(prev => ({ ...prev, name: e.target.value }))}
                    className={clsx(
                      'w-full px-3 py-2 border rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500',
                      errors.name ? 'border-red-300' : 'border-gray-300'
                    )}
                    placeholder="为此拦截规则起一个描述性的名称"
                  />
                  {errors.name && (
                    <p className="mt-1 text-sm text-red-600">{errors.name}</p>
                  )}
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    优先级
                  </label>
                  <input
                    type="number"
                    min="1"
                    max="100"
                    value={formData.priority}
                    onChange={(e) => setFormData(prev => ({ ...prev, priority: parseInt(e.target.value) || 1 }))}
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                  />
                  <p className="mt-1 text-xs text-gray-500">数字越小优先级越高</p>
                </div>
              </div>

              <div className="mt-4">
                <label className="block text-sm font-medium text-gray-700 mb-2">
                  描述（可选）
                </label>
                <textarea
                  value={formData.description}
                  onChange={(e) => setFormData(prev => ({ ...prev, description: e.target.value }))}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                  rows={2}
                  placeholder="描述这个规则的用途和行为"
                />
              </div>

              <div className="mt-4 space-y-4">
                <div className="flex items-center">
                  <input
                    type="checkbox"
                    id="enabled"
                    checked={formData.enabled}
                    onChange={(e) => setFormData(prev => ({ ...prev, enabled: e.target.checked }))}
                    className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                  />
                  <label htmlFor="enabled" className="ml-2 text-sm text-gray-700">
                    启用此规则
                  </label>
                </div>

                {/* 标签管理 */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    标签
                  </label>
                  <div className="flex flex-wrap gap-2 mb-2">
                    {formData.tags.map((tag, index) => (
                      <span
                        key={index}
                        className="inline-flex items-center px-2.5 py-1 rounded-full text-xs font-medium bg-blue-100 text-blue-800"
                      >
                        {tag}
                        <button
                          type="button"
                          onClick={() => removeTag(tag)}
                          className="ml-1 text-blue-600 hover:text-blue-800"
                        >
                          <X className="h-3 w-3" />
                        </button>
                      </span>
                    ))}
                  </div>
                  <div className="flex gap-2">
                    <input
                      type="text"
                      value={tagInput}
                      onChange={(e) => setTagInput(e.target.value)}
                      onKeyPress={(e) => e.key === 'Enter' && (e.preventDefault(), addTag())}
                      className="flex-1 px-3 py-1 text-sm border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                      placeholder="输入标签后按回车添加"
                    />
                    <button
                      type="button"
                      onClick={addTag}
                      className="px-3 py-1 text-sm bg-blue-600 text-white rounded-md hover:bg-blue-700"
                    >
                      添加
                    </button>
                  </div>
                </div>
              </div>
            </div>

            {/* 匹配条件区域 */}
            <div className="border-b border-gray-200">
              <button
                type="button"
                onClick={() => setExpandedSections(prev => ({ ...prev, conditions: !prev.conditions }))}
                className="w-full p-6 flex items-center justify-between text-left hover:bg-gray-50"
              >
                <div className="flex items-center space-x-3">
                  <div className="flex items-center">
                    {expandedSections.conditions ? (
                      <ChevronDown className="h-5 w-5 text-gray-400" />
                    ) : (
                      <ChevronRight className="h-5 w-5 text-gray-400" />
                    )}
                  </div>
                  <div>
                    <h3 className="text-lg font-medium text-gray-900">匹配条件</h3>
                    <p className="text-sm text-gray-500">定义触发此规则的条件（{formData.conditions.length} 个条件）</p>
                  </div>
                </div>
                <div className="flex items-center space-x-3">
                  {formData.conditions.length > 1 && (
                    <div className="flex items-center space-x-2">
                      <span className="text-sm text-gray-500">逻辑关系：</span>
                      <select
                        value={formData.logicOperator}
                        onChange={(e) => setFormData(prev => ({ ...prev, logicOperator: e.target.value as 'AND' | 'OR' }))}
                        className="text-sm border border-gray-300 rounded-md px-2 py-1 focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                        onClick={(e) => e.stopPropagation()}
                      >
                        <option value="AND">且（AND）</option>
                        <option value="OR">或（OR）</option>
                      </select>
                    </div>
                  )}
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation()
                      addCondition()
                    }}
                    className="flex items-center px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-md transition-colors"
                  >
                    <Plus className="h-4 w-4 mr-1" />
                    添加条件
                  </button>
                </div>
              </button>

              {expandedSections.conditions && (
                <div className="px-6 pb-6">
                  {errors.conditions && (
                    <div className="mb-4 p-3 bg-red-50 border border-red-200 rounded-md">
                      <p className="text-sm text-red-600">{errors.conditions}</p>
                    </div>
                  )}

                  <div className="space-y-4">
                    {formData.conditions.map((condition, index) => (
                      <div key={index} className="bg-gray-50 border border-gray-200 rounded-lg p-4">
                        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
                          {/* 条件类型 */}
                          <div>
                            <label className="block text-sm font-medium text-gray-700 mb-2">
                              条件类型 *
                            </label>
                            <select
                              value={condition.type}
                              onChange={(e) => {
                                const newType = e.target.value as ConditionType
                                updateCondition(index, { 
                                  type: newType,
                                  operator: 'contains' as ConditionOperator,
                                  value: '',
                                  headerName: newType.includes('header') ? '' : undefined
                                })
                              }}
                              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                            >
                              {conditionTypeOptions.map(group => (
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

                          {/* 操作符 */}
                          <div>
                            <label className="block text-sm font-medium text-gray-700 mb-2">
                              匹配方式 *
                            </label>
                            <select
                              value={condition.operator}
                              onChange={(e) => updateCondition(index, { operator: e.target.value as ConditionOperator })}
                              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                            >
                              {getOperatorOptions(condition.type).map(option => (
                                <option key={option.value} value={option.value}>
                                  {option.label}
                                </option>
                              ))}
                            </select>
                          </div>

                          {/* 操作按钮 */}
                          <div className="flex items-end">
                            <button
                              type="button"
                              onClick={() => removeCondition(index)}
                              className="p-2 text-red-600 hover:text-red-800 hover:bg-red-50 rounded-md transition-colors"
                              title="删除条件"
                            >
                              <Trash2 className="h-4 w-4" />
                            </button>
                          </div>
                        </div>

                        {/* Header 名称输入 */}
                        {(condition.type === 'request_header' || condition.type === 'response_header') && (
                          <div className="mt-4">
                            <label className="block text-sm font-medium text-gray-700 mb-2">
                              头部名称 *
                            </label>
                            <input
                              type="text"
                              value={condition.headerName || ''}
                              onChange={(e) => updateCondition(index, { headerName: e.target.value })}
                              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                              placeholder="例如：Content-Type, Authorization"
                            />
                          </div>
                        )}

                        {/* 值输入 */}
                        {!['exists', 'not_exists', 'is_empty', 'not_empty'].includes(condition.operator) && (
                          <div className="mt-4">
                            <label className="block text-sm font-medium text-gray-700 mb-2">
                              匹配值 *
                              {condition.operator === 'between' && ' （起始值）'}
                            </label>
                            {['in_list', 'not_in_list'].includes(condition.operator) ? (
                              <textarea
                                value={Array.isArray(condition.value) ? condition.value.join('\n') : condition.value}
                                onChange={(e) => {
                                  const lines = e.target.value.split('\n').filter(line => line.trim())
                                  updateCondition(index, { value: lines })
                                }}
                                className={clsx(
                                  'w-full px-3 py-2 border rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500',
                                  errors[`condition_${index}_value`] ? 'border-red-300' : 'border-gray-300'
                                )}
                                rows={3}
                                placeholder="每行一个值\n例如：\nGET\nPOST\nPUT"
                              />
                            ) : (
                              <input
                                type={['request_size', 'response_size', 'port'].includes(condition.type) ? 'number' : 'text'}
                                value={condition.value}
                                onChange={(e) => {
                                  const value = ['request_size', 'response_size', 'port'].includes(condition.type) 
                                    ? parseInt(e.target.value) || 0
                                    : e.target.value
                                  updateCondition(index, { value })
                                }}
                                className={clsx(
                                  'w-full px-3 py-2 border rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500',
                                  errors[`condition_${index}_value`] ? 'border-red-300' : 'border-gray-300'
                                )}
                                placeholder={
                                  condition.operator === 'regex' ? '输入正则表达式' :
                                  ['request_size', 'response_size'].includes(condition.type) ? '字节数' :
                                  condition.type === 'port' ? '端口号' :
                                  '输入匹配值'
                                }
                              />
                            )}
                            {errors[`condition_${index}_value`] && (
                              <p className="mt-1 text-sm text-red-600">{errors[`condition_${index}_value`]}</p>
                            )}
                          </div>
                        )}

                        {/* Between 操作符的第二个值 */}
                        {condition.operator === 'between' && (
                          <div className="mt-4">
                            <label className="block text-sm font-medium text-gray-700 mb-2">
                              结束值 *
                            </label>
                            <input
                              type="number"
                              value={condition.value2 || ''}
                              onChange={(e) => updateCondition(index, { value2: parseInt(e.target.value) || 0 })}
                              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                              placeholder="输入结束值"
                            />
                          </div>
                        )}

                        {/* 选项 */}
                        <div className="mt-4 flex flex-wrap gap-4">
                          {!['request_size', 'response_size', 'port', 'response_status'].includes(condition.type) && (
                            <label className="flex items-center">
                              <input
                                type="checkbox"
                                checked={condition.caseSensitive || false}
                                onChange={(e) => updateCondition(index, { caseSensitive: e.target.checked })}
                                className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                              />
                              <span className="ml-2 text-sm text-gray-700">区分大小写</span>
                            </label>
                          )}
                          
                          <label className="flex items-center">
                            <input
                              type="checkbox"
                              checked={condition.negate || false}
                              onChange={(e) => updateCondition(index, { negate: e.target.checked })}
                              className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                            />
                            <span className="ml-2 text-sm text-gray-700">取反（NOT）</span>
                          </label>
                        </div>

                        {/* 帮助信息 */}
                        {condition.operator === 'regex' && (
                          <div className="mt-3 p-3 bg-blue-50 border border-blue-200 rounded-md">
                            <div className="flex items-start">
                              <Info className="h-4 w-4 text-blue-500 mt-0.5 mr-2 flex-shrink-0" />
                              <div className="text-sm text-blue-700">
                                <p>正则表达式示例：</p>
                                <ul className="mt-1 list-disc list-inside space-y-1">
                                  <li><code>\.(js|css|png|jpg)$</code> - 匹配文件扩展名</li>
                                  <li><code>api\.(\w+)\.com</code> - 匹配 API 域名</li>
                                  <li><code>^https://</code> - 匹配 HTTPS 协议</li>
                                </ul>
                              </div>
                            </div>
                          </div>
                        )}
                      </div>
                    ))}

                    {formData.conditions.length === 0 && (
                      <div className="text-center py-8 border-2 border-dashed border-gray-300 rounded-lg">
                        <div className="text-gray-500">
                          <Info className="h-8 w-8 mx-auto mb-2" />
                          <p>暂无匹配条件</p>
                          <p className="text-sm mt-1">点击“添加条件”开始配置规则</p>
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>

            {/* 执行动作区域 */}
            <div className="border-b border-gray-200">
              <button
                type="button"
                onClick={() => setExpandedSections(prev => ({ ...prev, actions: !prev.actions }))}
                className="w-full p-6 flex items-center justify-between text-left hover:bg-gray-50"
              >
                <div className="flex items-center space-x-3">
                  <div className="flex items-center">
                    {expandedSections.actions ? (
                      <ChevronDown className="h-5 w-5 text-gray-400" />
                    ) : (
                      <ChevronRight className="h-5 w-5 text-gray-400" />
                    )}
                  </div>
                  <div>
                    <h3 className="text-lg font-medium text-gray-900">执行动作</h3>
                    <p className="text-sm text-gray-500">定义触发此规则时执行的动作（{formData.actions.length} 个动作）</p>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation()
                    addAction()
                  }}
                  className="flex items-center px-3 py-1.5 text-sm bg-green-600 hover:bg-green-700 text-white rounded-md transition-colors"
                >
                  <Plus className="h-4 w-4 mr-1" />
                  添加动作
                </button>
              </button>

              {expandedSections.actions && (
                <div className="px-6 pb-6">
                  {errors.actions && (
                    <div className="mb-4 p-3 bg-red-50 border border-red-200 rounded-md">
                      <p className="text-sm text-red-600">{errors.actions}</p>
                    </div>
                  )}

                  <div className="space-y-4">
                    {formData.actions.map((action, index) => (
                      <ActionEditor
                        key={index}
                        action={action}
                        index={index}
                        onUpdate={(updates) => updateAction(index, updates)}
                        onRemove={() => removeAction(index)}
                        errors={errors}
                      />
                    ))}

                    {formData.actions.length === 0 && (
                      <div className="text-center py-8 border-2 border-dashed border-gray-300 rounded-lg">
                        <div className="text-gray-500">
                          <Info className="h-8 w-8 mx-auto mb-2" />
                          <p>暂无执行动作</p>
                          <p className="text-sm mt-1">点击“添加动作”定义规则行为</p>
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* 底部按钮 */}
        <div className="flex items-center justify-between p-6 border-t border-gray-200 bg-gray-50">
          <div className="flex items-center space-x-4 text-sm text-gray-600">
            <span>条件: {formData.conditions.length}</span>
            <span>动作: {formData.actions.length}</span>
            {formData.conditions.length > 1 && (
              <span>逻辑: {formData.logicOperator}</span>
            )}
          </div>
          <div className="flex space-x-3">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-gray-700 bg-white border border-gray-300 hover:bg-gray-50 rounded-md transition-colors font-medium"
            >
              取消
            </button>
            <button
              type="button"
              onClick={handleSave}
              className="px-6 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-md transition-colors font-medium shadow-sm"
            >
              {rule ? '保存更改' : '创建规则'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
