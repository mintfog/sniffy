import { useState } from 'react'
import { FileCode2, Plus, Puzzle, Save } from 'lucide-react'
import { Button, Toggle } from '../ui/controls'
import { Chip, cx, EmptyState } from '../ui/primitives'
import { PageShell } from './PageShell'

interface Plugin {
  id: string
  name: string
  version: string
  description: string
  enabled: boolean
  /** 触发的钩子标签，用于详情区展示。 */
  hooks: string[]
  source: string
}

const SAMPLE_PLUGINS: Plugin[] = [
  {
    id: 'json-beautify',
    name: 'JSON 美化',
    version: '1.2.0',
    description: '自动格式化 application/json 响应体，按 2 空格缩进并补充内容类型。',
    enabled: true,
    hooks: ['onResponse'],
    source: `// JSON 美化 · 在响应阶段重排 JSON 正文
export function onResponse(flow) {
  const ct = flow.response.headers['content-type'] || ''
  if (!ct.includes('application/json')) return

  try {
    const data = JSON.parse(flow.response.body)
    flow.response.body = JSON.stringify(data, null, 2)
    flow.response.headers['x-sniffy-beautified'] = 'json'
  } catch (err) {
    console.warn('[json-beautify] 解析失败:', err.message)
  }
}
`,
  },
  {
    id: 'request-logger',
    name: '请求日志',
    version: '0.9.4',
    description: '将每条请求的方法、URL 与耗时输出到控制台，便于快速排查。',
    enabled: true,
    hooks: ['onRequest', 'onResponse'],
    source: `// 请求日志 · 记录请求起止与状态
const started = new WeakMap()

export function onRequest(flow) {
  started.set(flow, Date.now())
  console.info('→', flow.request.method, flow.request.url)
}

export function onResponse(flow) {
  const cost = Date.now() - (started.get(flow) || Date.now())
  console.info('←', flow.response.status, flow.request.url, cost + 'ms')
}
`,
  },
  {
    id: 'auto-mock',
    name: '自动 Mock',
    version: '2.0.1',
    description: '命中规则的请求直接返回本地预设响应，不再转发到上游服务器。',
    enabled: false,
    hooks: ['onRequest'],
    source: `// 自动 Mock · 拦截匹配请求并返回桩数据
const RULES = [
  { match: '/api/user/profile', status: 200, body: { id: 1, name: 'Demo' } },
  { match: '/api/feature-flags', status: 200, body: { beta: true } },
]

export function onRequest(flow) {
  const rule = RULES.find((r) => flow.request.url.includes(r.match))
  if (!rule) return

  flow.respond({
    status: rule.status,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(rule.body),
  })
}
`,
  },
  {
    id: 'sign-check',
    name: '签名校验',
    version: '1.0.0',
    description: '在发出前为请求追加 HMAC 签名头，并校验返回签名是否一致。',
    enabled: false,
    hooks: ['onRequest', 'onResponse'],
    source: `// 签名校验 · 注入与验证 X-Signature
import { hmacSHA256 } from 'sniffy/crypto'

const SECRET = 'replace-with-your-secret'

export function onRequest(flow) {
  const ts = String(Date.now())
  const payload = flow.request.method + flow.request.url + ts
  flow.request.headers['x-timestamp'] = ts
  flow.request.headers['x-signature'] = hmacSHA256(payload, SECRET)
}

export function onResponse(flow) {
  const got = flow.response.headers['x-signature']
  const want = hmacSHA256(flow.response.body, SECRET)
  if (got && got !== want) {
    console.error('[sign-check] 响应签名不匹配:', flow.request.url)
  }
}
`,
  },
]

