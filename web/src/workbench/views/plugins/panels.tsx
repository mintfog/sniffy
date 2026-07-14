import { type ReactNode, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, ChevronDown, Plus, Save, Search, Settings2, Trash2, X } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, SegTabs, Select, TextInput, Toggle } from '../../ui/controls'
import { cx } from '../../ui/primitives'
import { FieldGroup } from '../../ui/native'
import { PluginEditor } from './editor'
import { PLUGIN_TEMPLATES, type PluginTemplate } from './templates'
import { LOG_TONE, type LogEntry, type Plugin, type SettingField } from './model'

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
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line px-3">
        <span className="text-2xs text-fg-faint">{dirty ? t('plugins.detail.unsaved') : t('plugins.detail.hotReloadHint')}</span>
        <Button
          variant="primary"
          size="sm"
          className="ml-auto"
          icon={saved ? <Check className="h-3.5 w-3.5" /> : <Save className="h-3.5 w-3.5" />}
          onClick={save}
          disabled={!dirty || saving}
          title="Ctrl/Cmd+S"
        >
          {saved ? t('plugins.detail.saved') : saving ? t('plugins.detail.saving') : t('plugins.detail.save')}
        </Button>
      </div>
      {error && (
        <div className="shrink-0 border-b border-danger/40 bg-danger/10 px-3 py-1.5 text-2xs text-danger">
          {t('plugins.error.saveFailed', { msg: error })}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-hidden bg-inset">
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

/** 折叠时仅留头部一条;实时日志在折叠期间仍持续累积(状态在父组件)。 */
export function LogPanel({
  logs,
  onClear,
  open,
  onToggle,
}: {
  logs: LogEntry[]
  onClear: () => void
  open: boolean
  onToggle: () => void
}) {
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

  // shown 变化或重新展开时,若处于贴底状态则滚到最新。
  useEffect(() => {
    const el = scrollRef.current
    if (el && stickRef.current) el.scrollTop = el.scrollHeight
  }, [shown.length, open])

  const onScroll = () => {
    const el = scrollRef.current
    if (el) stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }

  const fmtTime = (ms: number) => (ms ? new Date(ms).toLocaleTimeString() : '')

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 px-3">
        <button
          type="button"
          onClick={onToggle}
          title={t('plugins.logs.toggle')}
          className="flex items-center gap-1.5 text-fg-muted outline-none transition-colors hover:text-fg"
        >
          <ChevronDown className={cx('h-3.5 w-3.5 transition-transform', !open && '-rotate-90')} />
          <span className="text-2xs font-semibold uppercase tracking-wide">{t('plugins.tabs.logs')}</span>
        </button>
        <span className="text-2xs tabular-nums text-fg-faint">{shown.length}</span>
        {open && (
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
        )}
      </div>
      {open && (
        <div
          ref={scrollRef}
          onScroll={onScroll}
          className="min-h-0 flex-1 overflow-auto border-t border-line bg-inset px-3 py-2 font-mono text-[11.5px] leading-relaxed"
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
      )}
    </div>
  )
}

/** 按字段类型返回空值，用于补齐 schema 声明但 settings 缺失的项。 */
function defaultForType(type?: SettingField['type']): unknown {
  if (type === 'number') return 0
  if (type === 'boolean') return false
  return ''
}

/** 合并已有 settings 与 schema 默认：已有值优先，缺失项填默认，保留 schema 未声明的键（不丢数据）。 */
function mergeSettings(current: Record<string, unknown>, schema: SettingField[]): Record<string, unknown> {
  const out: Record<string, unknown> = { ...current }
  for (const f of schema) {
    if (out[f.key] === undefined) out[f.key] = f.default ?? defaultForType(f.type)
  }
  return out
}

/** 键序无关的规整序列化，供脏检查比较（表单/JSON 两态与键序差异不应误判为已改）。 */
function canonSettings(v: unknown): string {
  return JSON.stringify(v, (_k, val) =>
    val && typeof val === 'object' && !Array.isArray(val)
      ? Object.fromEntries(Object.keys(val as Record<string, unknown>).sort().map((k) => [k, (val as Record<string, unknown>)[k]]))
      : val,
  )
}

/** schema 驱动的单个配置项控件。 */
function SchemaField({ field, value, onChange }: { field: SettingField; value: unknown; onChange: (v: unknown) => void }) {
  const label = field.label || field.key
  const desc = field.description

  if (field.type === 'boolean') {
    return (
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <div className="text-[12.5px] text-fg">{label}</div>
          {desc && <div className="mt-0.5 text-2xs leading-relaxed text-fg-faint">{desc}</div>}
        </div>
        <Toggle checked={Boolean(value)} onChange={onChange} />
      </div>
    )
  }

  let control: ReactNode
  if (field.type === 'enum') {
    // <select> 只认字符串，故展示用 String(value)，change 时按原值表回映回声明的类型。
    const lookup = new Map((field.options ?? []).map((o) => [String(o.value), o.value]))
    control = (
      <Select
        className="w-full"
        value={String(value ?? '')}
        onChange={(e) => onChange(lookup.has(e.target.value) ? lookup.get(e.target.value) : e.target.value)}
        options={(field.options ?? []).map((o) => ({ value: String(o.value), label: o.label ?? String(o.value) }))}
      />
    )
  } else if (field.type === 'text') {
    control = (
      <textarea
        spellCheck={false}
        value={String(value ?? '')}
        placeholder={field.placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="h-16 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
      />
    )
  } else if (field.type === 'number') {
    control = (
      <TextInput
        type="number"
        className="w-full"
        value={value == null || value === '' ? '' : String(value)}
        placeholder={field.placeholder}
        onChange={(e) => onChange(e.target.value === '' ? '' : Number(e.target.value))}
      />
    )
  } else {
    control = (
      <TextInput className="w-full" value={String(value ?? '')} placeholder={field.placeholder} onChange={(e) => onChange(e.target.value)} />
    )
  }

  return (
    <label className="block">
      <span className="mb-1 block text-2xs font-semibold uppercase tracking-wide text-fg-muted">{label}</span>
      {control}
      {desc && <span className="mt-1 block text-2xs text-fg-faint">{desc}</span>}
    </label>
  )
}

export function ConfigPanel({ plugin, onSaved, onDirtyChange }: { plugin: Plugin; onSaved: () => void; onDirtyChange?: (dirty: boolean) => void }) {
  const { t } = useTranslation()
  const [name, setName] = useState(plugin.name)
  const [version, setVersion] = useState(plugin.version)
  const [author, setAuthor] = useState(plugin.author ?? '')
  const [description, setDescription] = useState(plugin.description)
  const [priority, setPriority] = useState(String(plugin.priority ?? 100))
  const [whitelist, setWhitelist] = useState((plugin.whitelist ?? []).join('\n'))
  const [blacklist, setBlacklist] = useState((plugin.blacklist ?? []).join('\n'))

  const schema = useMemo(() => plugin.settingsSchema ?? [], [plugin.settingsSchema])
  const hasSchema = schema.length > 0
  // 有 schema 时默认表单视图，否则只有裸 JSON 一条路。
  const [rawMode, setRawMode] = useState(!hasSchema)
  const [values, setValues] = useState<Record<string, unknown>>(() => mergeSettings({ ...(plugin.settings ?? {}) }, schema))
  const [settingsText, setSettingsText] = useState(() => JSON.stringify(plugin.settings ?? {}, null, 2))

  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)

  // 脏检查基线：随保存推进，避免已保存的编辑仍被判脏而误触切换/关窗确认。
  const baseline = useRef({
    name,
    version,
    author,
    description,
    priority,
    whitelist,
    blacklist,
    settings: canonSettings(values),
  })

  // 当前 settings 的规整序列化:JSON 模式按文本解析,非法 JSON 视为已改。
  const settingsCanon = () => {
    if (!rawMode) return canonSettings(values)
    try {
      return canonSettings(settingsText.trim() ? JSON.parse(settingsText) : {})
    } catch {
      return ' invalid'
    }
  }
  const b = baseline.current
  const dirty =
    name !== b.name ||
    version !== b.version ||
    author !== b.author ||
    description !== b.description ||
    priority !== b.priority ||
    whitelist !== b.whitelist ||
    blacklist !== b.blacklist ||
    settingsCanon() !== b.settings
  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])

  // 表单↔JSON 切换时双向同步，避免丢失另一侧的编辑；JSON 非法则不切换并报错。
  const switchView = (toRaw: boolean) => {
    if (toRaw === rawMode) return
    if (toRaw) {
      setSettingsText(JSON.stringify(values, null, 2))
      setError(null)
      setRawMode(true)
      return
    }
    try {
      const parsed = settingsText.trim() ? JSON.parse(settingsText) : {}
      setValues(mergeSettings(parsed as Record<string, unknown>, schema))
      setError(null)
      setRawMode(false)
    } catch {
      setError(t('plugins.config.invalidJson'))
    }
  }

  const apply = async () => {
    let nextSettings: Record<string, unknown>
    if (rawMode) {
      try {
        nextSettings = settingsText.trim() ? JSON.parse(settingsText) : {}
      } catch {
        setError(t('plugins.config.invalidJson'))
        return
      }
    } else {
      nextSettings = values
    }
    setSaving(true)
    setError(null)
    try {
      await Bridge.updatePluginManifest(plugin.id, {
        name: name.trim() || plugin.id,
        version: version.trim(),
        author: author.trim(),
        description: description.trim(),
        priority: Number(priority) || 100,
        whitelist: lines(whitelist),
        blacklist: lines(blacklist),
        settings: nextSettings,
      })
      baseline.current = { name, version, author, description, priority, whitelist, blacklist, settings: canonSettings(nextSettings) }
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
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-auto">
        <FieldGroup title={t('plugins.config.metadata')} bodyClassName="space-y-2.5 p-3">
          <label className="block">
            <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.name')}</span>
            <TextInput value={name} onChange={(e) => setName(e.target.value)} className="w-full" />
          </label>
          <div className="flex gap-2">
            <label className="block min-w-0 flex-1">
              <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.version')}</span>
              <TextInput value={version} onChange={(e) => setVersion(e.target.value)} className="w-full" />
            </label>
            <label className="block min-w-0 flex-1">
              <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.author')}</span>
              <TextInput value={author} onChange={(e) => setAuthor(e.target.value)} className="w-full" />
            </label>
          </div>
          <label className="block">
            <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.description')}</span>
            <TextInput value={description} onChange={(e) => setDescription(e.target.value)} className="w-full" />
          </label>
          <label className="block">
            <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.priority')}</span>
            <TextInput type="number" width={90} value={priority} onChange={(e) => setPriority(e.target.value)} />
            <span className="mt-1 block text-2xs text-fg-faint">{t('plugins.config.priorityHint')}</span>
          </label>
        </FieldGroup>

        <FieldGroup title={t('plugins.config.scope')} bodyClassName="space-y-2 p-3">
          <label className="block">
            <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.whitelist')}</span>
            <textarea
              spellCheck={false}
              value={whitelist}
              onChange={(e) => setWhitelist(e.target.value)}
              placeholder="*.example.com/*"
              className="h-20 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
            />
          </label>
          <p className="text-2xs text-fg-faint">{t('plugins.config.matchHint')}</p>
          <label className="block">
            <span className="mb-1 block text-2xs text-fg-faint">{t('plugins.config.blacklist')}</span>
            <textarea
              spellCheck={false}
              value={blacklist}
              onChange={(e) => setBlacklist(e.target.value)}
              className="h-16 w-full resize-none rounded-wb border border-line bg-inset p-2 font-mono text-[12px] text-fg outline-none focus:border-accent"
            />
          </label>
        </FieldGroup>

        <FieldGroup
          title={t('plugins.config.settingsLabel')}
          right={
            hasSchema ? (
              <SegTabs
                value={rawMode ? 'json' : 'form'}
                onChange={(v) => switchView(v === 'json')}
                options={[
                  { key: 'form', label: t('plugins.config.viewForm') },
                  { key: 'json', label: t('plugins.config.viewJson') },
                ]}
              />
            ) : undefined
          }
          bodyClassName="p-3"
        >
          <p className="mb-2 text-2xs text-fg-faint">{t('plugins.config.settingsHint')}</p>
          {rawMode ? (
            <div className="h-40 overflow-hidden rounded-wb border border-line bg-inset transition-colors focus-within:border-accent">
              <PluginEditor value={settingsText} onChange={setSettingsText} language="json" className="h-full" ariaLabel={t('plugins.config.settingsLabel')} />
            </div>
          ) : (
            <div className="space-y-3">
              {schema.map((f) => (
                <SchemaField key={f.key} field={f} value={values[f.key]} onChange={(v) => setValues((prev) => ({ ...prev, [f.key]: v }))} />
              ))}
            </div>
          )}
        </FieldGroup>
      </div>

      {error && <div className="shrink-0 border-t border-danger/40 bg-danger/10 px-3 py-1.5 text-2xs text-danger">{error}</div>}
      <div className="flex h-9 shrink-0 items-center gap-2 border-t border-line px-3">
        <Button variant="primary" size="sm" icon={saved ? <Check className="h-3.5 w-3.5" /> : <Settings2 className="h-3.5 w-3.5" />} onClick={apply} disabled={saving}>
          {saved ? t('plugins.detail.saved') : t('plugins.config.apply')}
        </Button>
        <span className="text-2xs text-fg-faint">{t('plugins.config.reloadHint')}</span>
      </div>
    </div>
  )
}

