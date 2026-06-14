import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, FileCode2, Plus, Puzzle, RotateCw, Save } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import i18n from '@/i18n'
import { Button, Toggle } from '../ui/controls'
import { Chip, cx, EmptyState } from '../ui/primitives'
import { PageShell } from './PageShell'

interface Plugin {
  id: string
  name: string
  version: string
  description: string
  enabled: boolean
  author?: string
  runtime?: string
  priority?: number
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
  }
}

export function PluginsView() {
  const { t } = useTranslation()
  const [plugins, setPlugins] = useState<Plugin[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const load = () => {
    Bridge.getPlugins()
      .then((list) => {
        if (!list) return
        const ps = (list as Record<string, unknown>[]).map(toPlugin)
        setPlugins(ps)
        setSelectedId((cur) => cur ?? ps[0]?.id ?? null)
      })
      .catch(() => {
        /* 未连接后端：保持空 */
      })
  }

  useEffect(load, [])

  const selected = plugins.find((p) => p.id === selectedId) ?? null
  const enabledCount = plugins.filter((p) => p.enabled).length

  const toggle = (id: string, value: boolean) => {
    setPlugins((prev) => prev.map((p) => (p.id === id ? { ...p, enabled: value } : p)))
    Bridge.enablePlugin(id, value).catch(() => {})
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
          <Button
            variant="primary"
            icon={<Plus className="h-3.5 w-3.5" />}
            disabled
            title={t('plugins.installTip')}
          >
            {t('plugins.install')}
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
                      <Toggle checked={p.enabled} onChange={(v) => toggle(p.id, v)} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-baseline gap-1.5">
                        <span className={cx('truncate text-[12.5px]', p.enabled ? 'text-fg' : 'text-fg-muted')}>
                          {p.name}
                        </span>
                        {p.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{p.version}</span>}
                      </span>
                      <span className="mt-0.5 block truncate text-2xs text-fg-faint">{p.description}</span>
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
            <PluginDetail key={selected.id} plugin={selected} onToggle={(v) => toggle(selected.id, v)} />
          ) : (
            <EmptyState
              icon={<Puzzle className="h-8 w-8" />}
              title={t('plugins.detail.noneSelected')}
              hint={t('plugins.detail.noneSelectedHint')}
            />
          )}
        </section>
      </div>
    </PageShell>
  )
}

function PluginDetail({ plugin, onToggle }: { plugin: Plugin; onToggle: (v: boolean) => void }) {
  const { t } = useTranslation()
  const [source, setSource] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

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
    try {
      await Bridge.savePluginSource(plugin.id, source)
      setDirty(false)
      setSaved(true)
      setTimeout(() => setSaved(false), 1400)
    } catch {
      /* ignore */
    } finally {
      setSaving(false)
    }
  }

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
            {plugin.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{plugin.version}</span>}
          </div>
          <div className="mt-0.5 flex items-center gap-1">
            {plugin.runtime && <Chip title={t('plugins.detail.runtime', { runtime: plugin.runtime })}>{plugin.runtime}</Chip>}
            {plugin.author && <Chip title={t('plugins.detail.author', { author: plugin.author })}>{plugin.author}</Chip>}
            {plugin.priority != null && <Chip title={t('plugins.detail.priority')}>#{plugin.priority}</Chip>}
          </div>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-3">
          <span className="flex items-center gap-1.5">
            <span className="text-2xs text-fg-faint">{plugin.enabled ? t('plugins.detail.enabled') : t('plugins.detail.disabled')}</span>
            <Toggle checked={plugin.enabled} onChange={onToggle} />
          </span>
          <Button
            variant="primary"
            icon={saved ? <Check className="h-3.5 w-3.5" /> : <Save className="h-3.5 w-3.5" />}
            onClick={save}
            disabled={!dirty || saving}
          >
            {saved ? t('plugins.detail.saved') : saving ? t('plugins.detail.saving') : t('plugins.detail.save')}
          </Button>
        </div>
      </header>

      {/* 描述 */}
      <p className="shrink-0 px-4 pt-3 text-[12.5px] leading-relaxed text-fg-muted">{plugin.description}</p>

      {/* 代码区（可编辑，保存即热重载） */}
      <div className="flex min-h-0 flex-1 flex-col px-4 py-3">
        <div className="mb-1.5 flex shrink-0 items-center gap-1.5">
          <FileCode2 className="h-3.5 w-3.5 text-fg-faint" />
          <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.detail.scriptLabel')}</span>
          <span className="ml-auto text-2xs text-fg-faint">{dirty ? t('plugins.detail.unsaved') : t('plugins.detail.hotReloadHint')}</span>
        </div>
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
    </div>
  )
}
