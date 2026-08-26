/**
 * 断点改包的纯逻辑层：解析暂停载荷、维护编辑草稿、算出要回传的补丁。
 *
 * 这里是「改包」全部危险规则的唯一落点，也是唯一能被 node --test 覆盖的一层
 * （.tsx 测不了，且 react-refresh 不许在组件文件里导出非组件函数）：
 *   - 后端的头部是替换语义，回传一份不含某个头的列表就等于删掉它；
 *   - body 缺省表示「不改」，故载不进编辑器的二进制 / 超大体一律不回传；
 *   - 状态码与状态文本必须同源，只改状态码时不能把旧文本一起送回去。
 * 因此本文件不许出现 `@/` 别名的运行期 import —— node --test 直接加载它。
 */
import { newHeaderRow, normalizeHeaders, type HeaderRow } from '../compose/model.ts'

export type BreakPhase = 'request' | 'response'

/** 能载进编辑器的正文上限。再大就不解码：atob 一份几十 MB 的 base64 会把界面卡住。 */
export const MAX_EDITABLE_BODY = 2 * 1024 * 1024

/** 由出线侧按最终报文重算的头，改了也不会生效，界面上置灰。 */
export const MANAGED_HEADERS = ['content-length', 'content-encoding', 'transfer-encoding']

export interface PausedBody {
  /** 原始字节的 base64。放行时原样保留用得上。 */
  base64: string
  /** 解码后的文本；binary 或 tooLarge 时为空串。 */
  text: string
  size: number
  /** 不是合法 UTF-8：文本编辑器里往返会损坏内容。 */
  binary: boolean
  /** 超过 MAX_EDITABLE_BODY。 */
  tooLarge: boolean
}

export interface PausedFlow {
  id: string
  phase: BreakPhase
  method: string
  url: string
  host: string
  /** 自动放行时刻（epoch ms）；后端未给出时为 0，界面不显示倒计时。 */
  pausedUntil: number
  requestHeaders: [string, string][]
  requestBody: PausedBody
  status: number
  statusText: string
  responseHeaders: [string, string][]
  responseBody: PausedBody
  /** 有响应：响应阶段断点才有。 */
  hasResponse: boolean
  /** 流式响应（SSE / gRPC / 分块流）：正文逐条中继，暂停时不在内存里。 */
  streamed: boolean
  /** 透传旁路（媒体 / 206 / 超阈值）：正文边收边发落盘，同样不在内存里。 */
  passthrough: boolean
}

const EMPTY_BODY: PausedBody = { base64: '', text: '', size: 0, binary: false, tooLarge: false }

/* ───────────────────────── base64 ───────────────────────── */

/**
 * 就地实现而不复用 workbench/lib/tools.ts：那边 import 了 `@/i18n`，node --test 加载不了。
 * 解码用 fatal 模式——非 UTF-8 的字节流在文本编辑器里往返一趟就被替换字符毁掉了，
 * 必须当场认出来并转成只读。
 */
export function decodeBody(base64: string): PausedBody {
  const b64 = (base64 ?? '').trim()
  if (!b64) return EMPTY_BODY
  // 先按 base64 长度估字节数，超限就不解码：atob 本身才是卡住界面的那一步。
  const approx = Math.floor((b64.length * 3) / 4)
  if (approx > MAX_EDITABLE_BODY) {
    return { base64: b64, text: '', size: approx, binary: false, tooLarge: true }
  }
  let bytes: Uint8Array
  try {
    bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
  } catch {
    return { base64: b64, text: '', size: approx, binary: true, tooLarge: false }
  }
  try {
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
    return { base64: b64, text, size: bytes.length, binary: false, tooLarge: false }
  } catch {
    return { base64: b64, text: '', size: bytes.length, binary: true, tooLarge: false }
  }
}

export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length
}

