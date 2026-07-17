import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Apple,
  Download,
  FileKey2,
  Info,
  ListChecks,
  Monitor,
  Plus,
  RefreshCw,
  ShieldCheck,
  ShieldPlus,
  Smartphone,
  Trash2,
} from 'lucide-react'
import { Bridge, type ServerCert } from '@/lib/bridge'
import { Button, Field, Panel, SegTabs } from '../ui/controls'
import { cx } from '../ui/primitives'
import { saveFile } from '../lib/download'
import { encodeQrText } from '../lib/qrcode'
import { PageShell } from './PageShell'
import { ConfirmDialog } from '../ui/ConfirmDialog'

interface CertsViewProps {
  /** 触发「安装到本机」流程;状态与对话框由父组件持有。 */
  onInstall: () => void
  /** 安装进行中;按钮据此置 busy/disabled。 */
  installing: boolean
}

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

/** 一个整行占位的 PEM 文本域,风格与设置页的主机清单一致。 */
function PemField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  return (
    <div className="px-3 py-2">
      <div className="mb-1 text-[12.5px] text-fg">{label}</div>
      <textarea
        spellCheck={false}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={4}
        className="w-full resize-y rounded-wb border border-line bg-inset px-2 py-1.5 font-mono text-[11px] leading-relaxed text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface"
      />
    </div>
  )
}

/** 导入服务端证书面板:应对固定证书场景——用真实证书+私钥替代 MITM 现签的伪造证书。 */
function ImportedServerCerts() {
  const { t } = useTranslation()
  const [certs, setCerts] = useState<ServerCert[]>([])
  const [certPem, setCertPem] = useState('')
  const [keyPem, setKeyPem] = useState('')
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState('')
  const [confirmDelete, setConfirmDelete] = useState<ServerCert | null>(null)

  const refresh = () => {
    Bridge.getServerCerts()
      .then((list) => setCerts(list ?? []))
      .catch(() => {
        /* 非 Wails / 未连接:保持空 */
      })
  }
  useEffect(refresh, [])

  const doImport = async () => {
    if (!certPem.trim() || !keyPem.trim()) {
      setError(t('certs.serverCert.fieldsRequired'))
      return
    }
    setImporting(true)
    setError('')
    try {
      await Bridge.importServerCert(certPem, keyPem)
      setCertPem('')
      setKeyPem('')
      refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setImporting(false)
    }
  }

  const doDelete = (id: string) => {
    Bridge.deleteServerCert(id)
      .catch(() => {})
      .finally(() => {
        setConfirmDelete(null)
        refresh()
      })
  }

  return (
    <Panel title={t('certs.serverCert.title')} icon={<FileKey2 className="h-4 w-4" />}>
      <div className="px-3 py-2.5 text-2xs leading-relaxed text-fg-faint">{t('certs.serverCert.desc')}</div>

      {certs.length > 0 && (
        <div className="flex flex-col divide-y divide-line border-y border-line">
          {certs.map((c) => {
            const expired = !!c.notAfter && new Date(c.notAfter).getTime() < Date.now()
            return (
            <div key={c.id} className="flex items-center gap-3 px-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <div className="truncate font-mono text-[12.5px] text-fg">{(c.hosts ?? []).join(', ') || '—'}</div>
                  {expired && <StatusBadge tone="warn">{t('certs.serverCert.expired')}</StatusBadge>}
                </div>
                <div className="truncate text-2xs text-fg-faint">
                  {c.subject || '—'}
                  {c.notAfter && (
                    <>
                      {' · '}
                      {t('certs.serverCert.expires')} {c.notAfter.slice(0, 10)}
                    </>
                  )}
                </div>
              </div>
              <Button
                variant="danger"
                size="sm"
                icon={<Trash2 className="h-3.5 w-3.5" />}
                onClick={() => setConfirmDelete(c)}
              >
                {t('certs.serverCert.deleteBtn')}
              </Button>
            </div>
            )
          })}
        </div>
      )}
      {certs.length === 0 && (
        <div className="px-3 py-2 text-2xs text-fg-faint">{t('certs.serverCert.empty')}</div>
      )}

      <PemField
        label={t('certs.serverCert.certLabel')}
        value={certPem}
        onChange={setCertPem}
        placeholder="-----BEGIN CERTIFICATE-----"
      />
      <PemField
        label={t('certs.serverCert.keyLabel')}
        value={keyPem}
        onChange={setKeyPem}
        placeholder="-----BEGIN PRIVATE KEY-----"
      />
      {error && <div className="px-3 pb-1 text-2xs leading-relaxed text-danger">{error}</div>}
      <div className="px-3 py-2.5">
        <Button
          variant="primary"
          size="sm"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={doImport}
          disabled={importing}
        >
          {importing ? t('certs.serverCert.importing') : t('certs.serverCert.importBtn')}
        </Button>
      </div>

      {confirmDelete && (
        <ConfirmDialog
          title={t('certs.serverCert.deleteTitle')}
          message={t('certs.serverCert.deleteConfirm', { host: (confirmDelete.hosts ?? []).join(', ') })}
          confirmLabel={t('certs.serverCert.deleteBtn')}
          cancelLabel={t('certs.cancel')}
          tone="danger"
          onConfirm={() => doDelete(confirmDelete.id)}
          onClose={() => setConfirmDelete(null)}
        />
      )}
    </Panel>
  )
}

export function CertsView({ onInstall, installing }: CertsViewProps) {
  const { t } = useTranslation()
  const [platform, setPlatform] = useState<Platform>('windows')
  const [pem, setPem] = useState('')
  const [fingerprint, setFingerprint] = useState('')
  const [regenerating, setRegenerating] = useState(false)
  const [confirmRegen, setConfirmRegen] = useState(false)

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

  const doRegenerate = async () => {
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
      setConfirmRegen(false)
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
            variant="primary"
            size="sm"
            icon={<ShieldPlus className="h-3.5 w-3.5" />}
            onClick={onInstall}
            disabled={!hasCert || installing}
            title={hasCert ? t('certs.installToSystemTip') : t('certs.noBackendShortTip')}
          >
            {installing ? t('certs.installing') : t('certs.installToSystem')}
          </Button>
          <Button
            variant="danger"
            size="sm"
            icon={<RefreshCw className={cx('h-3.5 w-3.5', regenerating && 'animate-spin')} />}
            onClick={() => setConfirmRegen(true)}
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

      {/* ─────────── 导入服务端证书(应对固定证书) ─────────── */}
      <ImportedServerCerts />

      {/* ─────────── 解密提示 ─────────── */}
      <Panel title={t('certs.decrypt.title')} icon={<Info className="h-4 w-4" />}>
        <div className="px-3 py-2.5 text-2xs leading-relaxed text-fg-faint">
          {t('certs.decrypt.notice')}
        </div>
        <div className="px-3 py-2.5 text-2xs leading-relaxed text-fg-faint">
          {t('certs.decrypt.whitelistHint')}
        </div>
      </Panel>

      {confirmRegen && (
        <ConfirmDialog
          title={t('certs.regenerateTitle')}
          message={t('certs.regenerateConfirm')}
          confirmLabel={t('certs.regenerate')}
          cancelLabel={t('certs.cancel')}
          tone="danger"
          busy={regenerating}
          onConfirm={doRegenerate}
          onClose={() => !regenerating && setConfirmRegen(false)}
        />
      )}
    </PageShell>
  )
}
