/**
 * 构造器的线缆预览：把一份草稿算成真正写到线上的报文。
 *
 * resolveWire 是预览与实发的共同来源，buildWire、wireText 和 toRequestSpec 均从此读取
 * method、body 与合成头。
 *
 * 一次性请求这一路是后端 flow.ApplyRequestToHTTP + app.splitComposedHeaders 的镜像：
 * 补 Host、删逐跳头与 Content-Encoding、按体长重算 Content-Length。
 * WS 握手那一路是 gorilla Dialer + net/http Request.Write 的镜像（见 handshakeWire）。
 * 两侧改动须同步：预览与实发不一致，构造器就不再是「所见即所发」。
 */
import i18n from '@/i18n'
import type { RequestSpec } from '@/lib/bridge'
import { encodeHeaderValue } from '../../../lib/headerBytes.ts'
import { badEscapeName, hasByteRows, parseUrl, wireHeadersFrom } from './model'
import type { Draft, DraftKind, HeaderRow, WireHeaders } from './model'

// parseUrl 与 ParsedUrl 定义在 model.ts；此处额外使用 i18n 生成握手占位文案。
export { parseUrl } from './model'
export type { ParsedUrl } from './model'

/**
 * 出站时被删掉的逐跳头，与 internal/flow/codec.go 的 hopByHopHeaders 一一对应。
 * 唯一的例外是 `TE: trailers`——gRPC 需要它，转发器删后会补回（见 ApplyRequestToHTTP）。
 */
export const HOP_BY_HOP = [
  'connection',
  'proxy-connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
]

const encoder = new TextEncoder()

export function byteLength(s: string): number {
  return encoder.encode(s).length
}

/* ───────────────────────── 类型决定的部分 ───────────────────────── */

export interface ResolvedWire {
  method: string
  body: string
  /**
   * 由构造器按类型补的头。一次性请求：顺序即出线顺序，用户已手写同名头时一律不补。
   * WS 握手：这些头由 Dialer 生成、不随 spec 上报，预览里一律出线（见 handshakeWire）。
   */
  extra: [string, string][]
  /** GraphQL 变量 JSON 非法：调用方据此禁用发送并标红。 */
  varsError?: string
}

function graphqlBody(draft: Draft): { body: string; varsError?: string } {
  const part = draft.graphql
  const query = part?.query ?? ''
  const vars = (part?.variables ?? '').trim()
  const opName = (part?.operationName ?? '').trim()

  const payload: { query: string; variables?: unknown; operationName?: string } = { query }
  if (vars) {
    try {
      payload.variables = JSON.parse(vars)
    } catch (e) {
      // 变量解析错误通过 varsError 返回，并保持请求体为空。
      return { body: '', varsError: e instanceof Error ? e.message : String(e) }
    }
  }
  if (opName) payload.operationName = opName
  return { body: JSON.stringify(payload) }
}

/** 真正写到线上的 method / body / 由构造器补的头。 */
export function resolveWire(draft: Draft): ResolvedWire {
  const method = (draft.method || 'GET').toUpperCase()
  switch (draft.kind || 'http') {
    case 'graphql': {
      const { body, varsError } = graphqlBody(draft)
      return {
        // GraphQL 请求统一使用 POST 承载 JSON body。
        method: 'POST',
        body,
        extra: [
          ['Content-Type', 'application/json'],
          ['Accept', 'application/json'],
        ],
        varsError,
      }
    }
    case 'sse':
      return {
        method,
        body: method === 'GET' || method === 'HEAD' ? '' : draft.body,
        // SSE 请求补充事件流协商头与缓存策略。
        extra: [
          ['Accept', 'text/event-stream'],
          ['Cache-Control', 'no-cache'],
        ],
      }
    case 'ws':
      return {
        method: 'GET',
        body: '',
        // 头名沿用 gorilla 按 RFC 示例写线的大小写，不是 textproto 的规范名。
        extra: [
          ['Connection', 'Upgrade'],
          ['Upgrade', 'websocket'],
          ['Sec-WebSocket-Version', '13'],
          // Sec-WebSocket-Key 每次握手随机，预览只能给占位文案。
          ['Sec-WebSocket-Key', i18n.t('compose.wire.wsKeyPlaceholder')],
        ],
      }
    case 'http':
      return { method, body: draft.body, extra: [] }
  }
}

/* ───────────────────────── 线缆预览 ───────────────────────── */

