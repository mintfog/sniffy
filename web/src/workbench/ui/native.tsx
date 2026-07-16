import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cx, StatusDot } from './primitives'

/*
 * 独立系统窗口的「原生主从」骨架：侧栏(源列表) + 内容区 + 底部状态栏。
 * 刻意避开网页式卡片/大圆角，改用整幅分组、发丝分割线与紧凑密度，贴近桌面原生工具。
 * 插件窗口与重写规则窗口共用同一套,保证两个独立窗口观感一致。
 */

/** 主从分栏：左侧栏 + 右内容,底部贴一条全宽状态栏。 */
export function SplitView({
  sidebar,
  status,
  children,
}: {
  sidebar: ReactNode
  status?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-base">
      <div className="flex min-h-0 flex-1 overflow-hidden">
        {sidebar}
        <main className="flex min-w-0 flex-1 flex-col bg-surface">{children}</main>
      </div>
      {status}
    </div>
  )
}

/** 源列表侧栏：可选头(标题/搜索) + 滚动列表 + 可选脚注(增删按钮)。底色比内容区略沉,形成原生分栏层次。 */
export function Sidebar({
  header,
  footer,
  children,
  width = 248,
}: {
  header?: ReactNode
  footer?: ReactNode
  children: ReactNode
  width?: number
}) {
  return (
    <aside className="flex shrink-0 flex-col border-r border-line bg-inset/40" style={{ width }}>
      {header && (
        <header className="flex h-8 shrink-0 items-center gap-2 border-b border-line px-2.5">{header}</header>
      )}
      <div className="min-h-0 flex-1 overflow-auto">{children}</div>
      {footer && (
        <footer className="flex h-9 shrink-0 items-center gap-1.5 border-t border-line px-2">{footer}</footer>
      )}
    </aside>
  )
}

/** 源列表行：选中态为实色强调底 + 强制高对比前景(wb-row-selected)。leading 内的交互(开关)不冒泡触发选中。 */
export function SidebarItem({
  active,
  dimmed,
  leading,
  title,
  subtitle,
  trailing,
  onClick,
}: {
  active: boolean
  dimmed?: boolean
  leading?: ReactNode
  title: ReactNode
  subtitle?: ReactNode
  trailing?: ReactNode
  onClick: () => void
}) {
  // leading 槽常放 Toggle(本身是 button);整行用 div+role=button 承载,避免 button 嵌 button 的非法结构。
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onClick()
        }
      }}
      className={cx(
        'flex w-full cursor-default items-center gap-2 px-2.5 py-1.5 text-left outline-none transition-colors',
        // 选中用实色 accent（wb-row-selected 强制前景）：微染底与列表底对比不足，选中态难辨认
        active ? 'wb-row-selected bg-accent' : 'hover:bg-elevated/50 focus-visible:bg-elevated/40',
      )}
    >
      {leading && (
        <span
          className="shrink-0"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
          role="presentation"
        >
          {leading}
        </span>
      )}
      <span className="min-w-0 flex-1">
        <span className={cx('block truncate text-[12.5px]', dimmed ? 'text-fg-muted' : 'text-fg')}>{title}</span>
        {subtitle != null && <span className="mt-px block truncate text-2xs text-fg-faint">{subtitle}</span>}
      </span>
      {trailing != null && <span className="shrink-0">{trailing}</span>}
    </div>
  )
}

/** 内容区顶部条:承载当前对象标识 + 视图切换 + 主操作。比网页页头更矮更紧。 */
export function DetailBar({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <header className={cx('flex h-10 shrink-0 items-center gap-2 border-b border-line px-3', className)}>
      {children}
    </header>
  )
}

/** 原生分组:整幅(无外框卡片),组头沉底色 + 发丝线。body 默认不分隔,需分隔行时传 bodyClassName="divide-y divide-line/60"。 */
export function FieldGroup({
  title,
  icon,
  right,
  children,
  bodyClassName,
  className,
}: {
  title?: ReactNode
  icon?: ReactNode
  right?: ReactNode
  children: ReactNode
  bodyClassName?: string
  className?: string
}) {
  return (
    <section className={cx('border-b border-line', className)}>
      {title != null && (
        <header className="flex h-8 items-center gap-2 bg-inset/40 px-3">
          {icon && <span className="text-fg-faint">{icon}</span>}
          <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{title}</span>
          {right != null && <span className="ml-auto flex items-center gap-1.5">{right}</span>}
        </header>
      )}
      <div className={bodyClassName}>{children}</div>
    </section>
  )
}

/** 底部状态栏:左侧自由内容 + 右侧后端连通指示。 */
export function StatusFooter({ left, right }: { left?: ReactNode; right?: ReactNode }) {
  return (
    <footer className="flex h-6 shrink-0 items-center gap-3 border-t border-line bg-inset/40 px-3 text-2xs text-fg-muted select-none">
      {left != null && <span className="min-w-0 truncate">{left}</span>}
      {right != null && <span className="ml-auto flex items-center gap-1.5">{right}</span>}
    </footer>
  )
}

/** 后端连通指示:桌面窗口里以首次数据加载是否成功近似「已连接」。 */
export function ConnIndicator({ connected }: { connected: boolean }) {
  const { t } = useTranslation()
  return (
    <span className={cx('flex items-center gap-1.5', connected ? 'text-fg-muted' : 'text-fg-faint')}>
      <StatusDot tone={connected ? 'ok' : 'neutral'} />
      {connected ? t('statusBar.live') : t('statusBar.offline')}
    </span>
  )
}
