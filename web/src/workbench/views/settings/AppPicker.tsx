import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AppWindow, RefreshCw, Search, X } from 'lucide-react'
import { Bridge, type AppChoice, type AppIcon } from '@/lib/bridge'
import { detectPlatform } from '@/lib/platform'
import { Button, SegTabs } from '../../ui/controls'
import { cx, IconButton, ProcessAvatar } from '../../ui/primitives'

type Source = 'network' | 'installed'

/** 一个标签页的列表：apps 是最近一次列出的结果，刷新期间照旧显示；error 是最近一次列不出的原因。 */
type Listing = { apps?: AppChoice[]; loading: boolean; error?: string }

const UNLISTED: Listing = { loading: true }

const FETCH: Record<Source, () => Promise<AppChoice[]>> = {
  network: Bridge.getNetworkApps,
  installed: Bridge.getInstalledApps,
}

/**
 * 勾选程序加入应用名单：「正在联网」列出此刻有网络连接的程序；Windows、macOS 上另有「已安装」。各标签页
 * 第一次打开时列一次，之后可以手动刷新；列表先显示，图标随后补上。listed 是名单里已有的条目，这些程序显示为
 * 已勾选、不可再选。
 */
export function AppPicker({
  listed,
  onAdd,
  onClose,
}: {
  listed: string[]
  onAdd: (apps: AppChoice[]) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const sources: Source[] = detectPlatform() === 'linux' ? ['network'] : ['network', 'installed']
  const [source, setSource] = useState<Source>('network')
  const [listings, setListings] = useState<Partial<Record<Source, Listing>>>({})
  // 按路径补上的图标。
  const [icons, setIcons] = useState<Record<string, AppIcon>>({})
  const [query, setQuery] = useState('')
  // 跨标签页、跨刷新保留勾选，按条目记。
  const [picked, setPicked] = useState<Map<string, AppChoice>>(() => new Map())
  const fetched = useRef(new Set<Source>())

  const loadIcons = useCallback((apps: AppChoice[]) => {
    const missing = apps.filter((a) => !a.iconData && a.path).map((a) => a.path as string)
    if (missing.length === 0) return
    Bridge.getAppIcons(missing)
      .then((got) => {
        const byPath = Object.fromEntries((got ?? []).map((i) => [i.path, i]))
        setIcons((prev) => ({ ...prev, ...byPath }))
      })
      .catch(() => {})
  }, [])

  const list = useCallback((src: Source) => {
    fetched.current.add(src)
    const update = (patch: Partial<Listing>) => setListings((prev) => ({ ...prev, [src]: { ...(prev[src] ?? UNLISTED), ...patch } }))
    update({ loading: true })
    FETCH[src]()
      .then((apps = []) => {
        update({ apps, loading: false, error: undefined })
        loadIcons(apps)
      })
      .catch((e: unknown) => update({ loading: false, error: e instanceof Error ? e.message : String(e) }))
  }, [loadIcons])

  useEffect(() => {
    if (!fetched.current.has(source)) list(source)
  }, [list, source])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const listing = listings[source] ?? UNLISTED
  const shown = useMemo(() => {
    const apps = listing.apps ?? []
    const q = query.trim().toLowerCase()
    return q ? apps.filter((a) => [a.name, a.entry, a.path].some((s) => s?.toLowerCase().includes(q))) : apps
  }, [listing, query])

  const withIcon = (app: AppChoice): AppChoice => {
    const icon = app.path ? icons[app.path] : undefined
    return app.iconData || !icon ? app : { ...app, iconData: icon.iconData, iconType: icon.iconType }
  }
  const toggle = (app: AppChoice) =>
    setPicked((prev) => {
      const next = new Map(prev)
      if (!next.delete(app.entry)) next.set(app.entry, app)
      return next
    })

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-overlay p-4" onClick={onClose} role="presentation">
      <div
        className="flex max-h-[80vh] w-full max-w-md flex-col overflow-hidden rounded-lg border border-line bg-popover shadow-dialog"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="app-picker-title"
      >
        <header className="flex items-center gap-2 border-b border-line px-4 py-3">
          <AppWindow className="h-4 w-4 text-accent" />
          <span id="app-picker-title" className="text-[13px] font-semibold text-fg">
            {t('settings.tun.picker.title')}
          </span>
          <button
            type="button"
            onClick={onClose}
            aria-label={t('settings.tun.picker.cancel')}
            title={t('settings.tun.picker.cancel')}
            className="ml-auto text-fg-faint hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="flex flex-col gap-2 border-b border-line px-4 py-2.5">
          <div className="flex items-center gap-2">
            {sources.length > 1 && (
              <SegTabs<Source>
                value={source}
                onChange={setSource}
                options={sources.map((key) => ({ key, label: t(`settings.tun.picker.tabs.${key}`) }))}
              />
            )}
            <IconButton
              size="sm"
              className="ml-auto"
              aria-label={t('settings.tun.picker.refresh')}
              title={t('settings.tun.picker.refresh')}
              disabled={listing.loading}
              onClick={() => list(source)}
            >
              <RefreshCw className={cx('h-3.5 w-3.5', listing.loading && 'animate-spin')} />
            </IconButton>
          </div>
          <div className="text-2xs leading-relaxed text-fg-muted">{t(`settings.tun.picker.hint.${source}`)}</div>
          <label className="flex items-center gap-2 rounded-wb border border-line bg-inset px-2 py-1 focus-within:border-accent">
            <Search className="h-3.5 w-3.5 text-fg-faint" />
            <input
              aria-label={t('settings.tun.picker.search')}
              placeholder={t('settings.tun.picker.search')}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="min-w-0 flex-1 bg-transparent text-[13px] text-fg outline-none placeholder:text-fg-faint"
            />
          </label>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {listing.error && (
            <div role="alert" className="px-4 py-6 text-center text-[12px] text-danger">
              {t(`settings.tun.picker.failed.${source}`, { reason: listing.error })}
            </div>
          )}
          {!listing.apps && listing.loading && (
            <div className="px-4 py-6 text-center text-[12px] text-fg-faint">{t(`settings.tun.picker.loading.${source}`)}</div>
          )}
          {listing.apps && !listing.loading && shown.length === 0 && (
            <div className="px-4 py-6 text-center text-[12px] text-fg-faint">{t(`settings.tun.picker.empty.${source}`)}</div>
          )}
          {shown.length > 0 && (
            <ul className="divide-y divide-line">
              {shown.map((raw) => {
                const app = withIcon(raw)
                const label = app.name || app.entry
                const already = listed.includes(app.entry)
                return (
                  <li key={`${app.entry}\n${app.path ?? ''}`}>
                    <label className="flex cursor-pointer items-center gap-3 px-4 py-2 hover:bg-elevated aria-disabled:cursor-default" aria-disabled={already}>
                      <input
                        type="checkbox"
                        checked={already || picked.has(app.entry)}
                        disabled={already}
                        onChange={() => toggle(app)}
                        aria-label={label}
                      />
                      <ProcessAvatar name={label} iconData={app.iconData} iconType={app.iconType} size={20} />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[13px] text-fg">{label}</div>
                        {app.path && <div className="truncate text-2xs text-fg-faint">{app.path}</div>}
                      </div>
                      {already && <span className="shrink-0 text-2xs text-fg-faint">{t('settings.tun.picker.listed')}</span>}
                    </label>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button onClick={onClose}>{t('settings.tun.picker.cancel')}</Button>
          <Button variant="primary" disabled={picked.size === 0} onClick={() => onAdd([...picked.values()].map(withIcon))}>
            {t('settings.tun.picker.add', { count: picked.size })}
          </Button>
        </footer>
      </div>
    </div>
  )
}