export function PluginsView() {
  const [plugins, setPlugins] = useState<Plugin[]>(SAMPLE_PLUGINS)
  const [selectedId, setSelectedId] = useState<string | null>(SAMPLE_PLUGINS[0]?.id ?? null)

  const selected = plugins.find((p) => p.id === selectedId) ?? null
  const enabledCount = plugins.filter((p) => p.enabled).length

  const toggle = (id: string, value: boolean) => {
    setPlugins((prev) => prev.map((p) => (p.id === id ? { ...p, enabled: value } : p)))
  }

  return (
    <PageShell
      icon={Puzzle}
      title="插件"
      subtitle={`${plugins.length} 个已安装 · ${enabledCount} 个启用`}
      contentWidth="full"
      actions={
        <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />}>
          安装插件
        </Button>
      }
    >
      <div className="flex h-full min-h-0 overflow-hidden rounded-wb border border-line bg-surface">
        {/* ── 左栏：插件列表 ── */}
        <aside className="flex w-[260px] shrink-0 flex-col border-r border-line">
          <header className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-inset/50 px-3">
            <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">已安装</span>
            <span className="text-2xs tabular-nums text-fg-faint">{plugins.length}</span>
          </header>
          <div className="wb-scroll min-h-0 flex-1 overflow-auto">
            {plugins.map((p) => {
              const active = p.id === selectedId
              return (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => setSelectedId(p.id)}
                  className={cx(
                    'flex w-full items-start gap-2 border-b border-line/60 px-3 py-2 text-left transition-colors',
                    active ? 'bg-accent/12' : 'hover:bg-elevated/50',
                  )}
                >
                  <span className="mt-0.5" onClick={(e) => e.stopPropagation()}>
                    <Toggle checked={p.enabled} onChange={(v) => toggle(p.id, v)} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline gap-1.5">
                      <span className={cx('truncate text-[12.5px]', p.enabled ? 'text-fg' : 'text-fg-muted')}>
                        {p.name}
                      </span>
                      <span className="shrink-0 font-mono text-2xs text-fg-faint">v{p.version}</span>
                    </span>
                    <span className="mt-0.5 block truncate text-2xs text-fg-faint">{p.description}</span>
                  </span>
                </button>
              )
            })}
          </div>
        </aside>

        {/* ── 右栏：插件详情 ── */}
        <section className="flex min-w-0 flex-1 flex-col">
          {selected ? (
            <PluginDetail
              key={selected.id}
              plugin={selected}
              onToggle={(v) => toggle(selected.id, v)}
            />
          ) : (
            <EmptyState
              icon={<Puzzle className="h-8 w-8" />}
              title="未选择插件"
              hint="从左侧列表选择一个插件以查看脚本与配置。"
            />
          )}
        </section>
      </div>
    </PageShell>
  )
}

function PluginDetail({ plugin, onToggle }: { plugin: Plugin; onToggle: (v: boolean) => void }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 顶部信息条 */}
      <header className="flex shrink-0 items-center gap-3 border-b border-line px-4 py-3">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-wb bg-accent/15 text-accent">
          <Puzzle className="h-4 w-4" />
        </div>
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <h2 className="truncate text-sm font-semibold text-fg">{plugin.name}</h2>
            <span className="shrink-0 font-mono text-2xs text-fg-faint">v{plugin.version}</span>
          </div>
          <div className="mt-0.5 flex items-center gap-1">
            {plugin.hooks.map((h) => (
              <Chip key={h} title={`钩子：${h}`}>
                {h}
              </Chip>
            ))}
          </div>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-3">
          <span className="flex items-center gap-1.5">
            <span className="text-2xs text-fg-faint">{plugin.enabled ? '已启用' : '已停用'}</span>
            <Toggle checked={plugin.enabled} onChange={onToggle} />
          </span>
          <Button variant="primary" icon={<Save className="h-3.5 w-3.5" />}>
            保存
          </Button>
        </div>
      </header>

      {/* 描述 */}
      <p className="shrink-0 px-4 pt-3 text-[12.5px] leading-relaxed text-fg-muted">{plugin.description}</p>

      {/* 代码区 */}
      <div className="flex min-h-0 flex-1 flex-col px-4 py-3">
        <div className="mb-1.5 flex shrink-0 items-center gap-1.5">
          <FileCode2 className="h-3.5 w-3.5 text-fg-faint" />
          <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">脚本 (JavaScript)</span>
          <span className="ml-auto text-2xs text-fg-faint">只读</span>
        </div>
        <pre className="wb-scroll min-h-0 flex-1 overflow-auto rounded-wb border border-line bg-inset p-3 font-mono text-[12px] leading-relaxed text-fg">
          {plugin.source}
        </pre>
      </div>
    </div>
  )
}
