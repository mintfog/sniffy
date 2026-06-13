import { useEffect, useMemo, useState, type ReactNode } from 'react'
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

const PLATFORM_STEPS: Record<Platform, { steps: string[]; icon: ReactNode }> = {
  windows: {
    icon: <Monitor className="h-3.5 w-3.5" />,
    steps: [
      '点击上方「下载证书 (.crt)」保存根证书文件到本地。',
      '双击下载的 .crt 文件，在弹出的对话框中点击「安装证书」。',
      '存储位置选择「本地计算机」，并将证书放入「受信任的根证书颁发机构」存储区。',
      '点击「完成」，重启浏览器后即可对 HTTPS 流量解密。',
    ],
  },
  macos: {
    icon: <Apple className="h-3.5 w-3.5" />,
    steps: [
      '下载证书并双击打开，系统会自动导入「钥匙串访问」。',
      '在「登录」或「系统」钥匙串中找到 Sniffy Root CA 证书并双击。',
      '展开「信任」一栏，将「使用此证书时」设置为「始终信任」。',
      '关闭窗口，输入系统密码确认更改即可生效。',
    ],
  },
  ios: {
    icon: <Smartphone className="h-3.5 w-3.5" />,
    steps: [
      '将 iPhone 的 WiFi 代理指向本机（主机填电脑局域网 IP，端口 8080）。',
      `用 Safari 扫描右侧二维码，或直接输入 ${CERT_URL}。`,
      '允许下载配置描述文件，前往「设置 - 通用 - VPN 与设备管理」点击安装。',
      '安装后进入「设置 - 通用 - 关于本机 - 证书信任设置」，为 Sniffy Root CA 打开完全信任。',
    ],
  },
  android: {
    icon: <Smartphone className="h-3.5 w-3.5" />,
    steps: [
      '将下载好的 .crt 证书文件传输到手机存储中。',
      '打开「设置 - 安全 - 加密与凭据 - 从存储设备安装证书」。',
      '选择证书文件并将用途指定为「CA 证书」，按提示完成安装。',
      '注意 Android 7+ 应用默认不信任用户 CA，调试时需配合可调试应用。',
    ],
  },
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
  const [platform, setPlatform] = useState<Platform>('windows')
  const [whitelistOnly, setWhitelistOnly] = useState(false)
  const [pem, setPem] = useState('')
  const [fingerprint, setFingerprint] = useState('')
  const [regenerating, setRegenerating] = useState(false)

  const active = PLATFORM_STEPS[platform]
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
    if (
      !window.confirm(
        '重新生成根证书将使旧证书立即失效，所有已安装旧证书的客户端都需要重新安装新证书。确定继续？',
      )
    )
      return
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
      title="证书"
      subtitle="根证书管理与安装引导"
      actions={
        <Button
          variant="primary"
          size="sm"
          icon={<Download className="h-3.5 w-3.5" />}
          onClick={downloadCert}
          disabled={!hasCert}
          title={hasCert ? '下载根证书 PEM' : '未连接到后端，暂无证书'}
        >
          下载证书 (.crt)
        </Button>
      }
    >
      {/* ─────────── 根证书状态 ─────────── */}
      <Panel title="根证书状态" icon={<ShieldCheck className="h-4 w-4" />}>
        <Field label="状态" hint="用于动态签发站点证书以解密 HTTPS 流量">
          {hasCert ? (
            <StatusBadge tone="ok">已生成</StatusBadge>
          ) : (
            <StatusBadge tone="warn">未获取</StatusBadge>
          )}
        </Field>
        <Field label="指纹 SHA-256" hint="安装前请核对指纹是否一致（取自真实根证书）">
          <span
            className="max-w-[280px] truncate font-mono text-2xs text-fg-muted"
            title={fingerprint || undefined}
          >
            {fingerprint || '连接后端后显示'}
          </span>
        </Field>
        <Field label="证书管理" hint="重新生成将使旧证书失效，需在客户端重新安装">
          <Button
            size="sm"
            icon={<Download className="h-3.5 w-3.5" />}
            onClick={downloadCert}
            disabled={!hasCert}
            title={hasCert ? '下载根证书 PEM' : '未连接到后端，暂无证书'}
          >
            下载证书 (.crt)
          </Button>
          <Button
            variant="danger"
            size="sm"
            icon={<RefreshCw className={cx('h-3.5 w-3.5', regenerating && 'animate-spin')} />}
            onClick={regenerate}
            disabled={!hasCert || regenerating}
            title={hasCert ? '重新生成根 CA（旧证书将失效）' : '未连接到后端'}
          >
            {regenerating ? '生成中…' : '重新生成'}
          </Button>
        </Field>
      </Panel>

      {/* ─────────── 安装引导 ─────────── */}
      <Panel
        title="安装引导"
        icon={<ListChecks className="h-4 w-4" />}
        right={<SegTabs<Platform> value={platform} onChange={setPlatform} options={PLATFORM_OPTIONS} />}
      >
        <div className="px-3 py-3">
          <div className="mb-2.5 flex items-center gap-1.5 text-2xs font-medium text-fg-muted">
            <span className="text-accent">{active.icon}</span>
            <span>{PLATFORM_OPTIONS.find((o) => o.key === platform)?.label} 安装步骤</span>
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
            {active.steps.map((step, i) => (
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
      <Panel title="解密提示" icon={<Info className="h-4 w-4" />}>
        <div className="px-3 py-2.5 text-2xs leading-relaxed text-fg-faint">
          安装并信任根证书后，Sniffy 会为访问的 HTTPS 站点动态签发证书以实现中间人解密。
          部分应用启用了证书绑定（Certificate Pinning），即使信任了根证书也可能无法解密，
          这属于应用层的安全保护，请仅在你拥有合法授权的设备与流量上进行调试。
        </div>
        <Field
          label="仅对白名单解密"
          hint="解密范围在「设置 → HTTPS 解密 → 解密范围」配置（后端接线后生效）"
        >
          <Toggle checked={whitelistOnly} onChange={setWhitelistOnly} disabled />
        </Field>
      </Panel>
    </PageShell>
  )
}
