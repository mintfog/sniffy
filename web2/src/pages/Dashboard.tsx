import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, Globe, MessageSquare, Clock, TrendingUp } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { useAppStore } from '@/store'

export function Dashboard() {
  const { statistics, setStatistics } = useAppStore()

  // 获取统计数据
  const { data: statsData } = useQuery({
    queryKey: ['statistics'],
    queryFn: () => sniffyApi.getStatistics(),
    refetchInterval: 5000, // 每5秒刷新一次
  })

  useEffect(() => {
    if (statsData?.data) {
      setStatistics(statsData.data)
    }
  }, [statsData, setStatistics])

  const statCards = [
    {
      title: '总请求数',
      value: statistics.totalRequests.toLocaleString(),
      icon: Globe,
      color: 'bg-blue-500',
      change: '+12.5%',
    },
    {
      title: '活跃会话',
      value: statistics.totalSessions.toLocaleString(),
      icon: Activity,
      color: 'bg-green-500',
      change: '+5.2%',
    },
    {
      title: 'WebSocket 连接',
      value: '24',
      icon: MessageSquare,
      color: 'bg-purple-500',
      change: '+8.1%',
    },
    {
      title: '平均响应时间',
      value: `${statistics.averageResponseTime}ms`,
      icon: Clock,
      color: 'bg-orange-500',
      change: '-2.3%',
    },
  ]

  return (
    <div className="space-y-6">
      {/* 页面标题 */}
      <div>
        <h1 className="text-3xl font-bold text-gray-900">仪表板</h1>
        <p className="mt-2 text-gray-600">网络流量监控概览</p>
      </div>

      {/* 统计卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        {statCards.map((card) => {
          const Icon = card.icon
          return (
            <div key={card.title} className="bg-white rounded-lg border border-gray-200 p-6">
              <div className="flex items-center">
                <div className={`${card.color} p-3 rounded-lg`}>
                  <Icon className="h-6 w-6 text-white" />
                </div>
                <div className="ml-4 flex-1">
                  <p className="text-sm font-medium text-gray-600">{card.title}</p>
                  <div className="flex items-center">
                    <p className="text-2xl font-bold text-gray-900">{card.value}</p>
                    <span className={`ml-2 text-sm ${
                      card.change.startsWith('+') ? 'text-green-600' : 'text-red-600'
                    }`}>
                      {card.change}
                    </span>
                  </div>
                </div>
              </div>
            </div>
          )
        })}
      </div>

      {/* 图表区域 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* 请求趋势图 */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-lg font-semibold text-gray-900">请求趋势</h3>
            <TrendingUp className="h-5 w-5 text-gray-400" />
          </div>
          <div className="h-64 flex items-center justify-center text-gray-500">
            {/* 这里可以集成图表库如 Chart.js 或 Recharts */}
            <p>请求趋势图表 (待集成图表库)</p>
          </div>
        </div>

        {/* 状态码分布 */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-lg font-semibold text-gray-900 mb-4">HTTP 状态码分布</h3>
          <div className="space-y-3">
            {Object.entries(statistics.statusCodeDistribution).map(([code, count]) => (
              <div key={code} className="flex items-center justify-between">
                <div className="flex items-center">
                  <div className={`w-3 h-3 rounded-full mr-3 ${
                    code.startsWith('2') ? 'bg-green-500' :
                    code.startsWith('3') ? 'bg-yellow-500' :
                    code.startsWith('4') ? 'bg-orange-500' :
                    'bg-red-500'
                  }`} />
                  <span className="text-sm text-gray-600">{code}</span>
                </div>
                <span className="text-sm font-medium text-gray-900">{count}</span>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* 热门主机 */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-lg font-semibold text-gray-900 mb-4">热门主机</h3>
        <div className="overflow-x-auto">
          <table className="min-w-full">
            <thead>
              <tr className="border-b border-gray-200">
                <th className="text-left py-3 px-4 font-medium text-gray-900">主机</th>
                <th className="text-left py-3 px-4 font-medium text-gray-900">请求数</th>
                <th className="text-left py-3 px-4 font-medium text-gray-900">占比</th>
              </tr>
            </thead>
            <tbody>
              {statistics.topHosts.map((host) => (
                <tr key={host.host} className="border-b border-gray-100">
                  <td className="py-3 px-4 text-sm text-gray-900">{host.host}</td>
                  <td className="py-3 px-4 text-sm text-gray-600">{host.count}</td>
                  <td className="py-3 px-4 text-sm text-gray-600">
                    {((host.count / statistics.totalRequests) * 100).toFixed(1)}%
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
