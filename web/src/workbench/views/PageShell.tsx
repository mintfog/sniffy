import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import { cx } from '../ui/primitives'

/** 内页统一外壳：顶部标题条 + 可滚动内容区（表单类内容居中限宽）。 */
export function PageShell({
  icon: Icon,
  title,
  subtitle,
  actions,
  children,
  contentWidth = 860,
}: {
  icon: LucideIcon
  title: string
  subtitle?: string
  actions?: ReactNode
  children: ReactNode
  contentWidth?: number | 'full'
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-base">
      <header className="flex h-9 shrink-0 items-center gap-2 border-b border-line px-3">
        <Icon className="h-4 w-4 text-accent" />
        <span className="text-[12.5px] font-semibold text-fg">{title}</span>
        {subtitle && <span className="truncate text-2xs text-fg-faint">· {subtitle}</span>}
        <div className="ml-auto flex items-center gap-1.5">{actions}</div>
      </header>
      <div className="min-h-0 flex-1 overflow-auto">
        <div
          className={cx('mx-auto flex flex-col gap-4 p-4', contentWidth === 'full' && 'h-full')}
          style={contentWidth === 'full' ? undefined : { maxWidth: contentWidth }}
        >
          {children}
        </div>
      </div>
    </div>
  )
}
