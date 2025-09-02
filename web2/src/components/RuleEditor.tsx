import { useState, useEffect } from 'react'
import { X, Plus, Trash2 } from 'lucide-react'
import { InterceptRule, InterceptCondition, InterceptAction } from '@/types'
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
    enabled: true,
    priority: 1,
    conditions: [] as InterceptCondition[],
    actions: [] as InterceptAction[]
  })

  const [errors, setErrors] = useState<Record<string, string>>({})

  // 初始化表单数据
  useEffect(() => {
    if (rule) {
      setFormData({
        name: rule.name,
        enabled: rule.enabled,
        priority: rule.priority,
        conditions: [...rule.conditions],
        actions: [...rule.actions]
      })
    } else {
      setFormData({
        name: '',
        enabled: true,
        priority: 1,
        conditions: [],
        actions: []
      })
    }
    setErrors({})
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

  // 添加条件
  const addCondition = () => {
    setFormData(prev => ({
      ...prev,
      conditions: [...prev.conditions, {
        type: 'url',
        operator: 'contains',
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
        type: 'block',
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
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[90vh] overflow-hidden flex flex-col">
        {/* 头部 */}
        <div className="flex items-center justify-between p-6 border-b border-gray-200">
          <h2 className="text-xl font-semibold text-gray-900">
            {rule ? '编辑拦截规则' : '新建拦截规则'}
          </h2>
          <button
            onClick={onClose}
            className="text-gray-400 hover:text-gray-600 transition-colors"
          >
            <X className="h-6 w-6" />
          </button>
        </div>

        {/* 内容区域 */}
        <div className="flex-1 overflow-y-auto p-6">
          <div className="space-y-6">
            {/* 基本信息 */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div className="md:col-span-2">
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
                  placeholder="请输入规则名称"
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
              </div>
            </div>

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

            {/* 匹配条件 */}
            <div>
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-lg font-medium text-gray-900">匹配条件</h3>
                <button
                  onClick={addCondition}
                  className="flex items-center px-3 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-md transition-colors"
                >
                  <Plus className="h-4 w-4 mr-1" />
                  添加条件
                </button>
              </div>

              {errors.conditions && (
                <p className="mb-4 text-sm text-red-600">{errors.conditions}</p>
              )}

              <div className="space-y-4">
                {formData.conditions.map((condition, index) => (
                  <div key={index} className="p-4 border border-gray-200 rounded-lg">
                    <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                      <div>
                        <label className="block text-sm font-medium text-gray-700 mb-2">
                          类型
                        </label>
                        <select
                          value={condition.type}
                          onChange={(e) => updateCondition(index, { type: e.target.value as any })}
                          className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                        >
                          <option value="url">URL</option>
                          <option value="method">请求方法</option>
                          <option value="header">请求头</option>
                          <option value="body">请求体</option>
                          <option value="status">状态码</option>
                        </select>
                      </div>

                      <div>
                        <label className="block text-sm font-medium text-gray-700 mb-2">
                          操作符
                        </label>
                        <select
                          value={condition.operator}
                          onChange={(e) => updateCondition(index, { operator: e.target.value as any })}
                          className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                        >
                          <option value="equals">等于</option>
                          <option value="contains">包含</option>
                          <option value="starts_with">开头为</option>
                          <option value="ends_with">结尾为</option>
                          <option value="regex">正则表达式</option>
                        </select>
                      </div>

                      <div>
                        <label className="block text-sm font-medium text-gray-700 mb-2">
                          值 *
                        </label>
                        <input
                          type="text"
                          value={condition.value}
                          onChange={(e) => updateCondition(index, { value: e.target.value })}
                          className={clsx(
                            'w-full px-3 py-2 border rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500',
                            errors[`condition_${index}_value`] ? 'border-red-300' : 'border-gray-300'
                          )}
                          placeholder="请输入匹配值"
                        />
                        {errors[`condition_${index}_value`] && (
                          <p className="mt-1 text-sm text-red-600">{errors[`condition_${index}_value`]}</p>
                        )}
                      </div>

                      <div className="flex items-end">
                        <button
                          onClick={() => removeCondition(index)}
                          className="p-2 text-red-600 hover:text-red-800 transition-colors"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </div>
                    </div>

                    {condition.type === 'url' || condition.type === 'header' || condition.type === 'body' ? (
                      <div className="mt-4">
                        <label className="flex items-center">
                          <input
                            type="checkbox"
                            checked={condition.caseSensitive || false}
                            onChange={(e) => updateCondition(index, { caseSensitive: e.target.checked })}
                            className="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
                          />
                          <span className="ml-2 text-sm text-gray-700">区分大小写</span>
                        </label>
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            </div>

            {/* 执行动作 */}
            <div>
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-lg font-medium text-gray-900">执行动作</h3>
                <button
                  onClick={addAction}
                  className="flex items-center px-3 py-2 text-sm bg-green-600 hover:bg-green-700 text-white rounded-md transition-colors"
                >
                  <Plus className="h-4 w-4 mr-1" />
                  添加动作
                </button>
              </div>

              {errors.actions && (
                <p className="mb-4 text-sm text-red-600">{errors.actions}</p>
              )}

              <div className="space-y-4">
                {formData.actions.map((action, index) => (
                  <div key={index} className="p-4 border border-gray-200 rounded-lg">
                    <div className="flex items-center justify-between mb-4">
                      <select
                        value={action.type}
                        onChange={(e) => {
                          const newType = e.target.value as any
                          updateAction(index, { 
                            type: newType,
                            parameters: {} // 重置参数
                          })
                        }}
                        className="px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                      >
                        <option value="block">阻止请求</option>
                        <option value="modify_request">修改请求</option>
                        <option value="modify_response">修改响应</option>
                        <option value="delay">延迟</option>
                        <option value="redirect">重定向</option>
                      </select>

                      <button
                        onClick={() => removeAction(index)}
                        className="p-2 text-red-600 hover:text-red-800 transition-colors"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>

                    {/* 动作参数 */}
                    {action.type === 'block' && (
                      <div>
                        <label className="block text-sm font-medium text-gray-700 mb-2">
                          阻止消息（可选）
                        </label>
                        <input
                          type="text"
                          value={action.parameters.message || ''}
                          onChange={(e) => updateAction(index, {
                            parameters: { ...action.parameters, message: e.target.value }
                          })}
                          className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                          placeholder="请求已被阻止"
                        />
                      </div>
                    )}

                    {action.type === 'redirect' && (
                      <div>
                        <label className="block text-sm font-medium text-gray-700 mb-2">
                          重定向URL *
                        </label>
                        <input
                          type="url"
                          value={action.parameters.url || ''}
                          onChange={(e) => updateAction(index, {
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
                    )}

                    {action.type === 'delay' && (
                      <div>
                        <label className="block text-sm font-medium text-gray-700 mb-2">
                          延迟时间（毫秒） *
                        </label>
                        <input
                          type="number"
                          min="0"
                          value={action.parameters.milliseconds || ''}
                          onChange={(e) => updateAction(index, {
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

                    {(action.type === 'modify_request' || action.type === 'modify_response') && (
                      <div className="space-y-4">
                        <div>
                          <label className="block text-sm font-medium text-gray-700 mb-2">
                            修改请求头（JSON格式）
                          </label>
                          <textarea
                            value={action.parameters.headers ? JSON.stringify(action.parameters.headers, null, 2) : ''}
                            onChange={(e) => {
                              try {
                                const headers = e.target.value ? JSON.parse(e.target.value) : {}
                                updateAction(index, {
                                  parameters: { ...action.parameters, headers }
                                })
                              } catch {
                                // 忽略JSON解析错误
                              }
                            }}
                            className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                            rows={3}
                            placeholder='{"User-Agent": "Custom Agent"}'
                          />
                        </div>

                        {action.type === 'modify_response' && (
                          <div>
                            <label className="block text-sm font-medium text-gray-700 mb-2">
                              修改响应体
                            </label>
                            <textarea
                              value={action.parameters.body || ''}
                              onChange={(e) => updateAction(index, {
                                parameters: { ...action.parameters, body: e.target.value }
                              })}
                              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                              rows={4}
                              placeholder="新的响应体内容"
                            />
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>

        {/* 底部按钮 */}
        <div className="flex justify-end space-x-3 p-6 border-t border-gray-200">
          <button
            onClick={onClose}
            className="px-4 py-2 text-gray-700 bg-gray-100 hover:bg-gray-200 rounded-md transition-colors"
          >
            取消
          </button>
          <button
            onClick={handleSave}
            className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-md transition-colors"
          >
            {rule ? '更新规则' : '创建规则'}
          </button>
        </div>
      </div>
    </div>
  )
}
