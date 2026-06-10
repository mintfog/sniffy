import { useEffect, useRef, useState } from 'react'
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

const CONDITION_TYPE_OPTIONS: { value: ConditionType; label: string }[] = [
  { value: 'url', label: 'URL' },
  { value: 'host', label: 'Host' },
  { value: 'path', label: 'Path' },
  { value: 'method', label: '请求方法' },
  { value: 'reqHeader', label: '请求头' },
  { value: 'status', label: '响应状态码' },
  { value: 'query', label: 'Query 参数' },
]

const CONDITION_OP_OPTIONS: { value: ConditionOp; label: string }[] = [
  { value: 'eq', label: '等于' },
  { value: 'ne', label: '不等于' },
  { value: 'contains', label: '包含' },
  { value: 'prefix', label: '前缀' },
  { value: 'suffix', label: '后缀' },
  { value: 'regex', label: '正则' },
]

const ACTION_TYPE_OPTIONS: { value: ActionType; label: string }[] = [
  { value: 'redirect', label: '重定向' },
  { value: 'rewriteUrl', label: '改写 URL' },
  { value: 'setReqHeader', label: '改请求头' },
  { value: 'setResBody', label: '改响应体' },
  { value: 'mock', label: 'Mock 响应' },
  { value: 'block', label: '阻断请求' },
  { value: 'delay', label: '延迟' },
]

