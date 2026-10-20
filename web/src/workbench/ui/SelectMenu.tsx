import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronDown } from 'lucide-react'
import { cx } from './primitives'

interface SelectMenuProps<T extends string> {
  label: string
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
  className?: string
}

export function SelectMenu<T extends string>({
  label,
  value,
  options,
  onChange,
  className,
}: SelectMenuProps<T>) {
  const id = useId()
  const triggerRef = useRef<HTMLButtonElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const typeahead = useRef({ text: '', time: 0 })
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  const [position, setPosition] = useState({
    left: 0,
    top: 0,
    minWidth: 0,
    maxHeight: 280,
  })
  const selectedIndex = options.findIndex(option => option.value === value)

  const show = (index = Math.max(0, selectedIndex)) => {
    if (options.length === 0) return
    typeahead.current = { text: '', time: 0 }
    setActiveIndex(index)
    setOpen(true)
  }

  const choose = (index: number) => {
    const option = options[index]
    if (option && option.value !== value) onChange(option.value)
    setOpen(false)
  }

  useEffect(() => setOpen(false), [value])

  useLayoutEffect(() => {
    if (!open) return
    const trigger = triggerRef.current
    const list = listRef.current
    if (!trigger || !list) return
    const anchor = trigger.getBoundingClientRect()
    const margin = 8
    const gap = 4
    const below = window.innerHeight - anchor.bottom - margin - gap
    const above = anchor.top - margin - gap
    const height = Math.min(list.scrollHeight + 2, 280)
    const upwards = below < height && above > below
    const maxHeight = Math.max(0, Math.min(280, upwards ? above : below))
    const minWidth = Math.min(
      Math.max(anchor.width, 160),
      window.innerWidth - margin * 2
    )
    const width = Math.max(minWidth, list.getBoundingClientRect().width)
    setPosition({
      left: Math.max(
        margin,
        Math.min(anchor.left, window.innerWidth - width - margin)
      ),
      top: upwards
        ? anchor.top - Math.min(height, maxHeight) - gap
        : anchor.bottom + gap,
      minWidth,
      maxHeight,
    })
    const close = () => setOpen(false)
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (!trigger.contains(target) && !list.contains(target)) close()
    }
    const onScroll = (event: Event) => {
      if (!(event.target instanceof Node) || !event.target.contains(trigger))
        return
      // 滚动事件可能在浮层展开后才派发，需核对锚点是否位移。
      const rect = trigger.getBoundingClientRect()
      if (rect.top !== anchor.top || rect.left !== anchor.left) close()
    }
    document.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', close)
    window.addEventListener('blur', close)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', close)
      window.removeEventListener('blur', close)
    }
  }, [open, options])

  useEffect(() => {
    if (open)
      listRef.current?.children[activeIndex]?.scrollIntoView?.({
        block: 'nearest',
      })
  }, [open, activeIndex])

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.nativeEvent.isComposing || options.length === 0) return
    const { key } = event
    if (key === 'Escape' && open) {
      event.preventDefault()
      event.stopPropagation()
      setOpen(false)
      return
    }
    if (key === 'Tab') {
      if (open) choose(activeIndex)
      return
    }
    if (key === 'Enter' || key === ' ') {
      event.preventDefault()
      if (open) choose(activeIndex)
      else show()
      return
    }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(key)) {
      event.preventDefault()
      let next = open ? activeIndex : Math.max(0, selectedIndex)
      if (key === 'Home') next = 0
      else if (key === 'End') next = options.length - 1
      else if (open)
        next = Math.max(
          0,
          Math.min(options.length - 1, next + (key === 'ArrowDown' ? 1 : -1))
        )
      if (open) setActiveIndex(next)
      else show(next)
      return
    }
    if (key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey)
      return
    event.preventDefault()
    const now = Date.now()
    const text =
      (now - typeahead.current.time < 500 ? typeahead.current.text : '') +
      key.toLocaleLowerCase()
    const prefix = [...text].every(character => character === text[0])
      ? text[0]
      : text
    const current = open ? activeIndex : selectedIndex
    const start = prefix.length === 1 ? current + 1 : Math.max(0, current)
    let match = Math.max(0, current)
    for (let offset = 0; offset < options.length; offset++) {
      const index = (start + offset) % options.length
      if (!options[index].label.toLocaleLowerCase().startsWith(prefix)) continue
      match = index
      break
    }
    if (open) setActiveIndex(match)
    else show(match)
    typeahead.current = { text, time: now }
  }

  return (
    <>
      <button
        ref={triggerRef}
        id={id}
        type="button"
        role="combobox"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? `${id}-list` : undefined}
        aria-activedescendant={open ? `${id}-option-${activeIndex}` : undefined}
        disabled={options.length === 0}
        onClick={() => {
          triggerRef.current?.focus()
          if (open) setOpen(false)
          else show()
        }}
        onKeyDown={onKeyDown}
        onBlur={() => setOpen(false)}
        className={cx(
          'inline-flex h-7 shrink-0 items-center justify-between gap-3 rounded-control border px-2 text-left text-[12px] text-fg outline-none transition-colors',
          'focus-visible:ring-1 focus-visible:ring-accent disabled:cursor-default disabled:opacity-40',
          open
            ? 'border-accent bg-elevated shadow-press'
            : 'border-line bg-inset shadow-raise hover:border-line-strong hover:bg-elevated',
          className
        )}
      >
        <span className="truncate">
          {options[selectedIndex]?.label ?? value}
        </span>
        <ChevronDown
          aria-hidden="true"
          className={cx(
            'h-3.5 w-3.5 shrink-0 text-fg-faint transition-transform',
            open && 'rotate-180 text-accent'
          )}
        />
      </button>
      {open &&
        createPortal(
          <div
            ref={listRef}
            id={`${id}-list`}
            role="listbox"
            aria-label={label}
            // 焦点留在触发器，由 aria-activedescendant 表达键盘浏览位置。
            onMouseDown={event => event.preventDefault()}
            className="wb-portal fixed z-[80] max-w-[calc(100vw-16px)] overflow-y-auto overscroll-contain rounded-control border border-line-strong bg-elevated p-1 shadow-wb"
            style={position}
          >
            {options.map((option, index) => (
              <div
                key={option.value}
                id={`${id}-option-${index}`}
                role="option"
                aria-selected={option.value === value}
                onPointerMove={() => setActiveIndex(index)}
                onClick={() => choose(index)}
                className={cx(
                  'flex min-h-7 cursor-default select-none items-center gap-2 rounded-[3px] px-2 py-1 text-[12px]',
                  index === activeIndex
                    ? 'bg-sel text-sel-fg'
                    : option.value === value
                      ? 'bg-accent/10 text-accent'
                      : 'text-fg'
                )}
              >
                <span className="flex-1 whitespace-nowrap">{option.label}</span>
                <Check
                  aria-hidden="true"
                  className={cx(
                    'ml-3 h-3.5 w-3.5 shrink-0',
                    option.value !== value && 'invisible'
                  )}
                />
              </div>
            ))}
          </div>,
          document.body
        )}
    </>
  )
}
