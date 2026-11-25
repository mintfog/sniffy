import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, FolderOpen, Pencil, Plus, RefreshCw, RotateCcw, ShieldCheck, X } from 'lucide-react'
import { Bridge, type AppChoice, type AppConfig, type DecryptBypassEntry, type TunAppMode } from '@/lib/bridge'
import { detectPlatform } from '@/lib/platform'
import { MAX_TUN_APPS, parseTunApps, type TunAppsProblem } from '../../lib/tunApps'
import { parseTunBypass, type TunBypassProblem } from '../../lib/tunBypass'
import { Button, Select, Toggle } from '../../ui/controls'
import { cx, IconButton, ProcessAvatar } from '../../ui/primitives'
import { AppPicker } from './AppPicker'
import { HostListField, ListTextarea, SettingsField } from './SettingsLayout'

type TunConfig = Pick<AppConfig, 'tunIPv6' | 'tunBlockQUIC' | 'tunBypass' | 'tunAppMode' | 'tunIncludeApps' | 'tunExcludeApps'>
type ScopedMode = Exclude<TunAppMode, 'all'>

const TUN_APP_MODES: TunAppMode[] = ['all', 'include', 'exclude']
const LIST_KEY = { include: 'tunIncludeApps', exclude: 'tunExcludeApps' } as const

/** TUN 模式的参数与直通名单。参数直接读写后端配置：抓取的应用即时生效，其余下次开启时生效。 */
export function TunSettings() {
  const { t } = useTranslation()
  const [cfg, setCfg] = useState<TunConfig | null>(null)
  useEffect(() => {
    Bridge.getConfig()
      .then(setCfg)
      .catch(() => {})
  }, [])
  // 布尔项的结果是确定的，按本地值显示即可：连续点击时多个返回值可能乱序，用返回值回填会让开关跳回旧值。
  const update = (patch: Partial<Pick<TunConfig, 'tunIPv6' | 'tunBlockQUIC'>>) => {
    setCfg((c) => c && { ...c, ...patch })
    Bridge.updateConfig(patch).catch(() => {})
  }
  return (
    <>
      <div className="px-4 py-2.5 text-2xs leading-relaxed text-fg-muted">{t('settings.tun.note')}</div>
      {cfg && <AppScope saved={cfg} />}
      <SettingsField label={t('settings.tun.ipv6')} hint={t('settings.tun.ipv6Hint')}>
        <Toggle
          aria-label={t('settings.tun.ipv6')}
          checked={cfg?.tunIPv6 ?? false}
          disabled={!cfg}
          onChange={(v) => update({ tunIPv6: v })}
        />
      </SettingsField>
      <SettingsField label={t('settings.tun.blockQuic')} hint={t('settings.tun.blockQuicHint')}>
        <Toggle
          aria-label={t('settings.tun.blockQuic')}
          checked={cfg?.tunBlockQUIC ?? false}
          disabled={!cfg}
          onChange={(v) => update({ tunBlockQUIC: v })}
        />
      </SettingsField>
      {cfg && <BypassNetworks saved={cfg.tunBypass ?? []} />}
      <DecryptBypassList />
    </>
  )
}

/** 抓取的应用：模式改了即保存。两种模式各用一份名单；全部抓取时名单不起作用，不显示，内容仍保留在配置里。 */
function AppScope({ saved }: { saved: TunConfig }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<TunAppMode>(saved.tunAppMode ?? 'all')
  const [lists, setLists] = useState({ include: saved.tunIncludeApps ?? [], exclude: saved.tunExcludeApps ?? [] })
  const changeMode = (next: TunAppMode) => {
    setMode(next)
    Bridge.updateConfig({ tunAppMode: next }).catch(() => {})
  }
  const save = async (scoped: ScopedMode, apps: string[]) => {
    const c = await Bridge.updateConfig({ [LIST_KEY[scoped]]: apps })
    setLists({ include: c.tunIncludeApps ?? [], exclude: c.tunExcludeApps ?? [] })
  }
  return (
    <>
      <SettingsField label={t('settings.tun.appMode')} hint={t('settings.tun.appModeHint')}>
        <Select
          aria-label={t('settings.tun.appMode')}
          value={mode}
          onChange={(e) => changeMode(e.target.value as TunAppMode)}
          options={TUN_APP_MODES.map((m) => ({ value: m, label: t(`settings.tun.appModes.${m}`) }))}
        />
      </SettingsField>
      {mode !== 'all' && <AppList key={mode} mode={mode} apps={lists[mode]} onSave={(apps) => save(mode, apps)} />}
    </>
  )
}

/**
 * 一种模式的应用名单：逐项列出并标出此刻有没有在运行。可以从正在联网的程序里勾选、选择程序文件，或手动编辑
 * （按本机平台的规则校验，与后端一致）；改动立即保存，显示后端规范化的结果。
 */