/** 由构造器合成的头，界面使用独立色标标记。 */
export type WireOrigin = 'typed' | 'synthesized'

export interface WireLine {
  name: string
  value: string
  origin: WireOrigin
  /** bytes 行使用 `\xNN` 转义显示；字节数按解码后的头值计算。 */
  enc?: 'text' | 'bytes'
}

/** 键入过但没能原样出线的头名，界面上各折叠成一句说明。 */
export interface WireNotes {
  /** 出线时整行删掉的头（逐跳头、Content-Encoding、握手写不出的头）。 */
  dropped: string[]
  /** 有行出线但值由出线规则重算的头（握手控制头、折叠成一行的重复 Host）。 */
  overridden: string[]
}

export interface WirePreview extends WireNotes {
  /** 请求行，如 `POST /v1/charges?x=1 HTTP/1.1`。 */
  requestLine: string
  headers: WireLine[]
  bodyBytes: number
  /** 整个请求（请求行 + 头 + 空行 + 体）的字节数。 */
  totalBytes: number
  /** WS 握手：空行之后不是体而是帧流，界面据此换一句说明代替 “Body …”。 */
  handshake?: boolean
}

function renderHead(requestLine: string, headers: WireLine[]): string {
  return `${requestLine}\r\n${headers.map((h) => `${h.name}: ${h.value}`).join('\r\n')}\r\n\r\n`
}

function valueBytes(h: WireLine): number {
  return h.enc === 'bytes' ? encodeHeaderValue(h.value, 'bytes').bytes.length : byteLength(h.value)
}

/** 计算请求行、头部与空行的线缆字节数。 */
function headBytes(requestLine: string, headers: WireLine[]): number {
  let n = byteLength(requestLine) + 2
  for (const h of headers) n += byteLength(h.name) + 2 + valueBytes(h) + 2
  return n + 2
}

/** 名字为空的行作为待填写的占位行，不参与出线。 */
function typedRows(draft: Draft) {
  return draft.headers.filter((h) => h.name.trim() !== '')
}

function lowerNames(rows: { name: string }[]): Set<string> {
  return new Set(rows.map((h) => h.name.trim().toLowerCase()))
}

const lowerName = (row: { name: string }) => row.name.trim().toLowerCase()

/** 头名的合法字符集，与 Go 的 validHeaderFieldByte 相同；越界的名字 Go 原样保留。 */
const TOKEN_NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

/** 镜像 textproto.CanonicalMIMEHeaderKey：每段首字母大写、其余小写。 */
function canonicalName(name: string): string {
  if (!TOKEN_NAME.test(name)) return name
  return name
    .split('-')
    .map((part) => (part === '' ? part : part[0].toUpperCase() + part.slice(1).toLowerCase()))
    .join('-')
}

/**
 * Host 只出一行：位置与大小写取首个 Host 行，值取最后一个非空值，全空则取 URL 主机。
 * 后端 splitComposedHeaders 把最后一个非空值存进 Flow.Host，reconcileOrderedHeaders 再把它
 * 写回首个 Host 行，其余同名行不出线；WS 握手侧的 http.Header.Set 同样只留一个值。
 */
function planHost(rows: HeaderRow[], urlHost: string) {
  const hosts = rows.filter((r) => lowerName(r) === 'host')
  const head = hosts[0]
  const valued = [...hosts].reverse().find((r) => r.value.trim() !== '')
  const line: WireLine = {
    name: head ? head.name.trim() : 'Host',
    value: valued ? valued.value.trim() : urlHost,
    origin: head ? 'typed' : 'synthesized',
    enc: valued?.enc,
  }
  return { line, typed: head !== undefined, folded: hosts.length > 1 }
}

interface WireInput {
  rows: HeaderRow[]
  requestLine: string
  urlHost: string
  bodyBytes: number
  extra: [string, string][]
}

function preview(inp: WireInput, headers: WireLine[], notes: WireNotes, handshake: boolean): WirePreview {
  return {
    requestLine: inp.requestLine,
    headers,
    ...notes,
    bodyBytes: inp.bodyBytes,
    totalBytes: headBytes(inp.requestLine, headers) + inp.bodyBytes,
    handshake,
  }
}

