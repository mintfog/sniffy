import React, { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, X } from 'lucide-react'
import { placeDropdown } from '../ui/dropdownPlacement'
import { cx } from '../ui/primitives'

interface SuggestInputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> {
  value: string
  onChange: (value: string) => void
  /** 候选取值；缺省时就是普通输入框 */
  options?: readonly string[]
  clearLabel: string
  invalid?: boolean
}

const LIST_MAX_HEIGHT = 360

/** 可自由输入、也可从候选里选的输入框；候选按已输入内容过滤。 */
export function SuggestInput({
  value,
  onChange,
  options,
  clearLabel,
  invalid,
  onKeyDown,
  className,
  ...rest
}: SuggestInputProps) {
  const id = useId()
  const boxRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const [pos, setPos] = useState({ left: 0, top: 0, width: 0, maxHeight: LIST_MAX_HEIGHT })

  const suggest = !!options
  // 值恰好是某个候选时列出全部，便于直接换成另一项
  const matches = useMemo(() => {
    if (!options) return []
    const q = value.trim().toLowerCase()
    if (!q || options.some((o) => o.toLowerCase() === q)) return options
    return options.filter((o) => o.toLowerCase().includes(q))
  }, [options, value])
  const shown = open && matches.length > 0

  // options 被调用方撤掉时一并复位 open，否则之后重新给出候选会在没有用户操作时直接弹出。
  useEffect(() => {
    if (!suggest) setOpen(false)
  }, [suggest])

  useLayoutEffect(() => {
    if (!shown) return
    const box = boxRef.current
    const list = listRef.current
    if (!box || !list) return
    const r = box.getBoundingClientRect()
    setPos({ left: r.left, width: r.width, ...placeDropdown(r, list.scrollHeight + 2, LIST_MAX_HEIGHT) })
  }, [shown, matches])

  useEffect(() => {
    const box = boxRef.current
    if (!shown || !box) return
    const anchor = box.getBoundingClientRect()
    const close = () => setOpen(false)
    // 只有输入框所在的滚动容器滚动、且确实带走了输入框才收起：开着「跟随最新」时流量表持续滚动，
    // 它不是输入框的祖先，不该打断选择。
    const onScroll = (e: Event) => {
      if (!(e.target instanceof Node) || !e.target.contains(box)) return
      const r = box.getBoundingClientRect()
      if (r.top !== anchor.top || r.left !== anchor.left) close()
    }
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', close)
    return () => {
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', close)
    }
  }, [shown])

  useEffect(() => {
    if (shown && active >= 0) listRef.current?.children[active]?.scrollIntoView?.({ block: 'nearest' })
  }, [shown, active])

  const show = () => {
    if (!suggest) return
    setActive(-1)
    setOpen(true)
  }

  const choose = (v: string) => {
    onChange(v)
    setOpen(false)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!e.nativeEvent.isComposing && suggest) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        if (!shown) return show()
        const step = e.key === 'ArrowDown' ? 1 : -1
        setActive((i) => Math.max(0, Math.min(matches.length - 1, i + step)))
        return
      }
      if (e.key === 'Enter' && shown && active >= 0) {
        e.preventDefault()
        return choose(matches[active])
      }
      // 先收起下拉，Esc 的其余语义（清空 / 移除 / 收起搜索栏）留给下一次按键
      if (e.key === 'Escape' && shown) {
        e.preventDefault()
        e.stopPropagation()
        return setOpen(false)
      }
    }
    onKeyDown?.(e)
  }

  return (
    <div ref={boxRef} className="relative min-w-0 flex-1">
      <input
        {...rest}
        value={value}
        onChange={(e) => {
          onChange(e.target.value)
          if (suggest && !open) show()
          else setActive(-1)
        }}
        onClick={() => !open && show()}
        onBlur={() => setOpen(false)}
        onKeyDown={handleKeyDown}
        role={suggest ? 'combobox' : undefined}
        aria-autocomplete={suggest ? 'list' : undefined}
        aria-expanded={suggest ? shown : undefined}
        aria-controls={shown ? `${id}-list` : undefined}
        aria-activedescendant={shown && active >= 0 ? `${id}-opt-${active}` : undefined}
        aria-invalid={invalid || undefined}
        autoComplete="off"
        spellCheck={false}
        className={cx(
          'h-control-md w-full rounded-control border bg-surface pl-2.5 text-[13px] text-fg placeholder:text-fg-faint',
          'outline-none transition-shadow focus:shadow-focus',
          suggest ? 'pr-14' : 'pr-8',
          invalid ? 'border-danger' : 'border-line',
          className,
        )}
      />
      <div className="absolute right-1.5 top-1/2 flex -translate-y-1/2 items-center gap-0.5">
        {value && (
          <button
            type="button"
            onClick={() => onChange('')}
            aria-label={clearLabel}
            className="flex h-5 w-5 items-center justify-center rounded-wb-sm text-fg-faint hover:bg-hover hover:text-fg"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
        {suggest && (
          <button
            type="button"
            tabIndex={-1}
            aria-hidden
            // 保持焦点在输入框，blur 不会抢先把下拉关掉
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => (open ? setOpen(false) : show())}
            className="flex h-5 w-5 items-center justify-center rounded-wb-sm text-fg-faint hover:bg-hover hover:text-fg"
          >
            <ChevronDown className={cx('h-3.5 w-3.5 transition-transform', shown && 'rotate-180 text-accent')} />
          </button>
        )}
      </div>
      {shown &&
        createPortal(
          <div
            ref={listRef}
            id={`${id}-list`}
            role="listbox"
            onMouseDown={(e) => e.preventDefault()}
            className="wb-portal fixed z-[80] overflow-y-auto overscroll-contain rounded-lg border border-line bg-popover p-1 shadow-wb"
            style={pos}
          >
            {matches.map((o, i) => (
              <div
                key={o}
                id={`${id}-opt-${i}`}
                role="option"
                aria-selected={o === value}
                onPointerMove={() => setActive(i)}
                onClick={() => choose(o)}
                className={cx(
                  'flex min-h-control-sm cursor-default select-none items-center truncate rounded-wb-sm px-2.5 py-1 text-[13px] text-fg',
                  i === active && 'bg-hover',
                )}
              >
                {o}
              </div>
            ))}
          </div>,
          document.body,
        )}
    </div>
  )
}
