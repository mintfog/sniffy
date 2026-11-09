import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { FileDown, X } from 'lucide-react'
import type { ExportSanitizePrefs } from '../prefs'
import { Button, Toggle } from './controls'
import { cx } from './primitives'

function OptionRow({
  label,
  hint,
  checked,
  onChange,
  children,
}: {
  label: string
  hint?: string
  checked: boolean
  onChange: (value: boolean) => void
  children?: ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="text-[13px] font-medium text-fg">{label}</span>
          {hint && <span className="text-2xs leading-relaxed text-fg-muted">{hint}</span>}
        </div>
        <Toggle checked={checked} onChange={onChange} aria-label={label} />
      </div>
      {children}
    </div>
  )
}

interface ExportDialogProps {
  count: number
  initial: ExportSanitizePrefs
  onSubmit: (prefs: ExportSanitizePrefs) => void
  onClose: () => void
}

export function ExportDialog({ count, initial, onSubmit, onClose }: ExportDialogProps) {
  const { t } = useTranslation()
  const [prefs, setPrefs] = useState(initial)
  const update = (patch: Partial<ExportSanitizePrefs>) => setPrefs((prev) => ({ ...prev, ...patch }))

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-overlay p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="w-full max-w-md overflow-hidden rounded-lg border border-line bg-popover shadow-dialog"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="export-dialog-title"
      >
        <header className="flex items-center gap-2 border-b border-line px-4 py-3">
          <FileDown className="h-4 w-4 text-accent" />
          <span id="export-dialog-title" className="text-[13px] font-semibold text-fg">
            {t('workbench.exportDialog.title')}
          </span>
          <button
            type="button"
            onClick={onClose}
            aria-label={t('workbench.exportDialog.cancel')}
            title={t('workbench.exportDialog.cancel')}
            className="ml-auto text-fg-faint hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="flex flex-col gap-4 px-4 py-4">
          <p className="text-[13px] text-fg-muted">{t('workbench.exportDialog.summary', { count })}</p>
          <OptionRow
            label={t('workbench.exportDialog.redact')}
            hint={t('workbench.exportDialog.redactHint')}
            checked={prefs.redact}
            onChange={(redact) => update({ redact })}
          >
            <label className="flex flex-col gap-1.5">
              <span className={cx('text-[12px] text-fg', !prefs.redact && 'opacity-40')}>
                {t('workbench.exportDialog.fields')}
              </span>
              <input
                type="text"
                spellCheck={false}
                disabled={!prefs.redact}
                value={prefs.fields}
                placeholder={t('workbench.exportDialog.fieldsPlaceholder')}
                onChange={(event) => update({ fields: event.currentTarget.value })}
                className={cx(
                  'h-7 min-w-0 rounded-wb border border-line bg-inset px-2 text-[12px] text-fg placeholder:text-fg-faint',
                  'outline-none transition-colors focus:border-accent focus:bg-surface disabled:opacity-40',
                )}
              />
              <span className={cx('text-2xs text-fg-faint', !prefs.redact && 'opacity-40')}>
                {t('workbench.exportDialog.fieldsHint')}
              </span>
            </label>
          </OptionRow>
          <OptionRow
            label={t('workbench.exportDialog.omitBodies')}
            hint={t('workbench.exportDialog.omitBodiesHint')}
            checked={prefs.omitBodies}
            onChange={(omitBodies) => update({ omitBodies })}
          />
          <OptionRow
            label={t('workbench.exportDialog.omitClientInfo')}
            checked={prefs.omitClientInfo}
            onChange={(omitClientInfo) => update({ omitClientInfo })}
          />
          {prefs.redact && !prefs.omitBodies && (
            <p className="rounded-wb bg-inset px-3 py-2 text-2xs leading-relaxed text-fg-muted">
              {t('workbench.exportDialog.unstructuredNote')}
            </p>
          )}
        </div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button onClick={onClose}>{t('workbench.exportDialog.cancel')}</Button>
          <Button variant="primary" onClick={() => onSubmit(prefs)}>
            {t('workbench.exportDialog.confirm')}
          </Button>
        </footer>
      </div>
    </div>
  )
}
