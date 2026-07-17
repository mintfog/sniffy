import { useRef, useState } from 'react'
import { ChevronDown, CircleDot, Gauge, Globe, Pause, Pencil, Play, Puzzle, ShieldCheck, Shuffle, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { LANAddr } from '@/lib/bridge'
import type { WorkbenchView } from './IconRail'
import { LanIpMenu } from './LanIpMenu'
import { cx, Divider, IconButton, Tooltip } from '../ui/primitives'

interface ProxyBarProps {
  proxyAddr: string
  /** 本机内网候选地址（多网卡时 >1，触发选择浮层与提示徽标）。 */
  lanIPs: LANAddr[]
  /** 当前生效的内网地址（proxyAddr 的 IP 部分）。 */
  selectedLanIP: string
  onSelectLanIP: (ip: string) => void
  /** 缺省表示后端桥不可用（非 Wails 预览），此时退回纯文本不渲染菜单入口。force 绕过节流。 */
  onRefreshLanIPs?: (force?: boolean) => Promise<void>
  capturing: boolean
  onToggleCapture: () => void
  onClear: () => void
  onNav: (v: WorkbenchView) => void
  onEditProxy: () => void
  systemProxy: boolean
  onToggleSystemProxy: () => void
  throttle: boolean
  onToggleThrottle: () => void
}

export function ProxyBar({
  proxyAddr,
  lanIPs,
  selectedLanIP,
  onSelectLanIP,
  onRefreshLanIPs,
  capturing,
  onToggleCapture,
  onClear,
  onNav,
  onEditProxy,
  systemProxy,
  onToggleSystemProxy,
  throttle,
  onToggleThrottle,
}: ProxyBarProps) {
  const { t } = useTranslation()
  const triggerRef = useRef<HTMLDivElement>(null)
  const [pickerAt, setPickerAt] = useState<{ x: number; y: number } | null>(null)
  const multi = lanIPs.length > 1
  // 单地址甚至无地址也保留菜单入口：刷新按钮在菜单里，否则单网卡启动后插网线永远发现不了新地址。
  const clickable = multi || Boolean(onRefreshLanIPs)

  // 切换开合;配合 LanIpMenu 放行 anchorRef 内的 mousedown,使再次点击触发器走这里的切换而非被点外关闭抢先收起。
  // 副作用放在 setState 更新器之外，避免 StrictMode 双调用；打开时顺带刷新一次，让列表天然是新的。
  const togglePicker = () => {
    if (pickerAt) {
      setPickerAt(null)
      return
    }
    void onRefreshLanIPs?.()
    const r = triggerRef.current?.getBoundingClientRect()
    setPickerAt(r ? { x: r.left, y: r.bottom + 4 } : null)
  }

  const addrText = (
    <span className="truncate text-[12.5px] text-fg">
      {t('proxyBar.listening')} <span className="font-mono text-fg-muted">{proxyAddr}</span>
    </span>
  )

  return (
    <div className="flex h-11 items-center gap-2 border-b border-line bg-surface px-2">
      {/* 代理状态条：端口常驻监听（软件开着就一直监听） */}
      <div className="flex h-7 min-w-0 flex-1 items-center gap-2 rounded-wb border border-line bg-inset px-2.5">
        <span className="h-2 w-2 shrink-0 rounded-full bg-ok wb-pulse" />
        <div ref={triggerRef} className="flex min-w-0 items-center gap-2">
          {clickable ? (
            <Tooltip label={multi ? t('proxyBar.selectLanIp') : t('proxyBar.networkMenuHint')} placement="bottom">
              <button
                type="button"
                onClick={togglePicker}
                className="flex min-w-0 items-center gap-1 rounded-sm outline-none hover:text-fg"
              >
                {addrText}
                <ChevronDown className="h-3 w-3 shrink-0 text-fg-faint" />
              </button>
            </Tooltip>
          ) : (
            <div className="flex min-w-0 items-center">{addrText}</div>
          )}
          {multi && (
            <Tooltip label={t('proxyBar.multipleNetworksHint')} placement="bottom">
              <button
                type="button"
                onClick={togglePicker}
                className="shrink-0 rounded-full bg-accent/15 px-1.5 py-px text-[10px] font-medium text-accent outline-none hover:bg-accent/25"
              >
                {t('proxyBar.multipleNetworks', { n: lanIPs.length })}
              </button>
            </Tooltip>
          )}
        </div>
        <span
          className={cx(
            'rounded-full px-1.5 py-px text-[10px] font-medium',
            systemProxy ? 'bg-ok/15 text-ok' : 'bg-fg-faint/15 text-fg-faint',
          )}
        >
          {systemProxy ? t('proxyBar.systemProxyOn') : t('proxyBar.systemProxyOff')}
        </span>
        {!capturing && <span className="rounded-full bg-warn/15 px-1.5 py-px text-[10px] font-medium text-warn">{t('proxyBar.paused')}</span>}
        <Tooltip label={t('proxyBar.editProxyConfig')} placement="bottom">
          <button
            type="button"
            onClick={onEditProxy}
            aria-label={t('proxyBar.editProxyConfig')}
            className="ml-auto flex h-5 w-5 items-center justify-center rounded-sm text-fg-faint hover:bg-elevated hover:text-fg"
          >
            <Pencil className="h-3 w-3" />
          </button>
        </Tooltip>
      </div>

      {pickerAt && (
        <LanIpMenu
          anchor={pickerAt}
          anchorRef={triggerRef}
          items={lanIPs}
          selected={selectedLanIP}
          onSelect={onSelectLanIP}
          onRefresh={onRefreshLanIPs && (() => onRefreshLanIPs(true))}
          onClose={() => setPickerAt(null)}
        />
      )}

      {/* 功能图标组 */}
      <div className="flex shrink-0 items-center gap-0.5">
        <Tooltip label={systemProxy ? t('proxyBar.disableSystemProxy') : t('proxyBar.enableSystemProxy')} placement="bottom">
          <IconButton active={systemProxy} onClick={onToggleSystemProxy}>
            <Globe className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.rules')} placement="bottom">
          <IconButton onClick={() => onNav('rules')}>
            <Shuffle className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.breakpoints')} placement="bottom">
          <IconButton onClick={() => onNav('breakpoints')}>
            <CircleDot className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.scriptsPlugins')} placement="bottom">
          <IconButton onClick={() => onNav('plugins')}>
            <Puzzle className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.throttle')} placement="bottom">
          <IconButton active={throttle} onClick={onToggleThrottle}>
            <Gauge className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.certificates')} placement="bottom">
          <IconButton onClick={() => onNav('certs')}>
            <ShieldCheck className="h-4 w-4" />
          </IconButton>
        </Tooltip>
      </div>

      <Divider vertical className="mx-0.5 my-2" />

      {/* 暂停 / 继续 捕获（端口不受影响，仅控制是否把新流量记入表格） */}
      <Tooltip label={capturing ? t('proxyBar.pauseHint') : t('proxyBar.resumeHint')} placement="bottom">
        <button
          type="button"
          onClick={onToggleCapture}
          className={cx(
            'inline-flex h-7 shrink-0 items-center gap-1.5 rounded-control px-3 text-[12px] font-semibold shadow-raise transition outline-none hover:shadow-raise-hover active:shadow-press active:translate-y-px',
            capturing ? 'bg-inset text-fg border border-line hover:bg-elevated' : 'bg-accent text-accent-fg hover:bg-accent-hover',
          )}
        >
          {capturing ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5 fill-current" />}
          {capturing ? t('proxyBar.pause') : t('proxyBar.resume')}
        </button>
      </Tooltip>
      <Tooltip label={t('proxyBar.clearTraffic')} placement="bottom">
        <IconButton onClick={onClear} tone="danger">
          <Trash2 className="h-4 w-4" />
        </IconButton>
      </Tooltip>
    </div>
  )
}