/** 一次性请求（http / graphql / sse）的出线报文：保真写线，沿用户的头部顺序与大小写。 */
function httpWire(inp: WireInput): WirePreview {
  const { rows, bodyBytes, extra } = inp
  const typed = lowerNames(rows)
  const notes: WireNotes = { dropped: [], overridden: [] }
  const out: WireLine[] = []
  const host = planHost(rows, inp.urlHost)
  if (host.folded) notes.overridden.push(host.line.name)

  // TE 与 Content-Length 的值由后端按最终正文重算，并沿用户头部顺序写回首个同名行。
  const keepTE = rows.some((r) => lowerName(r) === 'te' && /trailers/i.test(r.value))
  const sendCL = bodyBytes > 0 || rows.some((r) => lowerName(r) === 'content-length')

  let hostEmitted = false
  let teEmitted = false
  let clEmitted = false

  for (const row of rows) {
    const name = row.name.trim()
    const lower = name.toLowerCase()

    if (lower === 'host') {
      // 重复的 Host 行折叠进首行，写线时不会各自出一行。
      if (!hostEmitted) {
        hostEmitted = true
        out.push(host.line)
      }
      continue
    }
    if (lower === 'te') {
      if (keepTE && !teEmitted) {
        teEmitted = true
        out.push({ name, value: 'trailers', origin: 'typed' })
      } else {
        notes.dropped.push(name)
      }
      continue
    }
    if (HOP_BY_HOP.includes(lower)) {
      notes.dropped.push(name)
      continue
    }
    // 请求体按 identity 字节发送，Content-Encoding 头随之移除。
    if (lower === 'content-encoding') {
      notes.dropped.push(name)
      continue
    }
    if (lower === 'content-length') {
      if (sendCL && !clEmitted) {
        clEmitted = true
        out.push({ name, value: String(bodyBytes), origin: 'synthesized' })
      }
      continue
    }
    out.push({ name, value: row.value, origin: 'typed', enc: row.enc })
  }

  if (!host.typed) out.unshift(host.line)
  for (const [name, value] of extra) {
    if (!typed.has(name.toLowerCase())) out.push({ name, value, origin: 'synthesized' })
  }
  // 自动补充的 Content-Length 位于头部序列末尾。
  if (sendCL && !clEmitted) {
    out.push({ name: 'Content-Length', value: String(bodyBytes), origin: 'synthesized' })
  }

  return preview(inp, out, notes, false)
}

/** 由 Dialer 独占的握手控制头，与 internal/app/compose_ws.go 的 composeWSHopHeaders 一一对应。 */
const WS_DIALER_HEADERS = [
  'connection',
  'upgrade',
  'sec-websocket-key',
  'sec-websocket-version',
  'sec-websocket-extensions',
]

/** net/http 的 Request.Write 不从头部表写这三个头；握手没有正文，它们一律不出线。 */
const WS_UNWRITABLE = ['content-length', 'transfer-encoding', 'trailer']

/** gorilla 按 RFC 示例的大小写写这个键，不是 textproto 的规范名。 */
const WS_PROTOCOL_NAME = 'Sec-WebSocket-Protocol'

/** net/http 在用户没写 User-Agent 时补的值。 */
const GO_USER_AGENT = 'Go-http-client/1.1'

/**
 * WS 握手报文由 gorilla Dialer 经 net/http 的 Request.Write 写出，不走保真写线：
 * Host 与 User-Agent 固定在最前，其余头按写出名的字节序排列（同名头保持键入顺序）。
 * 头名按 textproto 规范名写出，Sec-WebSocket-Protocol 例外（见 WS_PROTOCOL_NAME）。
 * 握手控制头由 Dialer 生成，用户写的同名值不出线。
 */