/* ───────────────────────── 载荷解析 ───────────────────────── */

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function pairs(v: unknown): [string, string][] {
  if (!Array.isArray(v)) return []
  const out: [string, string][] = []
  for (const item of v) {
    if (Array.isArray(item) && item.length >= 2) out.push([str(item[0]), str(item[1])])
  }
  return out
}

function record(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : {}
}

/**
 * 把 breakpoint_hit / GetBreakpoints 的载荷解析成 PausedFlow。
 * 载荷是 Go 侧 pipeline.BreakpointFlow 摊平后的 JSON，形状对不上就返回 null——
 * 断点是能把请求按住五分钟的功能，宁可少显示一行，也不要拿半个对象去渲染编辑器。
 */
export function parsePausedFlow(raw: unknown): PausedFlow | null {
  const f = record(raw)
  const id = str(f.id)
  if (!id) return null

  const req = record(f.request)
  const resp = f.response ? record(f.response) : null
  const meta = record(f.metadata)
  const phase: BreakPhase = f.pausedAt === 'response' || (!f.pausedAt && resp) ? 'response' : 'request'
  const until = Date.parse(str(f.pausedUntil))

  return {
    id,
    phase,
    method: str(req.method) || 'GET',
    url: str(req.url),
    host: str(req.host),
    pausedUntil: Number.isNaN(until) ? 0 : until,
    requestHeaders: pairs(f.requestHeaders),
    requestBody: decodeBody(str(req.body)),
    status: typeof resp?.status === 'number' ? resp.status : 0,
    statusText: reasonOf(str(resp?.statusText), typeof resp?.status === 'number' ? resp.status : 0),
    responseHeaders: pairs(f.responseHeaders),
    responseBody: resp ? decodeBody(str(resp.body)) : EMPTY_BODY,
    hasResponse: resp !== null,
    // 这两个标记由抓包侧在调 onResponse 之前写入，是「正文能不能改」的唯一判据。
    streamed: meta.stream !== undefined,
    passthrough: meta.passthrough !== undefined,
  }
}

/**
 * 取状态行里的原因短语。后端存的是 Go 的 resp.Status，形如 "200 OK"——带状态码前缀的整串。
 * 原样填进编辑框，用户把状态码改成 404 之后再顺手补一个词，出线就会拼成
 * `HTTP/1.1 404 200 OK ...`。这里剥掉前缀，编辑框里从头到尾只是原因短语。
 */
function reasonOf(statusText: string, status: number): string {
  const prefix = String(status)
  return statusText.startsWith(prefix) ? statusText.slice(prefix.length).trim() : statusText
}

/** 该阶段的正文能不能载进编辑器；不能则如实告知并转只读。 */
export function bodyEditable(p: PausedFlow): boolean {
  if (p.phase === 'request') return !p.requestBody.binary && !p.requestBody.tooLarge
  // 流式与透传旁路暂停时 Response.Body 是空的，正文由上游逐条中继，改了也不会生效。
  // 状态行与响应头则相反——两条路径写回客户端时用的都是 Flow.Response 上的值，照改不误。
  if (p.streamed || p.passthrough) return false
  return !p.responseBody.binary && !p.responseBody.tooLarge
}

/* ───────────────────────── 编辑草稿 ───────────────────────── */

export interface BreakDraft {
  method: string
  url: string
  requestHeaders: HeaderRow[]
  requestBody: string
  /** 文本形态：编辑中允许空串等中间态，提交时才 parse。 */
  status: string
  statusText: string
  responseHeaders: HeaderRow[]
  responseBody: string
}

export function rowsFrom(list: [string, string][]): HeaderRow[] {
  return normalizeHeaders(list.map(([name, value]) => newHeaderRow(name, value)))
}

/** 丢掉尾部空行与无名行：无名头写不到线上，留着只会变成一条空的 `: value`。 */
export function pairsFrom(rows: HeaderRow[]): [string, string][] {
  return rows.filter((r) => r.name.trim() !== '').map((r) => [r.name, r.value] as [string, string])
}