const LOGIC_OPTIONS: { value: Logic; label: string }[] = [
  { value: 'and', label: '满足全部条件 (AND)' },
  { value: 'or', label: '满足任一条件 (OR)' },
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

const ACTION_META: Record<ActionType, { label: string; icon: typeof Shuffle }> = {
  redirect: { label: '重定向', icon: ArrowRightLeft },
  rewriteUrl: { label: '改写 URL', icon: Link2 },
  setReqHeader: { label: '改请求头', icon: FileText },
  setResBody: { label: '改响应体', icon: Wand2 },
  mock: { label: 'Mock 响应', icon: Zap },
  block: { label: '阻断请求', icon: Ban },
  delay: { label: '延迟', icon: Clock },
}

let seq = 100
const nextId = (p: string) => `${p}${seq++}`

/* ───────────────────────── 摘要工具 ───────────────────────── */

function summarize(rule: Rule): string {
  if (rule.conditions.length === 0) return '无匹配条件'
  const sep = rule.logic === 'and' ? ' · ' : ' | '
  return rule.conditions
    .map((c) => `${CONDITION_TYPE_LABEL[c.type]}=${c.value || '…'}`)
    .join(sep)
}

/* 不同动作类型对应的参数输入占位 */
function actionParamMeta(type: ActionType): {
  paramLabel: string
  paramPlaceholder: string
  extraLabel?: string
  extraPlaceholder?: string
  extraMultiline?: boolean
} {
  switch (type) {
    case 'redirect':
      return { paramLabel: '目标地址', paramPlaceholder: 'http://127.0.0.1:3000' }
    case 'rewriteUrl':
      return {
        paramLabel: '匹配正则',
        paramPlaceholder: '^https://cdn\\.x\\.com',
        extraLabel: '替换为',
        extraPlaceholder: 'https://cdn.local',
      }
    case 'setReqHeader':
      return {
        paramLabel: '头名称',
        paramPlaceholder: 'Authorization',
        extraLabel: '头值',
        extraPlaceholder: 'Bearer …',
      }
    case 'setResBody':
      return {
        paramLabel: '匹配文本',
        paramPlaceholder: '"env":"prod"',
        extraLabel: '替换为',
        extraPlaceholder: '"env":"dev"',
      }
    case 'mock':
      return {
        paramLabel: '状态码',
        paramPlaceholder: '200',
        extraLabel: '响应体 (JSON)',
        extraPlaceholder: '{ }',
        extraMultiline: true,
      }
    case 'block':
      return { paramLabel: '阻断说明', paramPlaceholder: '该请求将被直接断开（无参数）' }
    case 'delay':
      return { paramLabel: '延迟毫秒', paramPlaceholder: '1000' }
    default:
      return { paramLabel: '参数', paramPlaceholder: '' }
  }
}

/* ───────────────────────── 主组件 ───────────────────────── */

export function RulesView() {
  const [rules, setRules] = useState<Rule[]>([])
  const [selectedId, setSelectedId] = useState<string>('')
  const [query, setQuery] = useState('')

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
    (r) => r.name.toLowerCase().includes(query.toLowerCase()) || summarize(r).toLowerCase().includes(query.toLowerCase()),
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
      name: '未命名规则',
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
      title="重写规则"
      subtitle="请求 / 响应重写 · 重定向 · Mock"
      actions={
        <Button variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={addRule}>
          新建规则
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
              placeholder="搜索规则…"
              className="h-full flex-1 bg-transparent text-[12px] text-fg outline-none placeholder:text-fg-faint"
            />
            <span className="shrink-0 text-2xs tabular-nums text-fg-faint">
              {enabledCount}/{rules.length}
            </span>
          </div>

          <div className="wb-scroll min-h-0 flex-1 overflow-auto">
            {filtered.length === 0 ? (
              <div className="px-3 py-6 text-center text-2xs text-fg-faint">无匹配规则</div>
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
                      <span className="mt-0.5 block truncate text-2xs text-fg-faint">{summarize(r)}</span>
                    </span>
                    <span className="shrink-0 font-mono text-2xs tabular-nums text-fg-faint" title="优先级">
                      #{r.priority}
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </aside>

        {/* ───── 右栏：编辑器 ───── */}
        <div className="wb-scroll min-h-0 flex-1 overflow-auto">
          {!selected ? (
            <EmptyState
              icon={<Shuffle className="h-10 w-10" />}
              title="未选择规则"
              hint="从左侧列表选择一条规则进行编辑，或点击右上角“新建规则”。"
            />
          ) : (
            <div className="flex flex-col gap-4 p-4">
              {/* 基本信息 */}
              <Panel title="基本信息" icon={<Info className="h-4 w-4" />}>
                <Field label="规则名称">
                  <TextInput
                    value={selected.name}
                    onChange={(e) => patchRule(selected.id, { name: e.target.value })}
                    width={260}
                  />
                </Field>
                <Field label="启用" hint="关闭后该规则将被跳过，不影响其它规则">
                  <Toggle checked={selected.enabled} onChange={(v) => toggleRuleEnabled(selected.id, v)} />
                </Field>
                <Field label="优先级" hint="数字越小越先执行，命中后按顺序应用">
                  <TextInput
                    type="number"
                    value={String(selected.priority)}
                    onChange={(e) => patchRule(selected.id, { priority: Number(e.target.value) || 0 })}
                    width={90}
                  />
                </Field>
                <Field label="备注">
                  <TextInput
                    value={selected.note}
                    onChange={(e) => patchRule(selected.id, { note: e.target.value })}
                    placeholder="可选，描述该规则的用途"
                    width={260}
                  />
                </Field>
                <Field label="删除规则" hint="从重写列表中永久移除该规则">
                  <Button
                    variant="danger"
                    size="sm"
                    icon={<Trash2 className="h-3.5 w-3.5" />}
                    onClick={() => deleteRule(selected.id)}
                  >
                    删除规则
                  </Button>
                </Field>
              </Panel>

              {/* 匹配条件 */}
              <Panel
                title="匹配条件"
                icon={<Filter className="h-4 w-4" />}
                right={
                  <Button size="sm" icon={<Plus className="h-3 w-3" />} onClick={() => addCondition(selected.id)}>
                    添加条件
                  </Button>
                }
              >
                <Field label="条件逻辑" hint="多个条件之间的组合方式">
                  <Select
                    value={selected.logic}
                    onChange={(e) => patchRule(selected.id, { logic: e.target.value as Logic })}
                    options={LOGIC_OPTIONS}
                  />
                </Field>

                {selected.conditions.length === 0 ? (
                  <div className="px-3 py-4 text-2xs text-fg-faint">
                    暂无条件，规则将匹配所有流量。点击右上角“添加条件”进行限定。
                  </div>
                ) : (
                  selected.conditions.map((c, i) => (
                    <div key={c.id} className="flex items-center gap-2 px-3 py-2">
                      <span className="w-7 shrink-0 text-center font-mono text-2xs text-fg-faint">
                        {i === 0 ? '当' : selected.logic === 'and' ? '且' : '或'}
                      </span>
                      <Select
                        value={c.type}
                        onChange={(e) =>
                          updateCondition(selected.id, c.id, { type: e.target.value as ConditionType })
                        }
                        options={CONDITION_TYPE_OPTIONS}
                      />
                      {c.type === 'reqHeader' && (
                        <TextInput
                          value={c.name ?? ''}
                          onChange={(e) => updateCondition(selected.id, c.id, { name: e.target.value })}
                          placeholder="头名称"
                          className="w-32 font-mono"
                        />
                      )}
                      <Select
                        value={c.op}
                        onChange={(e) => updateCondition(selected.id, c.id, { op: e.target.value as ConditionOp })}
                        options={CONDITION_OP_OPTIONS}
                      />
                      <TextInput
                        value={c.value}
                        onChange={(e) => updateCondition(selected.id, c.id, { value: e.target.value })}
                        placeholder="匹配值"
                        className="flex-1 font-mono"
                      />
                      <IconButton
                        tone="danger"
                        size="sm"
                        title="删除条件"
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
                title="执行动作"
                icon={<Zap className="h-4 w-4" />}
                right={
                  <Button size="sm" icon={<Plus className="h-3 w-3" />} onClick={() => addAction(selected.id)}>
                    添加动作
                  </Button>
                }
              >
                {selected.actions.length === 0 ? (
                  <div className="px-3 py-4 text-2xs text-fg-faint">
                    暂无动作，命中条件时不会修改流量。点击右上角“添加动作”。
                  </div>
                ) : (
                  selected.actions.map((a) => {
                    const meta = actionParamMeta(a.type)
                    const ActIcon = ACTION_META[a.type].icon
                    return (
                      <div key={a.id} className="px-3 py-2.5">
                        <div className="flex items-center gap-2">
                          <ActIcon className="h-3.5 w-3.5 shrink-0 text-accent" />
                          <Select
                            value={a.type}
                            onChange={(e) =>
                              updateAction(selected.id, a.id, { type: e.target.value as ActionType })
                            }
                            options={ACTION_TYPE_OPTIONS}
                          />
                          <span className="ml-auto" />
                          <IconButton
                            tone="danger"
                            size="sm"
                            title="删除动作"
                            onClick={() => removeAction(selected.id, a.id)}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </IconButton>
                        </div>

                        {a.type === 'block' ? (
                          <div className="mt-2 ml-[22px] text-2xs text-fg-faint">
                            命中后将直接断开连接，无需额外参数。
                          </div>
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
                                    className="wb-scroll flex-1 resize-y rounded-wb border border-line bg-inset px-2 py-1.5 font-mono text-[11.5px] leading-relaxed text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface"
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
