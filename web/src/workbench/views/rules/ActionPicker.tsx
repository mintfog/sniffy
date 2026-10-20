import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  ArrowDown,
  ArrowUp,
  Check,
  CornerDownLeft,
  Plus,
  Search,
  X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { ActionType } from '../../lib/rulesMap'
import { cx, IconButton } from '../../ui/primitives'
import { ACTION_ICON } from './actions'

export function ActionPicker({
  anchor,
  value,
  onSelect,
  onClose,
}: {
  anchor: HTMLButtonElement
  value?: ActionType
  onSelect: (type: ActionType) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const Indicator = value ? Check : Plus
  const id = useId()
  const ref = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const options = Object.entries(ACTION_ICON).map(([value, icon]) => ({
    value: value as ActionType,
    icon,
    label: t(`rules.action.${value}`),
    description: t(`rules.action.description.${value}`),
  }))
  const selectedIndex = Math.max(
    0,
    options.findIndex(option => option.value === value)
  )
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(selectedIndex)
  const [position, setPosition] = useState<{
    left: number
    top: number
  } | null>(null)
  const searchQuery = query.trim().toLowerCase()
  const filtered = options.filter(option =>
    `${option.label} ${option.description} ${option.value}`
      .toLowerCase()
      .includes(searchQuery)
  )

  useEffect(() => {
    const nodes = ref.current?.querySelectorAll('[role="option"]')
    nodes?.[active]?.scrollIntoView?.({ block: 'nearest' })
  }, [active, query])

  useLayoutEffect(() => {
    const menu = ref.current
    if (!menu) return
    const anchorRect = anchor.getBoundingClientRect()
    const { width, height } = menu.getBoundingClientRect()
    const below = window.innerHeight - anchorRect.bottom - 12
    setPosition({
      left: Math.max(
        12,
        Math.min(anchorRect.left, window.innerWidth - width - 12)
      ),
      top:
        below >= height + 6
          ? anchorRect.bottom + 6
          : Math.max(12, anchorRect.top - height - 6),
    })
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (!menu.contains(target) && !anchor.contains(target)) onClose()
    }
    const onScroll = (event: Event) => {
      if (!(event.target instanceof Node) || !event.target.contains(anchor))
        return
      // 滚动事件可能在浮层展开后才派发，需核对锚点是否位移。
      const rect = anchor.getBoundingClientRect()
      if (rect.top !== anchorRect.top || rect.left !== anchorRect.left)
        onClose()
    }
    document.addEventListener('pointerdown', onPointerDown)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onClose)
    window.addEventListener('blur', onClose)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onClose)
      window.removeEventListener('blur', onClose)
    }
  }, [anchor, onClose])

  useLayoutEffect(() => {
    if (position) searchRef.current?.focus({ preventScroll: true })
  }, [position])

  const close = () => {
    anchor.focus({ preventScroll: true })
    onClose()
  }

  return createPortal(
    <div
      ref={ref}
      role="dialog"
      aria-label={t('rules.action.choose')}
      className="wb-portal fixed z-[80] flex max-h-[calc(100vh-24px)] w-[480px] max-w-[calc(100vw-24px)] flex-col rounded-lg border border-line-strong bg-elevated p-3 shadow-wb"
      style={position ?? { left: 0, top: 0, visibility: 'hidden' }}
      onBlur={event => {
        if (
          event.relatedTarget instanceof Node &&
          !event.currentTarget.contains(event.relatedTarget) &&
          !anchor.contains(event.relatedTarget)
        )
          onClose()
      }}
      onKeyDown={event => {
        if (event.nativeEvent.isComposing) return
        if (event.key === 'Escape') {
          event.preventDefault()
          event.stopPropagation()
          close()
        } else if (event.key === 'Tab') {
          close()
        }
      }}
    >
      <header className="mb-2 flex shrink-0 items-center justify-between">
        <span className="text-[12px] font-medium">
          {t(value ? 'rules.action.choose' : 'rules.action.add')}
        </span>
        <IconButton
          size="sm"
          title={t('rules.action.closePicker')}
          aria-label={t('rules.action.closePicker')}
          onClick={close}
        >
          <X className="h-3.5 w-3.5" />
        </IconButton>
      </header>
      <div className="flex h-8 shrink-0 items-center gap-2 rounded-control border border-line-strong bg-inset px-2 focus-within:border-accent">
        <Search className="h-3.5 w-3.5 text-fg-faint" />
        <input
          ref={searchRef}
          role="combobox"
          aria-label={t('rules.action.search')}
          aria-expanded="true"
          aria-autocomplete="list"
          aria-controls={`${id}-options`}
          aria-activedescendant={
            filtered[active] ? `${id}-option-${active}` : undefined
          }
          value={query}
          onChange={event => {
            setQuery(event.target.value)
            setActive(event.target.value.trim() ? 0 : selectedIndex)
          }}
          onKeyDown={event => {
            if (event.nativeEvent.isComposing) return
            if (event.key === 'Enter' && filtered[active]) {
              event.preventDefault()
              onSelect(filtered[active].value)
              return
            }
            const arrowKey =
              event.key === 'ArrowDown' || event.key === 'ArrowUp'
            const boundaryKey =
              !query && (event.key === 'Home' || event.key === 'End')
            if (!arrowKey && !boundaryKey) return
            event.preventDefault()
            let next = active
            if (event.key === 'Home') next = 0
            else if (event.key === 'End') next = filtered.length - 1
            else next += event.key === 'ArrowDown' ? 1 : -1
            setActive(Math.max(0, Math.min(filtered.length - 1, next)))
          }}
          placeholder={t('rules.action.searchPlaceholder')}
          spellCheck={false}
          className="min-w-0 flex-1 bg-transparent text-2xs outline-none placeholder:text-fg-faint"
        />
      </div>
      <div
        id={`${id}-options`}
        role="listbox"
        aria-label={t('rules.action.choose')}
        className="mt-2 grid h-60 min-h-0 flex-1 auto-rows-min grid-cols-2 gap-1 overflow-y-auto"
      >
        {filtered.map((option, index) => {
          const Icon = option.icon
          const selected = option.value === value
          return (
            <button
              key={option.value}
              id={`${id}-option-${index}`}
              type="button"
              role="option"
              aria-selected={selected}
              tabIndex={-1}
              onMouseDown={event => event.preventDefault()}
              onPointerMove={() => setActive(index)}
              onClick={() => onSelect(option.value)}
              className={cx(
                'flex min-h-[56px] items-center gap-2 rounded-control border px-2 py-2 text-left outline-none',
                index === active
                  ? 'border-accent/35 bg-accent/10'
                  : selected
                    ? 'border-accent/20 bg-accent/5'
                    : 'border-transparent'
              )}
            >
              <span
                className={cx(
                  'flex h-7 w-7 shrink-0 items-center justify-center rounded-control',
                  index === active || selected
                    ? 'bg-accent/15 text-accent'
                    : 'bg-surface text-fg-muted'
                )}
              >
                <Icon className="h-3.5 w-3.5" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block text-2xs font-medium">
                  {option.label}
                </span>
                <span className="mt-0.5 block text-[10px] leading-snug text-fg-muted">
                  {option.description}
                </span>
              </span>
              <Indicator
                aria-hidden="true"
                className={cx(
                  'shrink-0 text-accent',
                  value ? 'h-3.5 w-3.5' : 'h-3 w-3',
                  (value ? !selected : index !== active) && 'invisible'
                )}
              />
            </button>
          )
        })}
        {!filtered.length && (
          <div className="col-span-2 py-8 text-center text-2xs text-fg-faint">
            {t('rules.action.noMatch')}
          </div>
        )}
      </div>
      <footer
        className="mt-2 flex shrink-0 items-center gap-1 border-t border-line pt-2 text-[10px] text-fg-faint"
        aria-hidden="true"
      >
        <ArrowUp className="h-3 w-3" />
        <ArrowDown className="h-3 w-3" />
        <span className="mr-3">{t('rules.action.navigate')}</span>
        <CornerDownLeft className="h-3 w-3" />
        <span className="mr-3">{t('rules.action.select')}</span>
        <span>Esc {t('rules.action.close')}</span>
      </footer>
    </div>,
    document.body
  )
}
