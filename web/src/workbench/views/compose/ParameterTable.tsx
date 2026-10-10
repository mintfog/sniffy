import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { cx } from '../../ui/primitives'
import { isBlankParameter, type ParameterRow } from './parameters'

function restoreLineSeparator(value: string, previous: string): string {
  // textarea 将 CRLF/CR 读作 LF，回写时恢复该参数原有的换行形式。
  const separator = previous.match(/\r\n|\r|\n/)?.[0]
  return separator ? value.replace(/\n/g, separator) : value
}

export function ParameterTable({
  rows,
  onChange,
  label,
  hint,
}: {
  rows: ParameterRow[]
  onChange: (rows: ParameterRow[]) => void
  label: string
  hint?: string
}) {
  const { t } = useTranslation()
  const hintId = useId()
  const edit = (id: string, patch: Partial<ParameterRow>) => {
    onChange(rows.map(row => (row.id === id ? { ...row, ...patch } : row)))
  }
  const remove = (id: string) => {
    onChange(rows.filter(row => row.id !== id))
  }

  return (
    <div
      role="group"
      aria-label={label}
      aria-describedby={hint ? hintId : undefined}
      className="flex h-full min-w-0 flex-col overflow-auto"
    >
      <div className="sticky top-0 z-10 flex shrink-0 border-b border-line bg-inset/95 text-2xs font-semibold uppercase tracking-wide text-fg-muted backdrop-blur">
        <div className="w-8 shrink-0" />
        <div className="w-[34%] shrink-0 border-r border-line px-2 py-1.5">
          {t('compose.body.name')}
        </div>
        <div className="min-w-0 flex-1 px-2 py-1.5">
          {t('compose.body.value')}
        </div>
        <div className="w-7 shrink-0" />
      </div>
      {rows.map((row, index) => {
        const blank = isBlankParameter(row)
        return (
          <div
            key={row.id}
            className="group/param flex shrink-0 items-stretch border-b border-line/60"
          >
            <div className="flex w-8 shrink-0 items-center justify-center">
              <input
                type="checkbox"
                checked={row.enabled}
                disabled={blank}
                onChange={event =>
                  edit(row.id, { enabled: event.target.checked })
                }
                aria-label={`${t('compose.body.enabled')} ${index + 1}`}
                className="h-3 w-3 cursor-pointer accent-accent disabled:invisible"
              />
            </div>
            {(['name', 'value'] as const).map(field => (
              <textarea
                key={field}
                value={row[field].replace(/\r\n?/g, '\n')}
                rows={Math.min(5, row[field].split(/\r\n|\r|\n/).length)}
                wrap="off"
                spellCheck={false}
                onChange={event =>
                  edit(row.id, {
                    [field]: restoreLineSeparator(
                      event.target.value,
                      row[field]
                    ),
                  })
                }
                placeholder={blank ? t(`compose.body.${field}Placeholder`) : ''}
                aria-label={`${t(`compose.body.${field}`)} ${index + 1}`}
                className={cx(
                  'min-w-0 resize-none overflow-auto bg-transparent px-2 py-[7px] font-mono text-[11.5px] leading-[18px] outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:bg-elevated/60',
                  field === 'name'
                    ? 'w-[34%] shrink-0 border-r border-line/60 text-iris'
                    : 'flex-1 text-fg-muted focus:text-fg',
                  !row.enabled && 'opacity-50'
                )}
              />
            ))}
            <button
              type="button"
              onClick={() => remove(row.id)}
              disabled={blank}
              title={t('compose.body.remove')}
              aria-label={`${t('compose.body.remove')} ${index + 1}`}
              className="flex w-7 shrink-0 items-center justify-center text-fg-faint opacity-0 transition hover:text-danger focus-visible:opacity-100 focus-visible:outline-none group-hover/param:opacity-100 disabled:invisible"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        )
      })}
      {hint && (
        <p
          id={hintId}
          className="px-3 py-3 text-2xs leading-relaxed text-fg-faint"
        >
          {hint}
        </p>
      )}
    </div>
  )
}
