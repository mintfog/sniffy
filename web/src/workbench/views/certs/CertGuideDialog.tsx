import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  FileBadge,
  ShieldPlus,
  X,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { detectPlatform } from '@/lib/platform'
import { Button } from '../../ui/controls'
import { cx } from '../../ui/primitives'
import { saveFile } from '../../lib/download'
import { copyText } from '../../lib/clipboard'
import { encodeQrText } from '../../lib/qrcode'
import { usePrefs } from '../../prefs'
import { GuideImage } from './GuideImage'
import { resolveGuideImage } from './guideImages'
import { hostCertPlatform, type CertPlatform } from './platform'
import { PlatformIcon } from './PlatformIcon'
import './cert-guide.css'

/** cert.sniffy 由代理拦截处理，设备需先配置代理才能访问。 */
const CERT_URL = 'http://cert.sniffy'

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]'

const PLATFORMS: { key: CertPlatform; label: string }[] = [
  { key: 'windows', label: 'Windows' },
  { key: 'macos', label: 'macOS' },
  { key: 'ios', label: 'iOS' },
  { key: 'android', label: 'Android' },
  { key: 'firefox', label: 'Firefox' },
]

const NETWORK_SECURITY_CONFIG = `<network-security-config>
  <debug-overrides>
    <trust-anchors>
      <certificates src="user" />
    </trust-anchors>
  </debug-overrides>
</network-security-config>`

const NETWORK_SECURITY_ATTRIBUTE =
  'android:networkSecurityConfig="@xml/network_security_config"'

async function fingerprintFromPem(pem: string): Promise<string> {
  const b64 = pem
    .replace(/-----(BEGIN|END) CERTIFICATE-----/g, '')
    .replace(/\s+/g, '')
  const der = Uint8Array.from(atob(b64), c => c.charCodeAt(0))
  const buf = await crypto.subtle.digest('SHA-256', der)
  return [...new Uint8Array(buf)]
    .map(b => b.toString(16).padStart(2, '0').toUpperCase())
    .join(':')
}

/** 步骤文案里 <m>菜单名</m> 高亮、<c>值</c> 等宽、› 作为弱化的路径分隔符；换行把设置路径和操作分成两行。 */
function rich(text: string): ReactNode[] {
  return text.split('\n').map((line, n) => (
    <span key={n} className="cg-line">
      {line.split(/(<m>.*?<\/m>|<c>.*?<\/c>|›)/).map((part, i) => {
        if (part === '›')
          return (
            <span key={i} className="cg-sep">
              ›
            </span>
          )
        if (part.startsWith('<m>')) return <b key={i}>{part.slice(3, -4)}</b>
        if (part.startsWith('<c>'))
          return <CopyCode key={i} value={part.slice(3, -4)} />
        return part
      })}
    </span>
  ))
}

function useCopied(value: string): [boolean, () => Promise<void>] {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (!(await copyText(value))) return
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1200)
  }
  return [copied, copy]
}

function CopyCode({ value }: { value: string }) {
  const { t } = useTranslation()
  const [copied, copy] = useCopied(value)
  const tip = copied
    ? t('certs.guideDialog.copied')
    : t('toolbox.common.clickToCopy')
  return (
    <button
      type="button"
      className={cx('cg-code-copy', copied && 'is-copied')}
      title={tip}
      aria-label={`${tip} ${value}`}
      onClick={copy}
    >
      <code>{value}</code>
    </button>
  )
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation()
  const [copied, copy] = useCopied(value)
  const tip = copied
    ? t('certs.guideDialog.copied')
    : t('certs.guide.copyValue', { label })
  return (
    <button
      type="button"
      className="cg-copy"
      aria-label={tip}
      title={tip}
      onClick={copy}
    >
      {copied ? <Check className="text-ok" /> : <Copy />}
    </button>
  )
}

function CertCard({
  fingerprint,
  loading,
}: {
  fingerprint: string
  loading: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className="cg-card cg-cert">
      <div className="cg-cert-id">
        <FileBadge
          className="cg-cert-icon"
          strokeWidth={1.5}
          aria-hidden="true"
        />
        <div className="cg-cert-name">
          <strong>Sniffy Root CA</strong>
          <code>sniffy-ca.crt</code>
        </div>
      </div>
      {fingerprint ? (
        <div className="cg-cert-fp">
          <span>
            {t('certs.status.fingerprintLabel')}
            <CopyButton
              value={fingerprint}
              label={t('certs.status.fingerprintLabel')}
            />
          </span>
          <code>{fingerprint.replace(/:/g, ':\u200b')}</code>
        </div>
      ) : (
        <p className="cg-cert-fp cg-cert-fp--empty">
          {loading ? t('certs.guideDialog.loading') : t('certs.noBackendTip')}
        </p>
      )}
      <p className="cg-cert-desc">{t('certs.guideDialog.rootDescription')}</p>
    </div>
  )
}

