/**
 * 重写规则模型映射层。
 *
 * RulesView 用一套扁平、利于编辑的本地模型(Rule);后端/持久化与规则引擎用规范模型
 * InterceptRule(web/src/types ↔ internal/service.InterceptRule ↔ internal/rules 引擎)。
 * 本文件在两者之间互转。**条件类型 / 操作符 / 动作类型 / parameters 键必须与
 * internal/rules/engine.go 严格一致**,改一处需同步另一处。
 */
import i18n from '@/i18n'
import type {
  ActionParameters,
  ActionType as CanonActionType,
  ConditionOperator,
  ConditionType as CanonConditionType,
  InterceptAction,
  InterceptCondition,
  InterceptRule,
} from '@/types'

/* ───── 本地(UI)模型 ───── */

export type ConditionType = 'url' | 'host' | 'path' | 'method' | 'reqHeader' | 'status' | 'query'
export type ConditionOp = 'eq' | 'contains' | 'regex' | 'prefix' | 'suffix' | 'ne'
export type ActionType =
  | 'redirect'
  | 'rewriteUrl'
  | 'setReqHeader'
  | 'replaceReqBody'
  | 'setResBody'
  | 'mock'
  | 'block'
  | 'delay'
export type Logic = 'and' | 'or'

export interface Condition {
  id: string
  type: ConditionType
  op: ConditionOp
  value: string
  /** 仅 reqHeader 类型使用:头名称。 */
  name?: string
  /** 原始规范条件,用于回写时保留 UI 未建模的字段(caseSensitive/negate/value2 等)。 */
  raw?: InterceptCondition
}

export interface RuleAction {
  id: string
  type: ActionType
  /** 主参数,按动作类型解释含义。 */
  param: string
  /** 副参数(替换值 / 响应体等)。 */
  extra?: string
  /** 原始规范动作,用于回写时保留 UI 未建模的参数(mock 的 contentType/headers 等)。 */
  raw?: InterceptAction
}

export interface Rule {
  id: string
  name: string
  enabled: boolean
  priority: number
  note: string
  logic: Logic
  conditions: Condition[]
  actions: RuleAction[]
  /** UI 无法表达、按原样透传保留的条件(避免编辑保存时丢失)。 */
  extraConditions?: InterceptCondition[]
  /** UI 无法表达、按原样透传保留的动作。 */
  extraActions?: InterceptAction[]
}

/* ───── 条件映射 ───── */

const COND_TYPE_TO_CANON: Record<ConditionType, CanonConditionType> = {
  url: 'url',
  host: 'url_host',
  path: 'url_path',
  method: 'method',
  reqHeader: 'request_header',
  status: 'response_status',
  query: 'url_query',
}
const COND_TYPE_FROM_CANON: Partial<Record<CanonConditionType, ConditionType>> = {
  url: 'url',
  url_host: 'host',
  url_path: 'path',
  method: 'method',
  request_header: 'reqHeader',
  response_status: 'status',
  url_query: 'query',
}

const COND_OP_TO_CANON: Record<ConditionOp, ConditionOperator> = {
  eq: 'equals',
  ne: 'not_equals',
  contains: 'contains',
  prefix: 'starts_with',
  suffix: 'ends_with',
  regex: 'regex',
}
const COND_OP_FROM_CANON: Partial<Record<ConditionOperator, ConditionOp>> = {
  equals: 'eq',
  not_equals: 'ne',
  contains: 'contains',
  starts_with: 'prefix',
  ends_with: 'suffix',
  regex: 'regex',
}

/* ───── 转换:本地 → 规范 ───── */

function toCanonCondition(c: Condition): InterceptCondition {
  // 从原始条件出发,保留 UI 未建模的字段(caseSensitive/negate/value2),再覆盖可编辑字段。
  const out: InterceptCondition = c.raw
    ? { ...c.raw }
    : ({ type: 'url', operator: 'contains', value: '' } as InterceptCondition)
  out.type = COND_TYPE_TO_CANON[c.type] ?? 'url'
  out.operator = COND_OP_TO_CANON[c.op] ?? 'contains'
  out.value = c.value
  if (c.type === 'reqHeader' && c.name) out.headerName = c.name
  return out
}

function toCanonAction(a: RuleAction): InterceptAction {
  // 从原始动作参数出发,保留 UI 未建模的参数(mock 的 contentType/headers 等),再覆盖可编辑项。
  const params: ActionParameters = a.raw?.parameters ? { ...a.raw.parameters } : {}
  let type: CanonActionType
  switch (a.type) {
    case 'redirect':
      type = 'redirect'
      params.url = a.param
      break
    case 'rewriteUrl':
      type = 'modify_url'
      params.urlPattern = a.param
      params.replacement = a.extra ?? ''
      break
    case 'setReqHeader':
      type = 'modify_headers'
      params.name = a.param
      params.value = a.extra ?? ''
      break
    case 'replaceReqBody':
      type = 'replace_body'
      params.contentType = a.param || undefined
      params.body = a.extra ?? ''
      break
    case 'setResBody':
      type = 'modify_response_body'
      params.responseBodyPattern = a.param
      params.responseBodyReplacement = a.extra ?? ''
      break
    case 'mock':
      type = 'auto_respond'
      params.response = {
        ...(params.response ?? {}),
        status: Number(a.param) || 200,
        body: a.extra ?? '',
        contentType: params.response?.contentType ?? 'application/json',
      }
      break
    case 'block':
      type = 'block'
      break
    case 'delay':
      type = 'delay'
      params.milliseconds = Number(a.param) || 0
      break
    default:
      type = 'block'
  }
  return { type, parameters: params, enabled: true }
}

