import { useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { ArrowDownToLine, ArrowUpFromLine, CircleDot, Plus, Trash2 } from 'lucide-react'
import { Bridge, type BreakRule } from '@/lib/bridge'
import { Button, Field, Panel, TextInput, Toggle } from '../ui/controls'
import { Chip, cx, EmptyState, IconButton, MethodTag } from '../ui/primitives'
import { PageShell } from './PageShell'

type Phase = 'request' | 'response'

interface PausedItem {
  id: string
  method: string
  url: string
  pausedAt: Phase
}

/** 后端 breakpoint_hit / getBreakpoints 推送的原始 flow（仅取所需字段）。 */
interface RawFlow {
  id: string
  request?: { method?: string; url?: string }
  response?: unknown
}

function toPaused(f: RawFlow): PausedItem | null {
  if (!f || !f.id) return null
  return {
    id: f.id,
    method: f.request?.method ?? 'GET',
    url: f.request?.url ?? '',
    pausedAt: f.response ? 'response' : 'request',
  }
}

export function BreakpointsView() {
  const [reqBreak, setReqBreak] = useState(false)
  const [respBreak, setRespBreak] = useState(false)
  const [rules, setRules] = useState<BreakRule[]>([])
  const [paused, setPaused] = useState<PausedItem[]>([])

  // 初始加载 + 事件订阅。
  useEffect(() => {
    let alive = true
    Bridge.getGlobalBreak()
      .then((g) => {
        if (alive && g) {
          setReqBreak(g.onRequest)
          setRespBreak(g.onResponse)
        }
      })
      .catch(() => {})
    Bridge.getBreakRules()
      .then((rs) => alive && rs && setRules(rs))
      .catch(() => {})
    Bridge.getBreakpoints()
      .then((list) => {
        if (!alive || !list) return
        const snapshot = (list as RawFlow[]).map(toPaused).filter((x): x is PausedItem => x !== null)
        // 合并而非替换：初次加载的 promise 可能晚于已到达的 breakpoint_hit 事件，直接替换会丢掉事件新增项。
        setPaused((prev) => {
          const byId = new Map(prev.map((p) => [p.id, p]))
          for (const item of snapshot) byId.set(item.id, item)
          return [...byId.values()]
        })
      })
      .catch(() => {})

    const offs: Array<() => void> = []
    try {
      offs.push(
        Events.On('breakpoint_hit', (e) => {
          const item = toPaused(e.data as RawFlow)
          if (!item) return
          setPaused((ps) =>
            ps.some((p) => p.id === item.id) ? ps.map((p) => (p.id === item.id ? item : p)) : [...ps, item],
          )
        }),
      )
      offs.push(
        Events.On('breakpoint_resolved', (e) => {
          const f = e.data as RawFlow
          if (f?.id) setPaused((ps) => ps.filter((p) => p.id !== f.id))
        }),
      )
    } catch {
      /* runtime 不可用 */
    }
    return () => {
      alive = false
      for (const off of offs) {
        try {
          off()
        } catch {
          /* ignore */
        }
      }
    }
  }, [])

  // 全局断点开关。
  const setGlobal = (onReq: boolean, onResp: boolean) => {
    setReqBreak(onReq)
    setRespBreak(onResp)
    Bridge.setGlobalBreak(onReq, onResp).catch(() => {})
  }

  // URL 断点规则。
  const addRule = async () => {
    // 用具体的占位 URL（带 * 通配，只匹配 example.com）而非裸 'https://'：
    // 后者无 * 会退化为子串匹配，命中所有 HTTPS 流量，添加规则瞬间就会暂停全部请求。
    const created = await Bridge.addBreakRule('https://example.com/*', true, false).catch(() => null)
    if (created) setRules((rs) => [...rs, created])
  }
  const patchRule = (rule: BreakRule, patch: Partial<BreakRule>) => {
    const next = { ...rule, ...patch }
    setRules((rs) => rs.map((r) => (r.id === rule.id ? next : r)))
    Bridge.updateBreakRule(next.id, next.url, next.onRequest, next.onResponse, next.enabled).catch(() => {})
  }
  const removeRule = (id: string) => {
    setRules((rs) => rs.filter((r) => r.id !== id))
    Bridge.deleteBreakRule(id).catch(() => {})
  }

  // 暂停项处置。
  const resolvePaused = (id: string) => {
    setPaused((ps) => ps.filter((p) => p.id !== id))
    Bridge.resumeBreakpoint(id, null).catch(() => {})
  }
  const abortPaused = (id: string) => {
    setPaused((ps) => ps.filter((p) => p.id !== id))
    Bridge.abortBreakpoint(id).catch(() => {})
  }

  return (
    <PageShell icon={CircleDot} title="断点" subtitle="拦截请求 / 响应，改包后手动放行">
      {/* 全局断点 */}
      <Panel title="全局断点" icon={<CircleDot className="h-4 w-4" />}>
        <Field label="请求断点" hint="对所有流量在请求发出前暂停，等待你修改请求头 / 请求体并手动放行。">
          <Toggle checked={reqBreak} onChange={(v) => setGlobal(v, respBreak)} />
        </Field>
        <Field label="响应断点" hint="对所有流量在响应返回客户端前暂停，可在此修改状态码与响应内容。">
          <Toggle checked={respBreak} onChange={(v) => setGlobal(reqBreak, v)} />
        </Field>
      </Panel>

      {/* 断点规则 */}
      <Panel
        title="断点规则"
        icon={<CircleDot className="h-4 w-4" />}
        right={
          <Button size="sm" variant="secondary" icon={<Plus className="h-3.5 w-3.5" />} onClick={addRule}>
            添加规则
          </Button>
        }
      >
        {rules.length === 0 ? (
          <div className="px-3 py-6">
            <EmptyState
              icon={<CircleDot className="h-7 w-7" />}
              title="暂无断点规则"
              hint="添加 URL 匹配规则（支持 * 通配），命中的请求 / 响应将按所选阶段拦截。"
            />
          </div>
        ) : (
          rules.map((rule) => (
            <RuleRow
              key={rule.id}
              rule={rule}
              onPatch={(patch) => patchRule(rule, patch)}
              onRemove={() => removeRule(rule.id)}
            />
          ))
        )}
      </Panel>

      {/* 暂停的请求 */}
      <Panel
        title="暂停的请求"
        icon={<CircleDot className="h-4 w-4" />}
        right={<Chip count={paused.length}>等待处理</Chip>}
      >
        {paused.length === 0 ? (
          <div className="px-3 py-8">
            <EmptyState
              icon={<CircleDot className="h-7 w-7" />}
              title="当前无暂停的请求"
              hint="开启上方断点后，命中的请求会在此等待处理"
            />
          </div>
        ) : (
          paused.map((item) => (
            <PausedRow
              key={item.id}
              item={item}
              onResolve={() => resolvePaused(item.id)}
              onAbort={() => abortPaused(item.id)}
            />
          ))
        )}
      </Panel>
    </PageShell>
  )
}

/* ───────────────────────── 规则行 ───────────────────────── */

function RuleRow({
  rule,
  onPatch,
  onRemove,
}: {
  rule: BreakRule
  onPatch: (patch: Partial<BreakRule>) => void
  onRemove: () => void
}) {
  const [url, setUrl] = useState(rule.url)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])

  const onUrl = (v: string) => {
    setUrl(v)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => onPatch({ url: v }), 400)
  }

  return (
    <div className={cx('flex items-center gap-2.5 px-3 py-2', !rule.enabled && 'opacity-55')}>
      <Toggle checked={rule.enabled} onChange={(v) => onPatch({ enabled: v })} />
      <TextInput
        value={url}
        onChange={(e) => onUrl(e.target.value)}
        width="100%"
        placeholder="https://example.com/*"
        title={url}
        className="flex-1 font-mono text-[11.5px]"
      />
      <button
        type="button"
        onClick={() => onPatch({ onRequest: !rule.onRequest })}
        className={cx(
          'shrink-0 rounded-full px-2 py-px text-[10px] font-medium transition-colors',
          rule.onRequest ? 'bg-info/15 text-info' : 'bg-fg-faint/10 text-fg-faint hover:text-fg',
        )}
        title="在请求阶段拦截"
      >
        请求
      </button>
      <button
        type="button"
        onClick={() => onPatch({ onResponse: !rule.onResponse })}
        className={cx(
          'shrink-0 rounded-full px-2 py-px text-[10px] font-medium transition-colors',
          rule.onResponse ? 'bg-iris/15 text-iris' : 'bg-fg-faint/10 text-fg-faint hover:text-fg',
        )}
        title="在响应阶段拦截"
      >
        响应
      </button>
      <IconButton size="sm" tone="danger" onClick={onRemove} title="删除规则">
        <Trash2 className="h-3.5 w-3.5" />
      </IconButton>
    </div>
  )
}

/* ───────────────────────── 暂停项行 ───────────────────────── */

function PausedRow({
  item,
  onResolve,
  onAbort,
}: {
  item: PausedItem
  onResolve: () => void
  onAbort: () => void
}) {
  const PhaseIcon = item.pausedAt === 'request' ? ArrowUpFromLine : ArrowDownToLine
  const phaseLabel = item.pausedAt === 'request' ? '请求' : '响应'

  return (
    <div className="flex items-center gap-2.5 px-3 py-2.5">
      <MethodTag method={item.method} className="w-12 shrink-0 text-center" />
      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-muted" title={item.url}>
        {item.url}
      </span>
      <span
        className={cx(
          'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-px text-[10px] font-medium',
          item.pausedAt === 'request' ? 'bg-info/15 text-info' : 'bg-iris/15 text-iris',
        )}
        title={`暂停于：${phaseLabel}`}
      >
        <PhaseIcon className="h-3 w-3" />
        暂停于 {phaseLabel}
      </span>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button variant="primary" size="sm" onClick={onResolve}>
          放行
        </Button>
        <Button variant="danger" size="sm" onClick={onAbort}>
          阻断
        </Button>
      </div>
    </div>
  )
}
