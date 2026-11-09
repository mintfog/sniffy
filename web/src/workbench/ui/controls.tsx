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
  primary: 'bg-sel text-sel-fg hover:bg-sel-hover',
  secondary: 'border border-line text-fg hover:bg-hover [[data-theme=dark]_&]:bg-elevated/30 [[data-theme=dark]_&]:hover:bg-hover',
  ghost: 'text-fg-muted hover:bg-hover hover:text-fg',
  danger: 'bg-danger/10 text-danger hover:bg-danger/20',
}

export function Button({ variant = 'secondary', size = 'md', icon, className, children, ...rest }: ButtonProps) {
  return (
    <button
      type="button"
      className={cx(
        'inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-control font-medium transition-colors outline-none',
        'focus-visible:shadow-focus disabled:opacity-40 disabled:pointer-events-none',
        size === 'sm' ? 'h-control-sm px-2 text-[12px]' : 'h-control-md px-2.5 text-[13px]',
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

export function Toggle({
  checked,
  onChange,
  disabled,
  'aria-label': ariaLabel,
}: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  'aria-label'?: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-label={ariaLabel}
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        'relative inline-flex h-[18px] w-8 shrink-0 items-center rounded-full transition-colors outline-none',
        'focus-visible:shadow-focus disabled:opacity-40',
        checked ? 'bg-sel' : 'bg-line-strong',
      )}
    >
      <span
        className={cx(
          'inline-block h-3.5 w-3.5 transform rounded-full transition-transform',
          checked ? 'translate-x-[15px] bg-sel-fg' : 'translate-x-[2px] bg-fg-muted',
        )}
      />
    </button>
  )
}

/* ───────────────────────── TextInput ───────────────────────── */

interface TextInputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  width?: number | string
}

export const TextInput = React.forwardRef<HTMLInputElement, TextInputProps>(function TextInput(
  { width, className, ...rest },
  ref,
) {
  return (
    <input
      ref={ref}
      spellCheck={false}
      style={width ? { width } : undefined}
      className={cx(
        'h-control-md rounded-control border border-line bg-transparent px-2.5 text-[13px] text-fg placeholder:text-fg-faint',
        'outline-none transition-shadow focus:shadow-focus',
        className,
      )}
      {...rest}
    />
  )
})

/* ───────────────────────── Select ───────────────────────── */

interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  options: { value: string; label: string }[]
}