/** 代理参数必须如实反映 Sniffy 的认证开关：照「认证关闭」去填的设备只会一路拿到 407。 */
function ProxyCard({
  host,
  port,
  android,
}: {
  host: string
  port: string
  android: boolean
}) {
  const { t } = useTranslation()
  const auth = usePrefs(s => s.proxyAuth)
  const username = usePrefs(s => s.proxyUsername)
  const rows: [string, ReactNode][] = [
    [t('certs.guide.proxy.server'), host],
    [t('certs.guide.proxy.port'), port],
  ]
  return (
    <div className="cg-card cg-proxy">
      <dl>
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>
              <code>{value}</code>
              <CopyButton value={String(value)} label={label} />
            </dd>
          </div>
        ))}
        <div>
          <dt>{t('certs.guide.proxy.auth')}</dt>
          <dd className={cx(auth && android && 'text-warn')}>
            {auth
              ? android
                ? t('certs.guide.proxy.unsupported')
                : t('certs.guide.proxy.on')
              : t('certs.guide.proxy.off')}
          </dd>
        </div>
        {auth && !android && (
          <div>
            <dt>{t('certs.guide.proxy.username')}</dt>
            <dd>
              {username ? (
                <>
                  <code>{username}</code>
                  <CopyButton
                    value={username}
                    label={t('certs.guide.proxy.username')}
                  />
                </>
              ) : (
                <span className="text-warn">
                  {t('certs.guide.proxy.usernameUnset')}
                </span>
              )}
            </dd>
          </div>
        )}
      </dl>
      {auth && (
        <p className={cx('cg-proxy-note', android && 'cg-proxy-note--warn')}>
          {android
            ? t('certs.guide.proxy.androidNoAuth')
            : t('certs.guide.proxy.passwordHint')}
        </p>
      )}
    </div>
  )
}

/**
 * iOS 走 cert.sniffy（代理对该域名一律返回 .mobileconfig）；Android 走代理门户的 CRT
 * 端点，直连监听地址即可下载，不依赖手机已配好代理。
 */
function QrCard({ portal }: { portal?: { host: string; port: string } }) {
  const { t } = useTranslation()
  const android = !!portal
  const url = portal
    ? `http://${portal.host.includes(':') ? `[${portal.host}]` : portal.host}:${portal.port}/sniffy-ca.crt`
    : CERT_URL
  const addressLabel = t(
    android
      ? 'certs.guideDialog.certificateAddress'
      : 'certs.guide.profileAddress'
  )
  const path = useMemo(() => {
    const m = encodeQrText(url, 'M')
    let d = ''
    m.forEach((row, y) =>
      row.forEach((on, x) => on && (d += `M${x},${y}h1v1h-1z`))
    )
    return { d, n: m.length }
  }, [url])
  return (
    <div className="cg-qr-wrap">
      <div className="cg-qr" role="img" aria-label={addressLabel}>
        <svg
          viewBox={`-2 -2 ${path.n + 4} ${path.n + 4}`}
          shapeRendering="crispEdges"
        >
          <path d={path.d} />
        </svg>
      </div>
      <div className="cg-qr-caption">
        <p>
          {t(
            android
              ? 'certs.guideDialog.androidQrCaption'
              : 'certs.guideDialog.qrCaption'
          )}
        </p>
        <div>
          <code>{url}</code>
          <CopyButton value={url} label={addressLabel} />
        </div>
        {!android && <small>{t('certs.guideDialog.qrNeedsProxy')}</small>}
      </div>
    </div>
  )
}

function CodeCard({ name, code }: { name: string; code: string }) {
  return (
    <div className="cg-card cg-code">
      <div className="cg-code-head">
        <code>{name}</code>
        <CopyButton value={code} label={name} />
      </div>
      <pre>{code}</pre>
    </div>
  )
}

