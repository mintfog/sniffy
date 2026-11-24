import { ArrowUpCircle, CircleDot, PauseCircle, Globe, Network, Radio, ShieldAlert, Wifi, WifiOff } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { cx, StatusSpinner, Tooltip } from '../ui/primitives'
import { downloadPercent, useUpdate } from '../lib/update'
import { showTunError, useTun, useTunPhase, type TunPhase } from '../lib/tun'
import { UpdateDialog } from '../ui/UpdateDialog'

const STATUS_BUTTON = 'flex items-center gap-1.5 rounded-wb-sm px-1 outline-none transition-colors hover:bg-line focus-visible:shadow-focus'

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
  /** 安装根证书到本机（TUN 模式下根证书未被信任时的入口）。 */
  onInstallCert: () => void
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
  onInstallCert,
}: StatusBarProps) {
  const { t } = useTranslation()
  return (
    <footer className="flex h-6 shrink-0 items-center gap-3 border-t border-line bg-base px-3 text-[12px] text-fg-muted select-none">
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
            className={cx(STATUS_BUTTON, 'text-warn')}
          >
            <CircleDot className="h-3 w-3" />
            <span className="tabular-nums">{t('statusBar.paused_breakpoints', { count: pausedCount })}</span>
          </button>
        </>
      )}

      <TunStatusItem onInstallCert={onInstallCert} />

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

const TUN_TONE: Record<Exclude<TunPhase, 'off'>, string> = {
  starting: 'text-fg-muted',
  running: 'text-ok',
  stopping: 'text-fg-muted',
  stuck: 'text-warn',
  failed: 'text-danger',
}

/** TUN 模式的常驻状态：出错时点击查看原因，根证书未被信任时另挂一个安装根证书的标记。 */
function TunStatusItem({ onInstallCert }: { onInstallCert: () => void }) {
  const { t } = useTranslation()
  const phase = useTunPhase()
  const error = useTun((s) => s.status.error)
  const caUntrusted = useTun((s) => s.status.caUntrusted)
  if (phase === 'off') return null
  const content = (
    <>
      {phase === 'starting' || phase === 'stopping' ? <StatusSpinner /> : <Network className="h-3 w-3" />}
      {t(`tun.phase.${phase}`)}
    </>
  )
  let indicator = <span className={cx('flex items-center gap-1.5', TUN_TONE[phase])}>{content}</span>
  if (error) {
    indicator = (
      <Tooltip label={t('tun.showError')} placement="top">
        <button type="button" onClick={showTunError} className={cx(STATUS_BUTTON, TUN_TONE[phase])}>
          {content}
        </button>
      </Tooltip>
    )
  } else if (phase === 'running') {
    indicator = (
      <Tooltip label={t('tun.runningHint')} placement="top">
        {indicator}
      </Tooltip>
    )
  }
  return (
    <>
      <span className="h-3 w-px bg-line" />
      {indicator}
      {phase === 'running' && caUntrusted && (
        <Tooltip label={t('tun.caUntrustedHint')} placement="top">
          <button type="button" onClick={onInstallCert} className={cx(STATUS_BUTTON, 'text-warn')}>
            <ShieldAlert className="h-3 w-3" />
            {t('tun.caUntrusted')}
          </button>
        </Tooltip>
      )}
    </>
  )
}

function UpdateNotice() {
  const { t } = useTranslation()
  const { state, actions, installError, installing } = useUpdate()
  const [open, setOpen] = useState(false)
  if (!state.notify) return null
  return (
    <>
      <span className="h-3 w-px bg-line" />
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex h-5 shrink-0 items-center gap-1.5 rounded-full bg-info/[.12] px-2 text-[11px] font-medium leading-[14px] text-info outline-none hover:underline focus-visible:shadow-focus [[data-theme=dark]_&]:bg-info/[.16]"
      >
        <ArrowUpCircle className="h-3 w-3" />
        {state.status === 'downloading'
          ? t('statusBar.updateDownloading', { percent: downloadPercent(state) })
          : t('statusBar.updateAvailable', { version: state.latest })}
      </button>
      {open && (
        <UpdateDialog
          state={state}
          actions={actions}
          installError={installError}
          installing={installing}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}