export function draftFrom(p: PausedFlow): BreakDraft {
  return {
    method: p.method,
    url: p.url,
    requestHeaders: rowsFrom(p.requestHeaders),
    requestBody: p.requestBody.text,
    status: p.status ? String(p.status) : '',
    statusText: p.statusText,
    responseHeaders: rowsFrom(p.responseHeaders),
    responseBody: p.responseBody.text,
  }
}

/** 与蓝本对不上的头行 id 集合，用于在表格左侧画改动条。 */
export function changedRows(rows: HeaderRow[], base: [string, string][]): ReadonlySet<string> {
  const named = rows.filter((r) => r.name !== '' || r.value !== '')
  const out = new Set<string>()
  if (named.length === base.length) {
    // 行数没变：逐位置比对，改哪一行就标哪一行。
    named.forEach((row, i) => {
      const orig = base[i]
      if (!orig || orig[0] !== row.name || orig[1] !== row.value) out.add(row.id)
    })
    return out
  }
  // 行数变了（删了或插了一行）：再逐位置比对会把其后所有行整片标成改动，
  // 看起来像误触批量改写了整份头部。改按「这一行在蓝本里存不存在」判定。
  const seen = new Set(base.map(([n, v]) => `${n}\u0000${v}`))
  for (const row of named) {
    if (!seen.has(`${row.name}\u0000${row.value}`)) out.add(row.id)
  }
  return out
}

/* ───────────────────────── 回传补丁 ───────────────────────── */

/** 与 Go 侧 pipeline.BreakpointEdit 逐字段对齐：缺省 = 没动过，不是清空。 */
export interface ResumePatch {
  request?: { method?: string; url?: string; headers?: [string, string][]; body?: string }
  response?: { status?: number; statusText?: string; headers?: [string, string][]; body?: string }
}

function samePairs(a: [string, string][], b: [string, string][]): boolean {
  return a.length === b.length && a.every((kv, i) => kv[0] === b[i][0] && kv[1] === b[i][1])
}

/**
 * 算出要回传的补丁；一处没改就返回 null（调用方据此走「原样放行」）。
 *
 * 只产出当前阶段那一侧：响应阶段的请求早已发出，把它一起送回去只会把一份改不动的
 * 东西重写一遍。正文只在「载得进编辑器且真的改过」时回传——缺省即不改，二进制与
 * 超大体因此原封不动地留在后端。
 */
export function buildResumePatch(draft: BreakDraft, base: PausedFlow): ResumePatch | null {
  const patch: ResumePatch = {}

  if (base.phase === 'request') {
    const req: NonNullable<ResumePatch['request']> = {}
    if (draft.method.trim() && draft.method !== base.method) req.method = draft.method.trim()
    if (draft.url.trim() && draft.url !== base.url) req.url = draft.url.trim()
    const headers = pairsFrom(draft.requestHeaders)
    if (!samePairs(headers, base.requestHeaders)) req.headers = headers
    if (bodyEditable(base) && draft.requestBody !== base.requestBody.text) req.body = draft.requestBody
    if (Object.keys(req).length > 0) patch.request = req
  } else {
    const resp: NonNullable<ResumePatch['response']> = {}
    const status = Number.parseInt(draft.status, 10)
    if (Number.isFinite(status) && status !== base.status) resp.status = status
    // 状态文本只在用户自己改过时回传：只换状态码时由后端按新码重新派生，
    // 否则线上会拼出 "HTTP/1.1 404 200 OK"。
    if (draft.statusText !== base.statusText) resp.statusText = draft.statusText
    const headers = pairsFrom(draft.responseHeaders)
    if (!samePairs(headers, base.responseHeaders)) resp.headers = headers
    if (bodyEditable(base) && draft.responseBody !== base.responseBody.text) resp.body = draft.responseBody
    if (Object.keys(resp).length > 0) patch.response = resp
  }

  return patch.request || patch.response ? patch : null
}
