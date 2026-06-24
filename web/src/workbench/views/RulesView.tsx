import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  ArrowRightLeft,
  Ban,
  Clock,
  FileText,
  Filter,
  Info,
  Link2,
  Plus,
  Search,
  Shuffle,
  Trash2,
  Wand2,
  Zap,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, Field, Panel, Select, TextInput, Toggle } from '../ui/controls'
import { cx, EmptyState, IconButton, StatusDot } from '../ui/primitives'
import {
  toInterceptRule,
  toLocalRule,
  type ActionType,
  type Condition,
  type ConditionOp,
  type ConditionType,
  type Logic,
  type Rule,
  type RuleAction,
} from '../lib/rulesMap'
import { PageShell } from './PageShell'

/* ───────────────────────── 选项 ───────────────────────── */

const conditionTypeOptions = (t: TFunction): { value: ConditionType; label: string }[] => [
  { value: 'url', label: 'URL' },
  { value: 'host', label: 'Host' },
  { value: 'path', label: 'Path' },
  { value: 'method', label: t('rules.cond.type.method') },
  { value: 'reqHeader', label: t('rules.cond.type.reqHeader') },
  { value: 'status', label: t('rules.cond.type.status') },
  { value: 'query', label: t('rules.cond.type.query') },
]

const conditionOpOptions = (t: TFunction): { value: ConditionOp; label: string }[] => [
  { value: 'eq', label: t('rules.cond.op.eq') },
  { value: 'ne', label: t('rules.cond.op.ne') },
  { value: 'contains', label: t('rules.cond.op.contains') },
  { value: 'prefix', label: t('rules.cond.op.prefix') },
  { value: 'suffix', label: t('rules.cond.op.suffix') },
  { value: 'regex', label: t('rules.cond.op.regex') },
]

const actionTypeOptions = (t: TFunction): { value: ActionType; label: string }[] => [
  { value: 'redirect', label: t('rules.action.redirect') },
  { value: 'rewriteUrl', label: t('rules.action.rewriteUrl') },
  { value: 'setReqHeader', label: t('rules.action.setReqHeader') },
  { value: 'setResBody', label: t('rules.action.setResBody') },
  { value: 'mock', label: t('rules.action.mock') },
  { value: 'block', label: t('rules.action.block') },
  { value: 'delay', label: t('rules.action.delay') },
]

