/**
 * 断点改包的纯逻辑层：解析暂停载荷、维护编辑草稿、算出要回传的补丁。
 *
 * 本文件集中处理暂停载荷、编辑草稿和回传补丁，供断点界面与 node --test 共用。
 */
import {
  basisValueB64,
  hasByteRows,
  headerRowsFrom,
  normalizeHeaders,
  rowValueB64,
  wireHeadersFrom,
  type HeaderRow,
  type WireHeaders,
} from '../compose/model.ts'

export type BreakPhase = 'request' | 'response'

/** 可载入编辑器的正文上限。 */
export const MAX_EDITABLE_BODY = 2 * 1024 * 1024

/** 由出线侧按最终报文重算的头，界面上置灰。 */
export const MANAGED_HEADERS = ['content-length', 'content-encoding', 'transfer-encoding']

export interface PausedBody {
  /** 原始字节的 base64。放行时原样保留用得上。 */
  base64: string
  /** 解码后的文本；binary 或 tooLarge 时为空串。 */
  text: string
  size: number
  /** 非 UTF-8 正文的字节形态。 */
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
  /** 与 requestHeaders 下标对齐的值字节旁路；编辑与比较按这里的字节值处理。 */
  requestHeadersB64: string[]
  requestBody: PausedBody
  status: number
  statusText: string
  responseHeaders: [string, string][]
  responseHeadersB64: string[]
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
 * 解码使用 fatal 模式，将非 UTF-8 字节流标记为二进制。
 */
export function decodeBody(base64: string): PausedBody {
  const b64 = (base64 ?? '').trim()
  if (!b64) return EMPTY_BODY
  // 先按 base64 长度估算字节数，再决定是否解码。
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

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.map(str) : []
}

/** 将值字节旁路归一为与头对等长的数组；长度不匹配时按无旁路处理。 */
function alignedB64(v: unknown, n: number): string[] {
  const out = strings(v)
  return out.length === n ? out : []
}

function record(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' ? (v as Record<string, unknown>) : {}
}

/**
 * 把 breakpoint_hit / GetBreakpoints 的载荷解析成 PausedFlow。
 * 载荷是 Go 侧 pipeline.BreakpointFlow 摊平后的 JSON，字段形状不匹配时返回 null。
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
  const reqHeaders = pairs(f.requestHeaders)
  const resHeaders = pairs(f.responseHeaders)

  return {
    id,
    phase,
    method: str(req.method) || 'GET',
    url: str(req.url),
    host: str(req.host),
    pausedUntil: Number.isNaN(until) ? 0 : until,
    requestHeaders: reqHeaders,
    requestHeadersB64: alignedB64(f.requestHeadersB64, reqHeaders.length),
    requestBody: decodeBody(str(req.body)),
    status: typeof resp?.status === 'number' ? resp.status : 0,
    statusText: reasonOf(str(resp?.statusText), typeof resp?.status === 'number' ? resp.status : 0),
    responseHeaders: resHeaders,
    responseHeadersB64: alignedB64(f.responseHeadersB64, resHeaders.length),
    responseBody: resp ? decodeBody(str(resp.body)) : EMPTY_BODY,
    hasResponse: resp !== null,
    // 这两个标记由抓包侧在调用 onResponse 前写入，用于判定正文编辑能力。
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

/** 返回该阶段正文是否可载入编辑器。 */
export function bodyEditable(p: PausedFlow): boolean {
  if (p.phase === 'request') return !p.requestBody.binary && !p.requestBody.tooLarge
  // 流式与透传响应的正文不可编辑；状态行与响应头仍可编辑。
  if (p.streamed || p.passthrough) return false
  return !p.responseBody.binary && !p.responseBody.tooLarge
}

/* ───────────────────────── 编辑草稿 ───────────────────────── */

export interface BreakDraft {
  method: string
  url: string
  requestHeaders: HeaderRow[]
  requestBody: string
  /** 文本形态：编辑阶段保留原文，提交时解析。 */
  status: string
  statusText: string
  responseHeaders: HeaderRow[]
  responseBody: string
}

export function rowsFrom(list: [string, string][], b64: readonly string[] = []): HeaderRow[] {
  return normalizeHeaders(headerRowsFrom(list, b64))
}

/** 规范化头部行，移除尾部空行与无名行。 */
export function pairsFrom(rows: HeaderRow[]): [string, string][] {
  return wireHeadersFrom(rows).pairs
}

export function draftFrom(p: PausedFlow): BreakDraft {
  return {
    method: p.method,
    url: p.url,
    requestHeaders: rowsFrom(p.requestHeaders, p.requestHeadersB64),
    requestBody: p.requestBody.text,
    status: p.status ? String(p.status) : '',
    statusText: p.statusText,
    responseHeaders: rowsFrom(p.responseHeaders, p.responseHeadersB64),
    responseBody: p.responseBody.text,
  }
}

/** 返回与蓝本出线字节不一致的头行 id，用于显示改动标记。 */
export function changedRows(
  rows: HeaderRow[],
  base: [string, string][],
  baseB64: readonly string[] = [],
): ReadonlySet<string> {
  const named = rows.filter((r) => r.name !== '' || r.value !== '')
  const side = baseB64.length === base.length ? baseB64 : []
  const out = new Set<string>()
  if (named.length === base.length) {
    // 行数相同时按位置比对头部行。
    named.forEach((row, i) => {
      const orig = base[i]
      if (!orig || orig[0] !== row.name || basisValueB64(orig[1], side[i]) !== rowValueB64(row)) out.add(row.id)
    })
    return out
  }
  // 行数变化时按头名与出线字节判断每一行是否仍在蓝本中。
  const seen = new Set(base.map(([n, v], i) => `${n}\u0000${basisValueB64(v, side[i])}`))
  for (const row of named) {
    if (!seen.has(`${row.name}\u0000${rowValueB64(row)}`)) out.add(row.id)
  }
  return out
}

/* ───────────────────────── 回传补丁 ───────────────────────── */

/** 与 Go 侧 pipeline.BreakpointEdit 对齐的编辑补丁；缺省字段表示保持原值。 */
export interface ResumePatch {
  request?: { method?: string; url?: string; headers?: [string, string][]; headersB64?: string[]; body?: string }
  response?: {
    status?: number
    statusText?: string
    headers?: [string, string][]
    headersB64?: string[]
    body?: string
  }
}

/** 按头名与出线字节比较编辑结果和蓝本。 */
function sameHeaders(edited: WireHeaders, base: [string, string][], baseB64: readonly string[]): boolean {
  if (edited.pairs.length !== base.length) return false
  const side = baseB64.length === base.length ? baseB64 : []
  return edited.pairs.every(
    (kv, i) => kv[0] === base[i][0] && basisValueB64(kv[1], edited.valuesB64[i]) === basisValueB64(base[i][1], side[i]),
  )
}

/** 返回该侧是否需要携带值字节旁路。蓝本或编辑行包含字节视图时启用。 */
function headerBytesNeeded(rows: HeaderRow[], baseB64: readonly string[]): boolean {
  return hasByteRows(rows) || baseB64.length > 0
}

/**
 * 算出要回传的补丁；没有字段变化时返回 null。
 *
 * 只产出当前阶段对应一侧的编辑补丁；正文仅在可编辑且发生变化时回传。
 */
export function buildResumePatch(draft: BreakDraft, base: PausedFlow): ResumePatch | null {
  const patch: ResumePatch = {}

  if (base.phase === 'request') {
    const req: NonNullable<ResumePatch['request']> = {}
    if (draft.method.trim() && draft.method !== base.method) req.method = draft.method.trim()
    if (draft.url.trim() && draft.url !== base.url) req.url = draft.url.trim()
    const headers = wireHeadersFrom(draft.requestHeaders)
    if (!sameHeaders(headers, base.requestHeaders, base.requestHeadersB64)) {
      req.headers = headers.pairs
      if (headerBytesNeeded(draft.requestHeaders, base.requestHeadersB64)) req.headersB64 = headers.valuesB64
    }
    if (bodyEditable(base) && draft.requestBody !== base.requestBody.text) req.body = draft.requestBody
    if (Object.keys(req).length > 0) patch.request = req
  } else {
    const resp: NonNullable<ResumePatch['response']> = {}
    const status = Number.parseInt(draft.status, 10)
    if (Number.isFinite(status) && status !== base.status) resp.status = status
    // 状态文本按用户编辑结果回传；状态码变化时由后端生成默认状态文本。
    if (draft.statusText !== base.statusText) resp.statusText = draft.statusText
    const headers = wireHeadersFrom(draft.responseHeaders)
    if (!sameHeaders(headers, base.responseHeaders, base.responseHeadersB64)) {
      resp.headers = headers.pairs
      if (headerBytesNeeded(draft.responseHeaders, base.responseHeadersB64)) resp.headersB64 = headers.valuesB64
    }
    if (bodyEditable(base) && draft.responseBody !== base.responseBody.text) resp.body = draft.responseBody
    if (Object.keys(resp).length > 0) patch.response = resp
  }

  return patch.request || patch.response ? patch : null
}
