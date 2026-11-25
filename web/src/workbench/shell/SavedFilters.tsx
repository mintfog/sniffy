import { useEffect, useMemo, useRef, useState } from 'react'
import { Bookmark, BookmarkPlus, Check, Pencil, Trash2, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { usePrefs } from '../prefs'
import { ContextMenu, type MenuNode } from '../ui/Menu'
import { Button } from '../ui/controls'
import { cx, IconButton, Tooltip } from '../ui/primitives'
import {
  defaultFilterName,
  describeFilter,
  isSnapshotEmpty,
  removeFilter,
  renameError,
  renameFilter,
  restoreFilter,
  sameSnapshot,
  snapshotFilter,
  upsertFilter,
  type FilterState,
  type SavedFilter,
} from '../lib/savedFilters'

const INPUT_CLASS = cx(
  'h-7 min-w-0 flex-1 rounded-wb border border-line bg-inset px-2 text-[12px] text-fg',
  'outline-none transition-colors focus:border-accent focus:bg-surface',
)

interface SavedFiltersProps {
  current: FilterState
  bizEnabled: boolean
  onApply: (next: FilterState) => void
}

/** 工具栏上的「已保存的筛选」入口：套用、保存当前筛选、管理（重命名 / 删除）。 */
export function SavedFilters({ current, bizEnabled, onApply }: SavedFiltersProps) {
  const { t } = useTranslation()
  const saved = usePrefs((s) => s.savedFilters)
  const setPref = usePrefs((s) => s.set)
  const [menu, setMenu] = useState<{ x: number; y: number; trigger: HTMLElement } | null>(null)
  const [dialog, setDialog] = useState<'save' | 'manage' | null>(null)

  const snap = useMemo(() => snapshotFilter(current), [current])
  const savable = !isSnapshotEmpty(snap)
  const activeId = saved.find((f) => sameSnapshot(f, snap))?.id

  const apply = (f: SavedFilter) => onApply(restoreFilter(f, bizEnabled))

  // ContextMenu 不把触发按钮上的 mousedown 当作外部点击，再次点击按钮由这里收起。
  const toggleMenu = (trigger: HTMLElement) => {
    if (menu) {
      setMenu(null)
      return
    }
    const r = trigger.getBoundingClientRect()
    setMenu({ x: r.left, y: r.bottom + 4, trigger })
  }

  const title = t('savedFilters.title')
  const items: MenuNode[] = [
    { type: 'label', label: title },
    ...(saved.length
      ? saved.map<MenuNode>((f) => ({ label: f.name, checked: f.id === activeId, onSelect: () => apply(f) }))
      : [{ label: t('savedFilters.empty'), disabled: true }]),
    { type: 'separator' },
    { label: t('savedFilters.saveCurrent'), icon: BookmarkPlus, disabled: !savable, onSelect: () => setDialog('save') },
    { label: t('savedFilters.manage'), icon: Pencil, disabled: !saved.length, onSelect: () => setDialog('manage') },
  ]

  return (
    <>
      <Tooltip label={title} placement="bottom">
        <IconButton
          size="xs"
          active={!!activeId || !!menu}
          onClick={(e) => toggleMenu(e.currentTarget)}
          aria-label={title}
          aria-haspopup="menu"
          aria-expanded={!!menu}
        >
          <Bookmark className="h-4 w-4" />
        </IconButton>
      </Tooltip>

      {menu && (
        <ContextMenu x={menu.x} y={menu.y} items={items} onClose={() => setMenu(null)} trigger={menu.trigger} />
      )}

      {dialog === 'save' && (
        <SaveFilterDialog
          saved={saved}
          onClose={() => setDialog(null)}
          onSave={(name) => {
            setPref({ savedFilters: upsertFilter(saved, name, snap).list })
            setDialog(null)
          }}
        />
      )}

      {dialog === 'manage' && (
        <ManageFiltersDialog
          saved={saved}
          activeId={activeId}
          onApply={(f) => {
            apply(f)
            setDialog(null)
          }}
          onRename={(id, name) => setPref({ savedFilters: renameFilter(saved, id, name) })}
          onRemove={(id) => setPref({ savedFilters: removeFilter(saved, id) })}
          onClose={() => setDialog(null)}
        />
      )}
    </>
  )
}

function DialogShell({
  title,
  titleId,
  icon,
  onClose,
  children,
}: {
  title: string
  titleId: string
  icon: React.ReactNode
  onClose: () => void
  children: React.ReactNode
}) {
  const { t } = useTranslation()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // 输入法组字时的 Esc 用来取消组字，不关闭对话框
      if (e.key === 'Escape' && !e.isComposing) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-overlay p-4" onClick={onClose} role="presentation">
      <div
        className="w-full max-w-md overflow-hidden rounded-lg border border-line bg-popover shadow-dialog"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <header className="flex items-center gap-2 border-b border-line px-4 py-3">
          {icon}
          <span id={titleId} className="text-[13px] font-semibold text-fg">
            {title}
          </span>
          <button
            type="button"
            onClick={onClose}
            aria-label={t('savedFilters.close')}
            title={t('savedFilters.close')}
            className="ml-auto text-fg-faint hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>
        {children}
      </div>
    </div>
  )
}

