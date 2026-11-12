/**
 * 流量表多条件搜索：每个条件是「字段 + 匹配方式 + 值」，条件之间统一按且 / 或组合，
 * 整体再与快捷芯片按「与」叠加。匹配一律不区分大小写。
 *
 * 一个字段可能对应多段文本（「全部」的各个子字段、头部的每一行），正向匹配只要任一段命中，
 * 取反匹配要求没有任何一段命中；字段缺失视为没有可命中的文本，因此「不包含」对缺失字段成立。
 */

import { RE2JS } from 're2js'
import { decodeHeaderValue } from '../../lib/headerBytes.ts'
import type { MarkColor, TrafficRow } from './types'

export const SEARCH_FIELDS = [
  'all',
  'url',
  'host',
  'path',
  'method',
  'status',
  'bizCode',
  'bizMessage',
  'reqType',
  'reqHeader',
  'resHeader',
  'reqBody',
  'resBody',
  'process',
  'clientIP',
  'execution',
  'mark',
  'resType',
  'error',
] as const

export type SearchField = (typeof SEARCH_FIELDS)[number]

/** 仅在配置了业务状态码规则时可选。 */
export const BIZ_FIELDS: ReadonlySet<SearchField> = new Set(['bizCode', 'bizMessage'])

export const SEARCH_OPS = [
  'contains',
  'notContains',
  'equals',
  'notEquals',
  'startsWith',
  'endsWith',
  'wildcard',
  'notWildcard',
  'regex',
  'notRegex',
] as const

export type SearchOp = (typeof SEARCH_OPS)[number]

/** 标记色是枚举值，只比较是否相等。 */
const MARK_OPS: readonly SearchOp[] = ['equals', 'notEquals']

export const MARK_COLORS: readonly MarkColor[] = ['red', 'yellow', 'green', 'blue', 'cyan']

/** 正则与通配符的取值是模式：不给候选取值，按文本缓存匹配结果。 */
const PATTERN_OPS: ReadonlySet<SearchOp> = new Set(['wildcard', 'notWildcard', 'regex', 'notRegex'])

const NEGATED: ReadonlySet<SearchOp> = new Set(['notContains', 'notEquals', 'notWildcard', 'notRegex'])

export function opsForField(field: SearchField): readonly SearchOp[] {
  return field === 'mark' ? MARK_OPS : SEARCH_OPS
}

export interface SearchCondition {
  id: string
  field: SearchField
  op: SearchOp
  value: string
}

export type SearchLogic = 'and' | 'or'

let nextId = 0

export function newCondition(): SearchCondition {
  return { id: `c${++nextId}`, field: 'all', op: 'contains', value: '' }
}

/** 切换字段时让匹配方式与取值落在新字段的合法范围内。 */
export function withField(c: SearchCondition, field: SearchField): SearchCondition {
  if (field === c.field) return c
  if (field === 'mark') return { ...c, field, op: MARK_OPS.includes(c.op) ? c.op : 'equals', value: MARK_COLORS[0] }
  return { ...c, field, value: c.field === 'mark' ? '' : c.value }
}

type Marks = Readonly<Partial<Record<string, MarkColor>>>

export interface CompiledSearch {
  /** 没有生效条件（含正则全部写错）时为 null，调用方可直接跳过，也据此判断搜索是否构成筛选。 */
  filter: ((rows: readonly TrafficRow[], marks: Marks) => TrafficRow[]) | null
  /** 有标记色条件；没有时标记变化不影响结果，调用方不必重算。 */
  usesMarks: boolean
  /** 正则写错的条件 id；它们不参与过滤，避免输入途中整表变空。 */
  invalid: ReadonlySet<string>
}

const NONE: readonly string[] = []

// 行对象随每次推送重建，头部对象却沿用会话里的同一引用，按它缓存可在逐键过滤时免于重复拼接。
// 字节旁路与头部对象出自同一份会话，随之缓存不会错配。
const headerLinesCache = new WeakMap<Record<string, string>, readonly string[]>()

/** 与详情页一致：带字节旁路的头值按 Latin-1 还原后再参与匹配，搜的就是看到的。 */
function headerLines(h: Record<string, string> | undefined, b64: Record<string, string> | undefined): readonly string[] {
  if (!h) return NONE
  let lines = headerLinesCache.get(h)
  if (!lines) {
    lines = Object.entries(h).map(([k, v]) => `${k}: ${decodeHeaderValue(b64?.[k], v)?.latin1 ?? v}`)
    headerLinesCache.set(h, lines)
  }
  return lines
}

function contentTypeOf(h: Record<string, string> | undefined): string | undefined {
  if (!h) return undefined
  for (const k in h) if (k.toLowerCase() === 'content-type') return h[k]
  return undefined
}

type TextTest = (s: string) => boolean
type Hit = (r: TrafficRow, test: TextTest) => boolean

