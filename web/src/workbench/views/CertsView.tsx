import { useState, type ReactNode } from 'react'
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
import { Button, Field, Panel, SegTabs, Toggle } from '../ui/controls'
import { cx } from '../ui/primitives'
import { PageShell } from './PageShell'

type Platform = 'windows' | 'macos' | 'ios' | 'android'

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
      '将 iPhone 的 WiFi 代理指向本机，使用 Safari 访问证书下载页。',
      '点击下载并允许加载配置描述文件，前往「设置 - 通用 - VPN 与设备管理」安装。',
      '安装完成后进入「设置 - 通用 - 关于本机 - 证书信任设置」。',
      '为 Sniffy Root CA 打开「针对根证书启用完全信任」开关。',
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

  const active = PLATFORM_STEPS[platform]

  return (
    <PageShell
      icon={ShieldCheck}
      title="证书"
      subtitle="根证书管理与安装引导"
      actions={
        <Button variant="primary" size="sm" icon={<Download className="h-3.5 w-3.5" />}>
          下载证书 (.crt)
        </Button>
      }
    >
      {/* ─────────── 根证书状态 ─────────── */}
      <Panel title="根证书状态" icon={<ShieldCheck className="h-4 w-4" />}>
        <Field label="状态" hint="用于动态签发站点证书以解密 HTTPS 流量">
          <StatusBadge tone="ok">已生成</StatusBadge>
          <StatusBadge tone="ok">已信任</StatusBadge>
        </Field>
        <Field label="指纹 SHA-256" hint="安装前请核对指纹是否一致">
          <span className="max-w-[280px] truncate font-mono text-2xs text-fg-muted">
            9F:2C:1A:7E:55:D4:0B:8F:3A:E1:6C:90:4D:2B:F8:11:A0:73:6E:5C:88:1D:42:B9:0E:77:3F:A6:C2:14:9B:50
          </span>
        </Field>
        <Field label="有效期" hint="证书的可用时间区间">
          <span className="font-mono text-2xs text-fg-muted">2026-01-01 ~ 2036-01-01</span>
        </Field>
        <Field label="序列号" hint="本地生成的唯一标识">
          <span className="font-mono text-2xs text-fg-muted">03:A1:6F:9D:42:E8:7C:5B</span>
        </Field>
        <Field label="证书管理" hint="重新生成将使旧证书失效，需在客户端重新安装">
          <Button size="sm" icon={<Download className="h-3.5 w-3.5" />}>
            下载证书 (.crt)
          </Button>
          <Button variant="danger" size="sm" icon={<RefreshCw className="h-3.5 w-3.5" />}>
            重新生成
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
          hint="开启后只解密「过滤规则」中配置的域名，其余流量直接转发不解密"
        >
          <Toggle checked={whitelistOnly} onChange={setWhitelistOnly} />
        </Field>
      </Panel>
    </PageShell>
  )
}