export function Select({ options, className, ...rest }: SelectProps) {
  return (
    <div className="relative inline-flex min-w-0">
      <select
        className={cx(
          'h-control-md min-w-0 appearance-none rounded-control border border-line bg-transparent pl-2.5 pr-7 text-[13px] text-fg',
          'outline-none transition-shadow focus-visible:shadow-focus',
          className,
        )}
        {...rest}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} className="bg-popover text-fg">
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
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
        <div className="text-[13px] text-fg">{label}</div>
        {hint && <div className="mt-0.5 text-[12px] leading-4 text-fg-muted">{hint}</div>}
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
        <header className="flex h-control-lg items-center gap-2 border-b border-line bg-inset px-3">
          {icon && <span className="text-fg-muted">{icon}</span>}
          <span className="text-[13px] font-medium text-fg">{title}</span>
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
    <div className={cx('inline-flex items-center gap-0.5 rounded-control bg-elevated p-0.5', className)}>
      {options.map((o) => (
        <button
          key={o.key}
          type="button"
          aria-pressed={value === o.key}
          onClick={() => onChange(o.key)}
          className={cx(
            'inline-flex h-control-xs items-center gap-1 rounded-wb-sm px-2.5 text-[12px] font-medium transition-colors outline-none focus-visible:shadow-focus',
            value === o.key ? 'bg-sel text-sel-fg' : 'text-fg-muted hover:text-fg',
          )}
        >
          {o.label}
          {o.count != null && (
            <span className={cx('tabular-nums', value === o.key ? 'text-sel-fg/80' : 'text-fg-faint')}>{o.count}</span>
          )}
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
  alt,
}: {
  rows: [string, string][]
  colLabels?: [string, string]
  emptyText?: string
  /**
   * 与 rows 等长下标对齐的字节渲染。alt[i] 提供第 i 行的 \xNN 视图，用户可切换显示形态。
   */
  alt?: (string | undefined)[]
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState<string | null>(null)
  // showAlt 是当前表格实例的临时状态，键包含行内容以区分同一位置的不同响应。
  const [showAlt, setShowAlt] = useState<Record<string, boolean>>({})

  const copy = (text: string, cell: string) => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(cell)
      setTimeout(() => setCopied((c) => (c === cell ? null : c)), 1100)
    })
  }

  if (rows.length === 0) {
    return <div className="px-3 py-3 text-2xs text-fg-faint">{emptyText ?? t('controls.kvTable.empty')}</div>
  }

  return (
    <div className="overflow-hidden">
      {colLabels && (
        <div className="grid grid-cols-2 border-b border-line bg-inset text-[12px] font-medium text-fg-muted">
          <div className="border-r border-line px-3 py-1.5">{colLabels[0]}</div>
          <div className="px-3 py-1.5">{colLabels[1]}</div>
        </div>
      )}
      <div>
        {rows.map(([k, v], i) => {
          const altText = alt?.[i]
          const altKey = `${i}-${k}-${altText ?? ''}`
          const escaped = !!altText && !!showAlt[altKey]
          const shown = altText && showAlt[altKey] ? altText : v
          return (
            <div key={`${k}-${i}`} className="grid grid-cols-2 border-b border-line/60 last:border-b-0">
              <button
                type="button"
                onClick={() => copy(k, `${i}-key`)}
                title={t('controls.kvTable.copyKeyTip')}
                className="group/kv flex min-w-0 items-start gap-1 border-r border-line/60 px-3 py-[5px] text-left transition-colors hover:bg-hover focus-visible:bg-hover focus-visible:outline-none"
              >
                <span className="min-w-0 flex-1 break-all font-mono text-[12px] text-iris">{k}</span>
                {copied === `${i}-key` ? (
                  <Check className="mt-px h-3 w-3 shrink-0 text-ok" />
                ) : (
                  <Copy className="mt-px h-3 w-3 shrink-0 text-fg-faint opacity-0 transition group-hover/kv:opacity-100 group-focus-visible/kv:opacity-100" />
                )}
              </button>
              <div className="flex min-w-0 items-start">
                {altText && (
                  // 徽标作为值单元格的独立控件，并从页内查找文本中排除。
                  <button
                    type="button"
                    data-find-skip
                    aria-pressed={escaped}
                    aria-label={t('bytes.badgeAria')}
                    title={t(escaped ? 'bytes.escapedTip' : 'bytes.latin1Tip')}
                    onClick={() => setShowAlt((m) => ({ ...m, [altKey]: !m[altKey] }))}
                    className="ml-3 mt-[6px] shrink-0 rounded-full bg-warn/12 px-1.5 font-mono text-[11px] font-medium leading-[14px] text-warn outline-none focus-visible:shadow-focus"
                  >
                    {t('bytes.badge')}
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => copy(shown, `${i}-value`)}
                  title={t('controls.kvTable.copyValueTip')}
                  className={cx(
                    'group/kv flex min-w-0 flex-1 items-start gap-1 py-[5px] pr-3 text-left transition-colors hover:bg-hover focus-visible:bg-hover focus-visible:outline-none',
                    altText ? 'pl-1.5' : 'pl-3',
                  )}
                >
                  <span className="min-w-0 flex-1 break-all font-mono text-[12px] text-fg-muted">{shown}</span>
                  {copied === `${i}-value` ? (
                    <Check className="mt-px h-3 w-3 shrink-0 text-ok" />
                  ) : (
                    <Copy className="mt-px h-3 w-3 shrink-0 text-fg-faint opacity-0 transition group-hover/kv:opacity-100 group-focus-visible/kv:opacity-100" />
                  )}
                </button>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/* ───────────────────────── JumpToLatestButton ───────────────────────── */

/** 列表上滚查看历史时浮在右下角的「回到最新」按钮；父容器需为定位上下文。 */
export function JumpToLatestButton({ onClick }: { onClick: () => void }) {
  const { t } = useTranslation()
  return (
    <button
      type="button"
      onClick={onClick}
      className="wb-fade absolute bottom-3 right-4 z-20 flex h-control-sm items-center gap-1 rounded-full border border-line bg-popover px-3 text-[12px] font-medium text-fg shadow-wb transition-colors hover:bg-hover"
    >
      <ChevronDown className="h-3.5 w-3.5" />
      {t('traffic.jumpToLatest')}
    </button>
  )
}
