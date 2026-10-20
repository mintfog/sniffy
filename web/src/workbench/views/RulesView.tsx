import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import { Events } from '@wailsio/runtime'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  Check,
  Copy,
  Filter,
  ListFilter,
  MoreHorizontal,
  Plus,
  Power,
  PowerOff,
  Search,
  Shuffle,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { takeRuleSelection } from '../lib/windows'
import { Button, TextInput, Toggle } from '../ui/controls'
import { SelectMenu } from '../ui/SelectMenu'
import { cx, EmptyState, IconButton, LoadingSpinner } from '../ui/primitives'
import { SplitView, StatusFooter } from '../ui/native'
import { ContextMenu, type MenuNode } from '../ui/Menu'
import { RuleActions } from './rules/RuleActions'
import { ACTION_ICON } from './rules/actions'
import './rules/rules.css'
import {
  toInterceptRule,
  toLocalRule,
  type Condition,
  type ConditionOp,
  type ConditionType,
  type Rule,
} from '../lib/rulesMap'

const conditionTypeOptions = (
  t: TFunction
): { value: ConditionType; label: string }[] => [
  { value: 'url', label: 'URL' },
  { value: 'host', label: 'Host' },
  { value: 'path', label: 'Path' },
  { value: 'method', label: t('rules.cond.type.method') },
  { value: 'reqHeader', label: t('rules.cond.type.reqHeader') },
  { value: 'status', label: t('rules.cond.type.status') },
  { value: 'query', label: t('rules.cond.type.query') },
]

const conditionOpOptions = (
  t: TFunction
): { value: ConditionOp; label: string }[] => [
  { value: 'eq', label: t('rules.cond.op.eq') },
  { value: 'ne', label: t('rules.cond.op.ne') },
  { value: 'contains', label: t('rules.cond.op.contains') },
  { value: 'prefix', label: t('rules.cond.op.prefix') },
  { value: 'suffix', label: t('rules.cond.op.suffix') },
  { value: 'regex', label: t('rules.cond.op.regex') },
]

const CONDITION_TYPE_LABEL: Record<ConditionType, string> = {
  url: 'URL',
  host: 'Host',
  path: 'Path',
  method: 'Method',
  reqHeader: 'Header',
  status: 'Status',
  query: 'Query',
}

let seq = 100
const nextId = (p: string) => `${p}${seq++}`

// 创建失败时保留本地草稿；临时标识不能用于后端更新。
const isTempId = (id: string) => !id || id.startsWith('rtmp')

function summarize(rule: Rule, t: TFunction): string {
  if (rule.conditions.length === 0) return t('rules.summary.none')
  const sep = rule.logic === 'and' ? ' · ' : ' | '
  return rule.conditions
    .map(
      c =>
        `${CONDITION_TYPE_LABEL[c.type]} ${t(`rules.cond.op.${c.op}`)} ${c.value || '…'}`
    )
    .join(sep)
}

