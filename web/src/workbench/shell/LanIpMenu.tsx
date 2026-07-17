import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Cable, Check, Network, RefreshCw, Wifi } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { LANAddr } from '@/lib/bridge'
import { cx } from '../ui/primitives'

interface LanIpMenuProps {
  /** 锚点：触发元素的左下角屏幕坐标。 */
  anchor: { x: number; y: number }
  /** 触发元素：落在其内的 mousedown 不触发外部关闭，交给触发器自身做开合切换。 */
  anchorRef?: React.RefObject<HTMLElement>
  items: LANAddr[]
  /** 当前生效地址（高亮打勾）。 */
  selected: string
  onSelect: (ip: string) => void
  onRefresh?: () => Promise<void>
  onClose: () => void
}

/** 按网卡名/友好名猜测连接类型图标，帮助用户区分无线与有线。 */
function ifaceIcon(a: LANAddr) {
  const n = `${a.label} ${a.interface}`.toLowerCase()
  if (/wi-?fi|wlan|wireless|airport|无线|無線/.test(n)) return Wifi
  if (/ethernet|以太|\beth\d|有线|有線/.test(n)) return Cable
  return Network
}

/**
 * 内网地址选择浮层：多网卡时列出全部可用内网 IPv4，按推荐顺序排列，让用户挑选要
 * 暴露给同网段设备的地址。定位/关闭逻辑沿用 ContextMenu（portal + 视口夹紧 + 点外/Esc 关闭）。
 */
export function LanIpMenu({ anchor, anchorRef, items, selected, onSelect, onRefresh, onClose }: LanIpMenuProps) {
  const { t } = useTranslation()
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const spinTimer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(spinTimer.current), [])

  // 枚举毫秒级完成，spin 保底 300ms，否则图标只闪一下、看不出刷新发生过。
  const handleRefresh = () => {
    if (!onRefresh || refreshing) return
    setRefreshing(true)
    const started = Date.now()
    void onRefresh().finally(() => {
      spinTimer.current = window.setTimeout(() => setRefreshing(false), Math.max(0, 300 - (Date.now() - started)))
    })
  }

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    setPos({
      left: Math.min(anchor.x, Math.max(4, window.innerWidth - width - 4)),
      top: Math.min(anchor.y, Math.max(4, window.innerHeight - height - 4)),
    })
  }, [anchor.x, anchor.y, items.length])

  // 上层常传 inline arrow;用 ref 稳住,避免父组件高频重渲染时重装下面 4 个全局监听器。
  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  useEffect(() => {
    const close = () => onCloseRef.current()
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      if (ref.current?.contains(t) || anchorRef?.current?.contains(t)) return
      close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        close()
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('blur', close)
    window.addEventListener('resize', close)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('blur', close)
      window.removeEventListener('resize', close)
    }
  }, [anchorRef])

  return createPortal(
    <div
      ref={ref}
      data-wb-menu
      className="wb-portal wb-pop fixed z-[80] max-h-[70vh] min-w-[260px] overflow-y-auto overflow-x-hidden rounded-wb border border-line bg-surface py-1 shadow-wb"
      style={pos ? { left: pos.left, top: pos.top } : { left: -9999, top: 0, visibility: 'hidden' }}
    >
      <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wide text-fg-faint">
        {t('proxyBar.selectLanIp')}
      </div>

      {items.length === 0 && (
        <div className="px-3 py-2 text-[12px] text-fg-faint">
          <div>{t('proxyBar.noLanIp')}</div>
          {/* bar 上仍显示回环地址，不说明会显得两处自相矛盾 */}
          <div className="mt-0.5 text-[11px]">{t('proxyBar.noLanIpFallback')}</div>
        </div>
      )}

      {items.map((a) => {
        const Icon = ifaceIcon(a)
        const active = a.ip === selected
        const name = a.label && a.label !== a.interface ? `${a.label} · ${a.interface}` : a.interface
        return (
          <button
            key={`${a.interface}-${a.ip}`}
            type="button"
            onClick={() => {
              onSelect(a.ip)
              onClose()
            }}
            className={cx(
              'flex w-full items-center gap-2.5 px-3 py-1.5 text-left transition-colors outline-none',
              active ? 'bg-accent/12 text-fg' : 'text-fg hover:bg-elevated',
            )}
          >
            <Icon className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate text-[12.5px]">{name}</span>
              <span className="font-mono text-[11px] text-fg-muted">{a.ip}</span>
            </span>
            {!a.private && (
              <span className="shrink-0 rounded-full bg-warn/15 px-1.5 py-px text-[10px] font-medium text-warn">
                {t('proxyBar.publicNet')}
              </span>
            )}
            <Check className={cx('h-3.5 w-3.5 shrink-0 text-accent', active ? 'opacity-100' : 'opacity-0')} />
          </button>
        )
      })}

      {onRefresh && (
        <>
          <div className="my-1 h-px bg-line" />
          <button
            type="button"
            onClick={handleRefresh}
            disabled={refreshing}
            className="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-[12.5px] text-fg-muted transition-colors outline-none hover:bg-elevated hover:text-fg disabled:pointer-events-none"
          >
            <RefreshCw className={cx('h-3.5 w-3.5 shrink-0', refreshing && 'animate-spin')} />
            {t('proxyBar.refreshNetworks')}
          </button>
        </>
      )}
    </div>,
    document.body,
  )
}