function SaveFilterDialog({
  saved,
  onSave,
  onClose,
}: {
  saved: readonly SavedFilter[]
  onSave: (name: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(() => defaultFilterName(saved, (n) => t('savedFilters.defaultName', { n })))
  const inputRef = useRef<HTMLInputElement>(null)
  const trimmed = name.trim()
  const valid = trimmed !== ''
  const overwrite = saved.some((f) => f.name === trimmed)

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
  }, [])

  return (
    <DialogShell
      title={t('savedFilters.saveTitle')}
      titleId="save-filter-title"
      icon={<BookmarkPlus className="h-4 w-4 text-accent" />}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (valid) onSave(name)
        }}
      >
        <div className="flex flex-col gap-1.5 px-4 py-4">
          <label className="flex flex-col gap-1.5">
            <span className="text-[12px] font-medium text-fg">{t('savedFilters.nameLabel')}</span>
            <input
              ref={inputRef}
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              aria-invalid={!valid}
              className={cx(INPUT_CLASS, !valid && 'border-danger focus:border-danger')}
            />
          </label>
          <span className={cx('text-2xs', valid ? 'text-fg-faint' : 'text-danger')}>
            {!valid ? t('savedFilters.nameEmpty') : overwrite ? t('savedFilters.overwriteHint') : t('savedFilters.saveHint')}
          </span>
        </div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button onClick={onClose}>{t('savedFilters.cancel')}</Button>
          <Button type="submit" variant="primary" disabled={!valid}>
            {overwrite ? t('savedFilters.overwrite') : t('savedFilters.save')}
          </Button>
        </footer>
      </form>
    </DialogShell>
  )
}

function ManageFiltersDialog({
  saved,
  activeId,
  onApply,
  onRename,
  onRemove,
  onClose,
}: {
  saved: readonly SavedFilter[]
  activeId?: string
  onApply: (f: SavedFilter) => void
  onRename: (id: string, name: string) => void
  onRemove: (id: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null)
  const error = editing ? renameError(saved, editing.id, editing.name) : null
  const listRef = useRef<HTMLUListElement>(null)

  // 从菜单打开时焦点已还给工具栏按钮，不移进对话框的话，回车会在对话框背后重新打开菜单。
  useEffect(() => {
    listRef.current?.querySelector<HTMLButtonElement>('button')?.focus()
  }, [])

  const commit = () => {
    if (!editing || error) return
    onRename(editing.id, editing.name)
    setEditing(null)
  }

  const onRenameKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      commit()
    } else if (e.key === 'Escape') {
      // 拦下冒泡，Esc 只退出重命名，不关闭对话框
      e.stopPropagation()
      setEditing(null)
    }
  }

  return (
    <DialogShell
      title={t('savedFilters.manageTitle')}
      titleId="manage-filters-title"
      icon={<Bookmark className="h-4 w-4 text-accent" />}
      onClose={onClose}
    >
      <ul ref={listRef} className="max-h-[60vh] overflow-y-auto px-2 py-2">
        {saved.length === 0 && <li className="px-2 py-3 text-[12px] text-fg-faint">{t('savedFilters.empty')}</li>}
        {saved.map((f) => (
          <li key={f.id} className="flex items-center gap-2 rounded-wb px-2 py-1.5 hover:bg-hover">
            <span className="flex h-4 w-4 shrink-0 items-center justify-center">
              {f.id === activeId && <Check className="h-3.5 w-3.5 text-accent" />}
            </span>
            {editing?.id === f.id ? (
              <>
                <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <input
                    autoFocus
                    value={editing.name}
                    onChange={(e) => setEditing({ id: f.id, name: e.currentTarget.value })}
                    onKeyDown={onRenameKeyDown}
                    aria-label={t('savedFilters.nameLabel')}
                    aria-invalid={!!error}
                    className={cx(INPUT_CLASS, error && 'border-danger focus:border-danger')}
                  />
                  {error && (
                    <span className="text-2xs text-danger">
                      {error === 'empty' ? t('savedFilters.nameEmpty') : t('savedFilters.nameTaken')}
                    </span>
                  )}
                </span>
                <Tooltip label={t('savedFilters.confirmRename')} placement="bottom">
                  <IconButton size="sm" onClick={commit} disabled={!!error} aria-label={t('savedFilters.confirmRename')}>
                    <Check className="h-4 w-4" />
                  </IconButton>
                </Tooltip>
                <Tooltip label={t('savedFilters.cancel')} placement="bottom">
                  <IconButton size="sm" onClick={() => setEditing(null)} aria-label={t('savedFilters.cancel')}>
                    <X className="h-4 w-4" />
                  </IconButton>
                </Tooltip>
              </>
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => onApply(f)}
                  title={t('savedFilters.apply')}
                  className="flex min-w-0 flex-1 flex-col text-left outline-none focus-visible:shadow-focus"
                >
                  <span className="truncate text-[13px] text-fg">{f.name}</span>
                  <span className="truncate text-[11px] text-fg-muted">{describeFilter(f, t)}</span>
                </button>
                <Tooltip label={t('savedFilters.rename')} placement="bottom">
                  <IconButton
                    size="sm"
                    onClick={() => setEditing({ id: f.id, name: f.name })}
                    aria-label={`${t('savedFilters.rename')} ${f.name}`}
                  >
                    <Pencil className="h-4 w-4" />
                  </IconButton>
                </Tooltip>
                <Tooltip label={t('savedFilters.delete')} placement="bottom">
                  <IconButton
                    size="sm"
                    tone="danger"
                    onClick={() => onRemove(f.id)}
                    aria-label={`${t('savedFilters.delete')} ${f.name}`}
                  >
                    <Trash2 className="h-4 w-4" />
                  </IconButton>
                </Tooltip>
              </>
            )}
          </li>
        ))}
      </ul>
      <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
        <Button onClick={onClose}>{t('savedFilters.close')}</Button>
      </footer>
    </DialogShell>
  )
}
