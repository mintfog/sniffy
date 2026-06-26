import { type PointerEvent as ReactPointerEvent, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Events } from '@wailsio/runtime'
import { AlertTriangle, Plus, Puzzle, RotateCw, Trash2 } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, SegTabs, Toggle } from '../ui/controls'
import { Chip, cx, Divider, EmptyState, IconButton } from '../ui/primitives'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { ConnIndicator, DetailBar, Sidebar, SidebarItem, SplitView, StatusFooter } from '../ui/native'
import { LOG_CAP, type LogEntry, type Plugin, toLogs, toPlugin } from './plugins/model'
import { ConfigPanel, LogPanel, NewPluginModal, ScriptPanel } from './plugins/panels'

type Tab = 'code' | 'config'

const LOGS_OPEN_KEY = 'sniffy-plugin-logs-open'
const LOGS_H_KEY = 'sniffy-plugin-logs-h'
const LOGS_MIN = 120
const LOGS_MAX = 600
const COLLAPSED_H = 32

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
  const [tab, setTab] = useState<Tab>('code')
  const [ready, setReady] = useState(false)
  const [showNew, setShowNew] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [pendingSwitch, setPendingSwitch] = useState<string | null>(null)
  // 日志控制台抽屉的折叠态与高度,持久化到 localStorage。
  const [logsOpen, setLogsOpen] = useState(() => {
    try {
      return window.localStorage.getItem(LOGS_OPEN_KEY) !== '0'
    } catch {
      return true
    }
  })
  const [logsH, setLogsH] = useState(() => readNum(LOGS_H_KEY, 200))
  // 脚本面板与配置面板各自上报脏态;切换/关窗守卫看两者之和。
  const scriptDirtyRef = useRef(false)
  const configDirtyRef = useRef(false)
  const isDirty = () => scriptDirtyRef.current || configDirtyRef.current

  const load = useCallback(() => {
    Bridge.getPlugins()
      .then((list) => {
        setReady(true)
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
      .catch(() => setReady(false))
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

  // 有未保存的脚本或配置改动时，关闭窗口 / 刷新前给出原生确认（切换插件另由 selectPlugin 守护）。
  useEffect(() => {
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      if (!isDirty()) return
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
    if (isDirty()) {
      setPendingSwitch(id)
      return
    }
    setSelectedId(id)
  }

  const confirmSwitch = () => {
    if (pendingSwitch == null) return
    scriptDirtyRef.current = false
    configDirtyRef.current = false
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
      scriptDirtyRef.current = false
      configDirtyRef.current = false
      setSelectedId((cur) => (cur === id ? null : cur))
      load()
    } catch {
      /* ignore */
    } finally {
      setDeleting(false)
      setPendingDelete(null)
    }
  }

  const toggleLogs = () => {
    setLogsOpen((v) => {
      const next = !v
      try {
        window.localStorage.setItem(LOGS_OPEN_KEY, next ? '1' : '0')
      } catch {
        /* ignore */
      }
      return next
    })
  }

  // 日志抽屉竖向拖拽调高:向上拖更高。
  const startLogsResize = useCallback(
    (e: ReactPointerEvent) => {
      e.preventDefault()
      const startY = e.clientY
      const startH = logsH
      let last = startH
      const onMove = (ev: PointerEvent) => {
        // 另以窗口高度兜底,保证上方编辑器至少留一截可用空间。
        const maxByWin = Math.max(LOGS_MIN, window.innerHeight - 180)
        last = Math.min(LOGS_MAX, maxByWin, Math.max(LOGS_MIN, startH + (startY - ev.clientY)))
        setLogsH(last)
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
        try {
          window.localStorage.setItem(LOGS_H_KEY, String(Math.round(last)))
        } catch {
          /* ignore */
        }
      }
      document.body.style.cursor = 'row-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [logsH],
  )

  return (
    <SplitView
      status={
        <StatusFooter
          left={t('plugins.subtitle', { installed: plugins.length, enabled: enabledCount })}
          right={<ConnIndicator connected={ready} />}
        />
      }
      sidebar={
        <Sidebar
          header={
            <>
              <Puzzle className="h-3.5 w-3.5 text-accent" />
              <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('plugins.title')}</span>
              <span className="text-2xs tabular-nums text-fg-faint">{plugins.length}</span>
              <IconButton size="sm" className="ml-auto" title={t('plugins.refreshTip')} onClick={load}>
                <RotateCw className="h-3.5 w-3.5" />
              </IconButton>
            </>
          }
          footer={
            <Button variant="ghost" size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setShowNew(true)}>
              {t('plugins.create')}
            </Button>
          }
        >
          {plugins.length === 0 ? (
            <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('plugins.empty')}</div>
          ) : (
            plugins.map((p) => (
              <SidebarItem
                key={p.id}
                active={p.id === selectedId}
                dimmed={!p.enabled}
                onClick={() => selectPlugin(p.id)}
                leading={
                  p.error ? (
                    <AlertTriangle className="h-3.5 w-3.5 text-warn" />
                  ) : (
                    <Toggle checked={p.enabled} onChange={(v) => toggle(p.id, v)} />
                  )
                }
                title={
                  <span className="flex items-baseline gap-1.5">
                    <span className="truncate">{p.name}</span>
                    {p.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{p.version}</span>}
                  </span>
                }
                subtitle={p.error ? t('plugins.failed') : p.description}
              />
            ))
          )}
        </Sidebar>
      }
    >
      {!selected ? (
        <EmptyState icon={<Puzzle className="h-8 w-8" />} title={t('plugins.detail.noneSelected')} hint={t('plugins.detail.noneSelectedHint')} />
      ) : (
        <>
          <DetailBar>
            <Puzzle className="h-4 w-4 shrink-0 text-accent" />
            <span className="truncate text-[13px] font-semibold text-fg">{selected.name}</span>
            {selected.version && <span className="shrink-0 font-mono text-2xs text-fg-faint">v{selected.version}</span>}
            <span className="hidden items-center gap-1 lg:flex">
              {selected.runtime && <Chip title={t('plugins.detail.runtime', { runtime: selected.runtime })}>{selected.runtime}</Chip>}
              {selected.priority != null && <Chip title={t('plugins.detail.priority')}>#{selected.priority}</Chip>}
            </span>
            <span className="ml-auto flex shrink-0 items-center gap-2">
              {!selected.error && (
                <>
                  <SegTabs
                    value={tab}
                    onChange={setTab}
                    options={[
                      { key: 'code', label: t('plugins.tabs.script') },
                      { key: 'config', label: t('plugins.tabs.config') },
                    ]}
                  />
                  <Divider vertical className="h-5" />
                  <Toggle checked={selected.enabled} onChange={(v) => toggle(selected.id, v)} />
                </>
              )}
              <IconButton size="sm" tone="danger" title={t('plugins.delete')} onClick={() => setPendingDelete({ id: selected.id, name: selected.name })}>
                <Trash2 className="h-3.5 w-3.5" />
              </IconButton>
            </span>
          </DetailBar>

          {selected.error ? (
            <div className="min-h-0 flex-1 overflow-auto bg-warn/10 px-3 py-2 text-[12px] text-warn">
              <div className="mb-1 font-semibold">{t('plugins.failed')}</div>
              <pre className="whitespace-pre-wrap break-all font-mono text-[11.5px] leading-relaxed">{selected.error}</pre>
            </div>
          ) : (
            <>
              {/* 代码/配置常驻挂载、按页签切显隐:切页签不丢未保存的编辑与撤销栈。 */}
              <div className="relative flex min-h-0 flex-1 flex-col">
                <div className={cx('flex min-h-0 flex-1 flex-col', tab !== 'code' && 'hidden')}>
                  <ScriptPanel key={`s-${selected.id}`} plugin={selected} onDirtyChange={(d) => (scriptDirtyRef.current = d)} />
                </div>
                <div className={cx('flex min-h-0 flex-1 flex-col', tab !== 'config' && 'hidden')}>
                  <ConfigPanel key={`c-${selected.id}`} plugin={selected} onSaved={load} onDirtyChange={(d) => (configDirtyRef.current = d)} />
                </div>
              </div>

              <section
                className="flex shrink-0 flex-col border-t border-line"
                style={{ height: logsOpen ? Math.min(logsH, Math.max(LOGS_MIN, window.innerHeight - 180)) : COLLAPSED_H }}
              >
                {logsOpen && (
                  <div
                    onPointerDown={startLogsResize}
                    className="-mt-px h-1 shrink-0 cursor-row-resize transition-colors hover:bg-accent"
                  />
                )}
                <LogPanel
                  logs={logs[selected.id] ?? []}
                  open={logsOpen}
                  onToggle={toggleLogs}
                  onClear={() => {
                    Bridge.clearPluginLogs(selected.id).catch(() => {})
                    setLogs((prev) => ({ ...prev, [selected.id]: [] }))
                  }}
                />
              </section>
            </>
          )}
        </>
      )}

      {showNew && (
        <NewPluginModal
          onClose={() => setShowNew(false)}
          onCreated={(id) => {
            setShowNew(false)
            scriptDirtyRef.current = false
            configDirtyRef.current = false
            load()
            setSelectedId(id)
            setTab('code')
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
    </SplitView>
  )
}
