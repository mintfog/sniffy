import React, { useLayoutEffect, useRef, useState } from 'react'
import clsx from 'clsx'
import sniffyMarkUrl from '@/assets/sniffy-mark.png'
import {
  Binary,
  Braces,
  ChevronRight,
  File as FileIcon,
  FileCode,
  FileText,
  Film,
  Hash,
  Image as ImageIcon,
  Loader2,
  type LucideIcon,
  Music,
  Radio,
  Type,
} from 'lucide-react'
import type { ContentKind, Tone } from '../lib/types'

export const cx = clsx

/* ───────────────────────── SniffyMark（品牌标记） ───────────────────────── */

/** 品牌标记与桌面壳共用雪貂图形，避免窗口内外出现两套应用标识。 */
export function SniffyMark({ className }: { className?: string }) {
  return <img src={sniffyMarkUrl} className={clsx('shrink-0 object-contain', className)} alt="" aria-hidden draggable={false} />
}

/* ───────────────────────── IconButton ───────────────────────── */

interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  active?: boolean
  size?: 'xs' | 'sm' | 'md'
  tone?: 'default' | 'accent' | 'danger'
  /** 放在 chrome（标题栏 / 图标栏 / 状态栏）上：底色与 hover 太接近，悬停改用 line */
  onChrome?: boolean
}

export function IconButton({ active, size = 'md', tone = 'default', onChrome, className, children, ...rest }: IconButtonProps) {
  return (
    <button
      type="button"
      className={cx(
        'inline-flex shrink-0 items-center justify-center rounded-control transition-colors duration-100 outline-none',
        'focus-visible:shadow-focus disabled:opacity-40 disabled:pointer-events-none',
        size === 'xs' ? 'h-control-xs w-control-xs' : size === 'sm' ? 'h-control-sm w-control-sm' : 'h-control-md w-control-md',
        active
          ? cx('text-fg', onChrome ? 'bg-line' : 'bg-hover')
          : tone === 'danger'
            ? 'text-fg-muted hover:bg-danger/10 hover:text-danger'
            : tone === 'accent'
              ? cx('text-accent', onChrome ? 'hover:bg-line' : 'hover:bg-hover')
              : cx('text-fg-muted hover:text-fg', onChrome ? 'hover:bg-line' : 'hover:bg-hover'),
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  )
}

/* ───────────────────────── CountBadge（图标角标） ───────────────────────── */

/**
 * 挂在图标右上角的计数角标，父级须是 relative。只用于未处理的断点和错误数：
 * 实底危险色随方案保持色相不变，必须一眼看见。
 */
export function CountBadge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null
  return (
    <span
      className={cx(
        'pointer-events-none absolute -right-1 -top-1 z-10 flex h-4 min-w-4 items-center justify-center',
        'rounded-full bg-danger px-1 text-[11px] font-medium leading-none text-danger-fg tabular-nums',
        // 与 chrome 同色的描边把角标从底下的图标里切出来，否则实底色块与图标笔画糊成一团
        'ring-[1.5px] ring-base',
        className,
      )}
    >
      {count > 99 ? '99+' : count}
    </span>
  )
}

/* ───────────────────────── Chip（过滤芯片） ───────────────────────── */

interface ChipProps {
  active?: boolean
  onClick?: () => void
  children: React.ReactNode
  count?: number
  title?: string
  /** 成排出现的筛选项用 ghost：未激活项只有文字，避免一排描边胶囊；尺寸同时收到筛选栏的 20px */
  ghost?: boolean
}

export function Chip({ active, onClick, children, count, title, ghost }: ChipProps) {
  return (
    <button
      type="button"
      title={title}
      aria-pressed={active}
      onClick={onClick}
      className={cx(
        'inline-flex items-center gap-1 rounded-full border text-[12px] font-medium transition-colors duration-100 outline-none whitespace-nowrap focus-visible:shadow-focus',
        ghost ? 'h-5 px-2' : 'h-control-xs px-2.5',
        active
          ? 'border-sel bg-sel text-sel-fg'
          : cx(ghost ? 'border-transparent' : 'border-line', 'text-fg-muted hover:bg-hover hover:text-fg'),
      )}
    >
      <span>{children}</span>
      {count != null && (
        <span className={cx('tabular-nums', active ? 'text-sel-fg/80' : ghost ? 'text-fg-faint' : 'text-fg-muted')}>{count}</span>
      )}
    </button>
  )
}

/* ───────────────────────── StatusDot ───────────────────────── */

const dotBg: Record<Tone, string> = {
  ok: 'bg-ok',
  info: 'bg-info',
  warn: 'bg-warn',
  danger: 'bg-danger',
  pending: 'bg-warn',
  neutral: 'bg-fg-faint',
}

export function StatusDot({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  return (
    <span className="relative inline-flex h-2 w-2 shrink-0">
      {pulse && <span className={cx('absolute inset-0 rounded-full opacity-60 wb-pulse', dotBg[tone])} />}
      <span className={cx('relative inline-flex h-2 w-2 rounded-full', dotBg[tone])} />
    </span>
  )
}

const SPIN_PERIOD_MS = 1000
const REVEAL_AFTER_MS = 300

/**
 * 旋转相位按文档时间线对齐，同屏多个图标同步转动。
 * 传入 since（请求开始时间，毫秒）时，在途不足 REVEAL_AFTER_MS 的请求延后淡入，快请求不闪烁；
 * 延迟按开始时间而非挂载时间计算，虚拟列表滚动重挂载时，已等待较久的行立即可见。
 */
export function LoadingSpinner({ className, since }: { className?: string; since?: number }) {
  const [delays] = useState(
    () =>
      ({
        '--wb-spin-delay': `${-(performance.now() % SPIN_PERIOD_MS)}ms`,
        '--wb-reveal-delay': `${since === undefined ? 0 : Math.min(REVEAL_AFTER_MS, REVEAL_AFTER_MS - (Date.now() - since))}ms`,
      }) as React.CSSProperties,
  )
  return <Loader2 aria-hidden className={cx('wb-spinner shrink-0', className)} style={delays} />
}

/** 与 StatusDot 占同样的 8px 盒子，两者切换时同行文字不位移；图标向两侧间距各溢出 2px。 */
export function StatusSpinner({ since }: { since?: number }) {
  return (
    <span className="relative inline-flex h-2 w-2 shrink-0">
      <LoadingSpinner since={since} className="absolute -left-0.5 -top-0.5 h-3 w-3 text-warn" />
    </span>
  )
}

/* ───────────────────────── MethodTag ───────────────────────── */

export function MethodTag({ method, className }: { method: string; className?: string }) {
  const color =
    {
      GET: 'text-method-get',
      POST: 'text-method-post',
      PUT: 'text-method-put',
      DELETE: 'text-method-delete',
      PATCH: 'text-method-patch',
    }[method.toUpperCase()] || 'text-method-other'
  return <span className={cx('font-mono text-[11px] font-semibold', color, className)}>{method.toUpperCase()}</span>
}

/* ───────────────────────── Kbd（快捷键提示） ───────────────────────── */

export function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded-wb-sm border border-line bg-inset px-1 py-px font-sans text-[11px] leading-none text-fg-faint">
      {children}
    </kbd>
  )
}

