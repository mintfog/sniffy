import { ArrowUpCircle, CircleDot, PauseCircle, Globe, Radio, Wifi, WifiOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cx } from '../ui/primitives'
import { useUpdate } from '../lib/update'
import { openAboutWindow } from '../lib/windows'

interface StatusBarProps {
  proxyAddr: string
  capturing: boolean
  total: number
  filtered: number
  selectedSeq?: number
  /** 多选数量（>1 时优先于 selectedSeq 展示） */
  selectedCount?: number
  connected: boolean
  /** 命中断点、正被按住的请求数；>0 时常驻提示并可点击前往。 */
  pausedCount?: number
  onGoBreakpoints?: () => void
}

export function StatusBar({
  proxyAddr,
  capturing,
  total,
  filtered,
  selectedSeq,
  selectedCount = 0,
  connected,
  pausedCount = 0,
  onGoBreakpoints,
}: StatusBarProps) {
  const { t } = useTranslation()
  return (
    <footer className="flex h-6 shrink-0 items-center gap-3 border-t border-line bg-surface px-3 text-[11px] text-fg-muted select-none">
      {/* 端口常驻监听 */}
      <span className="flex items-center gap-1.5 text-ok">
        <Globe className="h-3 w-3" />
        {t('statusBar.listening')} <span className="font-mono text-fg-muted">{proxyAddr}</span>
      </span>

      <span className="h-3 w-px bg-line" />

      {/* 捕获开关：仅控制是否记录新流量 */}
      <span className={cx('flex items-center gap-1.5', capturing ? 'text-fg-muted' : 'text-warn')}>
        {capturing ? <Radio className="h-3 w-3 text-ok" /> : <PauseCircle className="h-3 w-3" />}
        {capturing ? t('statusBar.capturing') : t('statusBar.paused')}
      </span>

      {/* 底部常驻条是最后一道兜底：用户可能把主窗停在设置或证书页，
          而被自己按住的请求在那里没有任何其它出口。 */}
      {pausedCount > 0 && (
        <>
          <span className="h-3 w-px bg-line" />
          <button
            type="button"
            onClick={onGoBreakpoints}
            className="flex items-center gap-1.5 rounded-sm px-1 text-warn transition-colors hover:bg-elevated"
          >
            <CircleDot className="h-3 w-3" />
            <span className="tabular-nums">{t('statusBar.paused_breakpoints', { count: pausedCount })}</span>
          </button>
        </>
      )}

      <UpdateNotice />

      <div className="flex-1" />

      <span className="tabular-nums">
        {t('statusBar.showLabel')} <span className="font-medium text-fg">{filtered.toLocaleString()}</span>
        {filtered !== total && (
          <>
            {' '}/ <span className="text-fg-faint">{total.toLocaleString()}</span>
          </>
        )}{' '}
        {t('statusBar.itemsUnit')}
      </span>

      {(selectedCount > 0 || selectedSeq != null) && (
        <>
          <span className="h-3 w-px bg-line" />
          <span className="tabular-nums">
            {t('statusBar.selectedLabel')}{' '}
            <span className="font-medium text-accent">
              {selectedCount > 1
                ? t('statusBar.selectedCount', { n: selectedCount.toLocaleString() })
                : selectedSeq != null
                  ? `#${selectedSeq}`
                  : t('statusBar.selectedCount', { n: selectedCount })}
            </span>
          </span>
        </>
      )}

      <span className="h-3 w-px bg-line" />

      <span className={cx('flex items-center gap-1.5', connected ? 'text-ok' : 'text-fg-faint')}>
        {connected ? <Wifi className="h-3 w-3" /> : <WifiOff className="h-3 w-3" />}
        {connected ? t('statusBar.live') : t('statusBar.offline')}
      </span>
    </footer>
  )
}

function UpdateNotice() {
  const { t } = useTranslation()
  const { state } = useUpdate()
  if (!state.notify) return null
  return (
    <>
      <span className="h-3 w-px bg-line" />
      <button
        type="button"
        onClick={() => openAboutWindow().catch(() => {})}
        className="flex items-center gap-1.5 rounded-sm px-1 text-accent transition-colors hover:bg-elevated"
      >
        <ArrowUpCircle className="h-3 w-3" />
        {t('statusBar.updateAvailable', { version: state.latest })}
      </button>
    </>
  )
}
