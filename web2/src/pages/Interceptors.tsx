import { useState, useEffect } from 'react'
import { Play, Pause, Plus, Edit, Trash2, Filter, Search, BarChart3 } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { InterceptRule, InterceptStats } from '@/types'
import { RuleEditor } from '@/components/RuleEditor'
import { ExpandableCell } from '@/components/ui'
import clsx from 'clsx'

export function Interceptors() {
  const [rules, setRules] = useState<InterceptRule[]>([])
  const [stats, setStats] = useState<InterceptStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [searchTerm, setSearchTerm] = useState('')
  const [filterEnabled, setFilterEnabled] = useState<'all' | 'enabled' | 'disabled'>('all')
  const [selectedRule, setSelectedRule] = useState<InterceptRule | null>(null)
  const [showRuleEditor, setShowRuleEditor] = useState(false)

  // 加载拦截规则和统计信息
  const loadData = async () => {
    try {
      setLoading(true)
      const [rulesRes, statsRes] = await Promise.all([
        sniffyApi.getInterceptRules({ enabled: filterEnabled === 'all' ? undefined : filterEnabled === 'enabled' }),
        sniffyApi.getInterceptStats()
      ])
      setRules(rulesRes.data)
      setStats(statsRes.data)
    } catch (error) {
      console.error('加载拦截器数据失败:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadData()
  }, [filterEnabled])

  // 切换规则启用状态
  const toggleRule = async (rule: InterceptRule) => {
    try {
      await sniffyApi.toggleInterceptRule(rule.id, !rule.enabled)
      await loadData()
    } catch (error) {
      console.error('切换规则状态失败:', error)
    }
  }

  // 删除规则
  const deleteRule = async (ruleId: string) => {
    if (!confirm('确定要删除这个拦截规则吗？此操作不可撤销。')) return
    
    try {
      await sniffyApi.deleteInterceptRule(ruleId)
      await loadData()
    } catch (error) {
      console.error('删除规则失败:', error)
    }
  }

  // 保存规则
  const handleSaveRule = async (ruleData: Omit<InterceptRule, 'id' | 'createdAt' | 'updatedAt'>) => {
    try {
      if (selectedRule) {
        await sniffyApi.updateInterceptRule(selectedRule.id, ruleData)
      } else {
        await sniffyApi.createInterceptRule(ruleData)
      }
      await loadData()
    } catch (error) {
      console.error('保存规则失败:', error)
    }
  }

  // 过滤规则
  const filteredRules = rules.filter(rule => {
    // 搜索过滤
    if (searchTerm) {
      const term = searchTerm.toLowerCase()
      return (
        rule.name.toLowerCase().includes(term) ||
        rule.conditions.some(c => c.value.toLowerCase().includes(term)) ||
        rule.actions.some(a => a.type.toLowerCase().includes(term))
      )
    }
    return true
  })

  // 获取动作类型的中文名称
  const getActionTypeLabel = (type: string) => {
    const labels: Record<string, string> = {
      block: '阻止',
      modify_request: '修改请求',
      modify_response: '修改响应',
      delay: '延迟',
      redirect: '重定向'
    }
    return labels[type] || type
  }

  // 获取条件类型的中文名称
  const getConditionTypeLabel = (type: string) => {
    const labels: Record<string, string> = {
      url: 'URL',
      method: '方法',
      header: '请求头',
      body: '请求体',
      status: '状态码'
    }
    return labels[type] || type
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* 页面标题和统计 */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold text-gray-900">请求拦截器</h1>
          <p className="mt-2 text-gray-600">管理和配置HTTP请求拦截规则</p>
        </div>
        
        <button
          onClick={() => setShowRuleEditor(true)}
          className="flex items-center px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-md font-medium transition-colors"
        >
          <Plus className="h-4 w-4 mr-2" />
          新建规则
        </button>
      </div>

      {/* 统计卡片 */}
      {stats && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center">
              <BarChart3 className="h-8 w-8 text-blue-600" />
              <div className="ml-4">
                <p className="text-sm font-medium text-gray-600">总规则数</p>
                <p className="text-2xl font-bold text-gray-900">{stats.totalRules}</p>
              </div>
            </div>
          </div>
          
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center">
              <Play className="h-8 w-8 text-green-600" />
              <div className="ml-4">
                <p className="text-sm font-medium text-gray-600">活跃规则</p>
                <p className="text-2xl font-bold text-gray-900">{stats.activeRules}</p>
              </div>
            </div>
          </div>
          
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center">
              <Filter className="h-8 w-8 text-orange-600" />
              <div className="ml-4">
                <p className="text-sm font-medium text-gray-600">总拦截次数</p>
                <p className="text-2xl font-bold text-gray-900">{stats.totalInterceptions}</p>
              </div>
            </div>
          </div>
          
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center">
              <Trash2 className="h-8 w-8 text-red-600" />
              <div className="ml-4">
                <p className="text-sm font-medium text-gray-600">阻止请求</p>
                <p className="text-2xl font-bold text-gray-900">{stats.blockedRequests}</p>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* 搜索和过滤栏 */}
      <div className="bg-white rounded-lg border border-gray-200 p-4">
        <div className="flex items-center space-x-4">
          {/* 搜索框 */}
          <div className="flex-1 relative">
            <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-gray-400" />
            <input
              type="text"
              placeholder="搜索规则名称、条件或动作..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="pl-10 pr-4 py-2 w-full border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            />
          </div>

          {/* 状态过滤 */}
          <select
            value={filterEnabled}
            onChange={(e) => setFilterEnabled(e.target.value as any)}
            className="px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
          >
            <option value="all">所有规则</option>
            <option value="enabled">已启用</option>
            <option value="disabled">已禁用</option>
          </select>
        </div>

        <div className="mt-3 text-sm text-gray-600">
          显示 {filteredRules.length} / {rules.length} 个规则
        </div>
      </div>

      {/* 规则列表 */}
      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
        <div className="overflow-auto">
          <table className="min-w-full divide-y divide-gray-200 table-fixed">
            <thead className="bg-gray-50 sticky top-0 z-10">
              <tr>
                <th className="w-48 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  规则名称
                </th>
                <th className="w-24 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  状态
                </th>
                <th className="w-20 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  优先级
                </th>
                <th className="w-80 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  条件
                </th>
                <th className="w-48 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  动作
                </th>
                <th className="w-28 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  更新时间
                </th>
                <th className="w-20 px-4 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider">
                  操作
                </th>
              </tr>
            </thead>
            <tbody className="bg-white divide-y divide-gray-200">
              {filteredRules.map((rule) => (
                <tr key={rule.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3">
                    <ExpandableCell 
                      content={rule.name} 
                      maxLength={30} 
                      className="text-sm font-medium text-gray-900"
                    />
                  </td>
                  <td className="px-4 py-3">
                    <button
                      onClick={() => toggleRule(rule)}
                      className={clsx(
                        'flex items-center px-2 py-1 text-xs font-medium rounded-full transition-colors whitespace-nowrap',
                        rule.enabled 
                          ? 'text-green-700 bg-green-100 hover:bg-green-200' 
                          : 'text-gray-700 bg-gray-100 hover:bg-gray-200'
                      )}
                    >
                      {rule.enabled ? (
                        <Play className="h-3 w-3 mr-1" />
                      ) : (
                        <Pause className="h-3 w-3 mr-1" />
                      )}
                      {rule.enabled ? '已启用' : '已禁用'}
                    </button>
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-600 text-center">
                    {rule.priority}
                  </td>
                  <td className="px-4 py-3">
                    <div className="space-y-1">
                      {rule.conditions.slice(0, 2).map((condition, index) => (
                        <ExpandableCell 
                          key={index}
                          content={`${getConditionTypeLabel(condition.type)}: ${condition.value}`}
                          maxLength={40}
                          className="text-sm text-gray-600"
                        />
                      ))}
                      {rule.conditions.length > 2 && (
                        <div className="text-xs text-gray-400">
                          +{rule.conditions.length - 2} 个条件
                        </div>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {rule.actions.slice(0, 3).map((action, index) => (
                        <span
                          key={index}
                          className={clsx(
                            'px-2 py-1 text-xs font-medium rounded whitespace-nowrap',
                            action.type === 'block' ? 'text-red-700 bg-red-100' :
                            action.type === 'modify_request' ? 'text-blue-700 bg-blue-100' :
                            action.type === 'modify_response' ? 'text-purple-700 bg-purple-100' :
                            action.type === 'delay' ? 'text-orange-700 bg-orange-100' :
                            action.type === 'redirect' ? 'text-green-700 bg-green-100' :
                            'text-gray-700 bg-gray-100'
                          )}
                        >
                          {getActionTypeLabel(action.type)}
                        </span>
                      ))}
                      {rule.actions.length > 3 && (
                        <span className="text-xs text-gray-400">+{rule.actions.length - 3}</span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {new Date(rule.updatedAt).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end space-x-1">
                      <button
                        onClick={() => {
                          setSelectedRule(rule)
                          setShowRuleEditor(true)
                        }}
                        className="p-1 text-blue-600 hover:text-blue-800 hover:bg-blue-50 rounded transition-colors"
                        title="编辑"
                      >
                        <Edit className="h-4 w-4" />
                      </button>
                      <button
                        onClick={() => deleteRule(rule.id)}
                        className="p-1 text-red-600 hover:text-red-800 hover:bg-red-50 rounded transition-colors"
                        title="删除"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {filteredRules.length === 0 && (
          <div className="text-center py-12">
            <Filter className="h-12 w-12 text-gray-300 mx-auto mb-4" />
            <p className="text-gray-500">
              {searchTerm || filterEnabled !== 'all' ? '没有找到匹配的规则' : '暂无拦截规则'}
            </p>
            {!searchTerm && filterEnabled === 'all' && (
              <button
                onClick={() => setShowRuleEditor(true)}
                className="mt-4 text-blue-600 hover:text-blue-800 font-medium"
              >
                创建第一个拦截规则
              </button>
            )}
          </div>
        )}
      </div>

      {/* 规则编辑器 */}
      <RuleEditor
        rule={selectedRule}
        isOpen={showRuleEditor}
        onClose={() => {
          setShowRuleEditor(false)
          setSelectedRule(null)
        }}
        onSave={handleSaveRule}
      />
    </div>
  )
}
