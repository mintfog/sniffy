/**
 * 可编辑的头部表格。构造器与断点编辑器共用同一张表——两处呈现的是同一件东西
 * （即将写到线上的头部），排版若各写一套，用户就得在两个界面上分别学一次。
 *
 * 刻意只吃 rows/changed/onChange 而不吃 Draft：断点放行不经构造器的 Draft，
 * 也不需要它底部那条构造器专属的线缆读数。
 */
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { Chip, cx } from '../../ui/primitives'
import { appendHeader, normalizeHeaders, type HeaderRow } from './model'

export function HeaderTable({
  rows,
  changed,
  onChange,
  readOnly,
  /** 由出线侧按最终报文重算的头（小写名）：置灰并给出说明，改了也不会生效。 */
  managed,
  managedHint,
  suggestions,
}: {
  rows: HeaderRow[]
  changed: ReadonlySet<string>
  onChange: (rows: HeaderRow[]) => void
  readOnly?: boolean
  managed?: readonly string[]
  managedHint?: string
  suggestions?: [string, string][]
}) {
  const { t } = useTranslation()

  const edit = (id: string, patch: Partial<HeaderRow>) => {
    onChange(normalizeHeaders(rows.map((h) => (h.id === id ? { ...h, ...patch } : h))))
  }
  const remove = (id: string) => {
    onChange(normalizeHeaders(rows.filter((h) => h.id !== id)))
  }
  const isManaged = (name: string) => !!managed?.includes(name.trim().toLowerCase())

  return (
    <div className="flex h-full flex-col overflow-auto">
      <div className="sticky top-0 z-10 flex shrink-0 border-b border-line bg-inset/95 text-2xs font-semibold uppercase tracking-wide text-fg-muted backdrop-blur">
        <div className="w-[34%] shrink-0 border-r border-line px-3 py-1.5">{t('compose.req.nameCol')}</div>
        <div className="flex-1 px-3 py-1.5">{t('compose.req.valueCol')}</div>
        <div className="w-6 shrink-0" />
      </div>
      {rows.map((row) => {
        const blank = row.name === '' && row.value === ''
        const locked = readOnly || isManaged(row.name)
        return (
          <div key={row.id} className="group/hr relative flex shrink-0 items-stretch border-b border-line/60">
            {changed.has(row.id) && !blank && (
              <span aria-hidden className="absolute left-0 top-0 h-full w-[2px] bg-accent" />
            )}
            <input
              value={row.name}
              spellCheck={false}
              readOnly={locked}
              title={locked && !readOnly ? managedHint : undefined}
              onChange={(e) => edit(row.id, { name: e.target.value })}
              placeholder={blank ? t('compose.req.namePlaceholder') : ''}
              aria-label={t('compose.req.nameCol')}
              className={cx(
                'w-[34%] shrink-0 border-r border-line/60 bg-transparent px-3 py-[5px] font-mono text-[11.5px] text-iris outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:bg-elevated/60',
                locked && 'cursor-default opacity-60',
              )}
            />
            <input
              value={row.value}
              spellCheck={false}
              readOnly={locked}
              title={locked && !readOnly ? managedHint : undefined}
              onChange={(e) => edit(row.id, { value: e.target.value })}
              placeholder={blank ? t('compose.req.valuePlaceholder') : ''}
              aria-label={t('compose.req.valueCol')}
              className={cx(
                'min-w-0 flex-1 bg-transparent px-3 py-[5px] font-mono text-[11.5px] text-fg-muted outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:bg-elevated/60 focus:text-fg',
                locked && 'cursor-default opacity-60',
              )}
            />
            <button
              type="button"
              onClick={() => remove(row.id)}
              disabled={blank || locked}
              title={t('compose.req.removeHeader')}
              aria-label={t('compose.req.removeHeader')}
              className="flex w-6 shrink-0 items-center justify-center text-fg-faint opacity-0 transition hover:text-danger focus-visible:opacity-100 focus-visible:outline-none group-hover/hr:opacity-100 disabled:invisible"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        )
      })}
      {!readOnly && suggestions && suggestions.length > 0 && (
        <CommonHeaders
          items={suggestions}
          onAdd={(name, value) => onChange(appendHeader(rows, name, value))}
        />
      )}
    </div>
  )
}

function CommonHeaders({
  items,
  onAdd,
}: {
  items: [string, string][]
  onAdd: (name: string, value: string) => void
}) {
  const { t } = useTranslation()
  return (
    <div className="relative flex-1 px-3 pb-4 pt-5">
      <div
        aria-hidden
        className="wb-grid pointer-events-none absolute inset-0 opacity-60"
        style={{
          maskImage: 'linear-gradient(to bottom, #000 0%, transparent 70%)',
          WebkitMaskImage: 'linear-gradient(to bottom, #000 0%, transparent 70%)',
        }}
      />
      <div className="relative text-2xs font-semibold uppercase tracking-wide text-fg-faint">
        {t('compose.req.commonHeaders')}
      </div>
      <div className="relative mt-2 flex flex-wrap gap-1.5">
        {items.map(([name, value]) => (
          <Chip key={name} title={`${name}: ${value}`} onClick={() => onAdd(name, value)}>
            {name}
          </Chip>
        ))}
      </div>
    </div>
  )
}
