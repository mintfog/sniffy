import { PauseCircle, Globe, Radio, Wifi, WifiOff } from 'lucide-react'
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
  return (
    <footer className="flex h-6 shrink-0 items-center gap-3 border-t border-line bg-surface px-3 text-[11px] text-fg-muted select-none">
      {/* 端口常驻监听 */}
      <span className="flex items-center gap-1.5 text-ok">
        <Globe className="h-3 w-3" />
        监听中 <span className="font-mono text-fg-muted">{proxyAddr}</span>
      </span>

      <span className="h-3 w-px bg-line" />

      {/* 捕获开关：仅控制是否记录新流量 */}
      <span className={cx('flex items-center gap-1.5', capturing ? 'text-fg-muted' : 'text-warn')}>
        {capturing ? <Radio className="h-3 w-3 text-ok" /> : <PauseCircle className="h-3 w-3" />}
        {capturing ? '捕获中' : '已暂停'}
      </span>

      <div className="flex-1" />

      <span className="tabular-nums">
        显示 <span className="font-medium text-fg">{filtered.toLocaleString()}</span>
        {filtered !== total && (
          <>
            {' '}/ <span className="text-fg-faint">{total.toLocaleString()}</span>
          </>
        )}{' '}
        条
      </span>

      {(selectedCount > 0 || selectedSeq != null) && (
        <>
          <span className="h-3 w-px bg-line" />
          <span className="tabular-nums">
            已选{' '}
            <span className="font-medium text-accent">
              {selectedCount > 1
                ? `${selectedCount.toLocaleString()} 项`
                : selectedSeq != null
                  ? `#${selectedSeq}`
                  : `${selectedCount} 项`}
            </span>
          </span>
        </>
      )}

      <span className="h-3 w-px bg-line" />

      <span className={cx('flex items-center gap-1.5', isDemo ? 'text-iris' : connected ? 'text-ok' : 'text-fg-faint')}>
        {connected && !isDemo ? <Wifi className="h-3 w-3" /> : <WifiOff className="h-3 w-3" />}
        {isDemo ? '演示' : connected ? '实时' : '离线'}
      </span>
    </footer>
  )
}
