import { useEffect } from 'react'
import { AlertTriangle, X } from 'lucide-react'
import { Button } from './controls'

interface ConfirmDialogProps {
  title: string
  message: string
  confirmLabel: string
  cancelLabel: string
  /** danger 用红色确认按钮(删除等破坏性操作),primary 用强调色。 */
  tone?: 'danger' | 'primary'
  busy?: boolean
  onConfirm: () => void
  onClose: () => void
}

/** 应用内确认弹窗,替代浏览器原生 confirm();样式与 NewPluginModal 一致。 */
export function ConfirmDialog({ title, message, confirmLabel, cancelLabel, tone = 'danger', busy, onConfirm, onClose }: ConfirmDialogProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose} role="presentation">
      <div
        className="w-full max-w-sm overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="alertdialog"
        aria-modal="true"
      >
        <header className="flex items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          {tone === 'danger' && <AlertTriangle className="h-4 w-4 text-danger" />}
          <span className="text-[13px] font-semibold text-fg">{title}</span>
          <button type="button" onClick={onClose} className="ml-auto text-fg-faint hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="px-4 py-4 text-[12.5px] leading-relaxed text-fg-muted">{message}</div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          {/* 默认聚焦取消:破坏性操作不让回车误触确认。 */}
          <Button autoFocus onClick={onClose}>
            {cancelLabel}
          </Button>
          <Button variant={tone} onClick={onConfirm} disabled={busy}>
            {confirmLabel}
          </Button>
        </footer>
      </div>
    </div>
  )
}
