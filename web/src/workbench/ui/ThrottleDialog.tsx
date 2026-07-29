import { useEffect, useRef, useState } from 'react'
import { Gauge, X } from 'lucide-react'
import {
  MAX_THROTTLE_KIBPS,
  MIN_THROTTLE_KIBPS,
  normalizeThrottleKiBps,
} from '../prefs'
import { Button } from './controls'
import { cx } from './primitives'

const INPUT_CLASS = cx(
  'h-7 min-w-0 flex-1 rounded-wb border border-line bg-inset px-2 text-[12px] text-fg',
  'outline-none transition-colors focus:border-accent focus:bg-surface'
)

interface ThrottleDialogProps {
  initialValue: string
  title: string
  message: string
  rateLabel: string
  rangeLabel: string
  invalidLabel: string
  confirmLabel: string
  cancelLabel: string
  onSubmit: (rateKiBps: number) => void
  onClose: () => void
}

export function ThrottleDialog({
  initialValue,
  title,
  message,
  rateLabel,
  rangeLabel,
  invalidLabel,
  confirmLabel,
  cancelLabel,
  onSubmit,
  onClose,
}: ThrottleDialogProps) {
  const [value, setValue] = useState(() => normalizeThrottleKiBps(initialValue))
  const inputRef = useRef<HTMLInputElement>(null)
  const parsed = Number(value)
  const valid =
    Number.isInteger(parsed) &&
    parsed >= MIN_THROTTLE_KIBPS &&
    parsed <= MAX_THROTTLE_KIBPS

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const submit = () => {
    if (valid) onSubmit(parsed)
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="w-full max-w-sm overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={event => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="throttle-dialog-title"
      >
        <header className="flex items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          <Gauge className="h-4 w-4 text-accent" />
          <span
            id="throttle-dialog-title"
            className="text-[13px] font-semibold text-fg"
          >
            {title}
          </span>
          <button
            type="button"
            onClick={onClose}
            aria-label={cancelLabel}
            title={cancelLabel}
            className="ml-auto text-fg-faint hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <form
          onSubmit={event => {
            event.preventDefault()
            submit()
          }}
        >
          <div className="flex flex-col gap-3 px-4 py-4">
            <p className="text-[12.5px] leading-relaxed text-fg-muted">
              {message}
            </p>
            <label className="flex flex-col gap-1.5">
              <span className="text-[12px] font-medium text-fg">
                {rateLabel}
              </span>
              <span className="flex items-center gap-2">
                <input
                  ref={inputRef}
                  type="number"
                  min={MIN_THROTTLE_KIBPS}
                  max={MAX_THROTTLE_KIBPS}
                  step={1}
                  inputMode="numeric"
                  value={value}
                  onChange={event => setValue(event.currentTarget.value)}
                  aria-invalid={!valid}
                  aria-describedby="throttle-rate-help"
                  className={cx(
                    INPUT_CLASS,
                    !valid && 'border-danger focus:border-danger'
                  )}
                />
                <span className="shrink-0 font-mono text-2xs text-fg-muted">
                  KiB/s
                </span>
              </span>
            </label>
            <span
              id="throttle-rate-help"
              className={cx(
                'text-2xs',
                valid ? 'text-fg-faint' : 'text-danger'
              )}
            >
              {valid ? rangeLabel : invalidLabel}
            </span>
          </div>
          <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
            <Button onClick={onClose}>{cancelLabel}</Button>
            <Button variant="primary" onClick={submit} disabled={!valid}>
              {confirmLabel}
            </Button>
          </footer>
        </form>
      </div>
    </div>
  )
}