/**
 * 把本地 Rule 转成规范 InterceptRule(用于 create/update;id/时间戳由后端补全)。
 * 透传保留 UI 无法表达的 extraConditions/extraActions,避免编辑保存导致数据丢失。
 */
export function toInterceptRule(r: Rule): InterceptRule {
  return {
    id: r.id, // create 时后端忽略并生成新 id;update 时以路径 id 为准
    name: r.name,
    description: r.note,
    enabled: r.enabled,
    priority: r.priority,
    logicOperator: r.logic === 'or' ? 'OR' : 'AND',
    conditions: [...r.conditions.map(toCanonCondition), ...(r.extraConditions ?? [])],
    actions: [...r.actions.map(toCanonAction), ...(r.extraActions ?? [])],
    createdAt: '',
    updatedAt: '',
  }
}

/* ───── 转换:规范 → 本地 ───── */

let mapSeq = 1
const genId = (p: string) => `${p}_${mapSeq++}`

/** 可映射为本地条件则返回,否则返回 null(交由调用方作为不透明项保留)。 */
function toLocalCondition(c: InterceptCondition): Condition | null {
  const type = COND_TYPE_FROM_CANON[c.type]
  const op = COND_OP_FROM_CANON[c.operator]
  if (!type || !op) return null // UI 无法表达的类型/操作符,保留为 extraConditions
  return {
    id: genId('c'),
    type,
    op,
    value: c.value == null ? '' : String(c.value),
    name: c.headerName,
    raw: c,
  }
}

function toLocalAction(a: InterceptAction): RuleAction | null {
  const p = a.parameters ?? {}
  switch (a.type) {
    case 'redirect':
      return { id: genId('a'), type: 'redirect', param: p.url ?? '', extra: '', raw: a }
    case 'modify_url':
      return { id: genId('a'), type: 'rewriteUrl', param: p.urlPattern ?? '', extra: p.replacement ?? '', raw: a }
    case 'modify_headers': {
      let name = p.name ?? ''
      let value = p.value ?? ''
      if (!name && p.headers?.modify) {
        const k = Object.keys(p.headers.modify)[0]
        if (k) {
          name = k
          value = p.headers.modify[k]
        }
      }
      return { id: genId('a'), type: 'setReqHeader', param: name, extra: value, raw: a }
    }
    case 'replace_body':
      return { id: genId('a'), type: 'replaceReqBody', param: p.contentType ?? '', extra: p.body ?? '', raw: a }
    case 'modify_response_body':
      return {
        id: genId('a'),
        type: 'setResBody',
        param: p.responseBodyPattern ?? '',
        extra: p.responseBodyReplacement ?? '',
        raw: a,
      }
    case 'auto_respond':
      return {
        id: genId('a'),
        type: 'mock',
        param: String(p.response?.status ?? 200),
        extra: p.response?.body ?? '',
        raw: a,
      }
    case 'block':
      return { id: genId('a'), type: 'block', param: '', extra: '', raw: a }
    case 'delay':
      return { id: genId('a'), type: 'delay', param: String(p.milliseconds ?? 0), extra: '', raw: a }
    default:
      return null // 本地模型不支持的动作类型,保留为 extraActions
  }
}

/** 把后端 InterceptRule 转成本地 Rule(用于加载/回填);UI 无法表达的条件/动作按原样保留。 */
export function toLocalRule(ir: InterceptRule): Rule {
  const conditions: Condition[] = []
  const extraConditions: InterceptCondition[] = []
  for (const c of ir.conditions ?? []) {
    const local = toLocalCondition(c)
    if (local) conditions.push(local)
    else extraConditions.push(c)
  }

  const actions: RuleAction[] = []
  const extraActions: InterceptAction[] = []
  for (const a of ir.actions ?? []) {
    const local = toLocalAction(a)
    if (local) actions.push(local)
    else extraActions.push(a)
  }

  return {
    id: ir.id,
    name: ir.name || i18n.t('rules.untitled'),
    enabled: ir.enabled,
    priority: ir.priority ?? 0,
    note: ir.description ?? '',
    logic: ir.logicOperator === 'OR' ? 'or' : 'and',
    conditions,
    actions,
    extraConditions: extraConditions.length ? extraConditions : undefined,
    extraActions: extraActions.length ? extraActions : undefined,
  }
}