/* ───────────────────────── Divider ───────────────────────── */

export function Divider({ vertical, className }: { vertical?: boolean; className?: string }) {
  return vertical ? (
    <span className={cx('inline-block w-px self-stretch bg-line', className)} />
  ) : (
    <div className={cx('h-px w-full bg-line', className)} />
  )
}

/* ───────────────────────── Section（可折叠分区） ───────────────────────── */

interface SectionProps {
  title: React.ReactNode
  defaultOpen?: boolean
  right?: React.ReactNode
  count?: number
  children: React.ReactNode
  dense?: boolean
}

export function Section({ title, defaultOpen = true, right, count, children, dense }: SectionProps) {
  const [open, setOpen] = React.useState(defaultOpen)
  return (
    <div className="border-b border-line last:border-b-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex h-control-sm w-full items-center gap-1.5 px-3 text-left outline-none transition-colors hover:bg-hover focus-visible:shadow-focus"
      >
        <ChevronRight className={cx('h-3.5 w-3.5 shrink-0 text-fg-faint transition-transform', open && 'rotate-90')} />
        <span className="text-[12px] font-medium text-fg-muted">{title}</span>
        {count != null && <span className="text-[12px] tabular-nums text-fg-faint">{count}</span>}
        <span className="ml-auto flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {right}
        </span>
      </button>
      {open && <div className={cx(dense ? '' : 'pb-1')}>{children}</div>}
    </div>
  )
}

/* ───────────────────────── Tooltip ───────────────────────── */

type Placement = 'top' | 'bottom' | 'left' | 'right'

const tipPos: Record<Placement, string> = {
  top: 'bottom-full left-1/2 -translate-x-1/2 mb-1.5',
  bottom: 'top-full left-1/2 -translate-x-1/2 mt-1.5',
  left: 'right-full top-1/2 -translate-y-1/2 mr-1.5',
  right: 'left-full top-1/2 -translate-y-1/2 ml-1.5',
}

const TIP_EDGE_GAP = 6

