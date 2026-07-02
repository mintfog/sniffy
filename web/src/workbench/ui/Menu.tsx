import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronRight, type LucideIcon } from 'lucide-react'
import { cx, Kbd } from './primitives'

/* 菜单数据模型 */
export interface MenuItemNode {
  type?: 'item'
  label: string
  shortcut?: string
  icon?: LucideIcon
  /** 色板圆点（如高亮颜色），传 tailwind bg-* class，优先级低于 checked/icon */
  swatch?: string
  disabled?: boolean
  checked?: boolean
  danger?: boolean
  onSelect?: () => void
  submenu?: MenuNode[]
}

export type MenuNode = MenuItemNode | { type: 'separator' } | { type: 'label'; label: string }

export interface TopMenu {
  /** 稳定标识（与界面语言无关），供原生菜单适配器定位特定顶级菜单（如 edit/help）。 */
  id?: string
  label: string
  items: MenuNode[]
}

/** 点击目标是否落在任意（含 portal 渲染的）菜单层内 */
function inAnyMenu(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest('[data-wb-menu]')
}

/* ───────────── 锚定浮层菜单（portal + fixed 定位，避免被滚动/overflow 容器裁剪） ───────────── */

function AnchoredMenu({
  anchorRef,
  items,
  onClose,
  depth = 0,
  placement,
  onMouseEnter,
}: {
  anchorRef: React.RefObject<HTMLElement>
  items: MenuNode[]
  onClose: () => void
  depth?: number
  /** submenu：贴锚点右侧（越界翻到左侧）；below：贴锚点下方（用于顶部菜单栏下拉） */
  placement: 'submenu' | 'below'
  onMouseEnter?: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)

  // 先在屏外渲染量尺寸，再贴到锚点；越界时翻转 / 夹紧到视口内
  useLayoutEffect(() => {
    const anchor = anchorRef.current?.getBoundingClientRect()
    const el = ref.current
    if (!anchor || !el) return
    const { width, height } = el.getBoundingClientRect()
    let left: number
    let top: number
    if (placement === 'submenu') {
      left = anchor.right - 2
      if (left + width > window.innerWidth - 4) left = Math.max(4, anchor.left - width + 2)
      top = anchor.top - 5
    } else {
      left = anchor.left
      if (left + width > window.innerWidth - 4) left = Math.max(4, window.innerWidth - width - 4)
      top = anchor.bottom + 4
      if (top + height > window.innerHeight - 4) top = Math.max(4, anchor.top - height - 4)
    }
    if (top + height > window.innerHeight - 4) top = Math.max(4, window.innerHeight - height - 4)
    // 值未变时保持引用，避免 setState 触发循环
    setPos((p) => (p && p.left === left && p.top === top ? p : { left, top }))
  }, [anchorRef, placement])

  // fixed 定位的浮层不随锚点滚动：仅当「被滚动的容器包含锚点」（锚点确实移动了）才关闭。
  // 关键：流量表「跟随最新」会每帧自动滚动并触发捕获阶段 scroll 事件，但其滚动容器并不包含
  // 顶部菜单按钮（锚点在标题栏），因此不应关闭菜单——否则数据刷新时菜单会被反复关掉。
  // 菜单列表自身的滚动（target 在菜单层内）同样忽略。
  useEffect(() => {
    const onScroll = (e: Event) => {
      const t = e.target
      if (t instanceof Element && t.closest('[data-wb-menu]')) return
      const anchor = anchorRef.current
      if (anchor && t instanceof Node && typeof t.contains === 'function' && t.contains(anchor)) {
        onClose()
      }
    }
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onClose)
    return () => {
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onClose)
    }
  }, [onClose, anchorRef])

  return createPortal(
    <div
      ref={ref}
      data-wb-menu
      className="wb-portal fixed z-[80]"
      onMouseEnter={onMouseEnter}
      style={pos ? { left: pos.left, top: pos.top } : { left: -9999, top: 0, visibility: 'hidden' }}
    >
      <MenuList items={items} onClose={onClose} depth={depth} />
    </div>,
    document.body,
  )
}

/* ───────────── 单个菜单项（持有自身锚点 ref 以定位子菜单） ───────────── */

