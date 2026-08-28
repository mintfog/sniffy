/**
 * 请求构造器的草稿模型。
 *
 * 草稿以有序的 [名, 值] 列表保存头部，保留重复头和用户输入顺序，后端按同一顺序写线。
 */
import type { ComposeSeed } from '@/lib/bridge'
import type { CurlWarning } from './curl'
import {
  b64FromBytes,
  b64FromText,
  bytesFromB64,
  decodeHeaderValue,
  encodeHeaderValue,
  utf8Lossy,
  type HeaderEnc,
  type HeaderEncodeError,
} from '../../../lib/headerBytes.ts'

/** 收发模式，与 Go 侧 flow.SpecKind* 常量一致。 */
export type DraftKind = 'http' | 'graphql' | 'sse' | 'ws'

export interface HeaderRow {
  /** 本地行标识，头名可重复或为空。 */
  id: string
  name: string
  value: string
  /** 值单元格的编码模式：text 按 UTF-8 编码，bytes 按 \xNN 转义编码。 */
  enc?: HeaderEnc
  /** 后端明文槽原串及对应字节的 base64，用于回程保留原始字节。 */
  orig?: { text: string; b64: string }
}

export interface DraftSeed {
  flowId: string
  method: string
  url: string
  headers: [string, string][]
  /** 与 headers 下标对齐的值字节旁路；后端全部头值合法时为空数组。 */
  headersB64: string[]
  body: string
  /** 原请求体采用二进制形态，文本编辑器显示提示。 */
  bodyBinary: boolean
  /** 原请求体超过编辑器上限，文本编辑器显示大小提示。 */
  bodyTooLarge: boolean
  bodySize: number
}

/** GraphQL 是纯前端模式：这三项在发送前合成 JSON body。 */
export interface GraphQLPart {
  query: string
  /** 变量 JSON 原文，发送时解析。 */
  variables: string
  operationName: string
}

/** 出站 WebSocket：连接活在后端，这里只留可编辑与可展示的部分。 */
export interface WebSocketPart {
  outgoing: string
  outgoingBinary: boolean
  /** idle 从未连过；connecting 已发 OpenWebSocket 未回；open/closed 由会话 DTO 推。 */
  conn: 'idle' | 'connecting' | 'open' | 'closed'
  connError?: string
}

export interface Draft {
  id: string
  kind: DraftKind
  method: string
  url: string
  headers: HeaderRow[]
  body: string
  /** 是否让这次请求经过插件 / 重写规则 / 断点，默认关闭（见 RequestSpec.ViaPipeline）。 */
  viaPipeline: boolean
  seed?: DraftSeed
  /** 最近一次发出的 flow id，响应据此认领。 */
  sentFlowId?: string
  /** 上一次发出的 flow id：重发期间继续展示它的响应，好让「改一处、比一次」有参照。 */
  prevFlowId?: string
  sending: boolean
  /** 后端返回的发送错误原因（如 URL 解析错误），下一次发送时清空。 */
  sendError?: string
  /** 仅 kind==='graphql' 有值。 */
  graphql?: GraphQLPart
  /** 仅 kind==='ws' 有值。 */
  ws?: WebSocketPart
  /** cURL 导入时的提示，用户首次编辑或点关闭即清空；isPristine 必须忽略它。 */
  importWarnings?: CurlWarning[]
}

export const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const

export const GQL_EMPTY: GraphQLPart = { query: '', variables: '', operationName: '' }
export const WS_EMPTY: WebSocketPart = { outgoing: '', outgoingBinary: false, conn: 'idle' }

/**
 * 子对象按类型可选，页签保存 Draft[]，patch(id, Partial<Draft>) 只更新公共字段。
 */
export const gqlOf = (d: Draft): GraphQLPart => d.graphql ?? GQL_EMPTY
export const wsOf = (d: Draft): WebSocketPart => d.ws ?? WS_EMPTY

/**
 * ws 分片补丁支持函数形态，以读取合并时的最新值。
 */
export type WsPatch = Partial<WebSocketPart> | ((prev: WebSocketPart) => Partial<WebSocketPart>)

/**
 * 将补丁合入草稿的 ws 分片，基于草稿当前值更新字段。
 */
export function applyWsPatch(d: Draft, p: WsPatch): Draft {
  const cur = wsOf(d)
  return { ...d, ws: { ...cur, ...(typeof p === 'function' ? p(cur) : p) } }
}

let uid = 0
function nextId(prefix: string): string {
  uid += 1
  return `${prefix}-${uid}`
}

