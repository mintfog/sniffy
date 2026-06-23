import { useState } from 'react'
import { Events } from '@wailsio/runtime'
import { useTranslation } from 'react-i18next'
import {
  Database,
  Eraser,
  Info,
  Network,
  Palette,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { LANG_LABELS, SUPPORTED_LANGS, type Lang } from '@/i18n'
import { changeLang } from '@/i18n/bridge'
import { ACCENTS, usePrefs, type AccentKey, type FontSize, type ThemeMode } from '../prefs'
import { Button, Field, Panel, Select, TextInput, Toggle } from '../ui/controls'
import { cx } from '../ui/primitives'
import { APP_VERSION, RELEASES_URL, openExternal } from '../lib/links'
import { openAboutWindow, requestMainNav } from '../lib/windows'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { PageShell } from './PageShell'

const ACCENT_KEYS = Object.keys(ACCENTS) as AccentKey[]

export function SettingsView() {
  const p = usePrefs()
  const set = p.set
  const { t, i18n } = useTranslation()

  // 后端项（host/port/mitm/maxFlows/上游代理）改动即时生效：由 usePrefsBridge 监听偏好变更
  // 自动下发给 Bridge.updateConfig，无需「保存」按钮。

  const [confirmClear, setConfirmClear] = useState(false)

  const doClear = () => {
    Bridge.clearSessions().catch(() => {})
    // 通知主窗口清空其会话 store（设置可能在独立窗口中）
    try {
      void Events.Emit('data_cleared', null)
    } catch {
      /* ignore */
    }
    setConfirmClear(false)
  }

  return (
    <PageShell
      icon={SlidersHorizontal}
      title={t('settings.title')}
      subtitle={t('settings.subtitle')}
    >
      <Panel title={t('settings.proxy.title')} icon={<Network className="h-4 w-4" />}>
        <Field label={t('settings.proxy.listenPort')} hint={t('settings.proxy.listenPortHint')}>
          <TextInput
            type="number"
            min={1}
            max={65535}
            value={p.port}
            onChange={(e) => set({ port: e.target.value })}
            placeholder="8080"
            width={120}
          />
        </Field>
        <Field label={t('settings.proxy.systemProxy')} hint={t('settings.proxy.systemProxyHint')}>
          <Toggle checked={p.systemProxy} onChange={(v) => set({ systemProxy: v })} />
        </Field>
        <Field label={t('settings.proxy.autoSystemProxy')} hint={t('settings.proxy.autoSystemProxyHint')}>
          <Toggle checked={p.autoSystemProxy} onChange={(v) => set({ autoSystemProxy: v })} />
        </Field>
        <Field label={t('settings.proxy.throttle')} hint={t('settings.proxy.throttleHint')}>
          <Toggle checked={p.throttle} onChange={(v) => set({ throttle: v })} />
        </Field>
        <Field label={t('settings.proxy.upstream')} hint={t('settings.proxy.upstreamHint')}>
          <Toggle checked={p.upstream} onChange={(v) => set({ upstream: v })} />
        </Field>
        {p.upstream && (
          <Field label={t('settings.proxy.upstreamAddr')}>
            <TextInput
              value={p.upstreamAddr}
              onChange={(e) => set({ upstreamAddr: e.target.value })}
              placeholder="http://host:port"
              width={240}
            />
          </Field>
        )}
      </Panel>

      <Panel title={t('settings.decrypt.title')} icon={<ShieldCheck className="h-4 w-4" />}>
        <Field label={t('settings.decrypt.enableMitm')} hint={t('settings.decrypt.enableMitmHint')}>
          <Toggle checked={p.mitm} onChange={(v) => set({ mitm: v })} />
        </Field>
        <Field label={t('settings.decrypt.scope')}>
          <Select
            value={p.scope}
            onChange={(e) => set({ scope: e.target.value as typeof p.scope })}
            options={[
              { value: 'all', label: t('settings.decrypt.scopeAll') },
              { value: 'allow', label: t('settings.decrypt.scopeAllow') },
              { value: 'deny', label: t('settings.decrypt.scopeDeny') },
            ]}
          />
        </Field>
        <Field label={t('settings.decrypt.rootCert')} hint={t('settings.decrypt.rootCertHint')}>
          <Button icon={<ShieldCheck className="h-3.5 w-3.5" />} onClick={() => requestMainNav('certs')}>
            {t('settings.decrypt.manageCerts')}
          </Button>
        </Field>
      </Panel>

      <Panel title={t('settings.appearance.title')} icon={<Palette className="h-4 w-4" />}>
        <Field label={t('settings.language')}>
          <Select
            value={i18n.language}
            onChange={(e) => changeLang(e.target.value as Lang)}
            options={SUPPORTED_LANGS.map((l) => ({ value: l, label: LANG_LABELS[l] }))}
          />
        </Field>
        <Field label={t('settings.appearance.theme')}>
          <Select
            value={p.theme}
            onChange={(e) => set({ theme: e.target.value as ThemeMode })}
            options={[
              { value: 'dark', label: t('settings.appearance.themeDark') },
              { value: 'light', label: t('settings.appearance.themeLight') },
            ]}
          />
        </Field>
        <Field label={t('settings.appearance.accent')} hint={t('settings.appearance.accentHint')}>
          <div className="flex items-center gap-1.5">
            {ACCENT_KEYS.map((key) => (
              <button
                key={key}
                type="button"
                onClick={() => set({ accent: key })}
                title={key}
                aria-label={t('settings.appearance.accentSwatch', { name: key })}
                aria-pressed={p.accent === key}
                className={cx(
                  'h-5 w-5 rounded-full border-2 transition',
                  p.accent === key ? 'border-fg' : 'border-transparent hover:border-fg-faint',
                )}
                style={{ backgroundColor: ACCENTS[key].swatch }}
              />
            ))}
          </div>
        </Field>
        <Field label={t('settings.appearance.compact')} hint={t('settings.appearance.compactHint')}>
          <Toggle checked={p.compact} onChange={(v) => set({ compact: v })} />
        </Field>
        <Field label={t('settings.appearance.fontSize')}>
          <Select
            value={String(p.fontSize)}
            onChange={(e) => set({ fontSize: Number(e.target.value) as FontSize })}
            options={[
              { value: '12', label: t('settings.appearance.fontSmall') },
              { value: '13', label: t('settings.appearance.fontMedium') },
              { value: '14', label: t('settings.appearance.fontLarge') },
            ]}
          />
        </Field>
      </Panel>

      <Panel title={t('settings.storage.title')} icon={<Database className="h-4 w-4" />}>
        <Field label={t('settings.storage.maxFlows')} hint={t('settings.storage.maxFlowsHint')}>
          <Select
            value={String(p.maxFlows)}
            onChange={(e) => set({ maxFlows: Number(e.target.value) })}
            options={[
              { value: '1000', label: '1,000' },
              { value: '5000', label: '5,000' },
              { value: '10000', label: '10,000' },
              { value: '50000', label: '50,000' },
            ]}
          />
        </Field>
        <Field label={t('settings.storage.autoScroll')} hint={t('settings.storage.autoScrollHint')}>
          <Toggle checked={p.follow} onChange={(v) => set({ follow: v })} />
        </Field>
        <Field label={t('settings.storage.autoRecord')}>
          <Toggle checked={p.autoRecord} onChange={(v) => set({ autoRecord: v })} />
        </Field>
        <Field label={t('settings.storage.clearData')} hint={t('settings.storage.clearDataHint')}>
          <Button variant="danger" icon={<Eraser className="h-3.5 w-3.5" />} onClick={() => setConfirmClear(true)}>
            {t('settings.storage.clear')}
          </Button>
        </Field>
      </Panel>

      <Panel title={t('settings.about.title')} icon={<Info className="h-4 w-4" />}>
        <Field label={t('settings.about.version')}>
          <span className="font-mono text-[12px] text-fg-muted">Sniffy {APP_VERSION}</span>
        </Field>
        <Field label={t('settings.about.more')}>
          <Button onClick={() => openAboutWindow().catch(() => {})}>{t('settings.about.aboutSniffy')}</Button>
          <Button onClick={() => openExternal(RELEASES_URL)}>{t('settings.about.checkUpdate')}</Button>
        </Field>
      </Panel>

      {confirmClear && (
        <ConfirmDialog
          title={t('settings.storage.clearData')}
          message={t('settings.storage.clearConfirm')}
          confirmLabel={t('settings.storage.clear')}
          cancelLabel={t('settings.storage.cancel')}
          tone="danger"
          onConfirm={doClear}
          onClose={() => setConfirmClear(false)}
        />
      )}
    </PageShell>
  )
}