function handshakeWire(inp: WireInput): WirePreview {
  const { rows, extra } = inp
  const notes: WireNotes = { dropped: [], overridden: [] }
  const host = planHost(rows, inp.urlHost)
  if (host.folded) notes.overridden.push('Host')

  // Dialer 生成的握手控制头一律出线，与用户是否写过同名头无关。
  const generated = new Map(extra.map(([name]) => [name.toLowerCase(), name]))
  const sorted: WireLine[] = extra.map(([name, value]) => ({ name, value, origin: 'synthesized' }))
  let agent: WireLine | null = { name: 'User-Agent', value: GO_USER_AGENT, origin: 'synthesized' }
  let agentTyped = false

  for (const row of rows) {
    const name = row.name.trim()
    const lower = name.toLowerCase()

    if (lower === 'host') continue
    if (lower === 'user-agent') {
      // 多个 User-Agent 只写第一个；值为空串时整行不写。
      if (agentTyped) {
        notes.overridden.push('User-Agent')
        continue
      }
      agentTyped = true
      agent = row.value === '' ? null : { name: 'User-Agent', value: row.value, origin: 'typed', enc: row.enc }
      continue
    }
    if (WS_DIALER_HEADERS.includes(lower)) {
      const gen = generated.get(lower)
      // Sec-WebSocket-Extensions 没有对应的合成行：未开压缩协商时它不出线。
      if (gen) notes.overridden.push(gen)
      else notes.dropped.push(name)
      continue
    }
    if (WS_UNWRITABLE.includes(lower)) {
      notes.dropped.push(name)
      continue
    }
    const wire = lower === 'sec-websocket-protocol' ? WS_PROTOCOL_NAME : canonicalName(name)
    sorted.push({ name: wire, value: row.value, origin: 'typed', enc: row.enc })
  }

  sorted.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
  // Request.Write 写的是 "Host: "，与用户键入的大小写无关。
  const out = [{ ...host.line, name: 'Host' }, ...(agent ? [agent] : []), ...sorted]
  return preview(inp, out, notes, true)
}

/** 算出这份草稿将要写到线上的报文。 */
export function buildWire(draft: Draft): WirePreview {
  const handshake = draft.kind === 'ws'
  const parsed = parseUrl(draft.url, handshake ? 'wss' : 'https')
  const { method, body, extra } = resolveWire(draft)
  const inp: WireInput = {
    rows: typedRows(draft),
    requestLine: `${method} ${parsed.target} HTTP/1.1`,
    urlHost: parsed.host,
    bodyBytes: byteLength(body),
    extra,
  }
  return handshake ? handshakeWire(inp) : httpWire(inp)
}

/** 线缆报文的纯文本形式（供复制）。 */
export function wireText(draft: Draft): string {
  const wire = buildWire(draft)
  return renderHead(wire.requestLine, wire.headers) + resolveWire(draft).body
}

/* ───────────────────────── 发送入参 ───────────────────────── */

/** Go 侧 RequestSpec.Kind 为 omitempty（空串等价 http），构造器一律显式填写。 */
type ComposeSpec = RequestSpec & { kind: DraftKind }

function specHeaders(draft: Draft, extra: [string, string][]): WireHeaders {
  const rows = typedRows(draft)
  const typed = lowerNames(rows)
  const { pairs, valuesB64 } = wireHeadersFrom(rows)
  const out = pairs.map(([name, value]) => [name.trim(), value] as [string, string])
  for (const [name, value] of extra) {
    if (typed.has(name.toLowerCase())) continue
    out.push([name, value])
    // 合成头使用明文值，旁路位置保持为空。
    valuesB64.push('')
  }
  return { pairs: out, valuesB64 }
}

/** 返回草稿是否需要携带值字节旁路。 */
export function headerBytesNeeded(draft: Draft): boolean {
  return hasByteRows(draft.headers) || (draft.seed?.headersB64.length ?? 0) > 0
}

/** 返回首个转义错误的头名；无错误时为空串。 */
export function headerBytesError(draft: Draft): string {
  return badEscapeName(draft.headers)
}

/** 一次性往返（http / graphql / sse）的发送入参，包含构造器生成的协商头。 */
export function toRequestSpec(draft: Draft): RequestSpec {
  const { method, body, extra } = resolveWire(draft)
  const { pairs, valuesB64 } = specHeaders(draft, extra)
  const spec: ComposeSpec = {
    kind: draft.kind || 'http',
    method,
    url: draft.url.trim(),
    headers: pairs,
    body,
    fromId: draft.seed?.flowId ?? '',
    viaPipeline: draft.viaPipeline,
  }
  if (headerBytesNeeded(draft)) spec.headersB64 = valuesB64
  return spec
}

/** WS 握手发送 URL 与有序附加头，握手控制头由 dialer 生成。 */
export function toWSSpec(draft: Draft): RequestSpec {
  const { pairs, valuesB64 } = specHeaders(draft, [])
  const spec: ComposeSpec = {
    kind: 'ws',
    method: 'GET',
    url: draft.url.trim(),
    headers: pairs,
    body: '',
    fromId: draft.seed?.flowId ?? '',
    viaPipeline: draft.viaPipeline,
  }
  if (headerBytesNeeded(draft)) spec.headersB64 = valuesB64
  return spec
}
