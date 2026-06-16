import i18n from '@/i18n'
import type { HttpSession, WebSocketSession } from '@/types'
import type { ContentKind, Tone, TrafficRow } from './types'

/* ───────────────────────── 格式化 ───────────────────────── */

export function formatDuration(ms?: number): string {
  if (ms == null) return '—'
  if (ms < 1) return '<1ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)}s`
  return `${(ms / 60_000).toFixed(1)}m`
}

export function formatSize(bytes?: number): string {
  if (bytes == null) return '—'
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}K`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(2)}M`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)}G`
}

const pad2 = (n: number) => (n < 10 ? `0${n}` : `${n}`)
const pad3 = (n: number) => (n < 10 ? `00${n}` : n < 100 ? `0${n}` : `${n}`)

export function formatClock(epochMs?: number): string {
  if (!epochMs) return '—'
  const d = new Date(epochMs)
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}.${pad3(d.getMilliseconds())}`
}

export function formatRelative(epochMs?: number, now = Date.now()): string {
  if (!epochMs) return '—'
  const s = Math.max(0, Math.round((now - epochMs) / 1000))
  if (s < 60) return i18n.t('format.relative.seconds', { count: s })
  if (s < 3600) return i18n.t('format.relative.minutes', { count: Math.floor(s / 60) })
  return i18n.t('format.relative.hours', { count: Math.floor(s / 3600) })
}

/* ───────────────────────── 内容类型 ───────────────────────── */

export function detectContentKind(contentType: string, path = ''): ContentKind {
  const ct = (contentType || '').toLowerCase()
  const ext = path.split('?')[0].split('.').pop()?.toLowerCase() || ''

  if (ct.includes('application/json') || ct.includes('+json') || ext === 'json') return 'json'
  if (ct.includes('text/html') || ext === 'html' || ext === 'htm') return 'html'
  if (ct.includes('javascript') || ct.includes('ecmascript') || ['js', 'mjs', 'ts', 'tsx', 'jsx'].includes(ext)) return 'js'
  if (ct.includes('text/css') || ext === 'css' || ext === 'scss') return 'css'
  if (ct.includes('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico', 'avif'].includes(ext)) return 'image'
  if (ct.includes('font') || ['woff', 'woff2', 'ttf', 'otf', 'eot'].includes(ext)) return 'font'
  if (ct.includes('video/') || ['mp4', 'webm', 'mov', 'm3u8', 'ts'].includes(ext)) return 'video'
  if (ct.includes('audio/') || ['mp3', 'wav', 'ogg', 'flac'].includes(ext)) return 'audio'
  if (ct.includes('event-stream')) return 'stream'
  if (ct.includes('x-www-form-urlencoded') || ct.includes('multipart/form-data')) return 'form'
  if (ct.includes('xml') || ct.includes('text/') || ['txt', 'xml', 'md', 'csv'].includes(ext)) return 'text'
  if (ct.includes('pdf') || ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'zip', 'gz'].includes(ext)) return 'doc'
  if (ct.includes('application/octet-stream') || ct.includes('protobuf') || ct.includes('grpc')) return 'binary'
  return 'other'
}

export const contentKindLabel: Record<ContentKind, string> = {
  json: 'JSON',
  html: 'HTML',
  js: 'JS',
  css: 'CSS',
  image: 'Image',
  font: 'Font',
  video: 'Video',
  audio: 'Audio',
  text: 'Text',
  doc: 'Document',
  form: 'Form',
  stream: 'Stream',
  binary: 'Binary',
  other: 'Other',
}

/* ───────────────────────── 语义色调 ───────────────────────── */

/** 状态色调：pending(琥珀脉冲)/2xx 绿/3xx 蓝/4xx 琥珀/5xx 红/无响应 中性 */
export function statusTone(row: Pick<TrafficRow, 'state' | 'status' | 'blocked'>): Tone {
  if (row.blocked) return 'danger'
  if (row.state === 'pending') return 'pending'
  if (row.state === 'error') return 'danger'
  const s = row.status
  if (!s) return 'neutral'
  if (s >= 200 && s < 300) return 'ok'
  if (s >= 300 && s < 400) return 'info'
  if (s >= 400 && s < 500) return 'warn'
  if (s >= 500) return 'danger'
  return 'neutral'
}

export const toneText: Record<Tone, string> = {
  ok: 'text-ok',
  info: 'text-info',
  warn: 'text-warn',
  danger: 'text-danger',
  pending: 'text-warn',
  neutral: 'text-fg-faint',
}

export const toneDot: Record<Tone, string> = {
  ok: 'bg-ok',
  info: 'bg-info',
  warn: 'bg-warn',
  danger: 'bg-danger',
  pending: 'bg-warn',
  neutral: 'bg-fg-faint',
}

/** HTTP method → 文本色 class（method.* token） */
export function methodText(method: string): string {
  switch (method.toUpperCase()) {
    case 'GET': return 'text-method-get'
    case 'POST': return 'text-method-post'
    case 'PUT': return 'text-method-put'
    case 'DELETE': return 'text-method-delete'
    case 'PATCH': return 'text-method-patch'
    default: return 'text-method-other'
  }
}

export function statusLabel(row: Pick<TrafficRow, 'state' | 'status' | 'blocked'>): string {
  if (row.blocked) return 'BLOCKED'
  if (row.state === 'pending') return '···'
  if (row.state === 'error') return 'ERR'
  return row.status ? String(row.status) : '—'
}

/* ───────────────────────── 适配器：DTO → 行模型 ───────────────────────── */