export function newHeaderRow(name = '', value = ''): HeaderRow {
  return { id: nextId('h'), name, value }
}

function stripTrailingBlank(rows: HeaderRow[]): HeaderRow[] {
  const out = [...rows]
  while (out.length > 0) {
    const last = out[out.length - 1]
    if (last.name === '' && last.value === '') out.pop()
    else break
  }
  return out
}

/**
 * 尾部保留一行空行作为新增头部的输入行，中间空行保持原位置。
 */
export function normalizeHeaders(rows: HeaderRow[]): HeaderRow[] {
  const out = stripTrailingBlank(rows)
  out.push(newHeaderRow())
  return out
}

/**
 * 在末尾追加一条头，并重新整理尾部空行。
 */
export function appendHeader(rows: HeaderRow[], name: string, value: string): HeaderRow[] {
  return normalizeHeaders([...stripTrailingBlank(rows), newHeaderRow(name, value)])
}

/* ───────────────────────── 头值的字节旁路 ───────────────────────── */

/** 后端一行头转换为编辑行；有字节旁路时以 `\xNN` 形式展示。 */
export function headerRowFrom(name: string, value: string, b64?: string): HeaderRow {
  const view = decodeHeaderValue(b64, value)
  if (!view) return newHeaderRow(name, value)
  return { ...newHeaderRow(name, view.escaped), enc: 'bytes', orig: { text: value, b64: view.b64 } }
}

/** 将有序头及其字节旁路转换为编辑行。旁路长度匹配时按下标关联。 */
export function headerRowsFrom(pairs: [string, string][], b64: readonly string[] = []): HeaderRow[] {
  const side = b64.length === pairs.length ? b64 : []
  return pairs.map(([name, value], i) => headerRowFrom(name, value, side[i]))
}

/** 返回编辑行将写入线上的字节及转义错误。 */
export function rowBytes(row: HeaderRow): { bytes: Uint8Array; error?: HeaderEncodeError } {
  return encodeHeaderValue(row.value, row.enc ?? 'text')
}

/** 返回编辑行出线字节的 base64，用于字节级比较。 */
export function rowValueB64(row: HeaderRow): string {
  return b64FromBytes(rowBytes(row).bytes)
}

/** 返回蓝本行出线字节的 base64，与 rowValueB64 使用同一表示。 */
export function basisValueB64(value: string, b64?: string): string {
  const bytes = b64 ? bytesFromB64(b64) : null
  return bytes ? b64FromBytes(bytes) : b64FromText(value)
}

export function hasByteRows(rows: HeaderRow[]): boolean {
  return rows.some((r) => r.enc === 'bytes' && r.name.trim() !== '')
}

/** 返回首个转义错误的头名；无错误时返回空串。 */
export function badEscapeName(rows: HeaderRow[]): string {
  for (const row of rows) {
    if (row.name.trim() === '') continue
    if (rowBytes(row).error) return row.name.trim()
  }
  return ''
}

/** 一次遍历生成有序头及其值字节旁路。 */
export interface WireHeaders {
  pairs: [string, string][]
  valuesB64: string[]
}

/** 将编辑行编码为有序头对与下标对齐的值字节旁路。 */
export function wireHeadersFrom(rows: HeaderRow[]): WireHeaders {
  const pairs: [string, string][] = []
  const valuesB64: string[] = []
  for (const row of rows) {
    if (row.name.trim() === '') continue
    if (row.enc === 'bytes') {
      const { bytes } = rowBytes(row)
      const b64 = b64FromBytes(bytes)
      pairs.push([row.name, row.orig && row.orig.b64 === b64 ? row.orig.text : utf8Lossy(bytes)])
      valuesB64.push(b64)
    } else {
      pairs.push([row.name, row.value])
      valuesB64.push('')
    }
  }
  return { pairs, valuesB64 }
}

/** 新建草稿时该 kind 的默认方法。isPristine 靠它判断用户是否动过方法选择。 */
export function defaultMethod(kind: DraftKind): string {
  return kind === 'graphql' ? 'POST' : 'GET'
}

export function newDraft(kind: DraftKind = 'http'): Draft {
  const base: Draft = {
    id: nextId('d'),
    kind,
    method: defaultMethod(kind),
    url: '',
    headers: [],
    body: '',
    viaPipeline: false,
    sending: false,
  }
  switch (kind) {
    case 'graphql':
      return { ...base, graphql: { ...GQL_EMPTY } }
    case 'ws':
      return { ...base, ws: { ...WS_EMPTY } }
    case 'sse':
      return base
    case 'http':
      return base
  }
}

