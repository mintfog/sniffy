import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw, RotateCcw, ShieldCheck } from 'lucide-react'
import { Bridge, type AppConfig, type DecryptBypassEntry } from '@/lib/bridge'
import { Button, Toggle } from '../../ui/controls'
import { IconButton } from '../../ui/primitives'
import { SettingsField } from './SettingsLayout'

type TunConfig = Pick<AppConfig, 'tunIPv6' | 'tunBlockQUIC'>

/** TUN 模式的参数与直通名单。参数直接读写后端配置，下次开启时生效。 */
export function TunSettings() {
  const { t } = useTranslation()
  const [cfg, setCfg] = useState<TunConfig | null>(null)
  useEffect(() => {
    Bridge.getConfig()
      .then(setCfg)
      .catch(() => {})
  }, [])
  // 布尔项的结果是确定的，按本地值显示即可：连续点击时多个返回值可能乱序，用返回值回填会让开关跳回旧值。
  const update = (patch: Partial<TunConfig>) => {
    setCfg((c) => c && { ...c, ...patch })
    Bridge.updateConfig(patch).catch(() => {})
  }
  return (
    <>
      <div className="px-4 py-2.5 text-2xs leading-relaxed text-fg-muted">{t('settings.tun.note')}</div>
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
      <DecryptBypassList />
    </>
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