function schemeFromUrl(url: string, fallback: string): TrafficRow['scheme'] {
  if (url.startsWith('https')) return 'https'
  if (url.startsWith('http')) return 'http'
  if (url.startsWith('wss')) return 'wss'
  if (url.startsWith('ws')) return 'ws'
  const f = (fallback || '').toLowerCase()
  if (f === 'https' || f === 'http' || f === 'ws' || f === 'wss') return f
  return 'https'
}

function headerCT(headers?: Record<string, string>): string {
  if (!headers) return ''
  return headers['content-type'] || headers['Content-Type'] || ''
}

export function toRowFromHttp(s: HttpSession, seq: number): TrafficRow {
  const url = s.request.url || ''
  const contentType = headerCT(s.response?.headers)
  const startedAt = Date.parse(s.request.timestamp) || Date.now()
  return {
    id: s.id,
    seq,
    kind: 'http',
    method: s.request.method || 'GET',
    scheme: schemeFromUrl(url, s.request.protocol),
    host: s.request.host || safeHost(url),
    path: s.request.path || safePath(url),
    url,
    status: s.response?.status,
    statusText: s.response?.statusText,
    state: s.status,
    blocked: s.blocked,
    modified: s.modified,
    error: s.error,
    contentType,
    contentKind: detectContentKind(contentType, s.request.path || url),
    durationMs: s.duration ?? s.response?.responseTime,
    sizeBytes: s.response?.size,
    clientIP: s.request.clientIP,
    process: s.processName,
    iconData: s.iconData,
    iconType: s.iconType,
    startedAt,
    reqHeaders: s.request.headers,
    resHeaders: s.response?.headers,
    reqBody: s.request.body,
    resBody: s.response?.body,
  }
}

export function toRowFromWs(s: WebSocketSession, seq: number): TrafficRow {
  const url = s.url || ''
  const stateMap: Record<WebSocketSession['status'], TrafficRow['state']> = {
    connecting: 'pending',
    connected: 'completed',
    disconnected: 'completed',
    error: 'error',
  }
  return {
    id: s.id,
    seq,
    kind: 'ws',
    method: 'WS',
    scheme: schemeFromUrl(url, 'wss'),
    host: safeHost(url),
    path: safePath(url),
    url,
    state: stateMap[s.status] ?? 'completed',
    contentType: 'websocket',
    contentKind: 'stream',
    sizeBytes: s.totalSize,
    process: s.processName,
    iconData: s.iconData,
    iconType: s.iconType,
    startedAt: Date.parse(s.startTime) || Date.now(),
  }
}

function safeHost(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url.replace(/^[a-z]+:\/\//, '').split('/')[0] || ''
  }
}

function safePath(url: string): string {
  try {
    const u = new URL(url)
    return u.pathname + u.search
  } catch {
    const i = url.indexOf('/', url.indexOf('://') + 3)
    return i >= 0 ? url.slice(i) : '/'
  }
}

/** 安全美化 JSON；失败则原样返回 */
export function prettyJson(content: string): string {
  try {
    return JSON.stringify(JSON.parse(content), null, 2)
  } catch {
    return content
  }
}

/* ───────────────────────── 详情面板辅助 ───────────────────────── */

export function headerEntries(headers?: Record<string, string>): [string, string][] {
  return Object.entries(headers ?? {})
}

/** 取头部值（大小写不敏感） */
export function getHeader(headers: Record<string, string> | undefined, name: string): string | undefined {
  if (!headers) return undefined
  const lower = name.toLowerCase()
  for (const [k, v] of Object.entries(headers)) {
    if (k.toLowerCase() === lower) return v
  }
  return undefined
}

/** 解析 Cookie / Set-Cookie 头为 [名, 值] 列表 */
export function parseCookies(raw?: string): [string, string][] {
  if (!raw) return []
  return raw
    .split(/;\s*/)
    .filter((p) => p.includes('='))
    .map((p) => {
      const i = p.indexOf('=')
      return [p.slice(0, i).trim(), p.slice(i + 1).trim()] as [string, string]
    })
}

/** 解析 URL 查询参数为 [名, 值] 列表 */
export function parseQueryParams(url: string): [string, string][] {
  try {
    const u = new URL(url)
    return Array.from(u.searchParams.entries())
  } catch {
    const q = url.split('?')[1]
    if (!q) return []
    return q.split('&').map((p) => {
      const [k, ...rest] = p.split('=')
      return [decodeSafe(k), decodeSafe(rest.join('='))] as [string, string]
    })
  }
}

/** 解析 x-www-form-urlencoded 请求体为 [名, 值] 列表 */
export function parseFormParams(body?: string): [string, string][] {
  if (!body || !body.includes('=')) return []
  return body
    .split('&')
    .filter(Boolean)
    .map((p) => {
      const [k, ...rest] = p.split('=')
      return [decodeSafe(k), decodeSafe(rest.join('='))] as [string, string]
    })
}

function decodeSafe(s: string): string {
  try {
    return decodeURIComponent(s.replace(/\+/g, ' '))
  } catch {
    return s
  }
}

/** 构造原始请求文本 */
export function buildRawRequest(row: TrafficRow): string {
  let raw = `${row.method} ${row.path || '/'} HTTP/1.1\r\n`
  for (const [k, v] of headerEntries(row.reqHeaders)) raw += `${k}: ${v}\r\n`
  raw += '\r\n'
  if (row.reqBody) raw += row.reqBody
  return raw
}

/** 构造原始响应文本 */
export function buildRawResponse(row: TrafficRow): string {
  if (!row.status && !row.resHeaders && !row.resBody) return ''
  let raw = `HTTP/1.1 ${row.status ?? ''} ${row.statusText ?? ''}\r\n`
  for (const [k, v] of headerEntries(row.resHeaders)) raw += `${k}: ${v}\r\n`
  raw += '\r\n'
  if (row.resBody) raw += row.resBody
  return raw
}
