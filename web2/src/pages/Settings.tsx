import { useState, useEffect } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { Save, RefreshCw, Shield, Zap, Plug, Download } from 'lucide-react'
import { sniffyApi } from '@/services/api'
import { SniffyConfig } from '@/types'
import clsx from 'clsx'

export function Settings() {
  const [config, setConfig] = useState<SniffyConfig | null>(null)
  const [activeTab, setActiveTab] = useState('general')

  // 获取当前配置
  const { data: configData, refetch } = useQuery({
    queryKey: ['config'],
    queryFn: () => sniffyApi.getConfig(),
  })

  // 更新配置
  const updateConfigMutation = useMutation({
    mutationFn: (newConfig: Partial<SniffyConfig>) => sniffyApi.updateConfig(newConfig),
    onSuccess: () => {
      refetch()
    },
  })

  // 获取插件列表
  const { data: _pluginsData } = useQuery({
    queryKey: ['plugins'],
    queryFn: () => sniffyApi.getPlugins(),
  })

  useEffect(() => {
    if (configData?.data) {
      setConfig(configData.data)
    }
  }, [configData])

  const handleConfigChange = (key: keyof SniffyConfig, value: any) => {
    if (config) {
      setConfig({ ...config, [key]: value })
    }
  }

  const handleSave = () => {
    if (config) {
      updateConfigMutation.mutate(config)
    }
  }

  const tabs = [
    { id: 'general', label: '常规设置', icon: Zap },
    { id: 'security', label: '安全设置', icon: Shield },
    { id: 'plugins', label: '插件管理', icon: Plug },
  ]

  if (!config) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* 页面标题 */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-3xl font-bold text-gray-900">设置</h1>
          <p className="mt-2 text-gray-600">配置 Sniffy 代理服务器参数</p>
        </div>

        <button
          onClick={handleSave}
          disabled={updateConfigMutation.isPending}
          className="flex items-center px-4 py-2 bg-primary-600 hover:bg-primary-700 text-white rounded-md font-medium transition-colors disabled:opacity-50"
        >
          {updateConfigMutation.isPending ? (
            <RefreshCw className="h-4 w-4 mr-2 animate-spin" />
          ) : (
            <Save className="h-4 w-4 mr-2" />
          )}
          保存配置
        </button>
      </div>

      <div className="bg-white rounded-lg border border-gray-200">
        {/* 标签页导航 */}
        <div className="border-b border-gray-200">
          <nav className="flex space-x-8 px-6">
            {tabs.map((tab) => {
              const Icon = tab.icon
              return (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  className={clsx(
                    'flex items-center py-4 px-1 border-b-2 font-medium text-sm transition-colors',
                    activeTab === tab.id
                      ? 'border-primary-500 text-primary-600'
                      : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
                  )}
                >
                  <Icon className="h-4 w-4 mr-2" />
                  {tab.label}
                </button>
              )
            })}
          </nav>
        </div>

        {/* 标签页内容 */}
        <div className="p-6">
          {activeTab === 'general' && (
            <div className="space-y-6">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                {/* 监听端口 */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    监听端口
                  </label>
                  <input
                    type="number"
                    value={config.port}
                    onChange={(e) => handleConfigChange('port', parseInt(e.target.value))}
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
                  />
                  <p className="text-xs text-gray-500 mt-1">代理服务器监听的端口号</p>
                </div>

                {/* 监听地址 */}
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-2">
                    监听地址
                  </label>
                  <input
                    type="text"
                    value={config.host}
                    onChange={(e) => handleConfigChange('host', e.target.value)}
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
                  />
                  <p className="text-xs text-gray-500 mt-1">代理服务器绑定的IP地址</p>
                </div>
              </div>

              {/* HTTPS 支持 */}
              <div>
                <label className="flex items-center">
                  <input
                    type="checkbox"
                    checked={config.enableHTTPS}
                    onChange={(e) => handleConfigChange('enableHTTPS', e.target.checked)}
                    className="h-4 w-4 text-primary-600 focus:ring-primary-500 border-gray-300 rounded"
                  />
                  <span className="ml-2 text-sm font-medium text-gray-700">启用 HTTPS 代理</span>
                </label>
                <p className="text-xs text-gray-500 mt-1 ml-6">启用后可以拦截和分析 HTTPS 流量</p>
              </div>

              {/* 录制状态 */}
              <div>
                <label className="flex items-center">
                  <input
                    type="checkbox"
                    checked={config.recording}
                    onChange={(e) => handleConfigChange('recording', e.target.checked)}
                    className="h-4 w-4 text-primary-600 focus:ring-primary-500 border-gray-300 rounded"
                  />
                  <span className="ml-2 text-sm font-medium text-gray-700">自动开始录制</span>
                </label>
                <p className="text-xs text-gray-500 mt-1 ml-6">启动时自动开始录制网络流量</p>
              </div>
            </div>
          )}

          {activeTab === 'security' && (
            <div className="space-y-6">
              {/* CA 证书路径 */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-2">
                  CA 证书路径
                </label>
                <div className="flex space-x-2">
                  <input
                    type="text"
                    value={config.caCertPath || ''}
                    onChange={(e) => handleConfigChange('caCertPath', e.target.value)}
                    className="flex-1 px-3 py-2 border border-gray-300 rounded-md focus:ring-2 focus:ring-primary-500 focus:border-primary-500"
                    placeholder="留空使用默认证书"
                  />
                  <button className="px-4 py-2 bg-gray-100 hover:bg-gray-200 text-gray-700 rounded-md font-medium transition-colors">
                    浏览
                  </button>
                </div>
                <p className="text-xs text-gray-500 mt-1">自定义 CA 证书文件路径</p>
              </div>

              {/* 证书操作 */}
              <div className="border border-gray-200 rounded-lg p-4">
                <h4 className="font-medium text-gray-900 mb-4">证书管理</h4>
                
                <div className="space-y-3">
                  <button className="flex items-center px-4 py-2 bg-green-600 hover:bg-green-700 text-white rounded-md font-medium transition-colors">
                    <Download className="h-4 w-4 mr-2" />
                    下载 CA 证书
                  </button>
                  
                  <button className="flex items-center px-4 py-2 bg-orange-600 hover:bg-orange-700 text-white rounded-md font-medium transition-colors">
                    <RefreshCw className="h-4 w-4 mr-2" />
                    重新生成证书
                  </button>
                </div>

                <div className="mt-4 p-3 bg-yellow-50 border border-yellow-200 rounded-md">
                  <p className="text-sm text-yellow-800">
                    <strong>注意：</strong> 重新生成证书后，需要在客户端重新安装新的 CA 证书。
                  </p>
                </div>
              </div>
            </div>
          )}

          {activeTab === 'plugins' && (
            <div className="space-y-6">
              <div className="flex items-center justify-between">
                <h4 className="font-medium text-gray-900">已安装的插件</h4>
                <span className="text-sm text-gray-500">
                  {config.plugins.length} 个插件
                </span>
              </div>

              <div className="space-y-4">
                {config.plugins.map((plugin, index) => (
                  <div key={plugin.name} className="border border-gray-200 rounded-lg p-4">
                    <div className="flex items-center justify-between">
                      <div>
                        <h5 className="font-medium text-gray-900">{plugin.name}</h5>
                        <p className="text-sm text-gray-500 mt-1">
                          {plugin.name === 'logger' && '记录所有网络请求到日志'}
                          {plugin.name === 'connection_monitor' && '监控连接状态和统计信息'}
                          {plugin.name === 'request_modifier' && '修改请求和响应内容'}
                          {plugin.name === 'websocket_logger' && '记录 WebSocket 消息'}
                        </p>
                      </div>

                      <label className="flex items-center">
                        <input
                          type="checkbox"
                          checked={plugin.enabled}
                          onChange={(e) => {
                            const newPlugins = [...config.plugins]
                            newPlugins[index] = { ...plugin, enabled: e.target.checked }
                            handleConfigChange('plugins', newPlugins)
                          }}
                          className="h-4 w-4 text-primary-600 focus:ring-primary-500 border-gray-300 rounded"
                        />
                        <span className="ml-2 text-sm text-gray-700">
                          {plugin.enabled ? '已启用' : '已禁用'}
                        </span>
                      </label>
                    </div>

                    {plugin.enabled && plugin.config && Object.keys(plugin.config).length > 0 && (
                      <div className="mt-4 pt-4 border-t border-gray-200">
                        <h6 className="text-sm font-medium text-gray-700 mb-2">配置选项</h6>
                        <div className="space-y-2">
                          {Object.entries(plugin.config).map(([key, value]) => (
                            <div key={key} className="flex items-center justify-between">
                              <span className="text-sm text-gray-600">{key}:</span>
                              <span className="text-sm text-gray-900">{String(value)}</span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                ))}
              </div>

              {config.plugins.length === 0 && (
                <div className="text-center py-8">
                  <Plug className="h-12 w-12 text-gray-300 mx-auto mb-4" />
                  <p className="text-gray-500">暂无安装的插件</p>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