const hitOne = (v: string | undefined, test: TextTest) => !!v && test(v)

function hitAny(list: readonly string[], test: TextTest): boolean {
  for (const s of list) if (test(s)) return true
  return false
}

const hitStatus: Hit = (r, test) => r.status !== undefined && test(String(r.status))

// 「全部」不含正文：正文可能很大，逐键扫描所有正文会拖慢输入，需要时单独选请求体 / 响应体字段。
// 逐字段短路检查而不先拼出段数组：每次按键都要对全部行跑一遍，分配开销占大头。
const hitAll: Hit = (r, test) =>
  test(r.url) ||
  test(r.host) ||
  test(r.method) ||
  hitStatus(r, test) ||
  hitOne(r.biz?.code, test) ||
  hitOne(r.biz?.message, test) ||
  hitOne(r.process, test) ||
  hitOne(r.contentType, test) ||
  hitAny(headerLines(r.reqHeaders, r.reqHeadersB64), test) ||
  hitAny(headerLines(r.resHeaders, r.resHeadersB64), test)

// 正文可能很长，不按文本缓存（见 HASHABLE_LEN），而是按行 id 缓存结果、以引用比对正文是否变化。
const BODY_OF: Partial<Record<SearchField, (r: TrafficRow) => string | undefined>> = {
  reqBody: (r) => r.reqBody,
  resBody: (r) => r.resBody,
}

const FIELD_HIT: Record<Exclude<SearchField, 'mark' | 'reqBody' | 'resBody'>, Hit> = {
  all: hitAll,
  url: (r, test) => hitOne(r.url, test),
  host: (r, test) => hitOne(r.host, test),
  path: (r, test) => hitOne(r.path, test),
  method: (r, test) => hitOne(r.method, test),
  status: hitStatus,
  bizCode: (r, test) => hitOne(r.biz?.code, test),
  bizMessage: (r, test) => hitOne(r.biz?.message, test),
  reqType: (r, test) => hitOne(contentTypeOf(r.reqHeaders), test),
  reqHeader: (r, test) => hitAny(headerLines(r.reqHeaders, r.reqHeadersB64), test),
  resHeader: (r, test) => hitAny(headerLines(r.resHeaders, r.resHeadersB64), test),
  process: (r, test) => hitOne(r.process, test),
  clientIP: (r, test) => hitOne(r.clientIP, test),
  execution: (r, test) => !!r.executions?.some((e) => test(e.name)),
  resType: (r, test) => hitOne(r.contentType, test),
  error: (r, test) => hitOne(r.error, test),
}

// 只给取值固定的字段列候选；其余字段取值开放，下拉反而碍事。
const VALUE_OPTIONS: Partial<Record<SearchField, readonly string[]>> = {
  method: ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS', 'CONNECT', 'TRACE'],
  reqType: [
    'application/json',
    'application/x-www-form-urlencoded',
    'multipart/form-data',
    'text/plain',
    'application/xml',
    'application/octet-stream',
    'application/grpc',
    'application/x-protobuf',
  ],
  resType: [
    'application/json',
    'text/html',
    'text/plain',
    'application/javascript',
    'text/css',
    'application/xml',
    'text/event-stream',
    'application/grpc',
    'application/x-protobuf',
    'application/octet-stream',
    'image/png',
    'image/jpeg',
    'image/gif',
    'image/webp',
    'image/svg+xml',
    'font/woff2',
    'video/mp4',
    'audio/mpeg',
  ],
}