function AppList({ mode, apps, onSave }: { mode: ScopedMode; apps: string[]; onSave: (apps: string[]) => Promise<void> }) {
  const { t } = useTranslation()
  const platform = detectPlatform()
  const running = useRunningApps(apps)
  const [problem, setProblem] = useState<TunAppsProblem>()
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState('')
  const [picking, setPicking] = useState(false)
  // 选择列表里带回的图标，名单里的其它项显示首字母。
  const [icons, setIcons] = useState<Record<string, AppChoice>>({})

  const replace = (next: string[]) => {
    if (next.length > MAX_TUN_APPS) {
      setProblem({ kind: 'tooMany', max: MAX_TUN_APPS })
      return
    }
    setProblem(undefined)
    onSave(next).catch(() => {})
  }
  const add = (entries: string[]) => replace([...apps, ...entries.filter((e) => !apps.includes(e))])
  const addPicked = (picked: AppChoice[]) => {
    setPicking(false)
    const byEntry = Object.fromEntries(picked.map((a) => [a.entry, a]))
    setIcons((prev) => ({ ...prev, ...byEntry }))
    add(picked.map((a) => a.entry))
  }
  const pickFile = () =>
    Bridge.pickProgram(t('settings.tun.pickProgramTitle'))
      .then((path) => path && add([path]))
      .catch(() => {})

  const startEditing = () => {
    setText(apps.join('\n'))
    setProblem(undefined)
    setEditing(true)
  }
  // 失焦即保存。点「完成」时输入框先失焦、内容已经保存过，finishEditing 只在内容有效时退出编辑。
  const saveText = () => {
    const parsed = parseTunApps(text, platform)
    setProblem(parsed.problem)
    if (parsed.list) onSave(parsed.list).catch(() => {})
  }
  const finishEditing = () => {
    if (parseTunApps(text, platform).list) setEditing(false)
  }

  const label = t(`settings.tun.apps.${mode}`)
  const problemKey = problem?.kind === 'relative' && platform === 'windows' ? 'relativeWindows' : problem?.kind
  const error = problem && t(`settings.tun.appsErrors.${problemKey}`, problem)
  // 说明里列出的来源与选择列表一致：Linux 上只有「正在联网」，见 AppPicker。
  const hintKey = editing ? 'appsEditHint' : platform === 'linux' ? 'appsHintLinux' : 'appsHint'
  return (
    <div className="settings-host-list">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="settings-field-label">{label}</div>
          <div className="settings-field-hint">{t(`settings.tun.${hintKey}`)}</div>
        </div>
        {editing ? (
          <Button size="sm" icon={<Check className="h-3.5 w-3.5" />} onClick={finishEditing}>
            {t('settings.tun.doneEditing')}
          </Button>
        ) : (
          <>
            <Button size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setPicking(true)}>
              {t('settings.tun.addApps')}
            </Button>
            <Button size="sm" icon={<FolderOpen className="h-3.5 w-3.5" />} onClick={pickFile}>
              {t('settings.tun.pickProgram')}
            </Button>
            <IconButton size="sm" aria-label={t('settings.tun.editApps')} title={t('settings.tun.editApps')} onClick={startEditing}>
              <Pencil className="h-3.5 w-3.5" />
            </IconButton>
          </>
        )}
      </div>
      <div className="mt-2.5">
        {editing ? (
          <ListTextarea
            label={label}
            value={text}
            onChange={(v) => {
              setText(v)
              setProblem(undefined)
            }}
            onBlur={saveText}
            placeholder={t(`settings.tun.appsPlaceholder.${platform}`)}
            error={error}
          />
        ) : apps.length > 0 ? (
          <ul aria-label={label} className="max-h-72 divide-y divide-line overflow-y-auto border-y border-line">
            {apps.map((app, i) => (
              <li key={app} className="flex items-center gap-3 py-2">
                <ProcessAvatar name={appLabel(app)} iconData={icons[app]?.iconData} iconType={icons[app]?.iconType} size={20} />
                <div className="min-w-0 flex-1 truncate text-[13px] text-fg">{app}</div>
                {running && (
                  <span className={cx('shrink-0 text-2xs', running[i] ? 'text-ok' : 'text-fg-faint')}>
                    {t(running[i] ? 'settings.tun.running' : 'settings.tun.notRunning')}
                  </span>
                )}
                <IconButton size="sm" aria-label={t('settings.tun.removeApp', { app })} title={t('settings.tun.removeApp', { app })} onClick={() => replace(apps.filter((e) => e !== app))}>
                  <X className="h-3.5 w-3.5" />
                </IconButton>
              </li>
            ))}
          </ul>
        ) : (
          <div className="text-[12px] leading-relaxed text-fg-faint">{t(`settings.tun.appsEmpty.${mode}`)}</div>
        )}
        {!editing && error && (
          <div role="alert" className="mt-1 text-[12px] leading-relaxed text-danger">
            {error}
          </div>
        )}
      </div>
      {picking && <AppPicker listed={apps} onAdd={addPicked} onClose={() => setPicking(false)} />}
    </div>
  )
}

