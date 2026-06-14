import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Apple,
  Download,
  Info,
  ListChecks,
  Monitor,
  RefreshCw,
  ShieldCheck,
  Smartphone,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, Field, Panel, SegTabs, Toggle } from '../ui/controls'
import { cx } from '../ui/primitives'
import { saveFile } from '../lib/download'
import { encodeQrText } from '../lib/qrcode'
import { PageShell } from './PageShell'

/** 从 PEM 提取 DER 字节，计算 SHA-256 指纹（冒号分隔大写十六进制）。 */
async function fingerprintFromPem(pem: string): Promise<string> {
  const b64 = pem
    .replace(/-----BEGIN CERTIFICATE-----/g, '')
    .replace(/-----END CERTIFICATE-----/g, '')
    .replace(/\s+/g, '')
  const der = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  const buf = await crypto.subtle.digest('SHA-256', der)
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0').toUpperCase()).join(':')
}

type Platform = 'windows' | 'macos' | 'ios' | 'android'

/** iOS 证书安装的魔法域名：手机设好代理后 Safari 访问此地址，由代理直接返回 .mobileconfig（见后端 capture/processors/http）。 */
const CERT_HOST = 'cert.sniffy'
const CERT_URL = `http://${CERT_HOST}`

const PLATFORM_OPTIONS: { key: Platform; label: ReactNode }[] = [
  { key: 'windows', label: 'Windows' },
  { key: 'macos', label: 'macOS' },
  { key: 'ios', label: 'iOS' },
  { key: 'android', label: 'Android' },
]

/** 各平台安装引导的图标（步骤文案随语言变化，移入组件内用 t 求值）。 */
const PLATFORM_ICONS: Record<Platform, ReactNode> = {
  windows: <Monitor className="h-3.5 w-3.5" />,
  macos: <Apple className="h-3.5 w-3.5" />,
  ios: <Smartphone className="h-3.5 w-3.5" />,
  android: <Smartphone className="h-3.5 w-3.5" />,
}

function StatusBadge({ tone, children }: { tone: 'ok' | 'warn'; children: ReactNode }) {
  return (
    <span
      className={cx(
        'inline-flex items-center rounded px-1.5 py-0.5 text-2xs font-medium',
        tone === 'ok' ? 'bg-ok/15 text-ok' : 'bg-warn/15 text-warn',
      )}
    >
      {children}
    </span>
  )
}

