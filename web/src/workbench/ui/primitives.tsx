import React from 'react'
import clsx from 'clsx'
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
  type LucideIcon,
  Music,
  Radio,
  Type,
} from 'lucide-react'
import type { ContentKind, Tone } from '../lib/types'

export const cx = clsx

/* ───────────────────────── SniffyMark（品牌标记） ───────────────────────── */

/** 品牌标记:线缆上的探针。用 currentColor 描边,随放置处的文字色走。 */
export function SniffyMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden
    >
      <path d="M3 17h18" />
      <path d="M12 14.6V7.8" />
      <circle cx="12" cy="5.6" r="2.2" />
      <circle cx="12" cy="17" r="1.5" fill="currentColor" stroke="none" />
    </svg>
  )
}

/* ───────────────────────── IconButton ───────────────────────── */

interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  active?: boolean
  size?: 'sm' | 'md'
  tone?: 'default' | 'accent' | 'danger'
}

export function IconButton({ active, size = 'md', tone = 'default', className, children, ...rest }: IconButtonProps) {
  return (
    <button
      type="button"
      className={cx(
        'inline-flex items-center justify-center rounded-control transition duration-100 outline-none',
        'focus-visible:ring-1 focus-visible:ring-accent disabled:opacity-40 disabled:pointer-events-none',
        size === 'sm' ? 'h-6 w-6' : 'h-7 w-7',
        active
          ? 'bg-accent/15 text-accent shadow-well'
          : tone === 'danger'
            ? 'text-fg-muted hover:bg-danger/15 hover:text-danger hover:shadow-raise'
            : tone === 'accent'
              ? 'text-accent hover:bg-accent/15 hover:shadow-raise'
              : 'text-fg-muted hover:bg-elevated hover:text-fg hover:shadow-raise',
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  )
}

/* ───────────────────────── Chip（过滤芯片） ───────────────────────── */

interface ChipProps {
  active?: boolean
  onClick?: () => void
  children: React.ReactNode
  count?: number
  title?: string
}

export function Chip({ active, onClick, children, count, title }: ChipProps) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      className={cx(
        'inline-flex h-[22px] items-center gap-1 rounded-control border px-2 text-2xs font-medium transition duration-100 outline-none whitespace-nowrap shadow-raise hover:shadow-raise-hover active:translate-y-px',
        active
          ? 'border-accent bg-accent text-accent-fg'
          : 'border-line bg-inset text-fg-muted hover:bg-elevated hover:text-fg',
      )}
    >
      <span>{children}</span>
      {count != null && (
        <span className={cx('tabular-nums', active ? 'text-accent-fg/80' : 'text-fg-faint')}>{count}</span>
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
  return <span className={cx('font-mono text-2xs font-semibold tracking-wide', color, className)}>{method.toUpperCase()}</span>
}

/* ───────────────────────── Kbd（快捷键提示） ───────────────────────── */

export function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded border border-line bg-inset px-1 py-px font-sans text-[10px] leading-none text-fg-faint">
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
        className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left transition-colors hover:bg-elevated/60"
      >
        <ChevronRight className={cx('h-3.5 w-3.5 shrink-0 text-fg-faint transition-transform', open && 'rotate-90')} />
        <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{title}</span>
        {count != null && <span className="text-2xs tabular-nums text-fg-faint">{count}</span>}
        <span className="ml-auto flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {right}
        </span>
      </button>
      {open && <div className={cx(dense ? '' : 'pb-1')}>{children}</div>}
    </div>
  )
}

/* ───────────────────────── Tooltip（CSS hover，无依赖） ───────────────────────── */

type Placement = 'top' | 'bottom' | 'left' | 'right'

const tipPos: Record<Placement, string> = {
  top: 'bottom-full left-1/2 -translate-x-1/2 mb-1.5',
  bottom: 'top-full left-1/2 -translate-x-1/2 mt-1.5',
  left: 'right-full top-1/2 -translate-y-1/2 mr-1.5',
  right: 'left-full top-1/2 -translate-y-1/2 ml-1.5',
}

export function Tooltip({
  label,
  placement = 'bottom',
  children,
}: {
  label: React.ReactNode
  placement?: Placement
  children: React.ReactNode
}) {
  return (
    <span className="group/tip relative inline-flex">
      {children}
      <span
        className={cx(
          'pointer-events-none absolute z-50 hidden whitespace-nowrap rounded-wb-sm border border-line bg-elevated px-2 py-1 text-[11px] text-fg shadow-wb group-hover/tip:block',
          tipPos[placement],
        )}
      >
        {label}
      </span>
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

const avatarPalette = ['bg-method-get/20 text-method-get', 'bg-method-post/20 text-method-post', 'bg-method-put/20 text-method-put', 'bg-method-patch/20 text-method-patch', 'bg-info/20 text-info']

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
      className={cx('inline-flex shrink-0 items-center justify-center rounded-[3px] text-[9px] font-bold', palette)}
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
      {icon && <div className="relative text-fg-faint opacity-60">{icon}</div>}
      <div className="relative text-sm font-medium text-fg-muted">{title}</div>
      {hint && <div className="relative max-w-xs text-2xs leading-relaxed text-fg-faint">{hint}</div>}
    </div>
  )
}
