import {
  Activity,
  CircleDot,
  Puzzle,
  Settings,
  ShieldCheck,
  Shuffle,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { CountBadge, cx, Tooltip } from '../ui/primitives'

export type WorkbenchView = 'traffic' | 'rules' | 'breakpoints' | 'plugins' | 'certs' | 'settings'

interface RailItem {
  key: WorkbenchView
  icon: LucideIcon
  label: string
  /** >0 时在图标右上角挂一枚计数角标。 */
  badge?: number
}

const TOP: { key: WorkbenchView; icon: LucideIcon }[] = [
  { key: 'traffic', icon: Activity },
  { key: 'rules', icon: Shuffle },
  { key: 'breakpoints', icon: CircleDot },
  { key: 'plugins', icon: Puzzle },
  { key: 'certs', icon: ShieldCheck },
]

const BOTTOM: { key: WorkbenchView; icon: LucideIcon }[] = [{ key: 'settings', icon: Settings }]

function RailButton({ item, active, onClick }: { item: RailItem; active: boolean; onClick: () => void }) {
  const Icon = item.icon
  return (
    <Tooltip label={item.label} placement="right">
      <button
        type="button"
        onClick={onClick}
        className={cx(
          'relative flex h-control-lg w-control-lg items-center justify-center rounded-control transition-colors duration-100 outline-none',
          'focus-visible:shadow-focus',
          active ? 'bg-line text-accent' : 'text-fg-muted hover:bg-line hover:text-fg',
        )}
      >
        {active && <span className="absolute left-[-6px] top-1/2 h-4 w-[2px] -translate-y-1/2 rounded-r bg-accent" />}
        <Icon className="relative h-4 w-4" strokeWidth={active ? 2 : 1.75} />
        <CountBadge count={item.badge ?? 0} />
      </button>
    </Tooltip>
  )
}

export function IconRail({
  view,
  onChange,
  badges,
}: {
  view: WorkbenchView
  onChange: (v: WorkbenchView) => void
  badges?: Partial<Record<WorkbenchView, number>>
}) {
  const { t } = useTranslation()
  const labels: Record<WorkbenchView, string> = {
    traffic: t('iconRail.traffic'),
    rules: t('iconRail.rules'),
    breakpoints: t('iconRail.breakpoints'),
    plugins: t('iconRail.plugins'),
    certs: t('iconRail.certs'),
    settings: t('iconRail.settings'),
  }
  return (
    <nav className="flex h-full w-12 flex-col items-center gap-1 border-r border-line bg-base py-2">
      {TOP.map((it) => (
        <RailButton
          key={it.key}
          item={{ ...it, label: labels[it.key], badge: badges?.[it.key] }}
          active={view === it.key}
          onClick={() => onChange(it.key)}
        />
      ))}
      <div className="flex-1" />
      {BOTTOM.map((it) => (
        <RailButton
          key={it.key}
          item={{ ...it, label: labels[it.key] }}
          active={view === it.key}
          onClick={() => onChange(it.key)}
        />
      ))}
    </nav>
  )
}
