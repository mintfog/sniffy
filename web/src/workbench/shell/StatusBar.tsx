import { PauseCircle, Globe, Radio, Wifi, WifiOff } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cx } from '../ui/primitives'

interface StatusBarProps {
  proxyAddr: string
  capturing: boolean
  total: number
  filtered: number
  selectedSeq?: number
  /** 多选数量（>1 时优先于 selectedSeq 展示） */
  selectedCount?: number
  connected: boolean
  isDemo: boolean
}

export function StatusBar({
  proxyAddr,
  capturing,
  total,
  filtered,
  selectedSeq,
  selectedCount = 0,
  connected,
  isDemo,
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

      <span className={cx('flex items-center gap-1.5', isDemo ? 'text-iris' : connected ? 'text-ok' : 'text-fg-faint')}>
        {connected && !isDemo ? <Wifi className="h-3 w-3" /> : <WifiOff className="h-3 w-3" />}
        {isDemo ? t('statusBar.demo') : connected ? t('statusBar.live') : t('statusBar.offline')}
      </span>
    </footer>
  )
}
