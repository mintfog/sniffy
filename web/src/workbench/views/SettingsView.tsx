import { useState } from 'react'
import { Events } from '@wailsio/runtime'
import {
  Check,
  Database,
  Eraser,
  Info,
  Network,
  Palette,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { ACCENTS, usePrefs, type AccentKey, type FontSize, type ThemeMode } from '../prefs'
import { Button, Field, Panel, Select, TextInput, Toggle } from '../ui/controls'
import { cx } from '../ui/primitives'
import { APP_VERSION, RELEASES_URL, openExternal } from '../lib/links'
import { openAboutWindow, requestMainNav } from '../lib/windows'
import { PageShell } from './PageShell'

const ACCENT_KEYS = Object.keys(ACCENTS) as AccentKey[]

export function SettingsView() {
  const p = usePrefs()
  const set = p.set
  const [saved, setSaved] = useState(false)

  // host/port/mitm 等后端项：尽力下发给已存在的 Bridge.updateConfig（无后端时静默忽略）。
  const save = () => {
    Bridge.updateConfig({ host: p.host, port: Number(p.port) || 8080, enableHTTPS: p.mitm }).catch(() => {})
    setSaved(true)
    setTimeout(() => setSaved(false), 1400)
  }

  const clearData = () => {
    if (!window.confirm('确定清空所有已捕获的流量记录？此操作不可撤销。')) return
    Bridge.clearSessions().catch(() => {})
    // 通知主窗口清空其会话 store（设置可能在独立窗口中）
    try {
      void Events.Emit('data_cleared', null)
    } catch {
      /* ignore */
    }
  }

  return (
    <PageShell
      icon={SlidersHorizontal}
      title="设置"
      subtitle="代理 · 解密 · 外观 · 存储"
      actions={
        <Button variant="primary" icon={saved ? <Check className="h-3.5 w-3.5" /> : undefined} onClick={save}>
          {saved ? '已保存' : '保存更改'}
        </Button>
      }
    >
      <Panel title="代理" icon={<Network className="h-4 w-4" />}>
        <Field label="监听地址" hint="代理服务器绑定的网卡地址（保存后下发，重启代理生效）">
          <TextInput value={p.host} onChange={(e) => set({ host: e.target.value })} width={160} />
        </Field>
        <Field label="监听端口">
          <TextInput value={p.port} onChange={(e) => set({ port: e.target.value })} width={100} />
        </Field>
        <Field label="设为系统代理" hint="启动时自动接管系统 HTTP/HTTPS 代理设置">
          <Toggle checked={p.systemProxy} onChange={(v) => set({ systemProxy: v })} />
        </Field>
        <Field label="网络限速" hint="模拟弱网，对流量注入带宽/延迟限制">
          <Toggle checked={p.throttle} onChange={(v) => set({ throttle: v })} />
        </Field>
        <Field label="上游代理" hint="将流量转发到二级代理（如公司网关）">
          <Toggle checked={p.upstream} onChange={(v) => set({ upstream: v })} />
        </Field>
        {p.upstream && (
          <Field label="上游代理地址">
            <TextInput
              value={p.upstreamAddr}
              onChange={(e) => set({ upstreamAddr: e.target.value })}
              placeholder="http://host:port"
              width={240}
            />
          </Field>
        )}
      </Panel>

      <Panel title="HTTPS 解密" icon={<ShieldCheck className="h-4 w-4" />}>
        <Field label="启用 HTTPS MITM" hint="使用动态证书解密 HTTPS 流量">
          <Toggle checked={p.mitm} onChange={(v) => set({ mitm: v })} />
        </Field>
        <Field label="解密范围">
          <Select
            value={p.scope}
            onChange={(e) => set({ scope: e.target.value as typeof p.scope })}
            options={[
              { value: 'all', label: '全部主机' },
              { value: 'allow', label: '仅白名单' },
              { value: 'deny', label: '黑名单除外' },
            ]}
          />
        </Field>
        <Field label="根证书" hint="安装到系统/设备信任库以解密 HTTPS">
          <Button icon={<ShieldCheck className="h-3.5 w-3.5" />} onClick={() => requestMainNav('certs')}>
            管理证书
          </Button>
        </Field>
      </Panel>

      <Panel title="外观" icon={<Palette className="h-4 w-4" />}>
        <Field label="主题">
          <Select
            value={p.theme}
            onChange={(e) => set({ theme: e.target.value as ThemeMode })}
            options={[
              { value: 'dark', label: '深色' },
              { value: 'light', label: '亮色' },
            ]}
          />
        </Field>
        <Field label="强调色" hint="界面主色（实时生效，所有窗口同步）">
          <div className="flex items-center gap-1.5">
            {ACCENT_KEYS.map((key) => (
              <button
                key={key}
                type="button"
                onClick={() => set({ accent: key })}
                title={key}
                aria-label={`强调色 ${key}`}
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
        <Field label="紧凑模式" hint="减小流量表行高以显示更多内容">
          <Toggle checked={p.compact} onChange={(v) => set({ compact: v })} />
        </Field>
        <Field label="字体大小">
          <Select
            value={String(p.fontSize)}
            onChange={(e) => set({ fontSize: Number(e.target.value) as FontSize })}
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
        <Field label="自动滚动到最新" hint="与工具栏「跟随最新」同步">
          <Toggle checked={p.follow} onChange={(v) => set({ follow: v })} />
        </Field>
        <Field label="启动时自动录制">
          <Toggle checked={p.autoRecord} onChange={(v) => set({ autoRecord: v })} />
        </Field>
        <Field label="清空所有数据" hint="删除全部已捕获的流量记录">
          <Button variant="danger" icon={<Eraser className="h-3.5 w-3.5" />} onClick={clearData}>
            清空
          </Button>
        </Field>
      </Panel>

      <Panel title="关于" icon={<Info className="h-4 w-4" />}>
        <Field label="版本">
          <span className="font-mono text-[12px] text-fg-muted">Sniffy {APP_VERSION}</span>
        </Field>
        <Field label="更多">
          <Button onClick={() => openAboutWindow().catch(() => {})}>关于 Sniffy</Button>
          <Button onClick={() => openExternal(RELEASES_URL)}>检查更新</Button>
        </Field>
      </Panel>
    </PageShell>
  )
}
