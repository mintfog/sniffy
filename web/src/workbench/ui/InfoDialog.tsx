import { useEffect } from 'react'
import { AlertCircle, CheckCircle2, Info, X } from 'lucide-react'
import { Button } from './controls'

interface InfoDialogProps {
  title: string
  message: string
  /** success 显示绿色勾;error 显示红色告警;info 显示中性图标。 */
  tone?: 'success' | 'error' | 'info'
  okLabel: string
  onClose: () => void
}

/** 单按钮结果弹窗:样式与 ConfirmDialog 对齐,用于一次性成功/失败提示。 */
export function InfoDialog({ title, message, tone = 'info', okLabel, onClose }: InfoDialogProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' || e.key === 'Enter') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const Icon = tone === 'success' ? CheckCircle2 : tone === 'error' ? AlertCircle : Info
  const iconColor = tone === 'success' ? 'text-ok' : tone === 'error' ? 'text-danger' : 'text-accent'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose} role="presentation">
      <div
        className="w-full max-w-sm overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="alertdialog"
        aria-modal="true"
      >
        <header className="flex items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          <Icon className={`h-4 w-4 ${iconColor}`} />
          <span className="text-[13px] font-semibold text-fg">{title}</span>
          <button type="button" onClick={onClose} className="ml-auto text-fg-faint hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="px-4 py-4 text-[12.5px] leading-relaxed text-fg-muted whitespace-pre-wrap break-words">
          {message}
        </div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button autoFocus variant="primary" onClick={onClose}>
            {okLabel}
          </Button>
        </footer>
      </div>
    </div>
  )
}