export function CertsView() {
  const { t } = useTranslation()
  const [platform, setPlatform] = useState<Platform>('windows')
  const [whitelistOnly, setWhitelistOnly] = useState(false)
  const [pem, setPem] = useState('')
  const [fingerprint, setFingerprint] = useState('')
  const [regenerating, setRegenerating] = useState(false)

  const platformSteps = useMemo<Record<Platform, string[]>>(
    () => ({
      windows: [
        t('certs.steps.windows.1'),
        t('certs.steps.windows.2'),
        t('certs.steps.windows.3'),
        t('certs.steps.windows.4'),
      ],
      macos: [
        t('certs.steps.macos.1'),
        t('certs.steps.macos.2'),
        t('certs.steps.macos.3'),
        t('certs.steps.macos.4'),
      ],
      ios: [
        t('certs.steps.ios.1'),
        t('certs.steps.ios.2', { url: CERT_URL }),
        t('certs.steps.ios.3'),
        t('certs.steps.ios.4'),
      ],
      android: [
        t('certs.steps.android.1'),
        t('certs.steps.android.2'),
        t('certs.steps.android.3'),
        t('certs.steps.android.4'),
      ],
    }),
    [t],
  )

  const activeSteps = platformSteps[platform]
  const activeIcon = PLATFORM_ICONS[platform]
  const hasCert = !!pem

  const iosQrSvg = useMemo(() => {
    try {
      const m = encodeQrText(CERT_URL, 'M')
      const n = m.length
      const border = 4
      const dim = n + border * 2
      let path = ''
      for (let y = 0; y < n; y++) {
        for (let x = 0; x < n; x++) {
          if (m[y][x]) path += `M${x + border},${y + border}h1v1h-1z`
        }
      }
      return `<svg xmlns="http://www.w3.org/2000/svg" width="100%" height="100%" viewBox="0 0 ${dim} ${dim}" shape-rendering="crispEdges" style="display:block"><rect width="${dim}" height="${dim}" fill="#ffffff"/><path d="${path}" fill="#000000"/></svg>`
    } catch {
      return ''
    }
  }, [])

  const regenerate = async () => {
    if (!window.confirm(t('certs.regenerateConfirm'))) return
    setRegenerating(true)
    try {
      const np = await Bridge.regenerateCA()
      if (np) {
        setPem(np)
        try {
          setFingerprint(await fingerprintFromPem(np))
        } catch {
          /* ignore */
        }
      }
    } catch {
      /* ignore */
    } finally {
      setRegenerating(false)
    }
  }

  // 拉取真实 CA PEM（Bridge 已暴露），并据此计算真实 SHA-256 指纹。
  useEffect(() => {
    let alive = true
    Bridge.getCertificatePEM()
      .then(async (p) => {
        if (!alive || !p) return
        setPem(p)
        try {
          setFingerprint(await fingerprintFromPem(p))
        } catch {
          /* ignore */
        }
      })
      .catch(() => {
        /* 非 Wails / 未连接：保持空，按钮禁用 */
      })
    return () => {
      alive = false
    }
  }, [])

  const downloadCert = () => {
    if (pem) void saveFile(pem, 'sniffy-ca.crt')
  }

  return (
    <PageShell
      icon={ShieldCheck}
      title={t('certs.title')}
      subtitle={t('certs.subtitle')}
      actions={
        <Button
          variant="primary"
          size="sm"
          icon={<Download className="h-3.5 w-3.5" />}
          onClick={downloadCert}
          disabled={!hasCert}
          title={hasCert ? t('certs.downloadPemTip') : t('certs.noBackendTip')}
        >
          {t('certs.downloadCert')}
        </Button>
      }
    >
      {/* ─────────── 根证书状态 ─────────── */}
      <Panel title={t('certs.status.title')} icon={<ShieldCheck className="h-4 w-4" />}>
        <Field label={t('certs.status.stateLabel')} hint={t('certs.status.stateHint')}>
          {hasCert ? (
            <StatusBadge tone="ok">{t('certs.status.generated')}</StatusBadge>
          ) : (
            <StatusBadge tone="warn">{t('certs.status.notLoaded')}</StatusBadge>
          )}
        </Field>
        <Field label={t('certs.status.fingerprintLabel')} hint={t('certs.status.fingerprintHint')}>
          <span
            className="max-w-[280px] truncate font-mono text-2xs text-fg-muted"
            title={fingerprint || undefined}
          >
            {fingerprint || t('certs.status.fingerprintPlaceholder')}
          </span>
        </Field>
        <Field label={t('certs.status.manageLabel')} hint={t('certs.status.manageHint')}>
          <Button
            size="sm"
            icon={<Download className="h-3.5 w-3.5" />}
            onClick={downloadCert}
            disabled={!hasCert}
            title={hasCert ? t('certs.downloadPemTip') : t('certs.noBackendTip')}
          >
            {t('certs.downloadCert')}
          </Button>
          <Button
            variant="danger"
            size="sm"
            icon={<RefreshCw className={cx('h-3.5 w-3.5', regenerating && 'animate-spin')} />}
            onClick={regenerate}
            disabled={!hasCert || regenerating}
            title={hasCert ? t('certs.regenerateTip') : t('certs.noBackendShortTip')}
          >
            {regenerating ? t('certs.regenerating') : t('certs.regenerate')}
          </Button>
        </Field>
      </Panel>

      {/* ─────────── 安装引导 ─────────── */}
      <Panel
        title={t('certs.guide.title')}
        icon={<ListChecks className="h-4 w-4" />}
        right={<SegTabs<Platform> value={platform} onChange={setPlatform} options={PLATFORM_OPTIONS} />}
      >
        <div className="px-3 py-3">
          <div className="mb-2.5 flex items-center gap-1.5 text-2xs font-medium text-fg-muted">
            <span className="text-accent">{activeIcon}</span>
            <span>
              {t('certs.guide.stepsHeading', {
                platform: PLATFORM_OPTIONS.find((o) => o.key === platform)?.label,
              })}
            </span>
          </div>
          {platform === 'ios' && iosQrSvg && (
            <div className="mb-4 flex flex-col items-center gap-1.5">
              <div
                className="rounded border border-line bg-white p-2"
                style={{ width: 148, height: 148 }}
                // eslint-disable-next-line react/no-danger
                dangerouslySetInnerHTML={{ __html: iosQrSvg }}
              />
              <span className="font-mono text-2xs text-fg-muted select-all">{CERT_URL}</span>
            </div>
          )}
          <ol className="flex flex-col gap-2.5">
            {activeSteps.map((step, i) => (
              <li key={i} className="flex items-start gap-2.5">
                <span className="mt-px inline-flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-full bg-accent/15 text-2xs font-semibold text-accent">
                  {i + 1}
                </span>
                <span className="text-[12.5px] leading-relaxed text-fg">{step}</span>
              </li>
            ))}
          </ol>
        </div>
      </Panel>

      {/* ─────────── 解密提示 ─────────── */}
      <Panel title={t('certs.decrypt.title')} icon={<Info className="h-4 w-4" />}>
        <div className="px-3 py-2.5 text-2xs leading-relaxed text-fg-faint">
          {t('certs.decrypt.notice')}
        </div>
        <Field label={t('certs.decrypt.whitelistLabel')} hint={t('certs.decrypt.whitelistHint')}>
          <Toggle checked={whitelistOnly} onChange={setWhitelistOnly} disabled />
        </Field>
      </Panel>
    </PageShell>
  )
}