/** 名单里每一项此刻有没有进程在运行，与 apps 一一对应；名单变了、窗口获得焦点时重查，还没有结果时为 null。 */
function useRunningApps(apps: string[]): boolean[] | null {
  const [result, setResult] = useState<{ apps: string[]; running: boolean[] } | null>(null)
  const [focusTick, setFocusTick] = useState(0)
  useEffect(() => {
    const onFocus = () => setFocusTick((n) => n + 1)
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [])
  useEffect(() => {
    if (apps.length === 0) return
    let alive = true
    Bridge.getTunAppsRunning(apps)
      .then((running) => alive && setResult({ apps, running }))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [apps, focusTick])
  return result?.apps === apps ? result.running : null
}

/** 名单项的显示名：路径取最后一段，供首字母头像用。 */
function appLabel(entry: string): string {
  return entry.split(/[\\/]/).filter(Boolean).pop() ?? entry
}

/** 不接管的网段：失焦时校验并保存，保存后显示后端规范化的结果；有无效项时不保存并指出是哪一项。 */
function BypassNetworks({ saved }: { saved: string[] }) {
  const { t } = useTranslation()
  const [text, setText] = useState(() => saved.join('\n'))
  const [problem, setProblem] = useState<TunBypassProblem>()
  const save = () => {
    const parsed = parseTunBypass(text)
    setProblem(parsed.problem)
    if (!parsed.list) return
    Bridge.updateConfig({ tunBypass: parsed.list })
      .then((c) => setText((c.tunBypass ?? []).join('\n')))
      .catch(() => {})
  }
  return (
    <HostListField
      label={t('settings.tun.bypassNetworks')}
      hint={t('settings.tun.bypassNetworksHint')}
      value={text}
      onChange={(v) => {
        setText(v)
        setProblem(undefined)
      }}
      onBlur={save}
      placeholder={t('settings.tun.bypassNetworksPlaceholder')}
      error={problem && t(`settings.tun.bypassNetworksErrors.${problem.kind}`, problem)}
    />
  )
}

/** 直通名单：经 TUN 的连接对这些主机不解密。名单随连接变化、没有推送，挂载时、窗口获得焦点时与手动刷新时拉取。 */
function DecryptBypassList() {
  const { t, i18n } = useTranslation()
  const [entries, setEntries] = useState<DecryptBypassEntry[]>([])
  // 未激活的窗口里直接点按钮时，焦点刷新与按钮的请求同时发出，只采纳最后发出的那次的结果。
  const seq = useRef(0)
  const load = useCallback((request: () => Promise<DecryptBypassEntry[]>) => {
    const id = ++seq.current
    request()
      .then((list) => {
        if (id === seq.current) setEntries(list)
      })
      .catch(() => {})
  }, [])
  const refresh = useCallback(() => load(Bridge.getDecryptBypass), [load])
  useEffect(() => {
    refresh()
    window.addEventListener('focus', refresh)
    return () => window.removeEventListener('focus', refresh)
  }, [refresh])
  const forget = (host: string) => load(() => Bridge.forgetDecryptBypass(host))
  const forgetAll = () => load(Bridge.clearDecryptBypass)

  return (
    <div className="settings-host-list">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="settings-field-label">{t('settings.tun.bypass')}</div>
          <div className="settings-field-hint">{t('settings.tun.bypassHint')}</div>
        </div>
        <IconButton size="sm" aria-label={t('settings.tun.refresh')} title={t('settings.tun.refresh')} onClick={refresh}>
          <RefreshCw className="h-3.5 w-3.5" />
        </IconButton>
        <Button size="sm" icon={<RotateCcw className="h-3.5 w-3.5" />} disabled={entries.length === 0} onClick={forgetAll}>
          {t('settings.tun.retryAll')}
        </Button>
      </div>
      {entries.length > 0 ? (
        <ul className="mt-2.5 max-h-72 divide-y divide-line overflow-y-auto border-y border-line">
          {entries.map((e) => (
            <li key={e.host} className="flex items-center gap-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="truncate font-mono text-[13px] text-fg">{e.host}</div>
                <div className="truncate text-2xs text-fg-faint">
                  {t(`settings.tun.reasons.${e.reason}`, { defaultValue: e.reason })} · {t('settings.tun.since', { time: new Date(e.since).toLocaleString(i18n.language) })}
                </div>
              </div>
              <Button
                size="sm"
                icon={<RotateCcw className="h-3.5 w-3.5" />}
                aria-label={t('settings.tun.retryHost', { host: e.host })}
                onClick={() => forget(e.host)}
              >
                {t('settings.tun.retry')}
              </Button>
            </li>
          ))}
        </ul>
      ) : (
        <div className="mt-2.5 flex items-center gap-2 text-[12px] text-fg-faint">
          <ShieldCheck className="h-4 w-4" />
          {t('settings.tun.bypassEmpty')}
        </div>
      )}
    </div>
  )
}
