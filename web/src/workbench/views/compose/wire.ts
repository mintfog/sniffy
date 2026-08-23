/**
 * 构造器的线缆预览：把一份草稿算成真正写到线上的报文。
 *
 * resolveWire 是预览与实发的唯一共同来源——buildWire / wireText / toRequestSpec 都从它取
 * method / body / 合成头。任何一条另起炉灶，界面上看到的就不再是发出去的东西，
 * 而预览会说谎正是这个功能最不能出的问题。
 *
 * 这里同时是后端 flow.ApplyRequestToHTTP + app.splitComposedHeaders 的镜像实现：
 * 补 Host、删逐跳头与 Content-Encoding、按体长重算 Content-Length，两侧改动须同步。
 * 后端那侧的参考行为由 TestSendRequestWritesHeadersVerbatim 与
 * TestSendRequestKeepsRecomputedHeadersInPlace（internal/app/compose_test.go）钉住 ——
 * 改这个文件之前先读那两条，它们是这份镜像唯一的对照物。
 */
import i18n from '@/i18n'
import type { RequestSpec } from '@/lib/bridge'
import { parseUrl } from './model'
import type { Draft, DraftKind } from './model'

// parseUrl 与 ParsedUrl 定义在 model.ts：那边不许有 `@/` 运行期 import（curl.test.ts 由
// node --test 直接加载 model.ts），而这里要用 i18n 取握手占位文案。
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
  /** 由构造器按类型补的头，顺序即出线顺序；用户已手写同名头时一律不补。 */
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
      // 把非法原文当字符串塞进 variables 会发出用户没写的东西，宁可交回错误、不给 body。
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
        // 方法锁死：body 是合成 JSON，换成 GET 就没有承载它的地方。
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
        // 不补 Connection: keep-alive——它是逐跳头，补了立刻进 dropped，看起来像坏了。
        extra: [
          ['Accept', 'text/event-stream'],
          ['Cache-Control', 'no-cache'],
        ],
      }
    case 'ws':
      return {
        method: 'GET',
        body: '',
        extra: [
          ['Connection', 'Upgrade'],
          ['Upgrade', 'websocket'],
          ['Sec-WebSocket-Version', '13'],
          // 真 key 由 Go 侧 dialer 握手时生成；这里现场随机既每帧一变又是假的。
          ['Sec-WebSocket-Key', i18n.t('compose.wire.wsKeyPlaceholder')],
        ],
      }
    case 'http':
      return { method, body: draft.body, extra: [] }
  }
}

/* ───────────────────────── 线缆预览 ───────────────────────── */

/** 由构造器合成、而非用户键入的头，界面上以不同色标出，说明「这行不是你写的」。 */
export type WireOrigin = 'typed' | 'synthesized'

export interface WireLine {
  name: string
  value: string
  origin: WireOrigin
}

export interface WirePreview {
  /** 请求行，如 `POST /v1/charges?x=1 HTTP/1.1`。 */
  requestLine: string
  headers: WireLine[]
  /** 被逐跳规则删掉的头名，界面上折叠成一句说明。 */
  dropped: string[]
  bodyBytes: number
  /** 整个请求（请求行 + 头 + 空行 + 体）的字节数。 */
  totalBytes: number
  /** WS 握手：空行之后不是体而是帧流，界面据此换一句说明代替 “Body …”。 */
  handshake?: boolean
}

function renderHead(requestLine: string, headers: WireLine[]): string {
  return `${requestLine}\r\n${headers.map((h) => `${h.name}: ${h.value}`).join('\r\n')}\r\n\r\n`
}

/** 名字为空的行是「待填的幽灵行」，不该出现在线上。 */
function typedRows(draft: Draft) {
  return draft.headers.filter((h) => h.name.trim() !== '')
}

function lowerNames(rows: { name: string }[]): Set<string> {
  return new Set(rows.map((h) => h.name.trim().toLowerCase()))
}

