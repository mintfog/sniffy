import { useEffect, useRef, useState } from 'react'
import { KeyRound, X } from 'lucide-react'
import { Button } from './controls'
import { cx } from './primitives'

const INPUT_CLASS = cx(
  'h-7 rounded-wb border border-line bg-inset px-2 text-[12px] text-fg placeholder:text-fg-faint',
  'outline-none transition-colors focus:border-accent focus:bg-surface',
)

interface PasswordDialogProps {
  title: string
  message: string
  confirmLabel: string
  cancelLabel: string
  /** 需要两次确认(导出时设置口令),避免拼错口令导致导出文件不可用。 */
  requireConfirm?: boolean
  /** 允许空口令。默认 false(导出时必须设),导入若原 p12 无口令可置 true。 */
  allowEmpty?: boolean
  confirmMismatchLabel?: string
  busy?: boolean
  onSubmit: (password: string) => void
  onClose: () => void
}

/** 单口令(可选二次确认)输入弹窗;样式与 ConfirmDialog 对齐。 */
export function PasswordDialog({
  title,
  message,
  confirmLabel,
  cancelLabel,
  requireConfirm = false,
  allowEmpty = false,
  confirmMismatchLabel,
  busy,
  onSubmit,
  onClose,
}: PasswordDialogProps) {
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const mismatch = requireConfirm && pw !== '' && pw !== pw2
  const canSubmit = !busy && (allowEmpty || pw !== '') && !mismatch

  const requestClose = () => {
    if (!busy) onClose()
  }
  const submit = () => {
    if (canSubmit) onSubmit(pw)
  }

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, busy])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={requestClose}
      role="presentation"
    >
      <div
        className="w-full max-w-sm overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="alertdialog"
        aria-modal="true"
      >
        <header className="flex items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          <KeyRound className="h-4 w-4 text-accent" />
          <span className="text-[13px] font-semibold text-fg">{title}</span>
          <button
            type="button"
            onClick={requestClose}
            disabled={busy}
            className="ml-auto text-fg-faint hover:text-fg disabled:cursor-not-allowed disabled:opacity-50"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
          className="flex flex-col gap-2.5 px-4 py-4"
        >
          <p className="text-[12.5px] leading-relaxed text-fg-muted">{message}</p>
          <input
            ref={inputRef}
            type="password"
            value={pw}
            onChange={(e) => setPw(e.currentTarget.value)}
            placeholder="••••••••"
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            className={INPUT_CLASS}
          />
          {requireConfirm && (
            <input
              type="password"
              value={pw2}
              onChange={(e) => setPw2(e.currentTarget.value)}
              placeholder="••••••••"
              disabled={busy}
              autoComplete="new-password"
              spellCheck={false}
              className={INPUT_CLASS}
            />
          )}
          {mismatch && confirmMismatchLabel && (
            <span className="text-2xs text-danger">{confirmMismatchLabel}</span>
          )}
        </form>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button onClick={requestClose} disabled={busy}>
            {cancelLabel}
          </Button>
          <Button variant="primary" onClick={submit} disabled={!canSubmit}>
            {confirmLabel}
          </Button>
        </footer>
      </div>
    </div>
  )
}
