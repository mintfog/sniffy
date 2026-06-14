import React, { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, ChevronDown, Copy } from 'lucide-react'
import { cx } from './primitives'

/* ───────────────────────── Button ───────────────────────── */

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: 'sm' | 'md'
  icon?: React.ReactNode
}

const variantCls: Record<ButtonVariant, string> = {
  primary: 'bg-accent text-accent-fg hover:bg-accent-hover',
  secondary: 'bg-inset text-fg border border-line hover:bg-elevated',
  ghost: 'text-fg-muted hover:bg-elevated hover:text-fg',
  danger: 'bg-danger/15 text-danger hover:bg-danger/25',
}

export function Button({ variant = 'secondary', size = 'md', icon, className, children, ...rest }: ButtonProps) {
  return (
    <button
      type="button"
      className={cx(
        'inline-flex items-center justify-center gap-1.5 rounded-wb font-medium transition-colors outline-none',
        'focus-visible:ring-1 focus-visible:ring-accent disabled:opacity-40 disabled:pointer-events-none',
        size === 'sm' ? 'h-6 px-2 text-2xs' : 'h-7 px-3 text-[12px]',
        variantCls[variant],
        className,
      )}
      {...rest}
    >
      {icon}
      {children}
    </button>
  )
}

/* ───────────────────────── Toggle ───────────────────────── */

export function Toggle({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        'relative inline-flex h-[18px] w-8 shrink-0 items-center rounded-full transition-colors outline-none',
        'focus-visible:ring-1 focus-visible:ring-accent disabled:opacity-40',
        checked ? 'bg-accent' : 'bg-line-strong',
      )}
    >
      <span
        className={cx(
          'inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform',
          checked ? 'translate-x-[15px]' : 'translate-x-[2px]',
        )}
      />
    </button>
  )
}

/* ───────────────────────── TextInput ───────────────────────── */

interface TextInputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  width?: number | string
}

export function TextInput({ width, className, ...rest }: TextInputProps) {
  return (
    <input
      spellCheck={false}
      style={width ? { width } : undefined}
      className={cx(
        'h-7 rounded-wb border border-line bg-inset px-2 text-[12px] text-fg placeholder:text-fg-faint',
        'outline-none transition-colors focus:border-accent focus:bg-surface',
        className,
      )}
      {...rest}
    />
  )
}

/* ───────────────────────── Select ───────────────────────── */

interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  options: { value: string; label: string }[]
}

export function Select({ options, className, ...rest }: SelectProps) {
  return (
    <div className="relative inline-flex">
      <select
        className={cx(
          'h-7 appearance-none rounded-wb border border-line bg-inset pl-2 pr-7 text-[12px] text-fg',
          'outline-none transition-colors focus:border-accent',
          className,
        )}
        {...rest}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} className="bg-surface text-fg">
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-1.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
    </div>
  )
}

/* ───────────────────────── Field（设置行：左标签+描述 / 右控件） ───────────────────────── */

export function Field({
  label,
  hint,
  children,
}: {
  label: React.ReactNode
  hint?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-4 px-3 py-2">
      <div className="min-w-0">
        <div className="text-[12.5px] text-fg">{label}</div>
        {hint && <div className="mt-0.5 text-2xs leading-relaxed text-fg-faint">{hint}</div>}
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  )
}

/* ───────────────────────── Panel（带标题的卡片/分组） ───────────────────────── */

export function Panel({
  title,
  icon,
  right,
  children,
  className,
  bodyClassName,
}: {
  title?: React.ReactNode
  icon?: React.ReactNode
  right?: React.ReactNode
  children: React.ReactNode
  className?: string
  bodyClassName?: string
}) {
  return (
    <section className={cx('overflow-hidden rounded-wb border border-line bg-surface', className)}>
      {title && (
        <header className="flex h-9 items-center gap-2 border-b border-line bg-inset/50 px-3">
          {icon && <span className="text-accent">{icon}</span>}
          <span className="text-[12px] font-semibold text-fg">{title}</span>
          <span className="ml-auto flex items-center gap-1.5">{right}</span>
        </header>
      )}
      <div className={cx('divide-y divide-line', bodyClassName)}>{children}</div>
    </section>
  )
}

/* ───────────────────────── SegTabs（分段子页签） ───────────────────────── */

export function SegTabs<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T
  onChange: (v: T) => void
  options: { key: T; label: React.ReactNode; count?: number }[]
  className?: string
}) {
  return (
    <div className={cx('inline-flex items-center gap-0.5 rounded-wb bg-inset p-0.5', className)}>
      {options.map((o) => (
        <button
          key={o.key}
          type="button"
          onClick={() => onChange(o.key)}
          className={cx(
            'inline-flex h-6 items-center gap-1 rounded-wb-sm px-2.5 text-2xs font-medium transition-colors outline-none',
            value === o.key ? 'bg-surface text-fg shadow-sm' : 'text-fg-muted hover:text-fg',
          )}
        >
          {o.label}
          {o.count != null && <span className="tabular-nums text-fg-faint">{o.count}</span>}
        </button>
      ))}
    </div>
  )
}

/* ───────────────────────── KVTable（左右等宽，单击即复制） ───────────────────────── */

export function KVTable({
  rows,
  colLabels,
  emptyText,
}: {
  rows: [string, string][]
  colLabels?: [string, string]
  emptyText?: string
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState<number | null>(null)

  const copy = (text: string, i: number) => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(i)
      setTimeout(() => setCopied((c) => (c === i ? null : c)), 1100)
    })
  }

  if (rows.length === 0) {
    return <div className="px-3 py-3 text-2xs text-fg-faint">{emptyText ?? t('controls.kvTable.empty')}</div>
  }

  return (
    <div className="overflow-hidden">
      {colLabels && (
        <div className="grid grid-cols-2 border-b border-line bg-inset/60 text-2xs font-semibold uppercase tracking-wide text-fg-faint">
          <div className="border-r border-line px-3 py-1.5">{colLabels[0]}</div>
          <div className="px-3 py-1.5">{colLabels[1]}</div>
        </div>
      )}
      <div>
        {rows.map(([k, v], i) => (
          <button
            key={`${k}-${i}`}
            type="button"
            onClick={() => copy(v, i)}
            title={t('controls.kvTable.copyValueTip')}
            className="group/kv grid w-full grid-cols-2 border-b border-line/60 text-left transition-colors last:border-b-0 hover:bg-elevated/50"
          >
            <div className="break-all border-r border-line/60 px-3 py-[5px] font-mono text-[11.5px] text-iris">{k}</div>
            <div className="relative flex min-w-0 items-start gap-1 px-3 py-[5px]">
              <span className="min-w-0 flex-1 break-all font-mono text-[11.5px] text-fg-muted">{v}</span>
              {copied === i ? (
                <Check className="mt-px h-3 w-3 shrink-0 text-ok" />
              ) : (
                <Copy className="mt-px h-3 w-3 shrink-0 text-fg-faint opacity-0 transition group-hover/kv:opacity-100" />
              )}
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}