const logicOptions = (t: TFunction): { value: Logic; label: string }[] => [
  { value: 'and', label: t('rules.logic.and') },
  { value: 'or', label: t('rules.logic.or') },
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

const ACTION_ICON: Record<ActionType, typeof Shuffle> = {
  redirect: ArrowRightLeft,
  rewriteUrl: Link2,
  setReqHeader: FileText,
  setResBody: Wand2,
  mock: Zap,
  block: Ban,
  delay: Clock,
}

let seq = 100
const nextId = (p: string) => `${p}${seq++}`

/* ───────────────────────── 摘要工具 ───────────────────────── */

function summarize(rule: Rule, t: TFunction): string {
  if (rule.conditions.length === 0) return t('rules.summary.none')
  const sep = rule.logic === 'and' ? ' · ' : ' | '
  return rule.conditions
    .map((c) => `${CONDITION_TYPE_LABEL[c.type]}=${c.value || '…'}`)
    .join(sep)
}

/* 不同动作类型对应的参数输入占位 */
function actionParamMeta(
  type: ActionType,
  t: TFunction,
): {
  paramLabel: string
  paramPlaceholder: string
  extraLabel?: string
  extraPlaceholder?: string
  extraMultiline?: boolean
} {
  switch (type) {
    case 'redirect':
      return { paramLabel: t('rules.param.targetAddr'), paramPlaceholder: 'http://127.0.0.1:3000' }
    case 'rewriteUrl':
      return {
        paramLabel: t('rules.param.matchRegex'),
        paramPlaceholder: '^https://cdn\\.x\\.com',
        extraLabel: t('rules.param.replaceWith'),
        extraPlaceholder: 'https://cdn.local',
      }
    case 'setReqHeader':
      return {
        paramLabel: t('rules.param.headerName'),
        paramPlaceholder: 'Authorization',
        extraLabel: t('rules.param.headerValue'),
        extraPlaceholder: 'Bearer …',
      }
    case 'setResBody':
      return {
        paramLabel: t('rules.param.matchText'),
        paramPlaceholder: '"env":"prod"',
        extraLabel: t('rules.param.replaceWith'),
        extraPlaceholder: '"env":"dev"',
      }
    case 'mock':
      return {
        paramLabel: t('rules.param.statusCode'),
        paramPlaceholder: '200',
        extraLabel: t('rules.param.responseBodyJson'),
        extraPlaceholder: '{ }',
        extraMultiline: true,
      }
    case 'block':
      return { paramLabel: t('rules.param.blockNote'), paramPlaceholder: t('rules.param.blockPlaceholder') }
    case 'delay':
      return { paramLabel: t('rules.param.delayMs'), paramPlaceholder: '1000' }
    default:
      return { paramLabel: t('rules.param.param'), paramPlaceholder: '' }
  }
}

/* ───────────────────────── 主组件 ───────────────────────── */

export function RulesView() {
  const { t } = useTranslation()
  const [rules, setRules] = useState<Rule[]>([])
  const [selectedId, setSelectedId] = useState<string>('')
  const [query, setQuery] = useState('')

  const conditionTypeOpts = useMemo(() => conditionTypeOptions(t), [t])
  const conditionOpOpts = useMemo(() => conditionOpOptions(t), [t])
  const actionTypeOpts = useMemo(() => actionTypeOptions(t), [t])
  const logicOpts = useMemo(() => logicOptions(t), [t])

  // 挂载时从后端加载规则。
  useEffect(() => {
    let alive = true
    Bridge.getRules()
      .then((list) => {
        if (!alive || !list) return
        const local = list.map(toLocalRule)
        setRules(local)
        setSelectedId((cur) => cur || local[0]?.id || '')
      })
      .catch(() => {
        /* 未连接后端：保持空列表 */
      })
    return () => {
      alive = false
    }
  }, [])

  const filtered = rules.filter(
    (r) =>
      r.name.toLowerCase().includes(query.toLowerCase()) ||
      summarize(r, t).toLowerCase().includes(query.toLowerCase()),
  )
  const selected = rules.find((r) => r.id === selectedId) ?? null
  const enabledCount = rules.filter((r) => r.enabled).length

  /* —— 后端持久化（编辑防抖保存，开关/增删立即） —— */
  const saveTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({})
  // 临时草稿 id(createRule 失败时的兜底)不向后端发 update——后端查无此 id,只会静默无效。
  const isTempId = (id: string) => !id || id.startsWith('rtmp')
  const scheduleSave = (rule: Rule) => {
    if (isTempId(rule.id)) return
    clearTimeout(saveTimers.current[rule.id])
    saveTimers.current[rule.id] = setTimeout(() => {
      Bridge.updateRule(rule.id, toInterceptRule(rule)).catch(() => {})
    }, 400)
  }
  useEffect(() => () => Object.values(saveTimers.current).forEach(clearTimeout), [])

  /* —— 规则级更新 helper：本地更新 + 安排保存 —— */
  const patchRule = (id: string, patch: Partial<Rule>) =>
    setRules((rs) =>
      rs.map((r) => {
        if (r.id !== id) return r
        const next = { ...r, ...patch }
        scheduleSave(next)
        return next
      }),
    )

  // 启用开关：立即调用 toggleRule（影响实时流量，不走防抖）。
  // 取消该规则任何待发的防抖 update，避免其携带旧 enabled 快照把这次开关覆盖回去。
  const toggleRuleEnabled = (id: string, enabled: boolean) => {
    clearTimeout(saveTimers.current[id])
    setRules((rs) => rs.map((r) => (r.id === id ? { ...r, enabled } : r)))
    Bridge.toggleRule(id, enabled).catch(() => {})
  }

  const addRule = async () => {
    const draft: Rule = {
      id: nextId('rtmp'),
      name: t('rules.untitled'),
      enabled: true,
      priority: (rules.reduce((m, x) => Math.max(m, x.priority), 0) || 0) + 10,
      note: '',
      logic: 'and',
      conditions: [{ id: nextId('c'), type: 'host', op: 'eq', value: '' }],
      actions: [{ id: nextId('a'), type: 'redirect', param: '', extra: '' }],
    }
    const created = await Bridge.createRule(toInterceptRule(draft)).catch(() => null)
    const rule = created ? toLocalRule(created) : draft
    setRules((rs) => [...rs, rule])
    setSelectedId(rule.id)
  }

  const deleteRule = (id: string) => {
    Bridge.deleteRule(id).catch(() => {})
    setRules((rs) => rs.filter((r) => r.id !== id))
    setSelectedId((cur) => (cur === id ? '' : cur))
  }

  /* —— 条件操作 —— */
  const addCondition = (ruleId: string) =>
    patchRule(ruleId, {
      conditions: [
        ...(rules.find((r) => r.id === ruleId)?.conditions ?? []),
        { id: nextId('c'), type: 'url', op: 'contains', value: '' },
      ],
    })

  const updateCondition = (ruleId: string, condId: string, patch: Partial<Condition>) => {
    const rule = rules.find((r) => r.id === ruleId)
    if (!rule) return
    patchRule(ruleId, {
      conditions: rule.conditions.map((c) => (c.id === condId ? { ...c, ...patch } : c)),
    })
  }

  const removeCondition = (ruleId: string, condId: string) => {
    const rule = rules.find((r) => r.id === ruleId)
    if (!rule) return
    patchRule(ruleId, { conditions: rule.conditions.filter((c) => c.id !== condId) })
  }

  /* —— 动作操作 —— */
  const addAction = (ruleId: string) =>
    patchRule(ruleId, {
      actions: [
        ...(rules.find((r) => r.id === ruleId)?.actions ?? []),
        { id: nextId('a'), type: 'setReqHeader', param: '', extra: '' },
      ],
    })

  const updateAction = (ruleId: string, actId: string, patch: Partial<RuleAction>) => {
    const rule = rules.find((r) => r.id === ruleId)
    if (!rule) return
    patchRule(ruleId, {
      actions: rule.actions.map((a) => (a.id === actId ? { ...a, ...patch } : a)),
    })
  }

  const removeAction = (ruleId: string, actId: string) => {
    const rule = rules.find((r) => r.id === ruleId)
    if (!rule) return
    patchRule(ruleId, { actions: rule.actions.filter((a) => a.id !== actId) })
  }

  return (
    <PageShell
      icon={Shuffle}
      title={t('rules.title')}
      subtitle={t('rules.subtitle')}
      actions={
        <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={addRule}>
          {t('rules.newRule')}
        </Button>
      }
      contentWidth="full"
    >
      <div className="flex h-full min-h-0 overflow-hidden rounded-wb border border-line bg-surface">
        {/* ───── 左栏：规则列表 ───── */}
        <aside className="flex w-[280px] shrink-0 flex-col border-r border-line bg-inset/30">
          <div className="flex h-9 shrink-0 items-center gap-2 border-b border-line px-2.5">
            <Search className="h-3.5 w-3.5 text-fg-faint" />
            <input
              spellCheck={false}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('rules.searchPlaceholder')}
              className="h-full flex-1 bg-transparent text-[12px] text-fg outline-none placeholder:text-fg-faint"
            />
            <span className="shrink-0 text-2xs tabular-nums text-fg-faint">
              {enabledCount}/{rules.length}
            </span>
          </div>

          <div className="min-h-0 flex-1 overflow-auto">
            {filtered.length === 0 ? (
              <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('rules.noMatch')}</div>
            ) : (
              filtered.map((r) => {
                const active = r.id === selectedId
                return (
                  <button
                    key={r.id}
                    type="button"
                    onClick={() => setSelectedId(r.id)}
                    className={cx(
                      'relative flex w-full items-center gap-2 border-b border-line/60 px-2.5 py-2 text-left transition-colors',
                      active ? 'bg-accent/12' : 'hover:bg-elevated/50',
                    )}
                  >
                    {active && <span className="absolute inset-y-0 left-0 w-[2px] bg-accent" />}
                    <span
                      className="shrink-0"
                      onClick={(e) => e.stopPropagation()}
                      onKeyDown={(e) => e.stopPropagation()}
                      role="presentation"
                    >
                      <Toggle checked={r.enabled} onChange={(v) => toggleRuleEnabled(r.id, v)} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5">
                        <StatusDot tone={r.enabled ? 'ok' : 'neutral'} />
                        <span
                          className={cx(
                            'truncate text-[12.5px]',
                            r.enabled ? 'text-fg' : 'text-fg-muted',
                          )}
                        >
                          {r.name}
                        </span>
                      </span>
                      <span className="mt-0.5 block truncate text-2xs text-fg-faint">{summarize(r, t)}</span>
                    </span>
                    <span className="shrink-0 font-mono text-2xs tabular-nums text-fg-faint" title={t('rules.priority')}>
                      #{r.priority}
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </aside>

        {/* ───── 右栏：编辑器 ───── */}
        <div className="min-h-0 flex-1 overflow-auto">
          {!selected ? (
            <EmptyState
              icon={<Shuffle className="h-10 w-10" />}
              title={t('rules.empty.title')}
              hint={t('rules.empty.hint')}
            />
          ) : (
            <div className="flex flex-col gap-4 p-4">
              {/* 基本信息 */}
              <Panel title={t('rules.basic.title')} icon={<Info className="h-4 w-4" />}>
                <Field label={t('rules.basic.name')}>
                  <TextInput
                    value={selected.name}
                    onChange={(e) => patchRule(selected.id, { name: e.target.value })}
                    width={260}
                  />
                </Field>
                <Field label={t('rules.basic.enable')} hint={t('rules.basic.enableHint')}>
                  <Toggle checked={selected.enabled} onChange={(v) => toggleRuleEnabled(selected.id, v)} />
                </Field>
                <Field label={t('rules.basic.priority')} hint={t('rules.basic.priorityHint')}>
                  <TextInput
                    type="number"
                    value={String(selected.priority)}
                    onChange={(e) => patchRule(selected.id, { priority: Number(e.target.value) || 0 })}
                    width={90}
                  />
                </Field>
                <Field label={t('rules.basic.note')}>
                  <TextInput
                    value={selected.note}
                    onChange={(e) => patchRule(selected.id, { note: e.target.value })}
                    placeholder={t('rules.basic.notePlaceholder')}
                    width={260}
                  />
                </Field>
                <Field label={t('rules.basic.delete')} hint={t('rules.basic.deleteHint')}>
                  <Button
                    variant="danger"
                    size="sm"
                    icon={<Trash2 className="h-3.5 w-3.5" />}
                    onClick={() => deleteRule(selected.id)}
                  >
                    {t('rules.basic.delete')}
                  </Button>
                </Field>
              </Panel>

              {/* 匹配条件 */}
              <Panel
                title={t('rules.cond.title')}
                icon={<Filter className="h-4 w-4" />}
                right={
                  <Button size="sm" icon={<Plus className="h-3 w-3" />} onClick={() => addCondition(selected.id)}>
                    {t('rules.cond.add')}
                  </Button>
                }
              >
                <Field label={t('rules.cond.logic')} hint={t('rules.cond.logicHint')}>
                  <Select
                    value={selected.logic}
                    onChange={(e) => patchRule(selected.id, { logic: e.target.value as Logic })}
                    options={logicOpts}
                  />
                </Field>

                {selected.conditions.length === 0 ? (
                  <div className="px-3 py-4 text-2xs text-fg-faint">{t('rules.cond.empty')}</div>
                ) : (
                  selected.conditions.map((c, i) => (
                    <div key={c.id} className="flex items-center gap-2 px-3 py-2">
                      <span className="w-7 shrink-0 text-center font-mono text-2xs text-fg-faint">
                        {i === 0
                          ? t('rules.cond.when')
                          : selected.logic === 'and'
                            ? t('rules.cond.and')
                            : t('rules.cond.or')}
                      </span>
                      <Select
                        value={c.type}
                        onChange={(e) =>
                          updateCondition(selected.id, c.id, { type: e.target.value as ConditionType })
                        }
                        options={conditionTypeOpts}
                      />
                      {c.type === 'reqHeader' && (
                        <TextInput
                          value={c.name ?? ''}
                          onChange={(e) => updateCondition(selected.id, c.id, { name: e.target.value })}
                          placeholder={t('rules.param.headerName')}
                          className="w-32 font-mono"
                        />
                      )}
                      <Select
                        value={c.op}
                        onChange={(e) => updateCondition(selected.id, c.id, { op: e.target.value as ConditionOp })}
                        options={conditionOpOpts}
                      />
                      <TextInput
                        value={c.value}
                        onChange={(e) => updateCondition(selected.id, c.id, { value: e.target.value })}
                        placeholder={t('rules.cond.matchValue')}
                        className="flex-1 font-mono"
                      />
                      <IconButton
                        tone="danger"
                        size="sm"
                        title={t('rules.cond.delete')}
                        onClick={() => removeCondition(selected.id, c.id)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </IconButton>
                    </div>
                  ))
                )}
              </Panel>

              {/* 执行动作 */}
              <Panel
                title={t('rules.action.title')}
                icon={<Zap className="h-4 w-4" />}
                right={
                  <Button size="sm" icon={<Plus className="h-3 w-3" />} onClick={() => addAction(selected.id)}>
                    {t('rules.action.add')}
                  </Button>
                }
              >
                {selected.actions.length === 0 ? (
                  <div className="px-3 py-4 text-2xs text-fg-faint">{t('rules.action.empty')}</div>
                ) : (
                  selected.actions.map((a) => {
                    const meta = actionParamMeta(a.type, t)
                    const ActIcon = ACTION_ICON[a.type]
                    return (
                      <div key={a.id} className="px-3 py-2.5">
                        <div className="flex items-center gap-2">
                          <ActIcon className="h-3.5 w-3.5 shrink-0 text-accent" />
                          <Select
                            value={a.type}
                            onChange={(e) =>
                              updateAction(selected.id, a.id, { type: e.target.value as ActionType })
                            }
                            options={actionTypeOpts}
                          />
                          <span className="ml-auto" />
                          <IconButton
                            tone="danger"
                            size="sm"
                            title={t('rules.action.delete')}
                            onClick={() => removeAction(selected.id, a.id)}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </IconButton>
                        </div>

                        {a.type === 'block' ? (
                          <div className="mt-2 ml-[22px] text-2xs text-fg-faint">{t('rules.action.blockNote')}</div>
                        ) : (
                          <div className="mt-2 ml-[22px] flex flex-col gap-2">
                            <label className="flex items-center gap-2">
                              <span className="w-20 shrink-0 text-2xs text-fg-muted">{meta.paramLabel}</span>
                              <TextInput
                                value={a.param}
                                onChange={(e) => updateAction(selected.id, a.id, { param: e.target.value })}
                                placeholder={meta.paramPlaceholder}
                                className="flex-1 font-mono"
                              />
                            </label>

                            {meta.extraLabel &&
                              (meta.extraMultiline ? (
                                <div className="flex items-start gap-2">
                                  <span className="mt-1.5 w-20 shrink-0 text-2xs text-fg-muted">
                                    {meta.extraLabel}
                                  </span>
                                  <textarea
                                    spellCheck={false}
                                    value={a.extra ?? ''}
                                    onChange={(e) => updateAction(selected.id, a.id, { extra: e.target.value })}
                                    placeholder={meta.extraPlaceholder}
                                    rows={5}
                                    className="flex-1 resize-y rounded-wb border border-line bg-inset px-2 py-1.5 font-mono text-[11.5px] leading-relaxed text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface"
                                  />
                                </div>
                              ) : (
                                <label className="flex items-center gap-2">
                                  <span className="w-20 shrink-0 text-2xs text-fg-muted">{meta.extraLabel}</span>
                                  <TextInput
                                    value={a.extra ?? ''}
                                    onChange={(e) => updateAction(selected.id, a.id, { extra: e.target.value })}
                                    placeholder={meta.extraPlaceholder}
                                    className="flex-1 font-mono"
                                  />
                                </label>
                              ))}
                          </div>
                        )}
                      </div>
                    )
                  })
                )}
              </Panel>
            </div>
          )}
        </div>
      </div>
    </PageShell>
  )
}