function AndroidInstallHelp() {
  const { t } = useTranslation()
  return (
    <div className="cg-help-group">
      <details className="cg-help">
        <summary>{t('certs.guideDialog.androidHelp.installTitle')}</summary>
        <div className="cg-help-body">
          <p className="cg-step-body">
            {rich(t('certs.guideDialog.androidHelp.findSettings'))}
          </p>
          <p className="cg-step-body">
            {rich(t('certs.guideDialog.androidHelp.certificateType'))}
          </p>
          <p className="cg-step-body">
            {rich(t('certs.guideDialog.androidHelp.findFile'))}
          </p>
        </div>
      </details>
      <details className="cg-help">
        <summary>
          {t('certs.guideDialog.androidHelp.appTitle')}
          <span className="cg-tag">{t('certs.guideDialog.optional')}</span>
        </summary>
        <div className="cg-help-body">
          <p className="cg-step-body">
            {rich(t('certs.guideDialog.androidHelp.appBody'))}
          </p>
          <CodeCard
            name="res/xml/network_security_config.xml"
            code={NETWORK_SECURITY_CONFIG}
          />
          <p className="cg-step-body">
            {rich(t('certs.guideDialog.androidHelp.manifestBody'))}
          </p>
          <CodeCard
            name="AndroidManifest.xml"
            code={NETWORK_SECURITY_ATTRIBUTE}
          />
        </div>
      </details>
    </div>
  )
}

interface Step {
  id: string
  /** key：最容易漏掉的一步；optional：只在特定场景需要。 */
  tag?: 'key' | 'optional'
  vars?: Record<string, string>
  /** 跟在步骤文案下方、需要用户照抄或操作的内容。 */
  detail?: ReactNode
  /** 为空时收起右侧展示区。 */
  media: ReactNode
}

interface CertGuideDialogProps {
  platform: CertPlatform
  onPlatformChange: (platform: CertPlatform) => void
  onClose: () => void
  onInstall: () => void
  installing: boolean
  /** 叠在上方的确认/结果对话框打开时为 true：Esc 交给它们，不连带关闭引导。 */
  covered: boolean
  proxyHost: string
  proxyPort: string
}

