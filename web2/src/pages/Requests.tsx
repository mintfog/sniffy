import { useState } from 'react'
import { Search, Download, Trash2, Shield, ShieldOff, Zap } from 'lucide-react'
import { useAppStore } from '@/store'
import { ExpandableCell } from '@/components/ui'
import clsx from 'clsx'

export function Requests() {
  const { sessions, clearAllData } = useAppStore()
  const [searchTerm, setSearchTerm] = useState('')
  const [filterBy, setFilterBy] = useState('all')
  const [sortBy, setSortBy] = useState('time')

  // 过滤和搜索逻辑
  const filteredSessions = sessions.filter(session => {
    // 搜索过滤
    if (searchTerm) {
      const term = searchTerm.toLowerCase()
      return (
        session.request.url.toLowerCase().includes(term) ||
        session.request.method.toLowerCase().includes(term) ||
        session.request.host.toLowerCase().includes(term)
      )
    }

    // 状态过滤
    if (filterBy !== 'all') {
      if (filterBy === 'success' && session.response?.status && session.response.status >= 200 && session.response.status < 300) return true
      if (filterBy === 'error' && session.response?.status && session.response.status >= 400) return true
      if (filterBy === 'pending' && session.status === 'pending') return true
      if (filterBy !== 'all') return false
    }

    return true
  })

  // 排序逻辑
  const sortedSessions = [...filteredSessions].sort((a, b) => {
    switch (sortBy) {
      case 'time':
        return new Date(b.request.timestamp).getTime() - new Date(a.request.timestamp).getTime()
      case 'status':
        return (a.response?.status || 0) - (b.response?.status || 0)
      case 'method':
        return a.request.method.localeCompare(b.request.method)
      case 'url':
        return a.request.url.localeCompare(b.request.url)
      case 'duration':
        return (b.duration || 0) - (a.duration || 0)
      default:
        return 0
    }
  })

  const handleClearAll = () => {
    if (confirm('确定要清除所有请求记录吗？此操作不可撤销。')) {
      clearAllData()
    }
  }

  return (
    <div className="space-y-6">
      {/* 页面标题和操作栏 */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold text-gray-900">请求详情</h1>
          <p className="mt-2 text-gray-600">查看和分析所有 HTTP 请求的详细信息</p>
        </div>
        
        <div className="flex items-center space-x-3">
          <button
            onClick={handleClearAll}
            className="flex items-center px-4 py-2 bg-red-600 hover:bg-red-700 text-white rounded-md font-medium transition-colors"
          >
            <Trash2 className="h-4 w-4 mr-2" />
            清除全部
          </button>
          
          <button className="flex items-center px-4 py-2 bg-green-600 hover:bg-green-700 text-white rounded-md font-medium transition-colors">
            <Download className="h-4 w-4 mr-2" />
            导出 HAR
          </button>
        </div>
      </div>

      {/* 搜索和过滤栏 */}
      <div className="bg-white rounded-lg border border-gray-200 p-4">
        <div className="flex items-center space-x-4">
          {/* 搜索框 */}
          <div className="flex-1 relative">
            <Search className="absolute left-3 top-1/2 transform -translate-y-1/2 h-4 w-4 text-gray-400" />
            <input
              type="text"
              placeholder="搜索 URL、方法或主机..."
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="pl-10 pr-4 py-2 w-full border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
            />
          </div>

          {/* 状态过滤 */}
          <select
            value={filterBy}
            onChange={(e) => setFilterBy(e.target.value)}
            className="px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
          >
            <option value="all">所有状态</option>
            <option value="success">成功 (2xx)</option>
            <option value="error">错误 (4xx/5xx)</option>
            <option value="pending">进行中</option>
          </select>

          {/* 排序 */}
          <select
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value)}
            className="px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
          >
            <option value="time">按时间</option>
            <option value="status">按状态码</option>
            <option value="method">按方法</option>
            <option value="url">按 URL</option>
            <option value="duration">按耗时</option>
          </select>
        </div>

        <div className="mt-3 text-sm text-gray-600">
          显示 {sortedSessions.length} / {sessions.length} 个请求
        </div>
      </div>

      {/* 请求列表表格 */}
      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
        <div className="overflow-auto max-h-[calc(100vh-20rem)]">
          <table className="min-w-full divide-y divide-gray-200 table-fixed">
            <thead className="bg-gray-50 sticky top-0 z-10">
              <tr>
                <th className="w-20 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  方法
                </th>
                <th className="w-20 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  状态
                </th>
                <th className="w-20 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  拦截
                </th>
                <th className="w-80 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  URL
                </th>
                <th className="w-32 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  主机
                </th>
                <th className="w-20 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  耗时
                </th>
                <th className="w-20 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  大小
                </th>
                <th className="w-24 px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                  时间
                </th>
              </tr>
            </thead>
            <tbody className="bg-white divide-y divide-gray-200">
              {sortedSessions.map((session) => (
                <tr key={session.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3">
                    <span className={clsx(
                      'px-2 py-1 text-xs font-medium rounded whitespace-nowrap',
                      session.request.method === 'GET' ? 'text-green-700 bg-green-100' :
                      session.request.method === 'POST' ? 'text-blue-700 bg-blue-100' :
                      session.request.method === 'PUT' ? 'text-orange-700 bg-orange-100' :
                      session.request.method === 'DELETE' ? 'text-red-700 bg-red-100' :
                      'text-gray-700 bg-gray-100'
                    )}>
                      {session.request.method}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    {session.response ? (
                      <span className={clsx(
                        'px-2 py-1 text-xs font-medium rounded whitespace-nowrap',
                        session.response.status >= 200 && session.response.status < 300 ? 'text-green-700 bg-green-100' :
                        session.response.status >= 300 && session.response.status < 400 ? 'text-blue-700 bg-blue-100' :
                        session.response.status >= 400 && session.response.status < 500 ? 'text-orange-700 bg-orange-100' :
                        'text-red-700 bg-red-100'
                      )}>
                        {session.response.status}
                      </span>
                    ) : (
                      <span className="px-2 py-1 text-xs font-medium rounded text-yellow-700 bg-yellow-100 whitespace-nowrap">
                        进行中
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    {session.blocked ? (
                      <span className="flex items-center px-2 py-1 text-xs font-medium rounded text-red-700 bg-red-100 whitespace-nowrap">
                        <ShieldOff className="h-3 w-3 mr-1" />
                        已阻止
                      </span>
                    ) : session.modified ? (
                      <span className="flex items-center px-2 py-1 text-xs font-medium rounded text-orange-700 bg-orange-100 whitespace-nowrap">
                        <Zap className="h-3 w-3 mr-1" />
                        已修改
                      </span>
                    ) : (
                      <span className="flex items-center px-2 py-1 text-xs font-medium rounded text-gray-600 bg-gray-100 whitespace-nowrap">
                        <Shield className="h-3 w-3 mr-1" />
                        正常
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <ExpandableCell 
                      content={session.request.url} 
                      maxLength={60} 
                      showCopy={true}
                      className="w-full"
                    />
                  </td>
                  <td className="px-4 py-3">
                    <ExpandableCell 
                      content={session.request.host} 
                      maxLength={20} 
                      className="text-sm text-gray-600"
                    />
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {session.duration ? `${session.duration}ms` : '-'}
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {session.response ? 
                      `${session.response.size < 1024 ? session.response.size + 'B' : 
                        session.response.size < 1024 * 1024 ? (session.response.size / 1024).toFixed(1) + 'KB' :
                        (session.response.size / (1024 * 1024)).toFixed(1) + 'MB'}` : '-'}
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {new Date(session.request.timestamp).toLocaleTimeString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {sortedSessions.length === 0 && (
          <div className="text-center py-12">
            <Search className="h-12 w-12 text-gray-300 mx-auto mb-4" />
            <p className="text-gray-500">
              {searchTerm || filterBy !== 'all' ? '没有找到匹配的请求' : '暂无请求记录'}
            </p>
          </div>
        )}
      </div>
    </div>
  )
}
