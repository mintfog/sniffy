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
import { cx, Tooltip } from '../ui/primitives'

export type WorkbenchView = 'traffic' | 'rules' | 'breakpoints' | 'plugins' | 'certs' | 'settings'

interface RailItem {
  key: WorkbenchView
  icon: LucideIcon
  label: string
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
          'relative flex h-9 w-9 items-center justify-center rounded-wb transition-colors duration-100 outline-none',
          active ? 'text-accent' : 'text-fg-faint hover:bg-elevated hover:text-fg',
        )}
      >
        {active && <span className="absolute left-[-7px] top-1/2 h-4 w-[3px] -translate-y-1/2 rounded-r bg-accent" />}
        <span className={cx('absolute inset-0 rounded-wb', active && 'bg-accent/12')} />
        <Icon className="relative h-[18px] w-[18px]" strokeWidth={active ? 2.2 : 1.9} />
      </button>
    </Tooltip>
  )
}

export function IconRail({ view, onChange }: { view: WorkbenchView; onChange: (v: WorkbenchView) => void }) {
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
    <nav className="flex h-full w-12 flex-col items-center gap-1 border-r border-line bg-surface py-2">
      {TOP.map((it) => (
        <RailButton
          key={it.key}
          item={{ ...it, label: labels[it.key] }}
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
