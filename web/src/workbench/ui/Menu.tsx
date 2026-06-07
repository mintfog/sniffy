import { useCallback, useEffect, useRef, useState } from 'react'
import { Check, ChevronRight, type LucideIcon } from 'lucide-react'
import { cx, Kbd } from './primitives'

/* 菜单数据模型 */
export interface MenuItemNode {
  type?: 'item'
  label: string
  shortcut?: string
  icon?: LucideIcon
  disabled?: boolean
  checked?: boolean
  danger?: boolean
  onSelect?: () => void
  submenu?: MenuNode[]
}

export type MenuNode = MenuItemNode | { type: 'separator' } | { type: 'label'; label: string }

export interface TopMenu {
  label: string
  items: MenuNode[]
}

/* ───────────── 单层菜单列表（含级联子菜单） ───────────── */

function MenuList({ items, onClose, depth = 0 }: { items: MenuNode[]; onClose: () => void; depth?: number }) {
  const [openSub, setOpenSub] = useState<number | null>(null)

  return (
    <div
      className={cx(
        'min-w-[200px] rounded-wb border border-line bg-surface py-1 shadow-wb wb-pop',
        'wb-scroll max-h-[70vh] overflow-y-auto',
      )}
    >
      {items.map((node, i) => {
        if ('type' in node && node.type === 'separator') {
          return <div key={i} className="my-1 h-px bg-line" />
        }
        if ('type' in node && node.type === 'label') {
          return (
            <div key={i} className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wide text-fg-faint">
              {node.label}
            </div>
          )
        }
        const item = node as MenuItemNode
        const Icon = item.icon
        const hasSub = !!item.submenu?.length
        return (
          <div
            key={i}
            className="relative"
            onMouseEnter={() => setOpenSub(hasSub ? i : null)}
          >
            <button
              type="button"
              disabled={item.disabled}
              onClick={() => {
                if (item.disabled) return
                if (hasSub) {
                  setOpenSub((v) => (v === i ? null : i))
                  return
                }
                item.onSelect?.()
                onClose()
              }}
              className={cx(
                'flex w-full items-center gap-2.5 px-3 py-[5px] text-left text-[12.5px] transition-colors outline-none',
                item.disabled
                  ? 'cursor-default text-fg-faint opacity-50'
                  : item.danger
                    ? 'text-danger hover:bg-danger/12'
                    : 'text-fg hover:bg-accent hover:text-accent-fg',
              )}
            >
              <span className="flex h-4 w-4 shrink-0 items-center justify-center">
                {item.checked ? <Check className="h-3.5 w-3.5" /> : Icon ? <Icon className="h-3.5 w-3.5 opacity-80" /> : null}
              </span>
              <span className="flex-1 whitespace-nowrap">{item.label}</span>
              {item.shortcut && !hasSub && (
                <span className="ml-6 shrink-0">
                  <Kbd>{item.shortcut}</Kbd>
                </span>
              )}
              {hasSub && <ChevronRight className="ml-4 h-3.5 w-3.5 shrink-0 opacity-70" />}
            </button>

            {hasSub && openSub === i && (
              <div className="absolute left-full top-[-5px] z-50 pl-0.5">
                <MenuList items={item.submenu!} onClose={onClose} depth={depth + 1} />
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

/* ───────────── 顶部菜单栏 ───────────── */

export function MenuBar({ menus, className }: { menus: TopMenu[]; className?: string }) {
  const [open, setOpen] = useState<number | null>(null)
  const barRef = useRef<HTMLDivElement>(null)

  const close = useCallback(() => setOpen(null), [])

  useEffect(() => {
    if (open == null) return
    const onDown = (e: MouseEvent) => {
      if (barRef.current && !barRef.current.contains(e.target as Node)) close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, close])

  return (
    <div ref={barRef} className={cx('flex items-center', className)}>
      {menus.map((menu, i) => (
        <div key={menu.label} className="relative">
          <button
            type="button"
            onClick={() => setOpen((v) => (v === i ? null : i))}
            onMouseEnter={() => open != null && setOpen(i)}
            className={cx(
              'h-7 rounded-wb-sm px-2.5 text-[12.5px] transition-colors outline-none',
              open === i ? 'bg-accent/15 text-fg' : 'text-fg-muted hover:bg-elevated hover:text-fg',
            )}
          >
            {menu.label}
          </button>
          {open === i && (
            <div className="absolute left-0 top-full z-50 mt-1">
              <MenuList items={menu.items} onClose={close} />
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