/** 把模板的 schema（i18n 键）解析为当前语言的 SettingField[]，并按 default 生成初始 settings。 */
function resolveTemplate(tpl: PluginTemplate, t: (k: string) => string) {
  if (!tpl.schema?.length) return { settings: undefined, settingsSchema: undefined }
  const settingsSchema: SettingField[] = tpl.schema.map((f) => ({
    key: f.key,
    type: f.type,
    default: f.default,
    label: t(f.labelKey),
    description: f.descKey ? t(f.descKey) : undefined,
    placeholder: f.placeholder,
    options: f.options?.map((o) => ({ value: o.value, label: o.labelKey ? t(o.labelKey) : undefined })),
  }))
  const settings: Record<string, unknown> = {}
  for (const f of tpl.schema) settings[f.key] = f.default ?? ''
  return { settings, settingsSchema }
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

  const tpl = PLUGIN_TEMPLATES.find((x) => x.key === template) ?? PLUGIN_TEMPLATES[0]
  const tplDesc = tpl.descKey ? t(tpl.descKey) : ''

  const create = async () => {
    if (!id.trim()) {
      setError(t('plugins.new.idRequired'))
      return
    }
    setCreating(true)
    setError(null)
    const { settings, settingsSchema } = resolveTemplate(tpl, t)
    try {
      await Bridge.createPlugin(
        {
          id: id.trim(),
          name: name.trim() || id.trim(),
          description: description.trim() || tplDesc,
          priority: Number(priority) || 100,
          enabled: true,
          ...(settings ? { settings, settingsSchema } : {}),
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
          {tplDesc && <p className="text-2xs leading-relaxed text-fg-faint">{tplDesc}</p>}
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
