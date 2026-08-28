/**
 * 可编辑的头部表格，供构造器与断点编辑器共用。
 * 组件接收行数据、改动集合和更新回调，不依赖具体草稿模型。
 */
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { Chip, cx } from '../../ui/primitives'
import { escapeForBytesMode, hasByteEscape, latin1FromBytes, unescapeForTextMode } from '../../../lib/headerBytes.ts'
import { appendHeader, normalizeHeaders, rowBytes, type HeaderRow } from './model'

export function HeaderTable({
  rows,
  changed,
  onChange,
  readOnly,
  /** 由出线侧按最终报文重算的头（小写名），界面上置灰。 */
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
  // text→bytes 与 bytes→text 使用互逆的转义规则；含字节转义的行保持 bytes 模式。
  const toBytes = (row: HeaderRow) => edit(row.id, { enc: 'bytes', value: escapeForBytesMode(row.value) })
  const toText = (row: HeaderRow) => edit(row.id, { enc: 'text', value: unescapeForTextMode(row.value) })

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
        const bytesMode = row.enc === 'bytes'
        const badEscape = bytesMode && !!rowBytes(row).error
        const toTextBlocked = bytesMode && hasByteEscape(row.value)
        return (
          <div key={row.id} className="group/hr relative flex shrink-0 items-stretch border-b border-line/60">
            {(changed.has(row.id) || badEscape) && !blank && (
              <span aria-hidden className={cx('absolute left-0 top-0 h-full w-[2px]', badEscape ? 'bg-danger' : 'bg-accent')} />
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
              title={
                locked && !readOnly
                  ? managedHint
                  : bytesMode
                    ? `${t('bytes.editBytesTip')}\n${latin1FromBytes(rowBytes(row).bytes)}`
                    : undefined
              }
              onChange={(e) => edit(row.id, { value: e.target.value })}
              placeholder={blank ? t('compose.req.valuePlaceholder') : ''}
              aria-label={t('compose.req.valueCol')}
              className={cx(
                'min-w-0 flex-1 bg-transparent px-3 py-[5px] font-mono text-[11.5px] text-fg-muted outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:bg-elevated/60 focus:text-fg',
                locked && 'cursor-default opacity-60',
                badEscape && 'text-danger',
              )}
            />
            <button
              type="button"
              // 8-BIT 徽标作为控件提示，不参与页内查找。
              data-find-skip
              aria-pressed={bytesMode}
              onClick={() => (bytesMode ? toText(row) : toBytes(row))}
              disabled={blank || locked || toTextBlocked}
              title={bytesMode ? (toTextBlocked ? t('bytes.toTextBlocked') : t('bytes.toText')) : t('bytes.toBytes')}
              aria-label={bytesMode ? (toTextBlocked ? t('bytes.toTextBlocked') : t('bytes.toText')) : t('bytes.toBytes')}
              className={cx(
                'my-[3px] mr-1 shrink-0 self-center rounded px-1 font-mono text-[10px] font-semibold transition',
                bytesMode
                  ? 'bg-warn/15 text-warn disabled:opacity-40'
                  : 'bg-warn/15 text-warn opacity-0 focus-visible:opacity-100 focus-visible:outline-none group-hover/hr:opacity-100 disabled:invisible',
              )}
            >
              {t('bytes.badge')}
            </button>
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
