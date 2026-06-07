import { useState } from 'react'
import {
  Database,
  Eraser,
  Globe,
  Info,
  Network,
  Palette,
  RefreshCw,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react'
import type { ThemeMode } from '../theme/useTheme'
import { Button, Field, Panel, Select, TextInput, Toggle } from '../ui/controls'
import { cx } from '../ui/primitives'
import { PageShell } from './PageShell'

const ACCENTS = [
  { key: 'iris', color: '#7C6CF5' },
  { key: 'sky', color: '#3B82F6' },
  { key: 'teal', color: '#14B8A6' },
  { key: 'amber', color: '#F5A623' },
  { key: 'rose', color: '#F43F5E' },
]

export function SettingsView({ mode, setMode }: { mode: ThemeMode; setMode: (m: ThemeMode) => void }) {
  // 本期为完整 UI；除主题外多为本地状态占位，后续接后端 /api/config。
  const [host, setHost] = useState('127.0.0.1')
  const [port, setPort] = useState('8080')
  const [systemProxy, setSystemProxy] = useState(true)
  const [upstream, setUpstream] = useState(false)
  const [upstreamAddr, setUpstreamAddr] = useState('')
  const [mitm, setMitm] = useState(true)
  const [scope, setScope] = useState('all')
  const [accent, setAccent] = useState('iris')
  const [compact, setCompact] = useState(false)
  const [fontSize, setFontSize] = useState('13')
  const [maxFlows, setMaxFlows] = useState('10000')
  const [autoScroll, setAutoScroll] = useState(true)
  const [autoRecord, setAutoRecord] = useState(true)

  return (
    <PageShell
      icon={SlidersHorizontal}
      title="设置"
      subtitle="代理 · 解密 · 外观 · 存储"
      actions={<Button variant="primary">保存更改</Button>}
    >
      <Panel title="代理" icon={<Network className="h-4 w-4" />}>
        <Field label="监听地址" hint="代理服务器绑定的网卡地址">
          <TextInput value={host} onChange={(e) => setHost(e.target.value)} width={160} />
        </Field>
        <Field label="监听端口">
          <TextInput value={port} onChange={(e) => setPort(e.target.value)} width={100} />
        </Field>
        <Field label="设为系统代理" hint="启动时自动接管系统 HTTP/HTTPS 代理设置">
          <Toggle checked={systemProxy} onChange={setSystemProxy} />
        </Field>
        <Field label="上游代理" hint="将流量转发到二级代理（如公司网关）">
          <Toggle checked={upstream} onChange={setUpstream} />
        </Field>
        {upstream && (
          <Field label="上游代理地址">
            <TextInput
              value={upstreamAddr}
              onChange={(e) => setUpstreamAddr(e.target.value)}
              placeholder="http://host:port"
              width={240}
            />
          </Field>
        )}
      </Panel>

      <Panel title="HTTPS 解密" icon={<ShieldCheck className="h-4 w-4" />}>
        <Field label="启用 HTTPS MITM" hint="使用动态证书解密 HTTPS 流量">
          <Toggle checked={mitm} onChange={setMitm} />
        </Field>
        <Field label="解密范围">
          <Select
            value={scope}
            onChange={(e) => setScope(e.target.value)}
            options={[
              { value: 'all', label: '全部主机' },
              { value: 'allow', label: '仅白名单' },
              { value: 'deny', label: '黑名单除外' },
            ]}
          />
        </Field>
        <Field label="根证书" hint="安装到系统/设备信任库以解密 HTTPS">
          <Button icon={<ShieldCheck className="h-3.5 w-3.5" />}>管理证书</Button>
        </Field>
      </Panel>

      <Panel title="外观" icon={<Palette className="h-4 w-4" />}>
        <Field label="主题">
          <Select
            value={mode}
            onChange={(e) => setMode(e.target.value as ThemeMode)}
            options={[
              { value: 'dark', label: '深色' },
              { value: 'light', label: '亮色' },
            ]}
          />
        </Field>
        <Field label="强调色" hint="界面主色（实时预览，后续接入主题变量）">
          <div className="flex items-center gap-1.5">
            {ACCENTS.map((a) => (
              <button
                key={a.key}
                type="button"
                onClick={() => setAccent(a.key)}
                title={a.key}
                className={cx(
                  'h-5 w-5 rounded-full border-2 transition',
                  accent === a.key ? 'border-fg' : 'border-transparent',
                )}
                style={{ backgroundColor: a.color }}
              />
            ))}
          </div>
        </Field>
        <Field label="紧凑模式" hint="减小行高与间距以显示更多内容">
          <Toggle checked={compact} onChange={setCompact} />
        </Field>
        <Field label="字体大小">
          <Select
            value={fontSize}
            onChange={(e) => setFontSize(e.target.value)}
            options={[
              { value: '12', label: '小 (12px)' },
              { value: '13', label: '中 (13px)' },
              { value: '14', label: '大 (14px)' },
            ]}
          />
        </Field>
      </Panel>

      <Panel title="抓包与存储" icon={<Database className="h-4 w-4" />}>
        <Field label="最大保留 Flow 数" hint="超出后自动丢弃最旧记录">
          <Select
            value={maxFlows}
            onChange={(e) => setMaxFlows(e.target.value)}
            options={[
              { value: '1000', label: '1,000' },
              { value: '5000', label: '5,000' },
              { value: '10000', label: '10,000' },
              { value: '50000', label: '50,000' },
            ]}
          />
        </Field>
        <Field label="自动滚动到最新">
          <Toggle checked={autoScroll} onChange={setAutoScroll} />
        </Field>
        <Field label="启动时自动录制">
          <Toggle checked={autoRecord} onChange={setAutoRecord} />
        </Field>
        <Field label="清空所有数据" hint="删除全部已捕获的流量记录">
          <Button variant="danger" icon={<Eraser className="h-3.5 w-3.5" />}>
            清空
          </Button>
        </Field>
      </Panel>

      <Panel title="关于" icon={<Info className="h-4 w-4" />}>
        <Field label="版本">
          <span className="font-mono text-[12px] text-fg-muted">Sniffy 0.1.0</span>
        </Field>
        <Field label="内核">
          <span className="font-mono text-[12px] text-fg-muted">Go · net/http MITM</span>
        </Field>
        <Field label="项目地址">
          <span className="flex items-center gap-1 font-mono text-[12px] text-accent">
            <Globe className="h-3.5 w-3.5" />
            github.com/sniffy/sniffy
          </span>
        </Field>
        <Field label="更新">
          <Button icon={<RefreshCw className="h-3.5 w-3.5" />}>检查更新</Button>
        </Field>
      </Panel>
    </PageShell>
  )
}
