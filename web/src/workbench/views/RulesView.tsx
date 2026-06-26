import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import {
  ArrowRightLeft,
  Ban,
  Clock,
  FileText,
  Filter,
  Link2,
  Plus,
  Search,
  Shuffle,
  Trash2,
  Wand2,
  Zap,
} from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { Button, Select, TextInput, Toggle } from '../ui/controls'
import { EmptyState, IconButton } from '../ui/primitives'
import { ConnIndicator, DetailBar, FieldGroup, Sidebar, SidebarItem, SplitView, StatusFooter } from '../ui/native'
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
  const [ready, setReady] = useState(false)

  const conditionTypeOpts = useMemo(() => conditionTypeOptions(t), [t])
  const conditionOpOpts = useMemo(() => conditionOpOptions(t), [t])
  const actionTypeOpts = useMemo(() => actionTypeOptions(t), [t])
  const logicOpts = useMemo(() => logicOptions(t), [t])

  // 挂载时从后端加载规则。
  useEffect(() => {
    let alive = true
    Bridge.getRules()
      .then((list) => {
        if (!alive) return
        setReady(true)
        if (!list) return
        const local = list.map(toLocalRule)
        setRules(local)
        setSelectedId((cur) => cur || local[0]?.id || '')
      })
      .catch(() => {
        if (alive) setReady(false)
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
    <SplitView
      status={
        <StatusFooter
          left={t('rules.statusbar', { total: rules.length, enabled: enabledCount })}
          right={<ConnIndicator connected={ready} />}
        />
      }
      sidebar={
        <Sidebar
          width={264}
          header={
            <>
              <Search className="h-3.5 w-3.5 shrink-0 text-fg-faint" />
              <input
                spellCheck={false}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t('rules.searchPlaceholder')}
                className="h-full min-w-0 flex-1 bg-transparent text-[12px] text-fg outline-none placeholder:text-fg-faint"
              />
              <span className="shrink-0 text-2xs tabular-nums text-fg-faint">
                {enabledCount}/{rules.length}
              </span>
            </>
          }
          footer={
            <Button variant="ghost" size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={addRule}>
              {t('rules.newRule')}
            </Button>
          }
        >
          {filtered.length === 0 ? (
            <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('rules.noMatch')}</div>
          ) : (
            filtered.map((r) => (
              <SidebarItem
                key={r.id}
                active={r.id === selectedId}
                dimmed={!r.enabled}
                onClick={() => setSelectedId(r.id)}
                leading={<Toggle checked={r.enabled} onChange={(v) => toggleRuleEnabled(r.id, v)} />}
                title={r.name}
                subtitle={summarize(r, t)}
                trailing={
                  <span className="font-mono text-2xs tabular-nums text-fg-faint" title={t('rules.priority')}>
                    #{r.priority}
                  </span>
                }
              />
            ))
          )}
        </Sidebar>
      }
    >
      {!selected ? (
        <EmptyState icon={<Shuffle className="h-8 w-8" />} title={t('rules.empty.title')} hint={t('rules.empty.hint')} />
      ) : (
        <>
          <DetailBar>
            <Shuffle className="h-4 w-4 shrink-0 text-accent" />
            <input
              spellCheck={false}
              value={selected.name}
              onChange={(e) => patchRule(selected.id, { name: e.target.value })}
              className="min-w-0 flex-1 bg-transparent text-[13px] font-semibold text-fg outline-none placeholder:text-fg-faint"
            />
            <span className="flex shrink-0 items-center gap-2">
              <Toggle checked={selected.enabled} onChange={(v) => toggleRuleEnabled(selected.id, v)} />
              <IconButton size="sm" tone="danger" title={t('rules.basic.delete')} onClick={() => deleteRule(selected.id)}>
                <Trash2 className="h-3.5 w-3.5" />
              </IconButton>
            </span>
          </DetailBar>

          <div className="min-h-0 flex-1 overflow-auto">
            <div className="flex items-center gap-3 border-b border-line px-3 py-2">
              <label className="flex shrink-0 items-center gap-1.5">
                <span className="text-2xs text-fg-muted">{t('rules.basic.priority')}</span>
                <TextInput
                  type="number"
                  width={72}
                  value={String(selected.priority)}
                  onChange={(e) => patchRule(selected.id, { priority: Number(e.target.value) || 0 })}
                />
              </label>
              <label className="flex min-w-0 flex-1 items-center gap-1.5">
                <span className="shrink-0 text-2xs text-fg-muted">{t('rules.basic.note')}</span>
                <TextInput
                  value={selected.note}
                  onChange={(e) => patchRule(selected.id, { note: e.target.value })}
                  placeholder={t('rules.basic.notePlaceholder')}
                  className="min-w-0 flex-1"
                />
              </label>
            </div>

            <FieldGroup
              title={t('rules.cond.title')}
              icon={<Filter className="h-3.5 w-3.5" />}
              right={
                <>
                  <Select
                    value={selected.logic}
                    onChange={(e) => patchRule(selected.id, { logic: e.target.value as Logic })}
                    options={logicOpts}
                  />
                  <Button size="sm" icon={<Plus className="h-3 w-3" />} onClick={() => addCondition(selected.id)}>
                    {t('rules.cond.add')}
                  </Button>
                </>
              }
              bodyClassName="divide-y divide-line/60"
            >
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
                      onChange={(e) => updateCondition(selected.id, c.id, { type: e.target.value as ConditionType })}
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
            </FieldGroup>

            <FieldGroup
              title={t('rules.action.title')}
              icon={<Zap className="h-3.5 w-3.5" />}
              right={
                <Button size="sm" icon={<Plus className="h-3 w-3" />} onClick={() => addAction(selected.id)}>
                  {t('rules.action.add')}
                </Button>
              }
              bodyClassName="divide-y divide-line/60"
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
                          onChange={(e) => updateAction(selected.id, a.id, { type: e.target.value as ActionType })}
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
                                <span className="mt-1.5 w-20 shrink-0 text-2xs text-fg-muted">{meta.extraLabel}</span>
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
            </FieldGroup>
          </div>
        </>
      )}
    </SplitView>
  )
}