export function Tooltip({
  label,
  placement = 'bottom',
  children,
}: {
  label: React.ReactNode
  placement?: Placement
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [offset, setOffset] = useState({ x: 0, y: 0 })
  const tipRef = useRef<HTMLSpanElement>(null)

  // 贴着窗口边缘的触发元素（如工具栏最右侧按钮）按默认居中定位会被窗口裁掉，
  // 显示时测一次实际位置，越界则沿边内收。
  useLayoutEffect(() => {
    if (!open || !tipRef.current) return
    const r = tipRef.current.getBoundingClientRect()
    const shift = (start: number, end: number, limit: number) => {
      if (end > limit - TIP_EDGE_GAP) return Math.max(limit - TIP_EDGE_GAP - end, TIP_EDGE_GAP - start)
      if (start < TIP_EDGE_GAP) return TIP_EDGE_GAP - start
      return 0
    }
    setOffset({
      x: shift(r.left, r.right, document.documentElement.clientWidth),
      y: shift(r.top, r.bottom, document.documentElement.clientHeight),
    })
  }, [open])

  const hide = () => {
    setOpen(false)
    setOffset({ x: 0, y: 0 })
  }

  return (
    <span className="relative inline-flex" onMouseEnter={() => setOpen(true)} onMouseLeave={hide}>
      {children}
      {open && (
        <span className={cx('pointer-events-none absolute z-50', tipPos[placement])}>
          <span
            ref={tipRef}
            className="relative block whitespace-nowrap rounded-lg border border-line bg-popover px-2 py-1 text-[12px] text-fg shadow-wb"
            style={{ left: offset.x, top: offset.y }}
          >
            {label}
          </span>
        </span>
      )}
    </span>
  )
}

/* ───────────────────────── ContentKindIcon ───────────────────────── */

const kindMeta: Record<ContentKind, { icon: LucideIcon; cls: string }> = {
  json: { icon: Braces, cls: 'text-method-put' },
  html: { icon: FileCode, cls: 'text-method-delete' },
  js: { icon: FileCode, cls: 'text-method-put' },
  css: { icon: Hash, cls: 'text-method-post' },
  image: { icon: ImageIcon, cls: 'text-method-get' },
  font: { icon: Type, cls: 'text-iris' },
  video: { icon: Film, cls: 'text-method-patch' },
  audio: { icon: Music, cls: 'text-method-patch' },
  text: { icon: FileText, cls: 'text-fg-muted' },
  doc: { icon: FileText, cls: 'text-info' },
  form: { icon: FileText, cls: 'text-method-post' },
  stream: { icon: Radio, cls: 'text-ok' },
  binary: { icon: Binary, cls: 'text-fg-muted' },
  other: { icon: FileIcon, cls: 'text-fg-faint' },
}

export function ContentKindIcon({ kind, className }: { kind: ContentKind; className?: string }) {
  const meta = kindMeta[kind] ?? kindMeta.other
  const Icon = meta.icon
  return <Icon className={cx('h-3.5 w-3.5 shrink-0', meta.cls, className)} />
}

/* ───────────────────────── ProcessAvatar ───────────────────────── */

const avatarPalette = ['bg-method-get/15 text-method-get', 'bg-method-post/15 text-method-post', 'bg-method-put/15 text-method-put', 'bg-method-patch/15 text-method-patch', 'bg-info/12 text-info']

export function ProcessAvatar({
  name,
  iconData,
  iconType,
  size = 16,
}: {
  name?: string
  iconData?: string
  iconType?: string
  size?: number
}) {
  if (iconData) {
    const mime = iconType === 'png' ? 'image/png' : iconType === 'svg' ? 'image/svg+xml' : 'image/x-icon'
    return (
      <img
        src={`data:${mime};base64,${iconData}`}
        alt={name || ''}
        width={size}
        height={size}
        className="shrink-0 rounded-[2px] object-contain"
      />
    )
  }
  const label = (name || '?').replace(/\.(exe|app)$/i, '')
  const initial = label.charAt(0).toUpperCase()
  const palette = avatarPalette[(label.charCodeAt(0) || 0) % avatarPalette.length]
  return (
    <span
      className={cx('inline-flex shrink-0 items-center justify-center rounded-[3px] text-[11px] font-bold', palette)}
      style={{ width: size, height: size }}
    >
      {initial}
    </span>
  )
}

/* ───────────────────────── 空态 ───────────────────────── */

export function EmptyState({ icon, title, hint }: { icon?: React.ReactNode; title: string; hint?: React.ReactNode }) {
  return (
    <div className="relative flex h-full flex-col items-center justify-center gap-2 px-6 text-center select-none">
      {/* 空态背景:向四周淡出的蓝图坐标纸方格 */}
      <div
        aria-hidden
        className="wb-grid pointer-events-none absolute inset-0 opacity-70"
        style={{
          maskImage: 'radial-gradient(ellipse at center, #000 0%, transparent 72%)',
          WebkitMaskImage: 'radial-gradient(ellipse at center, #000 0%, transparent 72%)',
        }}
      />
      {icon && <div className="relative text-fg-faint">{icon}</div>}
      <div className="relative text-[13px] font-medium text-fg-muted">{title}</div>
      {hint && <div className="relative max-w-xs text-[12px] leading-4 text-fg-faint">{hint}</div>}
    </div>
  )
}
