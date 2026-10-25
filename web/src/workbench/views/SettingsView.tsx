import { useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { useTranslation } from 'react-i18next'
import { Eraser, Gauge, ShieldCheck } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { LANG_LABELS, SUPPORTED_LANGS, type Lang } from '@/i18n'
import { changeLang } from '@/i18n/bridge'
import {
  ACCENTS,
  MAX_THROTTLE_KIBPS,
  MIN_THROTTLE_KIBPS,
  normalizeThrottleKiBps,
  usePrefs,
  type FontSize,
  type PresetAccent,
  type ThemeMode,
} from '../prefs'
import { Button, Select, TextInput, Toggle } from '../ui/controls'
import { cx } from '../ui/primitives'
import { useBackendVersion } from '../lib/version'
import { useUpdate } from '../lib/update'
import { openAboutWindow, requestMainNav } from '../lib/windows'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { ThrottleDialog } from '../ui/ThrottleDialog'
import {
  SettingsField,
  SettingsLayout,
  type SettingsSection,
} from './settings/SettingsLayout'

const ACCENT_KEYS = Object.keys(ACCENTS) as PresetAccent[]

// 失焦保存与关闭认证必须串行下发，避免保存请求后到达而重新开启认证。
let proxyAuthTail: Promise<unknown> = Promise.resolve()
function updateProxyAuth(patch: Record<string, unknown>) {
  const send = () => Bridge.updateConfig(patch)
  const next = proxyAuthTail.then(send, send)
  proxyAuthTail = next.catch(() => {})
  return next
}

function HostListField({
  label,
  hint,
  value,
  onChange,
  placeholder,
}: {
  label: string
  hint?: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  return (
    <div className="settings-host-list">
      <div className="settings-field-label">{label}</div>
      {hint && <div className="settings-field-hint">{hint}</div>}
      <textarea
        aria-label={label}
        spellCheck={false}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        rows={4}
        className="w-full resize-y rounded-wb border border-line bg-inset px-2 py-1.5 font-mono text-[12px] leading-relaxed text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface"
      />
    </div>
  )
}

export function SettingsView() {
  const p = usePrefs()
  const set = p.set
  const { t, i18n } = useTranslation()
  const version = useBackendVersion()
  // 自动检查由后端通过 update_state 同步到各窗口。
  const update = useUpdate()

  // 密码草稿留在页面层，切换分类后可恢复；仅提交后端，不进入 prefs 或持久化快照。
  const [upstreamPassword, setUpstreamPassword] = useState('')
  const [upstreamPasswordSet, setUpstreamPasswordSet] = useState(false)
  const passwordDirty = useRef(false)
  const passwordValue = useRef('')

  const [proxyPassword, setProxyPassword] = useState('')
  // null 表示后端状态尚未返回，加载期间不提示缺少密码。
  const [proxyPasswordSet, setProxyPasswordSet] = useState<boolean | null>(null)
  const proxyPasswordDirty = useRef(false)
  const proxyPasswordValue = useRef('')
  // 凭据补齐前仅开启草稿状态；prefs.proxyAuth 表示认证已在后端生效。
  const [proxyAuthDraft, setProxyAuthDraft] = useState(false)
  const proxyAuthOn = p.proxyAuth || proxyAuthDraft
  const proxyCredsIncomplete =
    proxyAuthOn &&
    (p.proxyUsername === '' ||
      (proxyPasswordSet === false && proxyPassword === ''))
  const resetPasswordDraft = () => {
    passwordValue.current = ''
    passwordDirty.current = false
    setUpstreamPassword('')
  }
  // 认证开关通过 prefs 防抖下发，保存密码时需一并启用，否则后端规范化会清除密码。
  const commitUpstreamPassword = () => {
    if (!passwordDirty.current) return
    const value = passwordValue.current
    passwordDirty.current = false
    Bridge.updateConfig({ upstreamPassword: value, upstreamAuth: true })
      .then(cfg => {
        setUpstreamPasswordSet(Boolean(cfg.upstreamPasswordSet))
        if (!passwordDirty.current && passwordValue.current === value) {
          passwordValue.current = ''
          setUpstreamPassword('')
        }
      })
      .catch(() => {
        passwordDirty.current = true
      })
  }
  const clearUpstreamPassword = () => {
    resetPasswordDraft()
    setUpstreamPasswordSet(false)
    Bridge.updateConfig({ upstreamPassword: '' })
      .then(cfg => setUpstreamPasswordSet(Boolean(cfg.upstreamPasswordSet)))
      .catch(() => {
        passwordDirty.current = true
      })
  }
  const changeUpstreamAuth = (enabled: boolean) => {
    if (!enabled) {
      resetPasswordDraft()
      setUpstreamPasswordSet(false)
    }
    set({ upstreamAuth: enabled })
  }
  const resetProxyPasswordDraft = () => {
    proxyPasswordValue.current = ''
    proxyPasswordDirty.current = false
    setProxyPassword('')
  }
  const clearProxyPassword = () => {
    resetProxyPasswordDraft()
    setProxyPasswordSet(false)
    // 已启用认证时，空密码让监听端继续拒绝请求，直到用户补齐凭据。
    updateProxyAuth({ proxyPassword: '' })
      .then(cfg => setProxyPasswordSet(Boolean(cfg.proxyPasswordSet)))
      .catch(() => {
        proxyPasswordDirty.current = true
      })
  }
  const commitProxyCredentials = () => {
    if (!proxyPasswordDirty.current) return
    const value = proxyPasswordValue.current
    // 提交空值等同于清除凭据，不能顺手把认证打开。
    if (value === '') {
      clearProxyPassword()
      return
    }
    // 账号缺失时保留密码草稿，待账号补齐并失焦后提交。
    const username = usePrefs.getState().proxyUsername
    if (username === '') return
    proxyPasswordDirty.current = false
    // 凭据与开关同批下发，避免中间状态因凭据不全而拒绝所有客户端。
    updateProxyAuth({
      proxyAuth: true,
      proxyUsername: username,
      proxyPassword: value,
    })
      .then(cfg => {
        setProxyPasswordSet(Boolean(cfg.proxyPasswordSet))
        set({ proxyAuth: Boolean(cfg.proxyAuth) })
        if (
          !proxyPasswordDirty.current &&
          proxyPasswordValue.current === value
        ) {
          proxyPasswordValue.current = ''
          setProxyPassword('')
        }
      })
      .catch(() => {
        proxyPasswordDirty.current = true
      })
  }
  const changeProxyAuth = (enabled: boolean) => {
    setProxyAuthDraft(enabled)
    if (enabled) return
    resetProxyPasswordDraft()
    setProxyPasswordSet(false)
    // 关闭认证直接下发，跳过 prefs 的防抖等待。
    void updateProxyAuth({ proxyAuth: false }).catch(() => {})
    set({ proxyAuth: false })
  }
  useEffect(() => {
    let active = true
    Bridge.getConfig()
      .then(cfg => {
        if (active) {
          setUpstreamPasswordSet(Boolean(cfg?.upstreamPasswordSet))
          setProxyPasswordSet(Boolean(cfg?.proxyPasswordSet))
        }
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [])
  useEffect(() => {
    return () => {
      if (passwordDirty.current) {
        void Bridge.updateConfig({
          upstreamPassword: passwordValue.current,
          upstreamAuth: true,
        })
      }
      if (!proxyPasswordDirty.current) return
      // 与失焦提交保持相同校验，避免关闭窗口时启用尚未补齐凭据的认证。
      const value = proxyPasswordValue.current
      const username = usePrefs.getState().proxyUsername
      if (value === '') {
        void updateProxyAuth({ proxyPassword: '' }).catch(() => {})
        return
      }
      if (username === '') return
      void updateProxyAuth({
        proxyAuth: true,
        proxyUsername: username,
        proxyPassword: value,
      })
        .then(cfg =>
          usePrefs.getState().set({ proxyAuth: Boolean(cfg.proxyAuth) })
        )
        .catch(() => {})
    }
  }, [])

  const [confirmClear, setConfirmClear] = useState(false)
  const [throttleDialogOpen, setThrottleDialogOpen] = useState(false)

  const changeThrottle = (enabled: boolean) => {
    if (!enabled) {
      set({ throttle: false })
      return
    }
    setThrottleDialogOpen(true)
  }

  const enableThrottle = (rateKiBps: number) => {
    set({ throttleKiBps: String(rateKiBps), throttle: true })
    setThrottleDialogOpen(false)
  }

  const doClear = () => {
    Bridge.clearSessions().catch(() => {})
    // 设置可能位于独立窗口，需通知主窗口同步清空会话。
    try {
      void Events.Emit('data_cleared', null)
    } catch {
      // 浏览器预览没有 Wails 事件通道。
    }
    setConfirmClear(false)
  }

  const sections: SettingsSection[] = [
    {
      id: 'general',
      category: 'general',
      title: t('settings.sections.application'),
      searchKeys: ['general.runInBackground', 'general.runInBackgroundHint'],
      content: (
        <SettingsField
          label={t('settings.general.runInBackground')}
          hint={t('settings.general.runInBackgroundHint')}
        >
          <Toggle
            aria-label={t('settings.general.runInBackground')}
            checked={p.runInBackground}
            onChange={v => set({ runInBackground: v })}
          />
        </SettingsField>
      ),
    },
    {
      id: 'local',
      category: 'proxy',
      title: t('settings.sections.localProxy'),
      description: t('settings.sections.localProxyHint'),
      searchKeys: [
        'proxy.listenPort',
        'proxy.listenPortHint',
        'proxy.proxyAuth',
        'proxy.proxyAuthHint',
        'proxy.proxyUsername',
        'proxy.proxyPassword',
        'proxy.proxyAuthIncomplete',
      ],
      content: (
        <>
          <SettingsField
            label={t('settings.proxy.listenPort')}
            hint={t('settings.proxy.listenPortHint')}
          >
            <TextInput
              aria-label={t('settings.proxy.listenPort')}
              type="number"
              min={1}
              max={65535}
              value={p.port}
              onChange={e => set({ port: e.target.value })}
              placeholder="8080"
              width={120}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.proxy.proxyAuth')}
            hint={t('settings.proxy.proxyAuthHint')}
          >
            <Toggle
              aria-label={t('settings.proxy.proxyAuth')}
              checked={proxyAuthOn}
              onChange={changeProxyAuth}
            />
          </SettingsField>
          {proxyAuthOn && (
            <>
              <SettingsField label={t('settings.proxy.proxyUsername')}>
                <TextInput
                  aria-label={t('settings.proxy.proxyUsername')}
                  value={p.proxyUsername}
                  onChange={e => set({ proxyUsername: e.target.value })}
                  onBlur={commitProxyCredentials}
                  autoComplete="username"
                  width={240}
                />
              </SettingsField>
              <SettingsField label={t('settings.proxy.proxyPassword')}>
                <div className="settings-password">
                  <TextInput
                    aria-label={t('settings.proxy.proxyPassword')}
                    type="password"
                    value={proxyPassword}
                    onChange={e => {
                      proxyPasswordValue.current = e.target.value
                      proxyPasswordDirty.current = true
                      setProxyPassword(e.target.value)
                    }}
                    onBlur={commitProxyCredentials}
                    autoComplete="new-password"
                    placeholder={proxyPasswordSet ? '••••••••' : ''}
                    width={240}
                    className="pr-9"
                  />
                  <Button
                    variant="ghost"
                    size="sm"
                    className="absolute right-0.5 top-1/2 -translate-y-1/2"
                    icon={<Eraser className="h-3.5 w-3.5" />}
                    disabled={!proxyPasswordSet && proxyPassword === ''}
                    onMouseDown={e => e.preventDefault()}
                    onClick={clearProxyPassword}
                    title={t('settings.proxy.proxyPasswordClear')}
                    aria-label={t('settings.proxy.proxyPasswordClear')}
                  />
                </div>
              </SettingsField>
              {proxyCredsIncomplete && (
                <div className="px-3 py-2 text-2xs leading-relaxed text-warn">
                  {t('settings.proxy.proxyAuthIncomplete')}
                </div>
              )}
            </>
          )}
        </>
      ),
    },
    {
      id: 'system',
      category: 'proxy',
      title: t('settings.sections.systemProxy'),
      description: t('settings.sections.systemProxyHint'),
      searchKeys: [
        'proxy.systemProxy',
        'proxy.systemProxyHint',
        'proxy.autoSystemProxy',
        'proxy.autoSystemProxyHint',
      ],
      content: (
        <>
          <SettingsField
            label={t('settings.proxy.systemProxy')}
            hint={t('settings.proxy.systemProxyHint')}
          >
            <Toggle
              aria-label={t('settings.proxy.systemProxy')}
              checked={p.systemProxy}
              onChange={v => set({ systemProxy: v })}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.proxy.autoSystemProxy')}
            hint={t('settings.proxy.autoSystemProxyHint')}
          >
            <Toggle
              aria-label={t('settings.proxy.autoSystemProxy')}
              checked={p.autoSystemProxy}
              onChange={v => set({ autoSystemProxy: v })}
            />
          </SettingsField>
        </>
      ),
    },
    {
      id: 'connection',
      category: 'proxy',
      title: t('settings.sections.connection'),
      description: t('settings.sections.connectionHint'),
      searchKeys: [
        'proxy.upstream',
        'proxy.upstreamHint',
        'proxy.upstreamAddr',
        'proxy.upstreamAuth',
        'proxy.upstreamAuthHint',
        'proxy.upstreamUsername',
        'proxy.upstreamPassword',
        'proxy.throttle',
        'proxy.throttleHint',
        'proxy.throttleEditRate',
      ],
      content: (
        <>
          <SettingsField
            label={t('settings.proxy.upstream')}
            hint={t('settings.proxy.upstreamHint')}
          >
            <Toggle
              aria-label={t('settings.proxy.upstream')}
              checked={p.upstream}
              onChange={v => set({ upstream: v })}
            />
          </SettingsField>
          {p.upstream && (
            <>
              <SettingsField label={t('settings.proxy.upstreamAddr')}>
                <TextInput
                  aria-label={t('settings.proxy.upstreamAddr')}
                  value={p.upstreamAddr}
                  onChange={e => set({ upstreamAddr: e.target.value })}
                  placeholder="http://host:port"
                  width={240}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.proxy.upstreamAuth')}
                hint={t('settings.proxy.upstreamAuthHint')}
              >
                <Toggle
                  aria-label={t('settings.proxy.upstreamAuth')}
                  checked={p.upstreamAuth}
                  onChange={changeUpstreamAuth}
                />
              </SettingsField>
              {p.upstreamAuth && (
                <>
                  <SettingsField label={t('settings.proxy.upstreamUsername')}>
                    <TextInput
                      aria-label={t('settings.proxy.upstreamUsername')}
                      value={p.upstreamUsername}
                      onChange={e => set({ upstreamUsername: e.target.value })}
                      autoComplete="username"
                      width={240}
                    />
                  </SettingsField>
                  <SettingsField label={t('settings.proxy.upstreamPassword')}>
                    <div className="settings-password">
                      <TextInput
                        aria-label={t('settings.proxy.upstreamPassword')}
                        type="password"
                        value={upstreamPassword}
                        onChange={e => {
                          passwordValue.current = e.target.value
                          passwordDirty.current = true
                          setUpstreamPassword(e.target.value)
                        }}
                        onBlur={commitUpstreamPassword}
                        autoComplete="new-password"
                        placeholder={upstreamPasswordSet ? '••••••••' : ''}
                        width={240}
                        className="pr-9"
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        className="absolute right-0.5 top-1/2 -translate-y-1/2"
                        icon={<Eraser className="h-3.5 w-3.5" />}
                        disabled={
                          !upstreamPasswordSet && upstreamPassword === ''
                        }
                        onMouseDown={e => e.preventDefault()}
                        onClick={clearUpstreamPassword}
                        title={t('settings.proxy.upstreamPasswordClear')}
                        aria-label={t('settings.proxy.upstreamPasswordClear')}
                      />
                    </div>
                  </SettingsField>
                </>
              )}
            </>
          )}
          <SettingsField
            label={t('settings.proxy.throttle')}
            hint={t('settings.proxy.throttleHint')}
          >
            {p.throttle && (
              <Button
                size="sm"
                icon={<Gauge className="h-3.5 w-3.5" />}
                onClick={() => setThrottleDialogOpen(true)}
                title={t('settings.proxy.throttleEditRate')}
              >
                <span className="font-mono">
                  {normalizeThrottleKiBps(p.throttleKiBps)} KiB/s
                </span>
              </Button>
            )}
            <Toggle
              aria-label={t('settings.proxy.throttle')}
              checked={p.throttle}
              onChange={changeThrottle}
            />
          </SettingsField>
        </>
      ),
    },
    {
      id: 'decrypt',
      category: 'decrypt',
      title: t('settings.sections.decryption'),
      searchKeys: [
        'decrypt.enableMitm',
        'decrypt.enableMitmHint',
        'decrypt.scope',
        'decrypt.scopeHint',
        'decrypt.scopeAll',
        'decrypt.scopeAllow',
        'decrypt.scopeDeny',
        'decrypt.allowlist',
        'decrypt.allowlistHint',
        'decrypt.hostListPlaceholder',
        'decrypt.denylist',
        'decrypt.denylistHint',
        'decrypt.rootCert',
        'decrypt.rootCertHint',
        'decrypt.manageCerts',
      ],
      content: (
        <>
          <SettingsField
            label={t('settings.decrypt.enableMitm')}
            hint={t('settings.decrypt.enableMitmHint')}
          >
            <Toggle
              aria-label={t('settings.decrypt.enableMitm')}
              checked={p.mitm}
              onChange={v => set({ mitm: v })}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.decrypt.scope')}
            hint={t('settings.decrypt.scopeHint')}
          >
            <Select
              aria-label={t('settings.decrypt.scope')}
              value={p.scope}
              disabled={!p.mitm}
              onChange={e => set({ scope: e.target.value as typeof p.scope })}
              options={[
                { value: 'all', label: t('settings.decrypt.scopeAll') },
                { value: 'allow', label: t('settings.decrypt.scopeAllow') },
                { value: 'deny', label: t('settings.decrypt.scopeDeny') },
              ]}
            />
          </SettingsField>
          {p.mitm && p.scope === 'allow' && (
            <HostListField
              label={t('settings.decrypt.allowlist')}
              hint={t('settings.decrypt.allowlistHint')}
              value={p.decryptAllow}
              onChange={v => set({ decryptAllow: v })}
              placeholder={t('settings.decrypt.hostListPlaceholder')}
            />
          )}
          {p.mitm && p.scope === 'deny' && (
            <HostListField
              label={t('settings.decrypt.denylist')}
              hint={t('settings.decrypt.denylistHint')}
              value={p.decryptDeny}
              onChange={v => set({ decryptDeny: v })}
              placeholder={t('settings.decrypt.hostListPlaceholder')}
            />
          )}
          <SettingsField
            label={t('settings.decrypt.rootCert')}
            hint={t('settings.decrypt.rootCertHint')}
          >
            <Button
              icon={<ShieldCheck className="h-3.5 w-3.5" />}
              onClick={() => requestMainNav('certs')}
            >
              {t('settings.decrypt.manageCerts')}
            </Button>
          </SettingsField>
        </>
      ),
    },
    {
      id: 'storage',
      category: 'storage',
      title: t('settings.sections.capture'),
      searchKeys: [
        'storage.maxFlows',
        'storage.maxFlowsHint',
        'storage.autoScroll',
        'storage.autoScrollHint',
        'storage.autoRecord',
        'storage.clearData',
        'storage.clearDataHint',
        'storage.clear',
      ],
      content: (
        <>
          <SettingsField
            label={t('settings.storage.maxFlows')}
            hint={t('settings.storage.maxFlowsHint')}
          >
            <Select
              aria-label={t('settings.storage.maxFlows')}
              value={String(p.maxFlows)}
              onChange={e => set({ maxFlows: Number(e.target.value) })}
              options={[
                { value: '1000', label: '1,000' },
                { value: '5000', label: '5,000' },
                { value: '10000', label: '10,000' },
                { value: '50000', label: '50,000' },
              ]}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.storage.autoScroll')}
            hint={t('settings.storage.autoScrollHint')}
          >
            <Toggle
              aria-label={t('settings.storage.autoScroll')}
              checked={p.follow}
              onChange={v => set({ follow: v })}
            />
          </SettingsField>
          <SettingsField label={t('settings.storage.autoRecord')}>
            <Toggle
              aria-label={t('settings.storage.autoRecord')}
              checked={p.autoRecord}
              onChange={v => set({ autoRecord: v })}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.storage.clearData')}
            hint={t('settings.storage.clearDataHint')}
          >
            <Button
              variant="danger"
              icon={<Eraser className="h-3.5 w-3.5" />}
              onClick={() => setConfirmClear(true)}
            >
              {t('settings.storage.clear')}
            </Button>
          </SettingsField>
        </>
      ),
    },
    {
      id: 'appearance',
      category: 'appearance',
      title: t('settings.sections.interface'),
      searchKeys: [
        'language',
        'appearance.theme',
        'appearance.themeDark',
        'appearance.themeLight',
        'appearance.accent',
        'appearance.accentHint',
        'appearance.accentSwatch',
        'appearance.accentCustom',
        'appearance.compact',
        'appearance.compactHint',
        'appearance.fontSize',
        'appearance.fontSmall',
        'appearance.fontMedium',
        'appearance.fontLarge',
      ],
      content: (
        <>
          <SettingsField label={t('settings.language')}>
            <Select
              aria-label={t('settings.language')}
              value={i18n.language}
              onChange={e => changeLang(e.target.value as Lang)}
              options={SUPPORTED_LANGS.map(l => ({
                value: l,
                label: LANG_LABELS[l],
              }))}
            />
          </SettingsField>
          <SettingsField label={t('settings.appearance.theme')}>
            <Select
              aria-label={t('settings.appearance.theme')}
              value={p.theme}
              onChange={e => set({ theme: e.target.value as ThemeMode })}
              options={[
                { value: 'dark', label: t('settings.appearance.themeDark') },
                { value: 'light', label: t('settings.appearance.themeLight') },
              ]}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.appearance.accent')}
            hint={t('settings.appearance.accentHint')}
          >
            <div className="flex flex-wrap items-center justify-end gap-2">
              {ACCENT_KEYS.map(key => (
                <button
                  key={key}
                  type="button"
                  onClick={() => set({ accent: key })}
                  title={t(`settings.appearance.accentNames.${key}`)}
                  aria-label={t('settings.appearance.accentSwatch', {
                    name: t(`settings.appearance.accentNames.${key}`),
                  })}
                  aria-pressed={p.accent === key}
                  className={cx(
                    'h-6 w-6 rounded-full border-2 transition',
                    p.accent === key
                      ? 'border-fg'
                      : 'border-transparent hover:border-fg-faint'
                  )}
                  style={{ backgroundColor: ACCENTS[key].swatch }}
                />
              ))}
              <label
                title={t('settings.appearance.accentCustom')}
                className={cx(
                  'relative h-6 w-6 cursor-pointer overflow-hidden rounded-full border-2 transition',
                  p.accent === 'custom'
                    ? 'border-fg'
                    : 'border-transparent hover:border-fg-faint'
                )}
                style={
                  p.accent === 'custom'
                    ? { backgroundColor: p.accentCustom }
                    : {
                        background:
                          'conic-gradient(from 0deg, #e5564d, #e5a24d, #6fbf4f, #4db5e5, #5a6be5, #c45fd6, #e5564d)',
                      }
                }
              >
                <input
                  type="color"
                  value={p.accentCustom}
                  onChange={e =>
                    set({ accent: 'custom', accentCustom: e.target.value })
                  }
                  className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
                  aria-label={t('settings.appearance.accentCustom')}
                />
              </label>
            </div>
          </SettingsField>
          <SettingsField
            label={t('settings.appearance.compact')}
            hint={t('settings.appearance.compactHint')}
          >
            <Toggle
              aria-label={t('settings.appearance.compact')}
              checked={p.compact}
              onChange={v => set({ compact: v })}
            />
          </SettingsField>
          <SettingsField label={t('settings.appearance.fontSize')}>
            <Select
              aria-label={t('settings.appearance.fontSize')}
              value={String(p.fontSize)}
              onChange={e =>
                set({ fontSize: Number(e.target.value) as FontSize })
              }
              options={[
                { value: '12', label: t('settings.appearance.fontSmall') },
                { value: '13', label: t('settings.appearance.fontMedium') },
                { value: '14', label: t('settings.appearance.fontLarge') },
              ]}
            />
          </SettingsField>
        </>
      ),
    },
    {
      id: 'about',
      category: 'about',
      title: t('settings.sections.updates'),
      searchKeys: [
        'about.version',
        'about.autoUpdate',
        'about.autoUpdateHint',
        'about.more',
        'about.moreHint',
        'about.aboutSniffy',
      ],
      content: (
        <>
          <SettingsField label={t('settings.about.version')}>
            <span className="font-mono text-[12px] text-fg-muted">
              Sniffy {version}
            </span>
          </SettingsField>
          <SettingsField
            label={t('settings.about.autoUpdate')}
            hint={t('settings.about.autoUpdateHint')}
          >
            <Toggle
              aria-label={t('settings.about.autoUpdate')}
              checked={update.state.autoCheck}
              onChange={update.actions.setAutoCheck}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.about.more')}
            hint={t('settings.about.moreHint')}
          >
            <Button onClick={() => openAboutWindow().catch(() => {})}>
              {t('settings.about.aboutSniffy')}
            </Button>
          </SettingsField>
        </>
      ),
    },
  ]

  return (
    <SettingsLayout sections={sections}>
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

      {throttleDialogOpen && (
        <ThrottleDialog
          initialValue={p.throttleKiBps}
          title={t('settings.proxy.throttleDialogTitle')}
          message={t('settings.proxy.throttleDialogMessage')}
          rateLabel={t('settings.proxy.throttleRate')}
          rangeLabel={t('settings.proxy.throttleRange', {
            min: MIN_THROTTLE_KIBPS,
            max: MAX_THROTTLE_KIBPS,
          })}
          invalidLabel={t('settings.proxy.throttleInvalid', {
            min: MIN_THROTTLE_KIBPS,
            max: MAX_THROTTLE_KIBPS,
          })}
          confirmLabel={
            p.throttle
              ? t('settings.proxy.throttleApply')
              : t('settings.proxy.throttleEnable')
          }
          cancelLabel={t('settings.proxy.throttleCancel')}
          onSubmit={enableThrottle}
          onClose={() => setThrottleDialogOpen(false)}
        />
      )}
    </SettingsLayout>
  )
}