/** 算出这份草稿将要写到线上的报文。 */
export function buildWire(draft: Draft): WirePreview {
  const handshake = draft.kind === 'ws'
  const parsed = parseUrl(draft.url, handshake ? 'wss' : 'https')
  const { method, body, extra } = resolveWire(draft)
  const requestLine = `${method} ${parsed.target} HTTP/1.1`

  const rows = typedRows(draft)
  const typed = lowerNames(rows)
  const dropped: string[] = []
  const out: WireLine[] = []
  const bodyBytes = byteLength(body)

  const isName = (row: { name: string }, n: string) => row.name.trim().toLowerCase() === n
  // TE 与 Content-Length 的值都由后端重算，但位置留在用户写它的那一行上：
  // ApplyRequestToHTTP 先 Del/Set 进头表，再由 reconcileOrderedHeaders 沿 RawHeaders
  // 的原顺序回填，于是「第一条同名行拿到新值、其余行消失」。这里逐字照搬那个结果。
  const keepTE = !handshake && rows.some((r) => isName(r, 'te') && /trailers/i.test(r.value))
  // 握手报文空行之后是帧流，Content-Length 在那里没有意义；其余仅在确有体、
  // 或用户原本就写了 Content-Length 时才发它：不给无体的 GET 凭空加 0。
  const sendCL = !handshake && (bodyBytes > 0 || rows.some((r) => isName(r, 'content-length')))

  let hasHost = false
  let teEmitted = false
  let clEmitted = false

  for (const row of rows) {
    const name = row.name.trim()
    const lower = name.toLowerCase()

    if (lower === 'host') {
      hasHost = true
      out.push({ name, value: row.value.trim() || parsed.host, origin: 'typed' })
      continue
    }
    // WS 握手是逐跳头本身即载荷的唯一场景：照常剥除会把用户写的 Connection / Upgrade
    // 扔进 dropped，界面上就成了「你写的握手头被丢了」。
    if (!handshake) {
      if (lower === 'te') {
        if (keepTE && !teEmitted) {
          teEmitted = true
          out.push({ name, value: 'trailers', origin: 'typed' })
        } else {
          dropped.push(name)
        }
        continue
      }
      if (HOP_BY_HOP.includes(lower)) {
        dropped.push(name)
        continue
      }
    }
    // 体一律以 identity 送出，原有的 Content-Encoding 必然与实际字节不符。
    if (lower === 'content-encoding') {
      dropped.push(name)
      continue
    }
    if (lower === 'content-length') {
      if (sendCL && !clEmitted) {
        clEmitted = true
        out.push({ name, value: String(bodyBytes), origin: 'synthesized' })
      }
      continue
    }
    out.push({ name, value: row.value, origin: 'typed' })
  }

  if (!hasHost) out.unshift({ name: 'Host', value: parsed.host, origin: 'synthesized' })
  for (const [name, value] of extra) {
    if (!typed.has(name.toLowerCase())) out.push({ name, value, origin: 'synthesized' })
  }
  // 用户没写 Content-Length 时它是「新增的头」，reconcile 把这类一律排在末尾。
  if (sendCL && !clEmitted) {
    out.push({ name: 'Content-Length', value: String(bodyBytes), origin: 'synthesized' })
  }

  const head = renderHead(requestLine, out)
  return { requestLine, headers: out, dropped, bodyBytes, totalBytes: byteLength(head) + bodyBytes, handshake }
}

/** 线缆报文的纯文本形式（供复制）。 */
export function wireText(draft: Draft): string {
  const wire = buildWire(draft)
  return renderHead(wire.requestLine, wire.headers) + resolveWire(draft).body
}

/* ───────────────────────── 发送入参 ───────────────────────── */

/** Go 侧 RequestSpec.Kind 为 omitempty（空串等价 http），构造器一律显式填写。 */
type ComposeSpec = RequestSpec & { kind: DraftKind }

function specHeaders(draft: Draft, extra: [string, string][]): [string, string][] {
  const rows = typedRows(draft)
  const typed = lowerNames(rows)
  const out = rows.map((h) => [h.name.trim(), h.value] as [string, string])
  for (const [name, value] of extra) {
    if (!typed.has(name.toLowerCase())) out.push([name, value])
  }
  return out
}

/**
 * 一次性往返（http / graphql / sse）的发送入参。合成头必须一并送出：
 * 后端逐字照发不注入任何头，漏掉它们预览与实发就对不上了。
 */
export function toRequestSpec(draft: Draft): RequestSpec {
  const { method, body, extra } = resolveWire(draft)
  const spec: ComposeSpec = {
    kind: draft.kind || 'http',
    method,
    url: draft.url.trim(),
    headers: specHeaders(draft, extra),
    body,
    fromId: draft.seed?.flowId ?? '',
    viaPipeline: draft.viaPipeline,
  }
  return spec
}

/**
 * WS 握手的输入就是 URL + 有序头，body 无意义；复用同一个 Go 类型免得多一份契约。
 * 合成的 Connection / Upgrade / Sec-WebSocket-* 不送：那几条由 dialer 自己生成，
 * 重复给会让 gorilla 直接报 duplicate header not allowed。
 */
export function toWSSpec(draft: Draft): RequestSpec {
  const spec: ComposeSpec = {
    kind: 'ws',
    method: 'GET',
    url: draft.url.trim(),
    headers: specHeaders(draft, []),
    body: '',
    fromId: draft.seed?.flowId ?? '',
    viaPipeline: draft.viaPipeline,
  }
  return spec
}