export function RulesView() {
  const { t } = useTranslation()
  const moreId = useId()
  const newRuleId = useId()
  const [rules, setRules] = useState<Rule[]>([])
  const [selectedId, setSelectedId] = useState<string>('')
  const [query, setQuery] = useState('')
  const [ready, setReady] = useState(false)
  const [loadError, setLoadError] = useState(false)
  const [ctxMenu, setCtxMenu] = useState<{
    x: number
    y: number
    ruleId: string
    trigger: HTMLElement
  } | null>(null)
  const [saveStates, setSaveStates] = useState<
    Record<string, 'pending' | 'saving' | 'saved' | 'error'>
  >({})
  const [focusNameId, setFocusNameId] = useState<string | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)

  const saveRequests = useRef(new Map<string, Promise<boolean>>())
  const saveTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({})

  const conditionTypeOpts = useMemo(() => conditionTypeOptions(t), [t])
  const conditionOpOpts = useMemo(() => conditionOpOptions(t), [t])

  useEffect(() => {
    let alive = true
    setSelectedId(
      takeRuleSelection() ||
        new URLSearchParams(window.location.search).get('rule') ||
        ''
    )
    Bridge.getRules()
      .then(list => {
        if (!alive) return
        const local = (list ?? []).map(toLocalRule)
        setReady(true)
        setRules(local)
        setSelectedId(cur => cur || local[0]?.id || '')
      })
      .catch(() => {
        if (alive) setLoadError(true)
      })
    let off = () => {}
    try {
      off = Events.On('rule_select', (event: { data?: unknown }) => {
        if (typeof event.data !== 'string' || !event.data) return
        takeRuleSelection()
        setQuery('')
        setSelectedId(event.data)
      })
    } catch {
      // 定位事件仅用于跨窗口跳转，订阅失败不影响规则编辑。
    }
    return () => {
      alive = false
      off()
    }
  }, [])

  const searchQuery = query.toLowerCase()
  const filtered = rules
    .filter(
      r =>
        r.name.toLowerCase().includes(searchQuery) ||
        summarize(r, t).toLowerCase().includes(searchQuery)
    )
    .sort((a, b) => a.priority - b.priority)
  const selected = rules.find(r => r.id === selectedId) ?? null
  const lookupError =
    selectedId && !selected
      ? loadError
        ? 'failed'
        : ready
          ? 'missing'
          : ''
      : ''
  const enabledCount = rules.filter(r => r.enabled).length

  useEffect(() => {
    if (!focusNameId || selectedId !== focusNameId) return
    nameRef.current?.focus()
    nameRef.current?.select()
    setFocusNameId(null)
  }, [focusNameId, selectedId])

  const saveRule = (rule: Rule) => {
    setSaveStates(states => ({ ...states, [rule.id]: 'saving' }))
    // 同一规则按编辑顺序写入，避免较早的请求在新编辑之后落盘。
    const previous = saveRequests.current.get(rule.id) ?? Promise.resolve(true)
    const request = previous
      .then(() => Bridge.updateRule(rule.id, toInterceptRule(rule)))
      .then(updated => updated !== null)
      .catch(() => false)
    saveRequests.current.set(rule.id, request)
    void request.then(saved => {
      if (saveRequests.current.get(rule.id) !== request) return
      saveRequests.current.delete(rule.id)
      if (saveTimers.current[rule.id]) return
      setSaveStates(states => ({
        ...states,
        [rule.id]: saved ? 'saved' : 'error',
      }))
    })
  }
  const scheduleSave = (rule: Rule) => {
    if (isTempId(rule.id)) return
    setSaveStates(states => ({ ...states, [rule.id]: 'pending' }))
    clearTimeout(saveTimers.current[rule.id])
    saveTimers.current[rule.id] = setTimeout(() => {
      delete saveTimers.current[rule.id]
      saveRule(rule)
    }, 400)
  }
  useEffect(
    () => () => Object.values(saveTimers.current).forEach(clearTimeout),
    []
  )

  const patchRule = (rule: Rule, patch: Partial<Rule>) => {
    const next = { ...rule, ...patch }
    setRules(rules => rules.map(item => (item.id === rule.id ? next : item)))
    scheduleSave(next)
  }

  const toggleRuleEnabled = (rule: Rule, enabled: boolean) => {
    // 开关影响实时流量，立即提交完整编辑，并取消持有旧开关状态的防抖保存。
    clearTimeout(saveTimers.current[rule.id])
    delete saveTimers.current[rule.id]
    const next = { ...rule, enabled }
    setRules(rules => rules.map(item => (item.id === rule.id ? next : item)))
    if (!isTempId(rule.id)) saveRule(next)
  }

  const createRule = async (draft: Rule) => {
    const created = await Bridge.createRule(toInterceptRule(draft)).catch(
      () => null
    )
    const rule = created ? toLocalRule(created) : draft
    setRules(rules => [...rules, rule])
    setSelectedId(rule.id)
    setQuery('')
    setFocusNameId(rule.id)
  }

  const addRule = () =>
    createRule({
      id: nextId('rtmp'),
      name: t('rules.untitled'),
      enabled: true,
      priority: (rules.reduce((m, x) => Math.max(m, x.priority), 0) || 0) + 10,
      note: '',
      logic: 'and',
      conditions: [{ id: nextId('c'), type: 'host', op: 'eq', value: '' }],
      actions: [],
    })

  const deleteRule = (id: string) => {
    clearTimeout(saveTimers.current[id])
    delete saveTimers.current[id]
    Bridge.deleteRule(id).catch(() => {})
    setRules(rs => rs.filter(r => r.id !== id))
    setSelectedId(cur => (cur === id ? '' : cur))
    if (selectedId === id)
      document.getElementById(newRuleId)?.focus({ preventScroll: true })
  }

  const duplicateRule = (source: Rule) =>
    createRule({
      ...source,
      id: nextId('rtmp'),
      name: `${source.name} ${t('rules.duplicateSuffix')}`,
      conditions: source.conditions.map(condition => ({
        ...condition,
        id: nextId('c'),
      })),
      actions: source.actions.map(action => ({ ...action, id: nextId('a') })),
    })

  const handleRuleContextMenu = (
    id: string,
    e: ReactMouseEvent<HTMLElement>
  ) => {
    e.preventDefault()
    e.stopPropagation()
    setSelectedId(id)
    setCtxMenu({
      x: e.clientX,
      y: e.clientY,
      ruleId: id,
      trigger: e.currentTarget,
    })
  }

  const contextRule = rules.find(rule => rule.id === ctxMenu?.ruleId)
  const ctxItems: MenuNode[] = contextRule
    ? [
        {
          label: contextRule.enabled
            ? t('rules.context.disable')
            : t('rules.context.enable'),
          icon: contextRule.enabled ? PowerOff : Power,
          onSelect: () => toggleRuleEnabled(contextRule, !contextRule.enabled),
        },
        {
          label: t('rules.context.duplicate'),
          icon: Copy,
          onSelect: () => void duplicateRule(contextRule),
        },
        { type: 'separator' },
        {
          label: t('rules.context.delete'),
          icon: Trash2,
          danger: true,
          onSelect: () => deleteRule(contextRule.id),
        },
      ]
    : []

  const updateCondition = (
    rule: Rule,
    condId: string,
    patch: Partial<Condition>
  ) => {
    patchRule(rule, {
      conditions: rule.conditions.map(c =>
        c.id === condId ? { ...c, ...patch } : c
      ),
    })
  }

  const selectedSaveState = selected
    ? isTempId(selected.id)
      ? 'draft'
      : (saveStates[selected.id] ?? 'saved')
    : 'saved'

  return (
    <SplitView
      status={
        <StatusFooter
          left={t('rules.statusbar', {
            total: rules.length,
            enabled: enabledCount,
          })}
        />
      }
      sidebar={
        <aside className="flex w-[210px] shrink-0 flex-col border-r border-line bg-inset/40 max-[800px]:w-[190px] min-[1000px]:w-56">
          <div className="px-4 pt-5 pb-4">
            <div className="mb-3 flex items-center justify-between">
              <h1 className="text-[14px] font-semibold">{t('rules.title')}</h1>
              <span className="text-2xs text-fg-faint">{rules.length}</span>
            </div>
            <Button
              id={newRuleId}
              variant="primary"
              icon={<Plus className="h-3.5 w-3.5" />}
              onClick={addRule}
              className="h-8 w-full"
            >
              {t('rules.newRule')}
            </Button>
            <div className="mt-3 flex h-8 items-center gap-2 rounded-control bg-elevated/60 px-2 text-fg-faint">
              <Search className="h-3.5 w-3.5 shrink-0" />
              <input
                aria-label={t('rules.searchPlaceholder')}
                spellCheck={false}
                value={query}
                onChange={event => setQuery(event.target.value)}
                placeholder={t('rules.searchPlaceholder')}
                className="min-w-0 flex-1 bg-transparent text-2xs text-fg outline-none placeholder:text-fg-faint"
              />
            </div>
          </div>
          <div className="mb-2 flex items-center justify-between px-4 text-[10px] text-fg-faint">
            <span>{t('rules.list')}</span>
            <span>{filtered.length}</span>
          </div>
          <div className="min-h-0 flex-1 space-y-1 overflow-auto px-2">
            {!filtered.length && (
              <div className="px-3 py-6 text-center text-2xs text-fg-faint">
                {t('rules.noMatch')}
              </div>
            )}
            {filtered.map(rule => {
              const Icon = rule.actions[0]
                ? ACTION_ICON[rule.actions[0].type]
                : Shuffle
              const active = rule.id === selectedId
              return (
                <button
                  key={rule.id}
                  type="button"
                  aria-current={active ? 'true' : undefined}
                  onClick={() => {
                    setSelectedId(rule.id)
                    setCtxMenu(null)
                  }}
                  onContextMenu={event => handleRuleContextMenu(rule.id, event)}
                  title={`${summarize(rule, t)} · ${t('rules.priority')} ${rule.priority}`}
                  className={cx(
                    'flex w-full items-start gap-2 rounded-lg border p-2.5 text-left outline-none transition-colors focus-visible:ring-1 focus-visible:ring-accent',
                    active
                      ? 'border-accent/20 bg-accent/10'
                      : 'border-transparent hover:bg-elevated/50',
                    !rule.enabled && 'opacity-60'
                  )}
                >
                  <span
                    className={cx(
                      'flex h-7 w-7 shrink-0 items-center justify-center rounded-control',
                      active
                        ? 'bg-accent/10 text-accent'
                        : 'bg-elevated/60 text-fg-faint'
                    )}
                  >
                    <Icon className="h-3.5 w-3.5" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2">
                      <span className="truncate text-2xs font-medium">
                        {rule.name || t('rules.untitled')}
                      </span>
                      <span
                        className={cx(
                          'ml-auto h-1.5 w-1.5 shrink-0 rounded-full',
                          rule.enabled ? 'bg-ok' : 'bg-fg-faint'
                        )}
                      />
                    </span>
                    <span className="mt-1 block truncate text-[10px] text-fg-faint">
                      {summarize(rule, t)}
                    </span>
                  </span>
                </button>
              )
            })}
          </div>
          <div className="flex items-center gap-1.5 px-4 py-4 text-[10px] text-fg-faint">
            <ListFilter className="h-3 w-3" />
            {t('rules.priorityOrder')}
          </div>
        </aside>
      }
    >
      {!selected ? (
        <EmptyState
          icon={<Shuffle className="h-8 w-8" />}
          title={
            lookupError
              ? t(`rules.open.${lookupError}`)
              : t('rules.empty.title')
          }
          hint={lookupError ? undefined : t('rules.empty.hint')}
        />
      ) : (
        <>
          <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-6 max-[800px]:px-4">
            <SlidersHorizontal className="h-3.5 w-3.5 text-fg-faint" />
            <span className="text-2xs text-fg-muted">{t('rules.edit')}</span>
            <span
              role="status"
              className={cx(
                'ml-auto flex items-center gap-1 text-[10px]',
                selectedSaveState === 'error' || selectedSaveState === 'draft'
                  ? 'text-warn'
                  : 'text-fg-faint'
              )}
            >
              {selectedSaveState === 'saved' && (
                <Check className="h-3 w-3 text-ok" />
              )}
              {(selectedSaveState === 'pending' ||
                selectedSaveState === 'saving') && (
                <LoadingSpinner className="h-3 w-3" />
              )}
              {t(`rules.save.${selectedSaveState}`)}
            </span>
            {selectedSaveState === 'error' && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => saveRule(selected)}
              >
                {t('rules.save.retry')}
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              icon={<Copy className="h-3 w-3" />}
              onClick={() => duplicateRule(selected)}
            >
              {t('rules.context.duplicate')}
            </Button>
            <IconButton
              id={moreId}
              size="sm"
              title={t('rules.more')}
              aria-label={t('rules.more')}
              aria-haspopup="menu"
              aria-expanded={ctxMenu?.trigger.id === moreId}
              aria-controls={
                ctxMenu?.trigger.id === moreId ? `${moreId}-menu` : undefined
              }
              onClick={event => {
                if (ctxMenu?.trigger === event.currentTarget) {
                  setCtxMenu(null)
                  return
                }
                const rect = event.currentTarget.getBoundingClientRect()
                setCtxMenu({
                  x: rect.right - 200,
                  y: rect.bottom + 4,
                  ruleId: selected.id,
                  trigger: event.currentTarget,
                })
              }}
            >
              <MoreHorizontal className="h-4 w-4" />
            </IconButton>
          </header>
          <div className="min-h-0 flex-1 overflow-auto px-6 py-6 max-[800px]:px-4">
            <div className="mx-auto max-w-[900px]">
              <div className="flex flex-wrap items-center gap-x-5 gap-y-2.5">
                <label className="flex min-w-0 max-w-[370px] flex-[1_1_240px] items-center gap-2.5">
                  <span className="shrink-0 text-2xs text-fg-muted">
                    {t('rules.basic.name')}
                  </span>
                  <TextInput
                    ref={nameRef}
                    value={selected.name}
                    onChange={event =>
                      patchRule(selected, { name: event.target.value })
                    }
                    className="rules-input w-full"
                  />
                </label>
                <label
                  className="flex items-center gap-2.5"
                  title={t('rules.basic.priorityHint')}
                >
                  <span className="text-2xs text-fg-muted">
                    {t('rules.basic.priority')}
                  </span>
                  <TextInput
                    type="number"
                    value={selected.priority}
                    onChange={event =>
                      patchRule(selected, {
                        priority: Number(event.target.value) || 0,
                      })
                    }
                    className="rules-input w-16 font-mono"
                  />
                </label>
                <label className="ml-auto flex items-center gap-2 text-[10px] text-fg-muted">
                  <span>
                    {t(selected.enabled ? 'rules.enabled' : 'rules.disabled')}
                  </span>
                  <Toggle
                    checked={selected.enabled}
                    onChange={enabled => toggleRuleEnabled(selected, enabled)}
                  />
                </label>
              </div>
              <label className="mt-2.5 flex items-center gap-2.5">
                <span className="w-11 shrink-0 text-2xs text-fg-faint">
                  {t('rules.basic.note')}
                </span>
                <TextInput
                  value={selected.note}
                  onChange={event =>
                    patchRule(selected, { note: event.target.value })
                  }
                  placeholder={t('rules.basic.notePlaceholder')}
                  className="rules-input w-full"
                />
              </label>
              <section aria-label={t('rules.cond.title')} className="mt-7">
                <header className="mb-4 flex flex-wrap items-center gap-2">
                  <Filter className="h-3.5 w-3.5 text-fg-muted" />
                  <h2 className="text-[12px] font-medium">
                    {t('rules.cond.title')}
                  </h2>
                  <span className="rounded border border-line px-1 font-mono text-[10px] text-fg-faint">
                    {selected.conditions.length}
                  </span>
                  <div
                    role="group"
                    aria-label={t('rules.cond.logic')}
                    className="ml-auto flex gap-0.5 rounded-control border border-line bg-inset p-0.5"
                  >
                    {(['and', 'or'] as const).map(logic => (
                      <button
                        key={logic}
                        type="button"
                        aria-pressed={selected.logic === logic}
                        title={t(`rules.logic.${logic}`)}
                        onClick={() => patchRule(selected, { logic })}
                        className={cx(
                          'flex h-6 items-center gap-1 rounded px-2 text-[10px] outline-none focus-visible:ring-1 focus-visible:ring-accent',
                          selected.logic === logic
                            ? 'bg-elevated text-fg shadow-raise'
                            : 'text-fg-faint hover:text-fg'
                        )}
                      >
                        {selected.logic === logic && (
                          <Check className="h-3 w-3 text-accent" />
                        )}
                        {t(`rules.logic.${logic}Short`)}
                      </button>
                    ))}
                  </div>
                </header>
                {!!selected.conditions.length && (
                  <div
                    className="rules-condition-grid mb-2 text-[10px] text-fg-faint"
                    aria-hidden="true"
                  >
                    <span />
                    <span>{t('rules.cond.field')}</span>
                    <span>{t('rules.cond.operator')}</span>
                    <span>{t('rules.cond.matchValue')}</span>
                    <span />
                  </div>
                )}
                <div className="space-y-2">
                  {selected.conditions.map((condition, index) => (
                    <div key={condition.id} className="rules-condition-grid">
                      <span className="font-mono text-[10px] text-fg-faint">
                        {index + 1}
                      </span>
                      <SelectMenu
                        label={t('rules.cond.field')}
                        value={condition.type}
                        onChange={type =>
                          updateCondition(selected, condition.id, { type })
                        }
                        options={conditionTypeOpts}
                        className="w-full min-w-0"
                      />
                      <SelectMenu
                        label={t('rules.cond.operator')}
                        value={condition.op}
                        onChange={op =>
                          updateCondition(selected, condition.id, { op })
                        }
                        options={conditionOpOpts}
                        className="w-full min-w-0"
                      />
                      <TextInput
                        aria-label={t('rules.cond.matchValue')}
                        value={condition.value}
                        onChange={event =>
                          updateCondition(selected, condition.id, {
                            value: event.target.value,
                          })
                        }
                        placeholder={t('rules.cond.matchValue')}
                        className="rules-input w-full font-mono"
                      />
                      <IconButton
                        size="sm"
                        tone="danger"
                        title={t('rules.cond.delete')}
                        aria-label={t('rules.cond.delete')}
                        onClick={() =>
                          patchRule(selected, {
                            conditions: selected.conditions.filter(
                              item => item.id !== condition.id
                            ),
                          })
                        }
                      >
                        <X className="h-3 w-3" />
                      </IconButton>
                      {condition.type === 'reqHeader' && (
                        <label className="col-start-2 col-end-5 flex items-center gap-2 text-2xs text-fg-muted">
                          <span className="shrink-0">
                            {t('rules.param.headerName')}
                          </span>
                          <TextInput
                            value={condition.name ?? ''}
                            onChange={event =>
                              updateCondition(selected, condition.id, {
                                name: event.target.value,
                              })
                            }
                            placeholder={t('rules.param.headerName')}
                            className="rules-input w-full font-mono"
                          />
                        </label>
                      )}
                    </div>
                  ))}
                  {!selected.conditions.length && (
                    <p className="py-3 text-2xs text-fg-faint">
                      {t('rules.cond.empty')}
                    </p>
                  )}
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  icon={<Plus className="h-3 w-3" />}
                  onClick={() =>
                    patchRule(selected, {
                      conditions: [
                        ...selected.conditions,
                        {
                          id: nextId('c'),
                          type: 'url',
                          op: 'contains',
                          value: '',
                        },
                      ],
                    })
                  }
                  className="mt-2 ml-5"
                >
                  {t('rules.cond.add')}
                </Button>
              </section>
              <RuleActions
                key={selected.id}
                actions={selected.actions}
                onChange={actions => patchRule(selected, { actions })}
              />
            </div>
          </div>
        </>
      )}
      {ctxMenu && ctxItems.length > 0 && (
        <ContextMenu
          x={ctxMenu.x}
          y={ctxMenu.y}
          items={ctxItems}
          trigger={ctxMenu.trigger}
          onClose={() => setCtxMenu(null)}
        />
      )}
    </SplitView>
  )
}