export function draftFromSeed(seed: ComposeSeed): Draft {
  const headers = seed.headers ?? []
  const headersB64 = seed.headersB64 ?? []
  return {
    id: nextId('d'),
    kind: 'http',
    method: seed.method || 'GET',
    url: seed.url || '',
    headers: headerRowsFrom(headers, headersB64),
    body: seed.body ?? '',
    viaPipeline: false,
    sending: false,
    seed: {
      flowId: seed.flowId,
      method: seed.method || 'GET',
      url: seed.url || '',
      headers: headers.map(([n, v]) => [n, v] as [string, string]),
      headersB64: headersB64.length === headers.length ? [...headersB64] : [],
      body: seed.body ?? '',
      bodyBinary: !!seed.bodyBinary,
      bodyTooLarge: !!seed.bodyTooLarge,
      bodySize: seed.bodySize ?? 0,
    },
  }
}

/* ───────────────────────── 改动标记 ───────────────────────── */

/** 与蓝本相比被改动的字段。未从已捕获请求预填时全为假，界面上不显示任何标记。 */
export interface DraftDiff {
  method: boolean
  url: boolean
  body: boolean
  /** 与蓝本对不上的头行 id 集合（含新增行）。 */
  headers: ReadonlySet<string>
}

const NO_DIFF: DraftDiff = { method: false, url: false, body: false, headers: new Set() }

export function draftDiff(draft: Draft): DraftDiff {
  // 仅在 HTTP 草稿与蓝本比较正文；GraphQL 等类型的正文由构造器重新合成。
  if (draft.kind !== 'http') return NO_DIFF
  const seed = draft.seed
  if (!seed) return NO_DIFF

  // 按列表位置逐项比对：头部是有序列表，插入一行会让后续位置整体错位，
  // 逐位置比对能把「这一行和蓝本对应位置不一样」如实标出来。
  // 头部改动按出线字节比较，确保字节视图与蓝本使用同一表示。
  const changed = new Set<string>()
  draft.headers.forEach((row, i) => {
    const orig = seed.headers[i]
    if (!orig || orig[0] !== row.name) {
      changed.add(row.id)
      return
    }
    if (basisValueB64(orig[1], seed.headersB64[i]) !== rowValueB64(row)) changed.add(row.id)
  })

  return {
    method: draft.method !== seed.method,
    url: draft.url !== seed.url,
    body: draft.body !== seed.body,
    headers: changed,
  }
}

/* ───────────────────────── URL 解析 ───────────────────────── */

export interface ParsedUrl {
  host: string
  /** 请求行里的 origin-form 目标：path + query。 */
  target: string
}

/**
 * 解析 URL 取 Host 与请求目标；未带协议时按模式补 https 或 wss。
 */
export function parseUrl(raw: string, defaultScheme: 'https' | 'wss' = 'https'): ParsedUrl {
  const trimmed = raw.trim()
  if (!trimmed) return { host: '', target: '/' }
  const withScheme = trimmed.includes('://') ? trimmed : `${defaultScheme}://${trimmed}`
  try {
    const u = new URL(withScheme)
    return { host: u.host, target: `${u.pathname || '/'}${u.search}` }
  } catch {
    return { host: '', target: '/' }
  }
}

/* ───────────────────────── 页签标签 ───────────────────────── */

/** 页签下拉里的长标签：带主机名，好区分末段路径撞名的几个页签。空草稿返回空串。 */
export function draftMenuLabel(draft: Draft): string {
  const raw = draft.url.trim()
  if (!raw) return ''
  const { host, target } = parseUrl(raw)
  if (!host) return raw
  const full = `${host}${target}`
  // 从左侧截断，保留尾部路径与查询串用于区分页签。
  return full.length > 64 ? `…${full.slice(-63)}` : full
}

/** 页签上的短标签：优先末段路径，退回主机名。空草稿返回空串，由调用方填「空白请求」。 */
export function draftLabel(draft: Draft): string {
  // GraphQL 的路径恒是同一个 /graphql，只有操作名能把几个页签区分开。
  if (draft.kind === 'graphql') {
    const op = gqlOf(draft).operationName.trim()
    if (op) return op
  }
  const raw = draft.url.trim()
  if (!raw) return ''
  const { host, target } = parseUrl(raw)
  const path = target.split('?')[0]
  const last = path.split('/').filter(Boolean).pop()
  return last || host || raw
}
