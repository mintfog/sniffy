import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, FileCode2, Plus, Puzzle, Save, ScrollText, Search, Settings2, Trash2, X } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, Field, Select, TextInput, Toggle } from '../../ui/controls'
import { Chip, cx } from '../../ui/primitives'
import { PluginEditor } from './editor'
import { PLUGIN_TEMPLATES } from './templates'
import { LOG_TONE, type LogEntry, type Plugin } from './model'

export function DetailHeader({ plugin, onToggle, onDelete }: { plugin: Plugin; onToggle: (v: boolean) => void; onDelete: () => void }) {
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

export function ScriptPanel({ plugin, onDirtyChange }: { plugin: Plugin; onDirtyChange?: (dirty: boolean) => void }) {
  const { t } = useTranslation()
  const [source, setSource] = useState('')
  const [savedSource, setSavedSource] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const dirty = source !== savedSource
  // 加载是异步的，用户若已抢先输入则不让回填覆盖。
  const editedRef = useRef(false)

  useEffect(() => {
    let alive = true
    Bridge.getPluginSource(plugin.id)
      .then((s) => {
        if (!alive || editedRef.current) return
        setSource(s ?? '')
        setSavedSource(s ?? '')
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [plugin.id])

  const onEdit = (v: string) => {
    editedRef.current = true
    setSource(v)
  }

  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])

  const save = async () => {
    if (saving || !dirty) return
    setSaving(true)
    setError(null)
    const snapshot = source
    try {
      await Bridge.savePluginSource(plugin.id, snapshot)
      setSavedSource(snapshot)
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
            title="Ctrl/Cmd+S"
          >
            {saved ? t('plugins.detail.saved') : saving ? t('plugins.detail.saving') : t('plugins.detail.save')}
          </Button>
        </span>
      </div>
      {error && (
        <div className="mb-1.5 shrink-0 rounded-wb border border-danger/40 bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">
          {t('plugins.error.saveFailed', { msg: error })}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-hidden rounded-wb border border-line bg-inset transition-colors focus-within:border-accent">
        <PluginEditor
          key={plugin.id}
          value={source}
          onChange={onEdit}
          onSave={save}
          language="js"
          ariaLabel={t('plugins.detail.scriptLabel')}
          className="h-full"
        />
      </div>
    </div>
  )
}

export function LogPanel({ logs, onClear }: { logs: LogEntry[]; onClear: () => void }) {
  const { t } = useTranslation()
  const [level, setLevel] = useState('all')
  const [query, setQuery] = useState('')
  const scrollRef = useRef<HTMLDivElement | null>(null)
  // 贴底跟随：仅当用户已在底部附近时才自动滚到最新，向上翻看时不打断。
  const stickRef = useRef(true)

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return logs.filter((l) => (level === 'all' || l.level === level) && (!q || l.msg.toLowerCase().includes(q)))
  }, [logs, level, query])

  useEffect(() => {
    const el = scrollRef.current
    if (el && stickRef.current) el.scrollTop = el.scrollHeight
  }, [shown.length])

  const onScroll = () => {
    const el = scrollRef.current
    if (el) stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }

  const fmtTime = (ms: number) => (ms ? new Date(ms).toLocaleTimeString() : '')

  return (
    <div className="flex min-h-0 flex-1 flex-col px-4 py-3">
      <div className="mb-1.5 flex shrink-0 items-center gap-2">
        <ScrollText className="h-3.5 w-3.5 text-fg-faint" />
        <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.tabs.logs')}</span>
        <span className="text-2xs tabular-nums text-fg-faint">{shown.length}</span>
        <span className="ml-auto flex items-center gap-2">
          <span className="relative">
            <Search className="pointer-events-none absolute left-1.5 top-1/2 h-3 w-3 -translate-y-1/2 text-fg-faint" />
            <TextInput value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('plugins.logs.search')} width={130} className="!pl-6" />
          </span>
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
      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-auto rounded-wb border border-line bg-inset p-2 font-mono text-[11.5px] leading-relaxed"
      >
        {shown.length === 0 ? (
          <div className="px-1 py-3 text-center text-2xs text-fg-faint">{logs.length === 0 ? t('plugins.logs.empty') : t('plugins.logs.noMatch')}</div>
        ) : (
          shown.map((l, i) => (
            <div key={i} className="flex gap-2 whitespace-pre-wrap break-all px-1 py-0.5">
              <span className="shrink-0 text-fg-faint">{fmtTime(l.time)}</span>
              <span className={cx('w-12 shrink-0 uppercase', LOG_TONE[l.level] ?? 'text-fg-muted')}>{l.level}</span>
              <span className="min-w-0 flex-1 text-fg">{l.msg}</span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

export function ConfigPanel({ plugin, onSaved }: { plugin: Plugin; onSaved: () => void }) {
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
    <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
      <div className="overflow-hidden rounded-wb border border-line">
        <Field label={t('plugins.config.priority')} hint={t('plugins.config.priorityHint')}>
          <TextInput type="number" width={90} value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </div>

      <label className="mt-3 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.config.whitelist')}</label>
      <p className="mb-1 text-2xs text-fg-faint">{t('plugins.config.matchHint')}</p>
      <textarea
        spellCheck={false}
        value={whitelist}
        onChange={(e) => setWhitelist(e.target.value)}
        placeholder="*.example.com/*"
        className="h-20 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />

      <label className="mt-3 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.config.blacklist')}</label>
      <textarea
        spellCheck={false}
        value={blacklist}
        onChange={(e) => setBlacklist(e.target.value)}
        className="h-16 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />

      <label className="mt-3 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.config.settings')}</label>
      <p className="mb-1 text-2xs text-fg-faint">{t('plugins.config.settingsHint')}</p>
      <div className="h-40 overflow-hidden rounded-wb border border-line bg-inset transition-colors focus-within:border-accent">
        <PluginEditor value={settings} onChange={setSettings} language="json" className="h-full" ariaLabel={t('plugins.config.settings')} />
      </div>

      {error && <div className="mt-2 rounded-wb border border-danger/40 bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{error}</div>}
      <div className="mt-3 flex items-center gap-2">
        <Button variant="primary" icon={saved ? <Check className="h-3.5 w-3.5" /> : <Settings2 className="h-3.5 w-3.5" />} onClick={apply} disabled={saving}>
          {saved ? t('plugins.detail.saved') : t('plugins.config.apply')}
        </Button>
        <span className="text-2xs text-fg-faint">{t('plugins.config.reloadHint')}</span>
      </div>
    </div>
  )
}

export function NewPluginModal({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const { t } = useTranslation()
  const [id, setId] = useState('')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState('100')
  const [template, setTemplate] = useState('log')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const create = async () => {
    if (!id.trim()) {
      setError(t('plugins.new.idRequired'))
      return
    }
    setCreating(true)
    setError(null)
    const tpl = PLUGIN_TEMPLATES.find((x) => x.key === template) ?? PLUGIN_TEMPLATES[0]
    try {
      await Bridge.createPlugin(
        {
          id: id.trim(),
          name: name.trim() || id.trim(),
          description: description.trim(),
          priority: Number(priority) || 100,
          enabled: true,
        },
        tpl.source,
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
      <div className="w-full max-w-md overflow-hidden rounded-wb border border-line bg-surface shadow-xl" onClick={(e) => e.stopPropagation()} role="dialog">
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
          <div className="flex items-end gap-3">
            <label className="block">
              <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.new.priority')}</span>
              <TextInput type="number" width={90} value={priority} onChange={(e) => setPriority(e.target.value)} />
            </label>
            <label className="block min-w-0 flex-1">
              <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.new.template')}</span>
              <Select
                value={template}
                onChange={(e) => setTemplate(e.target.value)}
                className="w-full"
                options={PLUGIN_TEMPLATES.map((x) => ({ value: x.key, label: t(x.labelKey) }))}
              />
            </label>
          </div>
          {error && <div className="rounded-wb border border-danger/40 bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{error}</div>}
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
