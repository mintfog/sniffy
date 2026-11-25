import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw, RotateCcw, ShieldCheck } from 'lucide-react'
import { Bridge, type AppConfig, type DecryptBypassEntry } from '@/lib/bridge'
import { detectPlatform } from '@/lib/platform'
import { parseTunApps, type TunAppsProblem } from '../../lib/tunApps'
import { parseTunBypass, type TunBypassProblem } from '../../lib/tunBypass'
import { Button, Select, Toggle } from '../../ui/controls'
import { IconButton } from '../../ui/primitives'
import { HostListField, SettingsField } from './SettingsLayout'

type TunConfig = Pick<AppConfig, 'tunIPv6' | 'tunBlockQUIC' | 'tunBypass' | 'tunAppMode' | 'tunApps'>
type TunAppMode = NonNullable<AppConfig['tunAppMode']>

const TUN_APP_MODES: TunAppMode[] = ['all', 'include', 'exclude']

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
      {cfg && <AppScope savedMode={cfg.tunAppMode ?? 'all'} savedApps={cfg.tunApps ?? []} />}
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

/**
 * 抓取的应用：模式改了即保存；名单失焦时按本机平台的规则（与后端一致）校验并保存，保存后显示后端规范化的
 * 结果，有无效项时不保存并指出是哪一项。全部抓取时名单不起作用，不显示，内容仍保留在配置里。
 */
function AppScope({ savedMode, savedApps }: { savedMode: TunAppMode; savedApps: string[] }) {
  const { t } = useTranslation()
  const platform = detectPlatform()
  const [mode, setMode] = useState(savedMode)
  const [text, setText] = useState(() => savedApps.join('\n'))
  const [problem, setProblem] = useState<TunAppsProblem>()
  const changeMode = (next: TunAppMode) => {
    setMode(next)
    Bridge.updateConfig({ tunAppMode: next }).catch(() => {})
  }
  const save = () => {
    const parsed = parseTunApps(text, platform)
    setProblem(parsed.problem)
    if (!parsed.list) return
    Bridge.updateConfig({ tunApps: parsed.list })
      .then((c) => setText((c.tunApps ?? []).join('\n')))
      .catch(() => {})
  }
  const problemKey = problem?.kind === 'relative' && platform === 'windows' ? 'relativeWindows' : problem?.kind
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
      {mode !== 'all' && (
        <HostListField
          label={t(`settings.tun.apps.${mode}`)}
          hint={t('settings.tun.appsHint')}
          value={text}
          onChange={(v) => {
            setText(v)
            setProblem(undefined)
          }}
          onBlur={save}
          placeholder={t(`settings.tun.appsPlaceholder.${platform}`)}
          error={problem && t(`settings.tun.appsErrors.${problemKey}`, problem)}
        />
      )}
    </>
  )
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
