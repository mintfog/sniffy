import { useEffect, useState, useCallback } from 'react'
import { Play, Ban, Pause } from 'lucide-react'
import { sniffyApi } from '@/services/api'

interface PausedFlow {
  id: string
  protocol?: string
  state?: string
  request?: { method?: string; url?: string }
  response?: { status?: number }
}

export function Breakpoints() {
  const [paused, setPaused] = useState<PausedFlow[]>([])
  const [breakReq, setBreakReq] = useState(false)
  const [breakResp, setBreakResp] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const res = await sniffyApi.getBreakpoints()
      setPaused((res.data || []) as PausedFlow[])
    } catch {
      /* ignore */
    }
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 1500)
    const onHit = () => refresh()
    window.addEventListener('sniffy:breakpoint_hit', onHit)
    window.addEventListener('sniffy:breakpoint_resolved', onHit)
    return () => {
      clearInterval(t)
      window.removeEventListener('sniffy:breakpoint_hit', onHit)
      window.removeEventListener('sniffy:breakpoint_resolved', onHit)
    }
  }, [refresh])

  const applyGlobal = async (req: boolean, resp: boolean) => {
    setBreakReq(req)
    setBreakResp(resp)
    try {
      await sniffyApi.setGlobalBreak(req, resp)
    } catch {
      /* ignore */
    }
  }

  const resume = async (id: string) => {
    await sniffyApi.resumeBreakpoint(id)
    refresh()
  }
  const abort = async (id: string) => {
    await sniffyApi.abortBreakpoint(id)
    refresh()
  }

  return (
    <div className="p-4 space-y-4">
      <div className="bg-white rounded-lg border border-gray-200 p-4">
        <div className="flex items-center gap-2 font-semibold text-gray-800 mb-3">
          <Pause className="h-4 w-4" /> 断点开关
        </div>
        <div className="flex items-center gap-6">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={breakReq}
              onChange={(e) => applyGlobal(e.target.checked, breakResp)}
            />
            <span className="text-sm text-gray-700">断在请求</span>
          </label>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={breakResp}
              onChange={(e) => applyGlobal(breakReq, e.target.checked)}
            />
            <span className="text-sm text-gray-700">断在响应</span>
          </label>
          <span className="text-xs text-gray-400">
            开启后所有流量将在对应阶段暂停,等待手动放行(或 5 分钟后自动放行)。
          </span>
        </div>
      </div>

      <div className="bg-white rounded-lg border border-gray-200">
        <div className="p-3 border-b border-gray-200 font-semibold text-gray-800">
          暂停中的请求 ({paused.length})
        </div>
        {paused.length === 0 ? (
          <div className="p-6 text-sm text-gray-400">当前没有暂停的请求。</div>
        ) : (
          <ul>
            {paused.map((f) => (
              <li key={f.id} className="border-b border-gray-100">
                <div className="flex items-center justify-between px-3 py-2">
                  <button
                    className="flex-1 text-left"
                    onClick={() => setExpanded(expanded === f.id ? null : f.id)}
                  >
                    <span className="inline-block px-2 py-0.5 rounded text-xs bg-gray-100 text-gray-700 mr-2">
                      {f.request?.method || '-'}
                    </span>
                    <span className="text-sm text-gray-800">{f.request?.url || f.id}</span>
                  </button>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => resume(f.id)}
                      className="flex items-center gap-1 px-2 py-1 rounded text-xs bg-green-100 text-green-700"
                    >
                      <Play className="h-3 w-3" /> 放行
                    </button>
                    <button
                      onClick={() => abort(f.id)}
                      className="flex items-center gap-1 px-2 py-1 rounded text-xs bg-red-100 text-red-700"
                    >
                      <Ban className="h-3 w-3" /> 阻断
                    </button>
                  </div>
                </div>
                {expanded === f.id && (
                  <pre className="px-3 py-2 bg-gray-900 text-gray-200 text-xs overflow-x-auto">
                    {JSON.stringify(f, null, 2)}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