function MenuItemRow({
  item,
  open,
  onHover,
  onKeepOpen,
  onClose,
  depth,
}: {
  item: MenuItemNode
  open: boolean
  onHover: () => void
  /** 鼠标进入子菜单时回调：取消父级待执行的关闭计时器并钉住本项（hover 走廊） */
  onKeepOpen: () => void
  onClose: () => void
  depth: number
}) {
  const ref = useRef<HTMLButtonElement>(null)
  const Icon = item.icon
  const hasSub = !!item.submenu?.length

  return (
    <>
      <button
        ref={ref}
        type="button"
        disabled={item.disabled}
        onMouseEnter={onHover}
        onClick={() => {
          if (item.disabled || hasSub) return
          item.onSelect?.()
          onClose()
        }}
        className={cx(
          'flex w-full items-center gap-2.5 px-3 py-[5px] text-left text-[12.5px] transition-colors outline-none',
          item.disabled
            ? 'cursor-default text-fg-faint opacity-50'
            : item.danger
              ? 'text-danger hover:bg-danger/12'
              : cx(
                  'hover:bg-accent hover:text-accent-fg',
                  // 子菜单展开期间父项保持高亮（鼠标已移入子菜单时 :hover 会丢失）；文字色类须互斥，同挂时 text-fg 会覆盖 accent-fg
                  hasSub && open ? 'bg-accent text-accent-fg' : 'text-fg',
                ),
        )}
      >
        <span className="flex h-4 w-4 shrink-0 items-center justify-center">
          {item.checked ? (
            <Check className="h-3.5 w-3.5" />
          ) : Icon ? (
            <Icon className="h-3.5 w-3.5 opacity-80" />
          ) : item.swatch ? (
            <span className={cx('h-2.5 w-2.5 rounded-full', item.swatch)} />
          ) : null}
        </span>
        <span className="flex-1 whitespace-nowrap">{item.label}</span>
        {item.shortcut && !hasSub && (
          <span className="ml-6 shrink-0">
            <Kbd>{item.shortcut}</Kbd>
          </span>
        )}
        {hasSub && <ChevronRight className="ml-4 h-3.5 w-3.5 shrink-0 opacity-70" />}
      </button>

      {hasSub && open && (
        <AnchoredMenu
          anchorRef={ref}
          items={item.submenu!}
          onClose={onClose}
          depth={depth + 1}
          placement="submenu"
          onMouseEnter={onKeepOpen}
        />
      )}
    </>
  )
}

/* ───────────── 单层菜单列表 ───────────── */

function MenuList({ items, onClose, depth = 0 }: { items: MenuNode[]; onClose: () => void; depth?: number }) {
  const [openSub, setOpenSub] = useState<number | null>(null)
  const openSubRef = useRef<number | null>(null)
  openSubRef.current = openSub
  const switchTimer = useRef<number>()

  // hover 意图延迟：斜向滑入子菜单时途经相邻项不至于立刻关闭
  const requestSub = useCallback((i: number | null) => {
    window.clearTimeout(switchTimer.current)
    if (i === openSubRef.current) return
    const delay = i == null ? 180 : openSubRef.current == null ? 60 : 180
    switchTimer.current = window.setTimeout(() => setOpenSub(i), delay)
  }, [])

  // 鼠标已进入第 i 项的子菜单：取消任何待执行的切换并钉住该项（修复 hover 走廊掉子菜单）
  const pinSub = useCallback((i: number) => {
    window.clearTimeout(switchTimer.current)
    setOpenSub(i)
  }, [])

  useEffect(() => () => window.clearTimeout(switchTimer.current), [])

  return (
    <div
      data-wb-menu
      onContextMenu={(e) => e.preventDefault()}
      className="max-h-[80vh] min-w-[200px] overflow-y-auto overflow-x-hidden rounded-wb border border-line bg-surface py-1 shadow-wb wb-pop"
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
        const hasSub = !!item.submenu?.length
        return (
          <MenuItemRow
            key={i}
            item={item}
            depth={depth}
            open={hasSub && openSub === i}
            onHover={() => requestSub(hasSub ? i : null)}
            onKeepOpen={() => pinSub(i)}
            onClose={onClose}
          />
        )
      })}
    </div>
  )
}

/* ───────────── 右键上下文菜单（portal + 视口夹紧） ───────────── */

export function ContextMenu({
  x,
  y,
  items,
  onClose,
}: {
  x: number
  y: number
  items: MenuNode[]
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    setPos({
      left: Math.min(x, Math.max(4, window.innerWidth - width - 4)),
      top: Math.min(y, Math.max(4, window.innerHeight - height - 4)),
    })
  }, [x, y])

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (!inAnyMenu(e.target)) onClose()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('blur', onClose)
    window.addEventListener('resize', onClose)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('blur', onClose)
      window.removeEventListener('resize', onClose)
    }
  }, [onClose])

  return createPortal(
    <div
      ref={ref}
      data-wb-menu
      onContextMenu={(e) => e.preventDefault()}
      className="wb-portal fixed z-[80]"
      style={pos ? { left: pos.left, top: pos.top } : { left: x, top: y, visibility: 'hidden' }}
    >
      <MenuList items={items} onClose={onClose} />
    </div>,
    document.body,
  )
}

/* ───────────── 顶部菜单栏 ───────────── */

export function MenuBar({ menus, className }: { menus: TopMenu[]; className?: string }) {
  const [open, setOpen] = useState<number | null>(null)
  const barRef = useRef<HTMLDivElement>(null)
  // 每个顶层菜单按钮的锚点 ref（下拉经 portal 渲染，逃出 wb-root 的 overflow-hidden）
  const btnRefs = useRef<(HTMLButtonElement | null)[]>([])

  const close = useCallback(() => setOpen(null), [])

  useEffect(() => {
    if (open == null) return
    const onDown = (e: MouseEvent) => {
      // 子菜单经 portal 渲染在 body 下，不能只看菜单栏自身
      if (barRef.current?.contains(e.target as Node)) return
      if (inAnyMenu(e.target)) return
      close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        // 此次 Esc 已被「关闭菜单」消费，不再传到 window 层（避免连带清空流量选择）
        e.stopPropagation()
        close()
      }
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
            ref={(el) => (btnRefs.current[i] = el)}
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
            <AnchoredMenu
              anchorRef={{ current: btnRefs.current[i] }}
              items={menu.items}
              onClose={close}
              placement="below"
            />
          )}
        </div>
      ))}
    </div>
  )
}
