import { type PointerEvent as ReactPointerEvent, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Events } from '@wailsio/runtime'
import { AlertTriangle, PanelRight, Plus, Puzzle, RotateCw } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, SegTabs, Toggle } from '../ui/controls'
import { cx, EmptyState } from '../ui/primitives'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { LOG_CAP, type LogEntry, type Plugin, toLogs, toPlugin } from './plugins/model'
import { ConfigPanel, DetailHeader, LogPanel, NewPluginModal, ScriptPanel } from './plugins/panels'

type DockTab = 'logs' | 'config'

const DOCK_W_KEY = 'sniffy-plugin-dock-w'
const DOCK_OPEN_KEY = 'sniffy-plugin-dock-open'
const DOCK_MIN = 280
const DOCK_MAX = 680

function readNum(key: string, fallback: number): number {
  try {
    const v = Number(window.localStorage.getItem(key))
    return Number.isFinite(v) && v > 0 ? v : fallback
  } catch {
    return fallback
  }
}

export function PluginsView() {
  const { t } = useTranslation()
  const [plugins, setPlugins] = useState<Plugin[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [logs, setLogs] = useState<Record<string, LogEntry[]>>({})
  const [showNew, setShowNew] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [pendingSwitch, setPendingSwitch] = useState<string | null>(null)
  const [dock, setDock] = useState<DockTab>('logs')
  const [dockOpen, setDockOpen] = useState(() => {
    try {
      return window.localStorage.getItem(DOCK_OPEN_KEY) !== '0'
    } catch {
      return true
    }
  })
  const [dockW, setDockW] = useState(() => readNum(DOCK_W_KEY, 360))
  const dirtyRef = useRef(false)

  const load = useCallback(() => {
    Bridge.getPlugins()
      .then((list) => {
        if (!list) return
        const raw = list as Record<string, unknown>[]
        const ps = raw.map(toPlugin)
        setPlugins(ps)
        setSelectedId((cur) => (cur && ps.some((p) => p.id === cur) ? cur : ps[0]?.id ?? null))
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
  }, [])

  useEffect(() => load(), [load])

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

  // 有未保存的脚本改动时，关闭窗口 / 刷新前给出原生确认（切换插件另由 selectPlugin 守护）。
  useEffect(() => {
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      if (!dirtyRef.current) return
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [])

  const selected = plugins.find((p) => p.id === selectedId) ?? null
  const enabledCount = plugins.filter((p) => p.enabled).length

  const selectPlugin = (id: string) => {
    if (id === selectedId) return
    if (dirtyRef.current) {
      setPendingSwitch(id)
      return
    }
    setSelectedId(id)
  }

  const confirmSwitch = () => {
    if (pendingSwitch == null) return
    dirtyRef.current = false
    setSelectedId(pendingSwitch)
    setPendingSwitch(null)
  }

  const toggle = (id: string, value: boolean) => {
    setPlugins((prev) => prev.map((p) => (p.id === id ? { ...p, enabled: value } : p)))
    Bridge.enablePlugin(id, value).catch(() => load())
  }

  const confirmDelete = async () => {
    if (!pendingDelete) return
    const { id } = pendingDelete
    setDeleting(true)
    try {
      await Bridge.deletePlugin(id)
      dirtyRef.current = false
      setSelectedId((cur) => (cur === id ? null : cur))
      load()
    } catch {
      /* ignore */
    } finally {
      setDeleting(false)
      setPendingDelete(null)
    }
  }

  const toggleDock = () => {
    setDockOpen((v) => {
      const next = !v
      try {
        window.localStorage.setItem(DOCK_OPEN_KEY, next ? '1' : '0')
      } catch {
        /* ignore */
      }
      return next
    })
  }

  const startDockResize = useCallback((e: ReactPointerEvent) => {
    e.preventDefault()
    const startX = e.clientX
    const startW = dockW
    let last = startW
    const onMove = (ev: PointerEvent) => {
      last = Math.min(DOCK_MAX, Math.max(DOCK_MIN, startW + (startX - ev.clientX)))
      setDockW(last)
    }
    const onUp = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      try {
        window.localStorage.setItem(DOCK_W_KEY, String(Math.round(last)))
      } catch {
        /* ignore */
      }
    }
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }, [dockW])

  const showDock = dockOpen && !!selected && !selected.error

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-base">
      {/* ── 顶部工具条 ── */}
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <Puzzle className="h-4 w-4 text-accent" />
        <span className="text-[13px] font-semibold text-fg">{t('plugins.title')}</span>
        <span className="hidden text-2xs text-fg-faint sm:inline">
          {t('plugins.subtitle', { installed: plugins.length, enabled: enabledCount })}
        </span>
        <span className="ml-auto flex items-center gap-1.5">
          <Button
            size="sm"
            icon={<PanelRight className="h-3.5 w-3.5" />}
            onClick={toggleDock}
            disabled={!selected || !!selected.error}
            title={t('plugins.dock.toggle')}
            className={cx(showDock && 'text-accent')}
          >
            {dock === 'logs' ? t('plugins.tabs.logs') : t('plugins.tabs.config')}
          </Button>
          <Button size="sm" icon={<RotateCw className="h-3.5 w-3.5" />} onClick={load} title={t('plugins.refreshTip')}>
            {t('plugins.refresh')}
          </Button>
          <Button variant="primary" size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setShowNew(true)}>
            {t('plugins.create')}
          </Button>
        </span>
      </div>

      <div className="flex min-h-0 flex-1 overflow-hidden">
        {/* ── 左栏：插件列表 ── */}
        <aside className="flex w-[240px] shrink-0 flex-col border-r border-line bg-surface">
          <header className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-inset/50 px-3">
            <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.installed')}</span>
            <span className="text-2xs tabular-nums text-fg-faint">{plugins.length}</span>
          </header>
          <div className="min-h-0 flex-1 overflow-auto">
            {plugins.length === 0 ? (
              <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('plugins.empty')}</div>
            ) : (
              plugins.map((p) => {
                const active = p.id === selectedId
                return (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() => selectPlugin(p.id)}
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
                        <span className={cx('truncate text-[12.5px]', p.enabled ? 'text-fg' : 'text-fg-muted')}>{p.name}</span>
                        {p.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{p.version}</span>}
                      </span>
                      <span className="mt-0.5 block truncate text-2xs text-fg-faint">{p.error ? t('plugins.failed') : p.description}</span>
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </aside>

        {/* ── 中栏：编辑器 ── */}
        <section className="flex min-w-0 flex-1 flex-col bg-surface">
          {selected ? (
            selected.error ? (
              <>
                <DetailHeader plugin={selected} onToggle={(v) => toggle(selected.id, v)} onDelete={() => setPendingDelete({ id: selected.id, name: selected.name })} />
                <div className="m-4 rounded-wb border border-warn/40 bg-warn/10 p-3 text-[12px] text-warn">
                  <div className="mb-1 font-semibold">{t('plugins.failed')}</div>
                  <pre className="whitespace-pre-wrap break-all font-mono text-[11.5px] leading-relaxed">{selected.error}</pre>
                </div>
              </>
            ) : (
              <>
                <DetailHeader plugin={selected} onToggle={(v) => toggle(selected.id, v)} onDelete={() => setPendingDelete({ id: selected.id, name: selected.name })} />
                <ScriptPanel key={selected.id} plugin={selected} onDirtyChange={(d) => (dirtyRef.current = d)} />
              </>
            )
          ) : (
            <EmptyState icon={<Puzzle className="h-8 w-8" />} title={t('plugins.detail.noneSelected')} hint={t('plugins.detail.noneSelectedHint')} />
          )}
        </section>

        {/* ── 右栏：日志 / 配置 ── */}
        {showDock && selected && (
          <>
            <div
              onPointerDown={startDockResize}
              className="w-px shrink-0 cursor-col-resize bg-line transition-colors hover:bg-accent"
            >
              <div className="h-full w-1 -translate-x-px" />
            </div>
            <section className="flex shrink-0 flex-col bg-surface" style={{ width: dockW }}>
              <div className="flex h-8 shrink-0 items-center border-b border-line px-3">
                <SegTabs
                  value={dock}
                  onChange={setDock}
                  options={[
                    { key: 'logs', label: t('plugins.tabs.logs'), count: (logs[selected.id] ?? []).length },
                    { key: 'config', label: t('plugins.tabs.config') },
                  ]}
                />
              </div>
              {dock === 'logs' ? (
                <LogPanel
                  logs={logs[selected.id] ?? []}
                  onClear={() => {
                    Bridge.clearPluginLogs(selected.id).catch(() => {})
                    setLogs((prev) => ({ ...prev, [selected.id]: [] }))
                  }}
                />
              ) : (
                <ConfigPanel key={selected.id} plugin={selected} onSaved={load} />
              )}
            </section>
          </>
        )}
      </div>

      {showNew && (
        <NewPluginModal
          onClose={() => setShowNew(false)}
          onCreated={(id) => {
            setShowNew(false)
            dirtyRef.current = false
            load()
            setSelectedId(id)
          }}
        />
      )}

      {pendingDelete && (
        <ConfirmDialog
          title={t('plugins.deleteTitle')}
          message={t('plugins.deleteConfirm', { name: pendingDelete.name })}
          confirmLabel={t('plugins.delete')}
          cancelLabel={t('plugins.cancel')}
          tone="danger"
          busy={deleting}
          onConfirm={confirmDelete}
          onClose={() => !deleting && setPendingDelete(null)}
        />
      )}

      {pendingSwitch != null && (
        <ConfirmDialog
          title={t('plugins.detail.unsavedTitle')}
          message={t('plugins.detail.unsavedSwitch')}
          confirmLabel={t('plugins.detail.discardSwitch')}
          cancelLabel={t('plugins.cancel')}
          tone="danger"
          onConfirm={confirmSwitch}
          onClose={() => setPendingSwitch(null)}
        />
      )}
    </div>
  )
}
