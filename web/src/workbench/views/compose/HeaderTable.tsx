/**
 * 可编辑的头部表格，供构造器与断点编辑器共用。
 * 组件接收行数据、改动集合和更新回调，不依赖具体草稿模型。
 */
import { useState, type MouseEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Clipboard } from '@wailsio/runtime'
import { X } from 'lucide-react'
import { Chip, cx } from '../../ui/primitives'
import { ContextMenu, type MenuNode } from '../../ui/Menu'
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
  const [menu, setMenu] = useState<{
    rowId: string
    x: number
    y: number
    input: HTMLInputElement
    start: number
    end: number
  } | null>(null)

  const edit = (id: string, patch: Partial<HeaderRow>) => {
    onChange(normalizeHeaders(rows.map((h) => (h.id === id ? { ...h, ...patch } : h))))
  }
  const remove = (id: string) => {
    onChange(normalizeHeaders(rows.filter((h) => h.id !== id)))
  }
  const isManaged = (name: string) => !!managed?.includes(name.trim().toLowerCase())

  const openMenu = (row: HeaderRow, e: MouseEvent<HTMLDivElement>) => {
    const input = e.target instanceof HTMLInputElement ? e.target : e.currentTarget.querySelectorAll('input')[1]
    if (!input) return
    e.preventDefault()
    e.stopPropagation()
    input.focus()
    setMenu({
      rowId: row.id,
      x: e.clientX,
      y: e.clientY,
      input,
      start: input.selectionStart ?? 0,
      end: input.selectionEnd ?? 0,
    })
  }

  // 菜单会抢走输入焦点，执行原生编辑命令前恢复选区，以保留剪贴板操作和撤销历史。
  const editCommand = (command: string, value?: string) => {
    if (!menu?.input.isConnected) return
    menu.input.focus()
    menu.input.setSelectionRange(menu.start, menu.end)
    document.execCommand(command, false, value)
  }
  const paste = async () => {
    try {
      const text = navigator.clipboard
        ? await navigator.clipboard.readText().catch(() => Clipboard.Text())
        : await Clipboard.Text()
      editCommand('insertText', text)
    } catch {
      editCommand('paste')
    }
  }

  const menuRow = rows.find((row) => row.id === menu?.rowId)
  const menuItems: MenuNode[] = []
  if (menu && menuRow) {
    const locked = readOnly || isManaged(menuRow.name)
    const blank = !menuRow.name && !menuRow.value
    const bytesMode = menuRow.enc === 'bytes'
    const toTextBlocked = bytesMode && hasByteEscape(menuRow.value)
    const selected = menu.start !== menu.end
    menuItems.push(
      {
        label: t('nativeMenu.clipboard.undo'),
        disabled: locked,
        onSelect: () => editCommand('undo'),
      },
      {
        label: t('nativeMenu.clipboard.redo'),
        disabled: locked,
        onSelect: () => editCommand('redo'),
      },
      { type: 'separator' },
      {
        label: t('nativeMenu.clipboard.cut'),
        disabled: locked || !selected,
        onSelect: () => editCommand('cut'),
      },
      {
        label: t('nativeMenu.clipboard.copy'),
        disabled: !selected,
        onSelect: () => editCommand('copy'),
      },
      {
        label: t('nativeMenu.clipboard.paste'),
        disabled: locked,
        onSelect: () => void paste(),
      },
      {
        label: t('workbench.ctx.selectAll'),
        onSelect: () => editCommand('selectAll'),
      },
      { type: 'separator' },
      {
        label: t('bytes.toText'),
        checked: !bytesMode,
        disabled: locked || blank || !bytesMode || toTextBlocked,
        onSelect: () => {
          edit(menuRow.id, {
            enc: 'text',
            value: unescapeForTextMode(menuRow.value),
          })
          menu.input.focus()
        },
      },
      {
        label: t('bytes.toBytes'),
        checked: bytesMode,
        disabled: locked || blank || bytesMode,
        onSelect: () => {
          edit(menuRow.id, {
            enc: 'bytes',
            value: escapeForBytesMode(menuRow.value),
          })
          menu.input.focus()
        },
      },
    )
    if (toTextBlocked) menuItems.push({ type: 'label', label: t('bytes.toTextBlocked') })
  }

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
        const parsedBytes = bytesMode ? rowBytes(row) : undefined
        const badEscape = !!parsedBytes?.error
        const toTextBlocked = bytesMode && hasByteEscape(row.value)
        let valueHint: string | undefined
        if (locked && !readOnly) valueHint = managedHint
        else if (parsedBytes) valueHint = `${t('bytes.editBytesTip')}\n${latin1FromBytes(parsedBytes.bytes)}`
        return (
          <div
            key={row.id}
            onContextMenu={(e) => openMenu(row, e)}
            className="group/hr relative flex shrink-0 flex-wrap items-stretch border-b border-line/60"
          >
            {(changed.has(row.id) || badEscape) && !blank && (
              <span
                aria-hidden
                className={cx('absolute left-0 top-0 h-full w-[2px]', badEscape ? 'bg-danger' : 'bg-accent')}
              />
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
              title={valueHint}
              onChange={(e) => edit(row.id, { value: e.target.value })}
              aria-describedby={bytesMode ? `header-bytes-${row.id}` : undefined}
              placeholder={blank ? t('compose.req.valuePlaceholder') : ''}
              aria-label={t('compose.req.valueCol')}
              className={cx(
                'min-w-0 flex-1 bg-transparent px-3 py-[5px] font-mono text-[11.5px] text-fg-muted outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:bg-elevated/60 focus:text-fg',
                locked && 'cursor-default opacity-60',
                badEscape && 'text-danger',
              )}
            />
            {toTextBlocked && (
              <span
                data-find-skip
                title={t('bytes.editBytesTip')}
                className="my-[3px] mr-1 shrink-0 self-center rounded bg-warn/15 px-1 font-mono text-[10px] font-semibold text-warn"
              >
                {t('bytes.bytesMode')}
              </span>
            )}
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
            {bytesMode && (
              <p
                id={`header-bytes-${row.id}`}
                data-find-skip
                className="hidden basis-full px-3 pb-1.5 text-2xs text-fg-muted group-focus-within/hr:block"
              >
                {t('bytes.editBytesTip')}
                {toTextBlocked && <span className="ml-1 text-warn">{t('bytes.toTextBlocked')}</span>}
              </p>
            )}
          </div>
        )
      })}
      {!readOnly && suggestions && suggestions.length > 0 && (
        <CommonHeaders items={suggestions} onAdd={(name, value) => onChange(appendHeader(rows, name, value))} />
      )}
      {menu && menuRow && <ContextMenu x={menu.x} y={menu.y} items={menuItems} onClose={() => setMenu(null)} />}
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