export function valueOptions(c: SearchCondition): readonly string[] | undefined {
  return PATTERN_OPS.has(c.op) ? undefined : VALUE_OPTIONS[c.field]
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/**
 * 通配符整串匹配（* 任意串、? 任意单字符），双指针回退到最近的 *：最坏 O(文本×模式)，
 * 不会像转成回溯正则那样被 `*a*a*a*b` 一类模式拖成指数级。调用方负责统一大小写。
 */
export function globMatch(pattern: string, text: string): boolean {
  let p = 0
  let t = 0
  let star = -1
  let mark = 0
  while (t < text.length) {
    if (p < pattern.length && (pattern[p] === '?' || pattern[p] === text[t])) {
      p++
      t++
    } else if (p < pattern.length && pattern[p] === '*') {
      star = p++
      mark = t
    } else if (star >= 0) {
      p = star + 1
      t = ++mark
    } else {
      return false
    }
  }
  while (pattern[p] === '*') p++
  return p === pattern.length
}

const MEMO_LIMIT = 50_000

/**
 * 两代缓存：每轮整表过滤开始时换代、结束时丢弃上一代，本轮没访问到的条目（已删除的行、
 * 被芯片筛掉的行）随之释放，清空会话后不会继续攥着旧正文。
 */
class PassCache<K, V> {
  private cur = new Map<K, V>()
  private prev = new Map<K, V>()

  get(k: K): V | undefined {
    let v = this.cur.get(k)
    if (v === undefined) {
      v = this.prev.get(k)
      if (v !== undefined) this.cur.set(k, v)
    }
    return v
  }

  set(k: K, v: V) {
    if (this.cur.size >= MEMO_LIMIT) this.cur.clear()
    this.cur.set(k, v)
  }

  begin() {
    this.prev = this.cur
    this.cur = new Map()
  }

  end() {
    this.prev = new Map()
  }
}

type PassScope = Pick<PassCache<unknown, unknown>, 'begin' | 'end'>

// V8 只对不超过 16383 个字符的字符串按内容求哈希，更长的只按长度：同长的长文本落进同一个桶，
// 以字符串为键的 Map 查找会退化成逐个比较全文（5000 段 17K 字符实测数十秒）。
const HASHABLE_LEN = 16_383

// 行对象随推送整体重建，但 URL、头部行等字符串沿用同一引用；按文本记住结果后，
// 新流量进来只需对新增文本求值，改条件时才整表重算。
function memoize(test: TextTest, cache: PassCache<string, boolean>): TextTest {
  return (s) => {
    if (s.length > HASHABLE_LEN) return test(s)
    let hit = cache.get(s)
    if (hit === undefined) {
      hit = test(s)
      cache.set(s, hit)
    }
    return hit
  }
}

// 字面量转义后的锚定正则只会线性扫描；带 i 标志比先 toLowerCase 少复制一份可能很大的正文。
function literalTest(source: string): TextTest {
  const re = new RegExp(source, 'i')
  return (s) => re.test(s)
}

function textTest(op: SearchOp, v: string): TextTest {
  switch (op) {
    case 'contains':
    case 'notContains':
      return literalTest(escapeRe(v))
    case 'equals':
    case 'notEquals':
      return literalTest(`^${escapeRe(v)}$`)
    case 'startsWith':
      return literalTest(`^${escapeRe(v)}`)
    case 'endsWith':
      return literalTest(`${escapeRe(v)}$`)
    case 'wildcard':
    case 'notWildcard': {
      const pattern = v.toLowerCase().replace(/\*+/g, '*')
      return (s) => globMatch(pattern, s.toLowerCase())
    }
    // 用户正则交给 RE2：匹配耗时随文本线性增长，`(a+)+$` 之类的写法也不会卡死主线程；
    // 语法与后端规则引擎（Go regexp）一致，不支持反向引用与环视。
    case 'regex':
    case 'notRegex': {
      const re = RE2JS.compile(v, RE2JS.CASE_INSENSITIVE)
      return (s) => re.test(s)
    }
  }
}

type Matcher = (r: TrafficRow, marks: Marks) => boolean

function compileOne(c: SearchCondition, caches: PassScope[]): Matcher | null {
  const negate = NEGATED.has(c.op)
  if (c.field === 'mark') {
    const color = c.value
    return (r, marks) => (marks[r.id] === color) !== negate
  }
  const v = c.value.trim()
  if (!v) return null
  const test = textTest(c.op, v)

  const bodyOf = BODY_OF[c.field]
  if (bodyOf) {
    const cache = new PassCache<string, { s: string | undefined; hit: boolean }>()
    caches.push(cache)
    return (r) => {
      const s = bodyOf(r)
      let e = cache.get(r.id)
      if (!e || e.s !== s) {
        e = { s, hit: hitOne(s, test) !== negate }
        cache.set(r.id, e)
      }
      return e.hit
    }
  }

  const hit = FIELD_HIT[c.field as keyof typeof FIELD_HIT]
  let t = test
  if (PATTERN_OPS.has(c.op)) {
    const cache = new PassCache<string, boolean>()
    caches.push(cache)
    t = memoize(test, cache)
  }
  return (r) => hit(r, t) !== negate
}

export function compileSearch(conds: readonly SearchCondition[], logic: SearchLogic): CompiledSearch {
  const matchers: Matcher[] = []
  const caches: PassScope[] = []
  const invalid = new Set<string>()
  for (const c of conds) {
    try {
      const m = compileOne(c, caches)
      if (m) matchers.push(m)
    } catch {
      invalid.add(c.id)
    }
  }
  const usesMarks = conds.some((c) => c.field === 'mark')
  if (matchers.length === 0) return { filter: null, usesMarks, invalid }
  // 单条件最常见，直接用它省掉每行一次 every / some 的调用，5000 行整表过滤约快一倍。
  let test: Matcher
  if (matchers.length === 1) test = matchers[0]
  else if (logic === 'and') test = (r, marks) => matchers.every((m) => m(r, marks))
  else test = (r, marks) => matchers.some((m) => m(r, marks))
  const filter = (rows: readonly TrafficRow[], marks: Marks) => {
    for (const c of caches) c.begin()
    const out = rows.filter((r) => test(r, marks))
    for (const c of caches) c.end()
    return out
  }
  return { filter, usesMarks, invalid }
}
