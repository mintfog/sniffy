import {
  Fragment,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react'
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  GripVertical,
  Plus,
  Trash2,
  Zap,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { ActionType, RuleAction } from '../../lib/rulesMap'
import { cx, IconButton } from '../../ui/primitives'
import { Button, TextInput } from '../../ui/controls'
import { ActionPicker } from './ActionPicker'
import { ACTION_ICON, actionParamMeta } from './actions'

export function RuleActions({
  actions,
  onChange,
}: {
  actions: RuleAction[]
  onChange: (actions: RuleAction[]) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const seq = useRef(0)
  const rootRef = useRef<HTMLElement>(null)
  const [picker, setPicker] = useState<{
    anchor: HTMLButtonElement
    actionId?: string
  } | null>(null)
  const [focusId, setFocusId] = useState<string | null>(null)
  const [drag, setDrag] = useState<{
    from: string
    over: string
    below: boolean
  } | null>(null)
  const closePicker = useCallback(() => setPicker(null), [])

  useEffect(() => {
    if (!focusId) return
    const row = Array.from(
      rootRef.current?.querySelectorAll<HTMLElement>('[data-action-id]') ?? []
    ).find(row => row.dataset.actionId === focusId)
    row?.scrollIntoView?.({ block: 'nearest' })
    const target =
      row?.querySelector<HTMLElement>('input, textarea') ??
      row?.querySelector('button')
    target?.focus({ preventScroll: true })
    setFocusId(null)
  }, [focusId])

  const chooseAction = (type: ActionType) => {
    const previous = actions.find(action => action.id === picker?.actionId)
    if (previous?.type === type) {
      picker?.anchor.focus({ preventScroll: true })
      closePicker()
      return
    }
    const action: RuleAction = {
      id: previous?.id ?? `${id}-${seq.current++}`,
      type,
      param: type === 'mock' ? '200' : type === 'delay' ? '1000' : '',
      extra: type === 'mock' ? '{\n  "success": true\n}' : '',
    }
    onChange(
      previous
        ? actions.map(item => (item.id === previous.id ? action : item))
        : [...actions, action]
    )
    closePicker()
    setFocusId(action.id)
  }

  const updateAction = (actionId: string, patch: Partial<RuleAction>) =>
    onChange(
      actions.map(action =>
        action.id === actionId ? { ...action, ...patch } : action
      )
    )

  const moveAction = (index: number, delta: -1 | 1) => {
    const nextIndex = index + delta
    if (nextIndex < 0 || nextIndex >= actions.length) return
    const next = actions.slice()
    const [moved] = next.splice(index, 1)
    next.splice(nextIndex, 0, moved)
    onChange(next)
  }

  const dropAction = () => {
    if (!drag || drag.from === drag.over) return
    const moved = actions.find(action => action.id === drag.from)
    if (!moved) return
    const next = actions.filter(action => action.id !== drag.from)
    const target = next.findIndex(action => action.id === drag.over)
    if (target < 0) return
    next.splice(target + (drag.below ? 1 : 0), 0, moved)
    if (next.some((action, index) => action.id !== actions[index].id))
      onChange(next)
  }

  return (
    <section
      ref={rootRef}
      aria-labelledby={`${id}-title`}
      className="mt-5 border-t border-line pt-5"
    >
      <header className="mb-3 flex items-center gap-2">
        <Zap className="h-3.5 w-3.5 text-fg-muted" />
        <h2 id={`${id}-title`} className="text-[12px] font-medium">
          {t('rules.action.title')}
        </h2>
        <span className="rounded border border-line px-1 font-mono text-[10px] text-fg-faint">
          {actions.length}
        </span>
        <span className="ml-auto flex items-center gap-1 text-[10px] text-fg-faint">
          <ArrowDown className="h-3 w-3" />
          {t('rules.action.orderHint')}
        </span>
      </header>
      <div className="space-y-2">
        {actions.map((action, index) => {
          const Icon = ACTION_ICON[action.type]
          const meta =
            action.type === 'block' ? null : actionParamMeta(action.type, t)
          const prefix = `${id}-${action.id}`
          const showInsert =
            drag && drag.over === action.id && drag.from !== action.id
          return (
            <div
              key={action.id}
              role="group"
              aria-label={t('rules.action.item', { n: index + 1 })}
              data-action-id={action.id}
              className={cx(
                'wb-fade relative rounded-lg border border-line bg-elevated/15',
                drag?.from === action.id && 'opacity-40'
              )}
              onDragOver={event => {
                if (!drag) return
                event.preventDefault()
                event.dataTransfer.dropEffect = 'move'
                const rect = event.currentTarget.getBoundingClientRect()
                const below = event.clientY > rect.top + rect.height / 2
                if (drag.over !== action.id || drag.below !== below)
                  setDrag({ ...drag, over: action.id, below })
              }}
              onDrop={event => {
                if (!drag) return
                event.preventDefault()
                dropAction()
                setDrag(null)
              }}
            >
              {showInsert && (
                <span
                  className={cx(
                    'pointer-events-none absolute inset-x-2 h-0.5 bg-accent',
                    drag.below ? '-bottom-px' : '-top-px'
                  )}
                />
              )}
              <div className="flex items-center gap-2 px-3 py-2">
                {actions.length > 1 && (
                  <span
                    draggable
                    role="button"
                    tabIndex={0}
                    aria-label={t('rules.action.drag')}
                    title={t('rules.action.drag')}
                    className="cursor-grab rounded text-fg-faint outline-none focus-visible:ring-1 focus-visible:ring-accent active:cursor-grabbing"
                    onDragStart={event => {
                      event.dataTransfer.effectAllowed = 'move'
                      event.dataTransfer.setData('text/plain', action.id)
                      const row =
                        event.currentTarget.closest<HTMLElement>(
                          '[data-action-id]'
                        )
                      if (row) event.dataTransfer.setDragImage(row, 12, 16)
                      setDrag({
                        from: action.id,
                        over: action.id,
                        below: false,
                      })
                    }}
                    onDragEnd={() => setDrag(null)}
                    onKeyDown={event => {
                      if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown')
                        return
                      event.preventDefault()
                      moveAction(index, event.key === 'ArrowUp' ? -1 : 1)
                    }}
                  >
                    <GripVertical className="h-3 w-3" />
                  </span>
                )}
                <span className="font-mono text-[10px] text-fg-faint">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <button
                  type="button"
                  aria-label={`${t('rules.action.type')}: ${t(`rules.action.${action.type}`)}`}
                  aria-haspopup="dialog"
                  aria-expanded={picker?.actionId === action.id}
                  onClick={event =>
                    setPicker(
                      picker?.actionId === action.id
                        ? null
                        : { anchor: event.currentTarget, actionId: action.id }
                    )
                  }
                  className="flex h-7 min-w-0 items-center gap-2 rounded-control bg-elevated/70 px-2 text-2xs outline-none hover:bg-elevated focus-visible:ring-1 focus-visible:ring-accent"
                >
                  <Icon className="h-3.5 w-3.5 shrink-0 text-accent" />
                  <span className="truncate">
                    {t(`rules.action.${action.type}`)}
                  </span>
                  <ChevronDown className="ml-2 h-3 w-3 shrink-0 text-fg-faint" />
                </button>
                <div className="ml-auto flex shrink-0 items-center gap-1">
                  <IconButton
                    size="sm"
                    title={t('rules.action.moveUp')}
                    aria-label={t('rules.action.moveUp')}
                    disabled={index === 0}
                    onClick={() => moveAction(index, -1)}
                  >
                    <ArrowUp className="h-3 w-3" />
                  </IconButton>
                  <IconButton
                    size="sm"
                    title={t('rules.action.moveDown')}
                    aria-label={t('rules.action.moveDown')}
                    disabled={index === actions.length - 1}
                    onClick={() => moveAction(index, 1)}
                  >
                    <ArrowDown className="h-3 w-3" />
                  </IconButton>
                  <span className="mx-1 h-3 w-px bg-line" />
                  <Button
                    size="sm"
                    variant="ghost"
                    className="group"
                    icon={
                      <Trash2 className="h-3.5 w-3.5 group-hover:text-danger group-focus-visible:text-danger" />
                    }
                    title={t('rules.action.delete')}
                    aria-label={t('rules.action.delete')}
                    onClick={() =>
                      onChange(actions.filter(item => item.id !== action.id))
                    }
                  >
                    <span className="group-hover:text-danger group-focus-visible:text-danger">
                      {t('rules.action.delete')}
                    </span>
                  </Button>
                </div>
              </div>
              {!meta ? (
                <p className="px-4 pb-3 text-2xs text-fg-muted">
                  {t('rules.action.blockNote')}
                </p>
              ) : (
                <div className="grid grid-cols-[80px_minmax(0,1fr)] items-center gap-x-2.5 gap-y-2 px-4 pb-3">
                  {(['param', 'extra'] as const).map(field => {
                    const label =
                      field === 'param' ? meta.paramLabel : meta.extraLabel
                    if (field === 'extra' && !label) return null
                    const multiline = field === 'extra' && meta.extraMultiline
                    const Input = multiline ? 'textarea' : TextInput
                    return (
                      <Fragment key={field}>
                        <label
                          htmlFor={`${prefix}-${field}`}
                          className={cx(
                            'text-2xs text-fg-muted',
                            multiline && 'self-start pt-1.5'
                          )}
                        >
                          {label}
                        </label>
                        <Input
                          id={`${prefix}-${field}`}
                          spellCheck={false}
                          value={action[field] ?? ''}
                          onChange={event =>
                            updateAction(action.id, {
                              [field]: event.target.value,
                            })
                          }
                          placeholder={
                            field === 'param'
                              ? meta.paramPlaceholder
                              : meta.extraPlaceholder
                          }
                          rows={multiline ? 3 : undefined}
                          className={
                            multiline
                              ? 'rules-textarea w-full'
                              : 'rules-input w-full font-mono'
                          }
                        />
                      </Fragment>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
        {!actions.length && (
          <div className="rounded-control border border-dashed border-line px-3 py-5 text-center text-2xs text-fg-faint">
            {t('rules.action.empty')}
          </div>
        )}
      </div>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={!!picker && !picker.actionId}
        onClick={event =>
          setPicker(
            picker && !picker.actionId ? null : { anchor: event.currentTarget }
          )
        }
        className="mt-2.5 inline-flex h-7 items-center gap-1.5 rounded-control border border-dashed border-line-strong px-2.5 text-2xs text-fg-muted outline-none hover:border-accent hover:bg-accent/10 hover:text-accent focus-visible:ring-1 focus-visible:ring-accent"
      >
        <Plus className="h-3.5 w-3.5" />
        {t('rules.action.add')}
        <ChevronDown className="ml-1 h-3 w-3" />
      </button>
      {picker && (
        <ActionPicker
          key={picker.actionId ?? 'add'}
          anchor={picker.anchor}
          value={actions.find(action => action.id === picker.actionId)?.type}
          onSelect={chooseAction}
          onClose={closePicker}
        />
      )}
    </section>
  )
}