export function CertGuideDialog({
  platform,
  onPlatformChange,
  onClose,
  onInstall,
  installing,
  covered,
  proxyHost,
  proxyPort,
}: CertGuideDialogProps) {
  const { t, i18n } = useTranslation()
  const [pem, setPem] = useState('')
  const [fingerprint, setFingerprint] = useState('')
  const [loading, setLoading] = useState(true)
  const [active, setActive] = useState(0)
  const [manual, setManual] = useState(false)
  const dialogRef = useRef<HTMLDivElement>(null)
  const auth = usePrefs(s => s.proxyAuth)

  useEffect(() => {
    let alive = true
    Bridge.getCertificatePEM()
      .then(async p => {
        if (!alive || !p) return
        setPem(p)
        const fp = await fingerprintFromPem(p).catch(() => '')
        if (alive) setFingerprint(fp)
      })
      .catch(() => {
        /* 未连接后端：保持空，下载按钮禁用 */
      })
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [])

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    dialogRef.current
      ?.querySelector<HTMLElement>('[aria-selected="true"]')
      ?.focus()
    return () => previous?.focus?.()
  }, [])

  // 遮罩只挡鼠标，Tab 仍会走到背景，需要手动把焦点限制在弹窗内循环。
  // 被上层确认框覆盖时交给上层；放大预览是原生模态 <dialog>，由浏览器约束焦点。
  useEffect(() => {
    if (covered) return
    const onKey = (e: globalThis.KeyboardEvent) => {
      const root = dialogRef.current
      if (e.key !== 'Tab' || !root || root.querySelector('dialog[open]')) return
      const items = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
        el =>
          el.tabIndex >= 0 && !el.closest('details:not([open]) > :not(summary)')
      )
      if (items.length === 0) return
      const first = items[0]
      const last = items[items.length - 1]
      const focused = document.activeElement
      if (!root.contains(focused)) {
        e.preventDefault()
        ;(e.shiftKey ? last : first).focus()
      } else if (e.shiftKey && focused === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && focused === last) {
        e.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [covered])

  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape' && !covered) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [covered, onClose])

  useEffect(() => {
    setActive(0)
    setManual(false)
  }, [platform])

  const download = () => {
    if (pem) void saveFile(pem, 'sniffy-ca.crt')
  }
  const downloadStep = (id: string): Step => ({
    id,
    detail: (
      <div>
        <Button
          icon={<Download className="h-3.5 w-3.5" />}
          onClick={download}
          disabled={!pem}
        >
          {t('certs.guideDialog.downloadCert')}
        </Button>
      </div>
    ),
    media: <CertCard fingerprint={fingerprint} loading={loading} />,
  })
  const image = (id: string, precedingId?: string) => {
    const language = i18n.resolvedLanguage ?? i18n.language
    const ids = precedingId ? [precedingId, id] : [id]
    const images = ids.flatMap(imageId => {
      const url = resolveGuideImage(imageId, language)
      return url
        ? [{ url, alt: t(`certs.guideDialog.steps.${imageId}.title`) }]
        : []
    })
    if (images.length === 0) return null
    return (
      <GuideImage
        key={id}
        images={images}
        proxy={
          id === 'ios-proxy'
            ? { host: proxyHost, port: proxyPort, language }
            : undefined
        }
      />
    )
  }

  const steps: Record<CertPlatform, Step[]> = {
    windows: [
      downloadStep('windows-download'),
      { id: 'windows-open', media: image('windows-open') },
      { id: 'windows-store', tag: 'key', media: image('windows-store') },
    ],
    macos: [
      downloadStep('macos-download'),
      { id: 'macos-find', media: image('macos-find') },
      { id: 'macos-trust', tag: 'key', media: image('macos-trust') },
      { id: 'macos-auth', media: image('macos-auth') },
    ],
    ios: [
      {
        id: auth ? 'ios-proxy-auth' : 'ios-proxy',
        detail: <ProxyCard host={proxyHost} port={proxyPort} android={false} />,
        media: image('ios-proxy'),
      },
      { id: 'ios-download', media: <QrCard /> },
      { id: 'ios-install', media: image('ios-install', 'ios-install-list') },
      { id: 'ios-trust', tag: 'key', media: image('ios-trust') },
    ],
    android: [
      {
        id: 'android-proxy',
        detail: (
          <>
            <ProxyCard host={proxyHost} port={proxyPort} android />
            <details className="cg-help">
              <summary>{t('certs.guideDialog.androidHelp.proxyTitle')}</summary>
              <div className="cg-help-body">
                <p className="cg-step-body">
                  {rich(t('certs.guideDialog.androidHelp.proxyBody'))}
                </p>
              </div>
            </details>
          </>
        ),
        media: null,
      },
      {
        id: 'android-download',
        media: <QrCard portal={{ host: proxyHost, port: proxyPort }} />,
      },
      {
        id: 'android-install',
        tag: 'key',
        detail: <AndroidInstallHelp />,
        media: null,
      },
    ],
    firefox: [
      downloadStep('firefox-download'),
      {
        id: 'firefox-manager',
        detail: (
          <p className="cg-step-note">
            {rich(t('certs.guideDialog.steps.firefox-manager.legacy'))}
          </p>
        ),
        media: image('firefox-manager'),
      },
      { id: 'firefox-import', tag: 'key', media: image('firefox-import') },
      {
        id: 'firefox-proxy',
        vars: { host: proxyHost, port: proxyPort },
        media: image('firefox-proxy'),
      },
    ],
  }
  const list = steps[platform]
  const index = Math.min(active, list.length - 1)
  const current = list[index]
  const last = index === list.length - 1
  const localInstall =
    platform === hostCertPlatform() && detectPlatform() !== 'linux'
  const showSteps = !localInstall || manual

  const onTabKey = (e: KeyboardEvent, index: number) => {
    const delta = { ArrowRight: 1, ArrowLeft: -1 }[e.key]
    if (!delta) return
    e.preventDefault()
    const next =
      PLATFORMS[(index + delta + PLATFORMS.length) % PLATFORMS.length].key
    onPlatformChange(next)
    dialogRef.current?.querySelector<HTMLElement>(`#cg-tab-${next}`)?.focus()
  }

  const goTo = (next: number) => {
    setActive(next)
    dialogRef.current?.querySelector<HTMLElement>(`#cg-step-${next}`)?.focus()
  }

  const forward = last ? (
    <Button variant="primary" onClick={onClose}>
      {t('certs.guideDialog.done')}
    </Button>
  ) : (
    <Button variant="primary" onClick={() => goTo(index + 1)}>
      {t('certs.guideDialog.next')}
      <ChevronRight className="h-3.5 w-3.5" />
    </Button>
  )

  const onStepKey = (e: KeyboardEvent, index: number) => {
    const delta = { ArrowDown: 1, ArrowUp: -1 }[e.key]
    if (!delta) return
    e.preventDefault()
    goTo(Math.max(0, Math.min(list.length - 1, index + delta)))
  }

  return (
    <div className="cg-scrim" role="presentation">
      <div
        ref={dialogRef}
        className="cg-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="cg-title"
      >
        <header className="cg-head">
          <h2 id="cg-title">{t('certs.guideDialog.title')}</h2>
          <div
            className="cg-tabs"
            role="tablist"
            aria-label={t('certs.guideDialog.platforms')}
          >
            {PLATFORMS.map((p, i) => (
              <button
                key={p.key}
                id={`cg-tab-${p.key}`}
                type="button"
                role="tab"
                aria-selected={platform === p.key}
                aria-controls="cg-panel"
                tabIndex={platform === p.key ? 0 : -1}
                onClick={() => onPlatformChange(p.key)}
                onKeyDown={e => onTabKey(e, i)}
              >
                <PlatformIcon platform={p.key} />
                {p.label}
              </button>
            ))}
          </div>
          <button
            type="button"
            className="cg-close"
            aria-label={t('certs.guideDialog.close')}
            onClick={onClose}
          >
            <X />
          </button>
        </header>

        {!showSteps ? (
          <div
            id="cg-panel"
            className="cg-recommend"
            role="tabpanel"
            aria-labelledby={`cg-tab-${platform}`}
          >
            <div className="cg-cert-id">
              <FileBadge
                className="cg-cert-icon"
                strokeWidth={1.5}
                aria-hidden="true"
              />
              <div className="cg-cert-name">
                <strong>Sniffy Root CA</strong>
                <code>
                  {fingerprint
                    ? `SHA-256 ${fingerprint.slice(0, 23)}…`
                    : t('certs.noBackendTip')}
                </code>
              </div>
            </div>
            <p>{t('certs.guideDialog.localInstall')}</p>
            <Button
              variant="primary"
              icon={<ShieldPlus className="h-3.5 w-3.5" />}
              onClick={onInstall}
              disabled={!pem || installing}
            >
              {installing ? t('certs.installing') : t('certs.installToSystem')}
            </Button>
            <p className="cg-recommend-note">
              {t(`certs.guideDialog.hint.${platform}`)}
            </p>
            <button
              type="button"
              className="cg-link"
              onClick={() => setManual(true)}
            >
              {t('certs.guideDialog.manualInstall')}
              <ChevronRight />
            </button>
          </div>
        ) : (
          <div
            id="cg-panel"
            className={cx('cg-body', !current.media && 'cg-body--solo')}
            role="tabpanel"
            aria-labelledby={`cg-tab-${platform}`}
          >
            <section className="cg-steps">
              <p className="cg-prereq">
                {t(`certs.guideDialog.hint.${platform}`)}
              </p>
              <ol>
                {list.map((step, i) => (
                  <li key={step.id}>
                    <button
                      id={`cg-step-${i}`}
                      type="button"
                      className="cg-step"
                      aria-current={step === current ? 'step' : undefined}
                      aria-expanded={step === current}
                      tabIndex={step === current ? 0 : -1}
                      onClick={() => setActive(i)}
                      onKeyDown={e => onStepKey(e, i)}
                    >
                      <span className="cg-step-n" aria-hidden="true">
                        {i + 1}
                      </span>
                      <span className="cg-step-title">
                        {t(`certs.guideDialog.steps.${step.id}.title`)}
                        {step.tag && (
                          <span className={cx('cg-tag', `cg-tag--${step.tag}`)}>
                            {t(
                              step.tag === 'key'
                                ? 'certs.guideDialog.keyStep'
                                : 'certs.guideDialog.optional'
                            )}
                          </span>
                        )}
                      </span>
                    </button>
                    {step === current && (
                      <div className="cg-step-more">
                        <p className="cg-step-body">
                          {rich(
                            t(
                              `certs.guideDialog.steps.${step.id}.body`,
                              step.vars
                            )
                          )}
                        </p>
                        {step.detail}
                        <div className="cg-step-nav">
                          {forward}
                          {index > 0 && (
                            <Button
                              variant="ghost"
                              icon={<ChevronLeft className="h-3.5 w-3.5" />}
                              onClick={() => goTo(index - 1)}
                            >
                              {t('certs.guideDialog.previous')}
                            </Button>
                          )}
                        </div>
                      </div>
                    )}
                  </li>
                ))}
              </ol>
            </section>
            {current.media && (
              <section className="cg-stage" aria-live="polite">
                {current.media}
              </section>
            )}
          </div>
        )}

        {showSteps && (
          <footer className="cg-foot">
            <span className="cg-progress">
              {t('certs.guideDialog.progress', {
                current: index + 1,
                total: list.length,
              })}
            </span>
            <Button
              icon={<ChevronLeft className="h-3.5 w-3.5" />}
              onClick={() => goTo(index - 1)}
              disabled={index === 0}
            >
              {t('certs.guideDialog.previous')}
            </Button>
            {forward}
          </footer>
        )}
      </div>
    </div>
  )
}
