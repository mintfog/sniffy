import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Events } from '@wailsio/runtime'
import {
  AlertTriangle,
  Check,
  FileCode2,
  Plus,
  Puzzle,
  RotateCw,
  Save,
  ScrollText,
  Settings2,
  Trash2,
  X,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import i18n from '@/i18n'
import { Button, Field, Select, TextInput, Toggle } from '../ui/controls'
import { Chip, cx, EmptyState } from '../ui/primitives'
import { PageShell } from './PageShell'

interface LogEntry {
  level: string
  msg: string
  time: number
}

interface Plugin {
  id: string
  name: string
  version: string
  description: string
  enabled: boolean
  author?: string
  runtime?: string
  priority?: number
  whitelist?: string[]
  blacklist?: string[]
  settings?: Record<string, unknown>
  error?: string
}

function asStringArray(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
}

function toPlugin(m: Record<string, unknown>): Plugin {
  return {
    id: String(m.id ?? ''),
    name: String(m.name ?? m.id ?? i18n.t('plugins.unnamed')),
    version: String(m.version ?? ''),
    description: String(m.description ?? ''),
    enabled: Boolean(m.enabled),
    author: m.author ? String(m.author) : undefined,
    runtime: m.runtime ? String(m.runtime) : undefined,
    priority: typeof m.priority === 'number' ? (m.priority as number) : undefined,
    whitelist: asStringArray(m.whitelist),
    blacklist: asStringArray(m.blacklist),
    settings: (m.settings as Record<string, unknown>) ?? undefined,
    error: m.error ? String(m.error) : undefined,
  }
}

function toLogs(v: unknown): LogEntry[] {
  if (!Array.isArray(v)) return []
  return v.map((e) => {
    const o = e as Record<string, unknown>
    return { level: String(o.level ?? 'log'), msg: String(o.msg ?? ''), time: Number(o.time ?? 0) }
  })
}

const LOG_CAP = 500

export function PluginsView() {
  const { t } = useTranslation()
  const [plugins, setPlugins] = useState<Plugin[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [logs, setLogs] = useState<Record<string, LogEntry[]>>({})
  const [showNew, setShowNew] = useState(false)

  const load = () => {
    Bridge.getPlugins()
      .then((list) => {
        if (!list) return
        const raw = list as Record<string, unknown>[]
        const ps = raw.map(toPlugin)
        setPlugins(ps)
        setSelectedId((cur) => cur ?? ps[0]?.id ?? null)
        const seeded: Record<string, LogEntry[]> = {}
        raw.forEach((m) => {
          const id = String(m.id ?? '')
          if (id) seeded[id] = toLogs(m.logs)
        })
        setLogs(seeded)
      })
      .catch(() => {
        /* 未连接后端：保持空 */
      })
  }

  useEffect(load, [])

  // 实时插件日志:console.* / notify 经 plugin_log 事件推送到此。
  useEffect(() => {
    const off = Events.On('plugin_log', (e: { data?: unknown }) => {
      const d = e.data as { id?: string; level?: string; msg?: string; time?: number } | undefined
      if (!d || !d.id) return
      setLogs((prev) => {
        const cur = prev[d.id as string] ?? []
        const next = [...cur, { level: d.level ?? 'log', msg: d.msg ?? '', time: d.time ?? 0 }]
        if (next.length > LOG_CAP) next.splice(0, next.length - LOG_CAP)
        return { ...prev, [d.id as string]: next }
      })
    })
    return () => {
      if (typeof off === 'function') off()
    }
  }, [])

  const selected = plugins.find((p) => p.id === selectedId) ?? null
  const enabledCount = plugins.filter((p) => p.enabled).length

  const toggle = (id: string, value: boolean) => {
    setPlugins((prev) => prev.map((p) => (p.id === id ? { ...p, enabled: value } : p)))
    Bridge.enablePlugin(id, value).catch(() => load())
  }

  const remove = async (id: string, name: string) => {
    if (!window.confirm(t('plugins.deleteConfirm', { name }))) return
    try {
      await Bridge.deletePlugin(id)
      setSelectedId((cur) => (cur === id ? null : cur))
      load()
    } catch {
      /* ignore */
    }
  }

  return (
    <PageShell
      icon={Puzzle}
      title={t('plugins.title')}
      subtitle={t('plugins.subtitle', { installed: plugins.length, enabled: enabledCount })}
      contentWidth="full"
      actions={
        <div className="flex items-center gap-1.5">
          <Button icon={<RotateCw className="h-3.5 w-3.5" />} onClick={load} title={t('plugins.refreshTip')}>
            {t('plugins.refresh')}
          </Button>
          <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setShowNew(true)}>
            {t('plugins.create')}
          </Button>
        </div>
      }
    >
      <div className="flex h-full min-h-0 overflow-hidden rounded-wb border border-line bg-surface">
        {/* ── 左栏：插件列表 ── */}
        <aside className="flex w-[260px] shrink-0 flex-col border-r border-line">
          <header className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-inset/50 px-3">
            <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.installed')}</span>
            <span className="text-2xs tabular-nums text-fg-faint">{plugins.length}</span>
          </header>
          <div className="wb-scroll min-h-0 flex-1 overflow-auto">
            {plugins.length === 0 ? (
              <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('plugins.empty')}</div>
            ) : (
              plugins.map((p) => {
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
                    <span className="mt-0.5" onClick={(e) => e.stopPropagation()} role="presentation">
                      {p.error ? (
                        <AlertTriangle className="h-3.5 w-3.5 text-warn" />
                      ) : (
                        <Toggle checked={p.enabled} onChange={(v) => toggle(p.id, v)} />
                      )}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-baseline gap-1.5">
                        <span className={cx('truncate text-[12.5px]', p.enabled ? 'text-fg' : 'text-fg-muted')}>
                          {p.name}
                        </span>
                        {p.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{p.version}</span>}
                      </span>
                      <span className="mt-0.5 block truncate text-2xs text-fg-faint">
                        {p.error ? t('plugins.failed') : p.description}
                      </span>
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </aside>

        {/* ── 右栏：插件详情 ── */}
        <section className="flex min-w-0 flex-1 flex-col">
          {selected ? (
            <PluginDetail
              key={selected.id}
              plugin={selected}
              logs={logs[selected.id] ?? []}
              onToggle={(v) => toggle(selected.id, v)}
              onDelete={() => remove(selected.id, selected.name)}
              onChanged={load}
              onClearLogs={() => {
                Bridge.clearPluginLogs(selected.id).catch(() => {})
                setLogs((prev) => ({ ...prev, [selected.id]: [] }))
              }}
            />
          ) : (
            <EmptyState
              icon={<Puzzle className="h-8 w-8" />}
              title={t('plugins.detail.noneSelected')}
              hint={t('plugins.detail.noneSelectedHint')}
            />
          )}
        </section>
      </div>

      {showNew && <NewPluginModal onClose={() => setShowNew(false)} onCreated={(id) => { setShowNew(false); load(); setSelectedId(id) }} />}
    </PageShell>
  )
}

type DetailTab = 'script' | 'logs' | 'config'

function PluginDetail({
  plugin,
  logs,
  onToggle,
  onDelete,
  onChanged,
  onClearLogs,
}: {
  plugin: Plugin
  logs: LogEntry[]
  onToggle: (v: boolean) => void
  onDelete: () => void
  onChanged: () => void
  onClearLogs: () => void
}) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<DetailTab>('script')

  // 加载失败的条目:只给出错误与删除入口。
  if (plugin.error) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <DetailHeader plugin={plugin} onToggle={onToggle} onDelete={onDelete} />
        <div className="m-4 rounded-wb border border-warn/40 bg-warn/10 p-3 text-[12px] text-warn">
          <div className="mb-1 font-semibold">{t('plugins.failed')}</div>
          <pre className="whitespace-pre-wrap break-all font-mono text-[11.5px] leading-relaxed">{plugin.error}</pre>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <DetailHeader plugin={plugin} onToggle={onToggle} onDelete={onDelete} />
      <div className="flex shrink-0 items-center gap-2 border-b border-line px-4 py-2">
        <SegTabsLocal
          value={tab}
          onChange={setTab}
          options={[
            { key: 'script', label: t('plugins.tabs.script') },
            { key: 'logs', label: t('plugins.tabs.logs'), count: logs.length },
            { key: 'config', label: t('plugins.tabs.config') },
          ]}
        />
      </div>
      {tab === 'script' && <ScriptPanel plugin={plugin} />}
      {tab === 'logs' && <LogPanel logs={logs} onClear={onClearLogs} />}
      {tab === 'config' && <ConfigPanel plugin={plugin} onSaved={onChanged} />}
    </div>
  )
}

function DetailHeader({ plugin, onToggle, onDelete }: { plugin: Plugin; onToggle: (v: boolean) => void; onDelete: () => void }) {
  const { t } = useTranslation()
  return (
    <header className="flex shrink-0 items-center gap-3 border-b border-line px-4 py-3">
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-wb bg-accent/15 text-accent">
        <Puzzle className="h-4 w-4" />
      </div>
      <div className="min-w-0">
        <div className="flex items-baseline gap-2">
          <h2 className="truncate text-sm font-semibold text-fg">{plugin.name}</h2>
          {plugin.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{plugin.version}</span>}
        </div>
        <div className="mt-0.5 flex items-center gap-1">
          {plugin.runtime && <Chip title={t('plugins.detail.runtime', { runtime: plugin.runtime })}>{plugin.runtime}</Chip>}
          {plugin.author && <Chip title={t('plugins.detail.author', { author: plugin.author })}>{plugin.author}</Chip>}
          {plugin.priority != null && <Chip title={t('plugins.detail.priority')}>#{plugin.priority}</Chip>}
        </div>
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-3">
        {!plugin.error && (
          <span className="flex items-center gap-1.5">
            <span className="text-2xs text-fg-faint">{plugin.enabled ? t('plugins.detail.enabled') : t('plugins.detail.disabled')}</span>
            <Toggle checked={plugin.enabled} onChange={onToggle} />
          </span>
        )}
        <Button icon={<Trash2 className="h-3.5 w-3.5" />} onClick={onDelete} title={t('plugins.delete')}>
          {t('plugins.delete')}
        </Button>
      </div>
    </header>
  )
}

function ScriptPanel({ plugin }: { plugin: Plugin }) {
  const { t } = useTranslation()
  const [source, setSource] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    Bridge.getPluginSource(plugin.id)
      .then((s) => {
        if (alive) setSource(s ?? '')
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [plugin.id])

  const save = async () => {
    setSaving(true)
    setError(null)
    try {
      await Bridge.savePluginSource(plugin.id, source)
      setDirty(false)
      setSaved(true)
      setTimeout(() => setSaved(false), 1400)
    } catch (err) {
      setError(String(err instanceof Error ? err.message : err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col px-4 py-3">
      <div className="mb-1.5 flex shrink-0 items-center gap-1.5">
        <FileCode2 className="h-3.5 w-3.5 text-fg-faint" />
        <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.detail.scriptLabel')}</span>
        <span className="ml-auto flex items-center gap-2">
          <span className="text-2xs text-fg-faint">{dirty ? t('plugins.detail.unsaved') : t('plugins.detail.hotReloadHint')}</span>
          <Button
            variant="primary"
            size="sm"
            icon={saved ? <Check className="h-3.5 w-3.5" /> : <Save className="h-3.5 w-3.5" />}
            onClick={save}
            disabled={!dirty || saving}
          >
            {saved ? t('plugins.detail.saved') : saving ? t('plugins.detail.saving') : t('plugins.detail.save')}
          </Button>
        </span>
      </div>
      {error && (
        <div className="mb-1.5 shrink-0 rounded-wb border border-err/40 bg-err/10 px-2.5 py-1.5 text-2xs text-err">
          {t('plugins.error.saveFailed', { msg: error })}
        </div>
      )}
      <textarea
        spellCheck={false}
        value={source}
        onChange={(e) => {
          setSource(e.target.value)
          setDirty(true)
        }}
        className="wb-scroll min-h-0 flex-1 resize-none overflow-auto rounded-wb border border-line bg-inset p-3 font-mono text-[12px] leading-relaxed text-fg outline-none transition-colors focus:border-accent focus:bg-surface"
      />
    </div>
  )
}

const LOG_TONE: Record<string, string> = {
  error: 'text-err',
  warn: 'text-warn',
  notify: 'text-iris',
  info: 'text-fg',
  log: 'text-fg-muted',
  debug: 'text-fg-faint',
}

function LogPanel({ logs, onClear }: { logs: LogEntry[]; onClear: () => void }) {
  const { t } = useTranslation()
  const [level, setLevel] = useState('all')

  const shown = useMemo(() => (level === 'all' ? logs : logs.filter((l) => l.level === level)), [logs, level])
  const fmtTime = (ms: number) => (ms ? new Date(ms).toLocaleTimeString() : '')

  return (
    <div className="flex min-h-0 flex-1 flex-col px-4 py-3">
      <div className="mb-1.5 flex shrink-0 items-center gap-2">
        <ScrollText className="h-3.5 w-3.5 text-fg-faint" />
        <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.tabs.logs')}</span>
        <span className="ml-auto flex items-center gap-2">
          <Select
            value={level}
            onChange={(e) => setLevel(e.target.value)}
            options={[
              { value: 'all', label: t('plugins.logs.all') },
              { value: 'log', label: 'log' },
              { value: 'info', label: 'info' },
              { value: 'warn', label: 'warn' },
              { value: 'error', label: 'error' },
              { value: 'notify', label: 'notify' },
            ]}
          />
          <Button size="sm" icon={<Trash2 className="h-3.5 w-3.5" />} onClick={onClear} disabled={logs.length === 0}>
            {t('plugins.logs.clear')}
          </Button>
        </span>
      </div>
      <div className="wb-scroll min-h-0 flex-1 overflow-auto rounded-wb border border-line bg-inset p-2 font-mono text-[11.5px] leading-relaxed">
        {shown.length === 0 ? (
          <div className="px-1 py-3 text-center text-2xs text-fg-faint">{t('plugins.logs.empty')}</div>
        ) : (
          shown.map((l, i) => (
            <div key={i} className="flex gap-2 whitespace-pre-wrap break-all px-1 py-0.5">
              <span className="shrink-0 text-fg-faint">{fmtTime(l.time)}</span>
              <span className={cx('shrink-0 w-12 uppercase', LOG_TONE[l.level] ?? 'text-fg-muted')}>{l.level}</span>
              <span className="min-w-0 flex-1 text-fg">{l.msg}</span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

function ConfigPanel({ plugin, onSaved }: { plugin: Plugin; onSaved: () => void }) {
  const { t } = useTranslation()
  const [priority, setPriority] = useState(String(plugin.priority ?? 100))
  const [whitelist, setWhitelist] = useState((plugin.whitelist ?? []).join('\n'))
  const [blacklist, setBlacklist] = useState((plugin.blacklist ?? []).join('\n'))
  const [settings, setSettings] = useState(JSON.stringify(plugin.settings ?? {}, null, 2))
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)

  const apply = async () => {
    let parsedSettings: Record<string, unknown> = {}
    if (settings.trim()) {
      try {
        parsedSettings = JSON.parse(settings)
      } catch {
        setError(t('plugins.config.invalidJson'))
        return
      }
    }
    setSaving(true)
    setError(null)
    try {
      await Bridge.updatePluginManifest(plugin.id, {
        name: plugin.name,
        version: plugin.version,
        author: plugin.author ?? '',
        description: plugin.description,
        priority: Number(priority) || 100,
        whitelist: lines(whitelist),
        blacklist: lines(blacklist),
        settings: parsedSettings,
      })
      setSaved(true)
      setTimeout(() => setSaved(false), 1400)
      onSaved()
    } catch (err) {
      setError(String(err instanceof Error ? err.message : err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="wb-scroll min-h-0 flex-1 overflow-auto px-4 py-3">
      <div className="overflow-hidden rounded-wb border border-line">
        <Field label={t('plugins.config.priority')} hint={t('plugins.config.priorityHint')}>
          <TextInput type="number" width={90} value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>

      <label className="mt-3 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">
        {t('plugins.config.whitelist')}
      </label>
      <p className="mb-1 text-2xs text-fg-faint">{t('plugins.config.matchHint')}</p>
      <textarea
        spellCheck={false}
        value={whitelist}
        onChange={(e) => setWhitelist(e.target.value)}
        placeholder="*.example.com/*"
        className="h-20 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />

      <label className="mt-3 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">
        {t('plugins.config.blacklist')}
      </label>
      <textarea
        spellCheck={false}
        value={blacklist}
        onChange={(e) => setBlacklist(e.target.value)}
        className="h-16 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />

      <label className="mt-3 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">
        {t('plugins.config.settings')}
      </label>
      <p className="mb-1 text-2xs text-fg-faint">{t('plugins.config.settingsHint')}</p>
      <textarea
        spellCheck={false}
        value={settings}
        onChange={(e) => setSettings(e.target.value)}
        className="h-32 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />

      {error && <div className="mt-2 rounded-wb border border-err/40 bg-err/10 px-2.5 py-1.5 text-2xs text-err">{error}</div>}
      <div className="mt-3 flex items-center gap-2">
        <Button variant="primary" icon={saved ? <Check className="h-3.5 w-3.5" /> : <Settings2 className="h-3.5 w-3.5" />} onClick={apply} disabled={saving}>
          {saved ? t('plugins.detail.saved') : t('plugins.config.apply')}
        </Button>
        <span className="text-2xs text-fg-faint">{t('plugins.config.reloadHint')}</span>
      </div>
    </div>
  )
}

function NewPluginModal({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const { t } = useTranslation()
  const [id, setId] = useState('')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState('100')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const create = async () => {
    if (!id.trim()) {
      setError(t('plugins.new.idRequired'))
      return
    }
    setCreating(true)
    setError(null)
    try {
      await Bridge.createPlugin(
        {
          id: id.trim(),
          name: name.trim() || id.trim(),
          description: description.trim(),
          priority: Number(priority) || 100,
          enabled: true,
        },
        '',
      )
      onCreated(id.trim())
    } catch (err) {
      setError(String(err instanceof Error ? err.message : err))
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose} role="presentation">
      <div
        className="w-full max-w-md overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <header className="flex items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          <Plus className="h-4 w-4 text-accent" />
          <span className="text-[13px] font-semibold text-fg">{t('plugins.new.title')}</span>
          <button type="button" onClick={onClose} className="ml-auto text-fg-faint hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="space-y-3 px-4 py-3">
          <label className="block">
            <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.new.id')}</span>
            <TextInput value={id} onChange={(e) => setId(e.target.value)} placeholder="my-plugin" className="w-full" autoFocus />
            <span className="mt-1 block text-2xs text-fg-faint">{t('plugins.new.idHint')}</span>
          </label>
          <label className="block">
            <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.new.name')}</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value)} className="w-full" />
          </label>
          <label className="block">
            <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.new.description')}</span>
            <TextInput value={description} onChange={(e) => setDescription(e.target.value)} className="w-full" />
          </label>
          <label className="block">
            <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.new.priority')}</span>
            <TextInput type="number" width={90} value={priority} onChange={(e) => setPriority(e.target.value)} />
          </label>
          {error && <div className="rounded-wb border border-err/40 bg-err/10 px-2.5 py-1.5 text-2xs text-err">{error}</div>}
        </div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button onClick={onClose}>{t('plugins.new.cancel')}</Button>
          <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={create} disabled={creating}>
            {t('plugins.new.create')}
          </Button>
        </footer>
      </div>
    </div>
  )
}

// 局部 SegTabs:复用 controls.SegTabs 的样式但避免泛型在此文件的类型噪音。
function SegTabsLocal({
  value,
  onChange,
  options,
}: {
  value: DetailTab
  onChange: (v: DetailTab) => void
  options: { key: DetailTab; label: string; count?: number }[]
}) {
  return (
    <div className="inline-flex items-center gap-0.5 rounded-wb bg-inset p-0.5">
      {options.map((o) => (
        <button
          key={o.key}
          type="button"
          onClick={() => onChange(o.key)}
          className={cx(
            'inline-flex h-6 items-center gap-1 rounded-wb-sm px-2.5 text-2xs font-medium outline-none transition-colors',
            value === o.key ? 'bg-surface text-fg shadow-sm' : 'text-fg-muted hover:text-fg',
          )}
        >
          {o.label}
          {o.count != null && <span className="tabular-nums text-fg-faint">{o.count}</span>}
        </button>
      ))}
    </div>
  )
}
