import { type PointerEvent as ReactPointerEvent, type ReactNode, useState } from 'react'
import { Check, Copy } from 'lucide-react'
import type { Tone } from '../../lib/types'
import { cx } from '../../ui/primitives'

const tonePill: Record<Tone, string> = {
  ok: 'bg-ok/15 text-ok',
  info: 'bg-info/15 text-info',
  warn: 'bg-warn/15 text-warn',
  danger: 'bg-danger/15 text-danger',
  pending: 'bg-warn/15 text-warn',
  neutral: 'bg-fg-faint/15 text-fg-muted',
}

export function Pill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={cx('rounded-full px-2 py-[1px] font-mono text-2xs font-semibold', tonePill[tone])}>{children}</span>
}

export function ActionIcon({ title, onClick, children }: { title: string; onClick?: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      className="flex h-6 w-6 items-center justify-center rounded-control text-fg-faint transition hover:bg-elevated hover:text-fg hover:shadow-raise"
    >
      {children}
    </button>
  )
}

export function CopyIcon({ text, title }: { text: string; title: string }) {
  const [done, setDone] = useState(false)
  return (
    <ActionIcon title={title} onClick={() => navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1100) })}>
      {done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
    </ActionIcon>
  )
}

export function SplitBar({ onPointerDown }: { onPointerDown: (e: ReactPointerEvent) => void }) {
  return (
    <div
      onPointerDown={onPointerDown}
      className="group/vd flex h-[5px] shrink-0 cursor-row-resize items-center justify-center bg-line transition-colors hover:bg-accent"
    >
      <span className="h-[3px] w-8 rounded-full bg-fg-faint/40 group-hover/vd:bg-accent-fg/60" />
    </div>
  )
}
