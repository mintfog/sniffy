import { useEffect, useState, useCallback } from 'react'
import Editor from '@monaco-editor/react'
import { Save, Power, RefreshCw, Plug } from 'lucide-react'
import { sniffyApi } from '@/services/api'

interface PluginInfo {
  id: string
  name: string
  version?: string
  description?: string
  author?: string
  runtime?: string
  enabled: boolean
  priority?: number
  logs?: string[]
}

export function Plugins() {
  const [plugins, setPlugins] = useState<PluginInfo[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [source, setSource] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')

  const loadPlugins = useCallback(async () => {
    try {
      const res = await sniffyApi.getPlugins()
      const list = (res.data || []) as PluginInfo[]
      setPlugins(list)
      if (!selected && list.length > 0) setSelected(list[0].id)
    } catch (e) {
      setMessage('加载插件列表失败')
    }
  }, [selected])

  const loadSource = useCallback(async (id: string) => {
    try {
      const res = await sniffyApi.getPluginSource(id)
      setSource(res.data?.source || '')
      setDirty(false)
    } catch (e) {
      setSource('// 无法加载源码')
    }
  }, [])

  useEffect(() => { loadPlugins() }, [])
  useEffect(() => { if (selected) loadSource(selected) }, [selected, loadSource])

  const current = plugins.find((p) => p.id === selected)

  const save = async () => {
    if (!selected) return
    setSaving(true)
    setMessage('')
    try {
      await sniffyApi.savePluginSource(selected, source)
      setDirty(false)
      setMessage('已保存并热重载 ✓')
      await loadPlugins()
    } catch (e) {
      setMessage('保存失败')
    } finally {
      setSaving(false)
    }
  }

  const toggleEnabled = async (p: PluginInfo) => {
    try {
      if (p.enabled) await sniffyApi.disablePlugin(p.id)
      else await sniffyApi.enablePlugin(p.id)
      await loadPlugins()
    } catch (e) {
      setMessage('切换状态失败')
    }
  }

  return (
    <div className="flex h-[calc(100vh-4rem)] gap-4 p-4">
      {/* 插件列表 */}
      <div className="w-72 flex-shrink-0 bg-white rounded-lg border border-gray-200 overflow-y-auto">
        <div className="flex items-center justify-between p-3 border-b border-gray-200">
          <div className="flex items-center gap-2 font-semibold text-gray-800">
            <Plug className="h-4 w-4" /> 插件
          </div>
          <button onClick={loadPlugins} className="p-1 rounded hover:bg-gray-100" title="刷新">
            <RefreshCw className="h-4 w-4 text-gray-500" />
          </button>
        </div>
        <ul>
          {plugins.map((p) => (
            <li key={p.id}>
              <button
                onClick={() => setSelected(p.id)}
                className={`w-full text-left px-3 py-2 border-b border-gray-100 hover:bg-gray-50 ${
                  selected === p.id ? 'bg-primary-50' : ''
                }`}
              >
                <div className="flex items-center justify-between">
                  <span className="font-medium text-gray-800 truncate">{p.name || p.id}</span>
                  <span
                    className={`w-2 h-2 rounded-full ${p.enabled ? 'bg-green-500' : 'bg-gray-300'}`}
                    title={p.enabled ? '已启用' : '已禁用'}
                  />
                </div>
                <div className="text-xs text-gray-500 truncate">{p.description}</div>
              </button>
            </li>
          ))}
          {plugins.length === 0 && (
            <li className="p-4 text-sm text-gray-400">暂无插件。插件目录:用户配置目录 /sniffy/plugins</li>
          )}
        </ul>
      </div>

      {/* 编辑器 */}
      <div className="flex-1 flex flex-col bg-white rounded-lg border border-gray-200 overflow-hidden">
        <div className="flex items-center justify-between p-3 border-b border-gray-200">
          <div className="font-semibold text-gray-800">
            {current ? `${current.name} (${current.id})` : '未选择插件'}
          </div>
          <div className="flex items-center gap-2">
            {message && <span className="text-sm text-gray-500">{message}</span>}
            {current && (
              <button
                onClick={() => toggleEnabled(current)}
                className={`flex items-center gap-1 px-3 py-1.5 rounded text-sm ${
                  current.enabled ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'
                }`}
              >
                <Power className="h-4 w-4" /> {current.enabled ? '已启用' : '已禁用'}
              </button>
            )}
            <button
              onClick={save}
              disabled={!selected || saving || !dirty}
              className="flex items-center gap-1 px-3 py-1.5 rounded text-sm bg-primary-600 text-white disabled:opacity-40"
            >
              <Save className="h-4 w-4" /> 保存并重载
            </button>
          </div>
        </div>

        <div className="flex-1 min-h-0">
          <Editor
            height="100%"
            defaultLanguage="javascript"
            theme="vs-dark"
            value={source}
            onChange={(v) => {
              setSource(v || '')
              setDirty(true)
            }}
            options={{ fontSize: 13, minimap: { enabled: false }, scrollBeyondLastLine: false }}
          />
        </div>

        {/* 日志面板 */}
        {current?.logs && current.logs.length > 0 && (
          <div className="h-32 border-t border-gray-200 bg-gray-900 text-gray-200 text-xs font-mono p-2 overflow-y-auto">
            {current.logs.map((l, i) => (
              <div key={i}>{l}</div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
