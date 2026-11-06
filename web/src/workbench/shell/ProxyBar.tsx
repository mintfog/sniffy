import { useRef, useState } from 'react'
import { ChevronDown, CircleDot, Gauge, Globe, Pause, Pencil, Play, Puzzle, ShieldCheck, Shuffle, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { LANAddr } from '@/lib/bridge'
import type { WorkbenchView } from './IconRail'
import { LanIpMenu } from './LanIpMenu'
import { CountBadge, cx, IconButton, Tooltip } from '../ui/primitives'

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
  /** 全局“断在请求/响应”是否有一个开着。 */
  breakpointsArmed?: boolean
  /** 命中断点、正被按住的请求数。 */
  pausedCount?: number
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
  breakpointsArmed = false,
  pausedCount = 0,
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
    <span className="truncate text-[13px] text-fg">
      {t('proxyBar.listening')} <span className="font-mono text-fg-muted">{proxyAddr}</span>
    </span>
  )

  return (
    <div className="flex h-[var(--wb-toolbar-h)] shrink-0 items-center gap-1 border-b border-line bg-inset px-1.5">
      {/* 代理状态条：端口常驻监听（软件开着就一直监听） */}
      <div className="mr-1 flex h-control-sm min-w-0 flex-1 items-center gap-2 rounded-control border border-line px-2.5">
        <span className="h-2 w-2 shrink-0 rounded-full bg-ok wb-pulse" />
        <div ref={triggerRef} className="flex min-w-0 items-center gap-2">
          {clickable ? (
            <Tooltip label={multi ? t('proxyBar.selectLanIp') : t('proxyBar.networkMenuHint')} placement="bottom">
              <button
                type="button"
                onClick={togglePicker}
                className="flex min-w-0 items-center gap-1 rounded-wb-sm outline-none hover:text-fg"
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
                className="h-5 shrink-0 rounded-full bg-fg-muted/12 px-2 text-[11px] font-medium leading-[14px] text-fg-muted outline-none transition-colors hover:text-fg focus-visible:shadow-focus"
              >
                {t('proxyBar.multipleNetworks', { n: lanIPs.length })}
              </button>
            </Tooltip>
          )}
        </div>
        <span
          className={cx(
            'inline-flex h-5 items-center rounded-full px-2 text-[11px] font-medium leading-[14px]',
            systemProxy ? 'bg-ok/12 text-ok [[data-theme=dark]_&]:bg-ok/16' : 'bg-fg-muted/12 text-fg-muted',
          )}
        >
          {systemProxy ? t('proxyBar.systemProxyOn') : t('proxyBar.systemProxyOff')}
        </span>
        {!capturing && <span className="inline-flex h-5 items-center rounded-full bg-warn/12 px-2 text-[11px] font-medium leading-[14px] text-warn [[data-theme=dark]_&]:bg-warn/16">{t('proxyBar.paused')}</span>}
        <Tooltip label={t('proxyBar.editProxyConfig')} placement="bottom">
          <button
            type="button"
            onClick={onEditProxy}
            aria-label={t('proxyBar.editProxyConfig')}
            className="ml-auto flex h-5 w-5 items-center justify-center rounded-wb-sm text-fg-faint outline-none hover:bg-hover hover:text-fg focus-visible:shadow-focus"
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
          <IconButton size="sm" active={systemProxy} onClick={onToggleSystemProxy}>
            <Globe className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.rules')} placement="bottom">
          <IconButton size="sm" onClick={() => onNav('rules')}>
            <Shuffle className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        {/* 与系统代理/限速同一套视觉语言：active 表示"这个开关现在开着"。
            断点还多一层——有请求正被按住时挂计数角标，那是必须立刻处理的状态。 */}
        <Tooltip
          label={pausedCount > 0 ? t('proxyBar.pausedCount', { count: pausedCount }) : t('proxyBar.breakpoints')}
          placement="bottom"
        >
          <span className="relative inline-flex">
            <IconButton size="sm" active={breakpointsArmed || pausedCount > 0} onClick={() => onNav('breakpoints')}>
              <CircleDot className="h-4 w-4" />
            </IconButton>
            <CountBadge count={pausedCount} />
          </span>
        </Tooltip>
        <Tooltip label={t('proxyBar.scriptsPlugins')} placement="bottom">
          <IconButton size="sm" onClick={() => onNav('plugins')}>
            <Puzzle className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.throttle')} placement="bottom">
          <IconButton size="sm" active={throttle} onClick={onToggleThrottle}>
            <Gauge className="h-4 w-4" />
          </IconButton>
        </Tooltip>
        <Tooltip label={t('proxyBar.certificates')} placement="bottom">
          <IconButton size="sm" onClick={() => onNav('certs')}>
            <ShieldCheck className="h-4 w-4" />
          </IconButton>
        </Tooltip>
      </div>

      <span className="mx-1 h-4 w-px shrink-0 bg-line" />

      {/* 暂停 / 继续 捕获（端口不受影响，仅控制是否把新流量记入表格） */}
      <Tooltip label={capturing ? t('proxyBar.pauseHint') : t('proxyBar.resumeHint')} placement="bottom">
        <button
          type="button"
          onClick={onToggleCapture}
          className={cx(
            'inline-flex h-control-sm shrink-0 items-center gap-1.5 rounded-control px-2 text-[12px] font-medium transition-colors outline-none focus-visible:shadow-focus',
            capturing ? 'text-fg-muted hover:bg-hover hover:text-fg' : 'bg-sel text-sel-fg hover:bg-sel-hover',
          )}
        >
          {capturing ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5 fill-current" />}
          {capturing ? t('proxyBar.pause') : t('proxyBar.resume')}
        </button>
      </Tooltip>
      <Tooltip label={t('proxyBar.clearTraffic')} placement="bottom">
        <IconButton size="sm" onClick={onClear} tone="danger">
          <Trash2 className="h-4 w-4" />
        </IconButton>
      </Tooltip>
    </div>
  )
}
