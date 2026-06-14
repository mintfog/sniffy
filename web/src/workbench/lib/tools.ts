/*
 * 纯前端实用工具（工具箱用）：编码/解码、消息摘要、生成。
 * 全部在本地计算，不依赖后端。
 */
import i18n from '@/i18n'

/* ───────────────────────── Base64 ───────────────────────── */

export function base64Encode(input: string): string {
  const bytes = new TextEncoder().encode(input)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin)
}

export function base64Decode(input: string): string {
  const cleaned = input.trim().replace(/\s+/g, '')
  const bin = atob(cleaned)
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

/* ───────────────────────── URL 编码 ───────────────────────── */

export function urlEncode(input: string): string {
  return encodeURIComponent(input)
}

export function urlDecode(input: string): string {
  return decodeURIComponent(input.replace(/\+/g, ' '))
}

/* ───────────────────────── JWT 解析 ───────────────────────── */

function base64UrlDecode(seg: string): string {
  const b64 = seg.replace(/-/g, '+').replace(/_/g, '/')
  const pad = b64.length % 4 ? '='.repeat(4 - (b64.length % 4)) : ''
  const bin = atob(b64 + pad)
  const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

export interface JwtParts {
  header: unknown
  payload: unknown
  signature: string
}

export function parseJwt(token: string): JwtParts {
  const parts = token.trim().split('.')
  if (parts.length < 2) throw new Error(i18n.t('toolbox.jwt.errInvalid'))
  let header: unknown
  let payload: unknown
  try {
    header = JSON.parse(base64UrlDecode(parts[0]))
  } catch {
    throw new Error(i18n.t('toolbox.jwt.errHeader'))
  }
  try {
    payload = JSON.parse(base64UrlDecode(parts[1]))
  } catch {
    throw new Error(i18n.t('toolbox.jwt.errPayload'))
  }
  return { header, payload, signature: parts[2] ?? '' }
}

/** 把 JWT 中常见的时间戳字段（exp/iat/nbf）翻译成可读时间，便于展示。 */
export function describeJwtClaims(payload: unknown): string[] {
  const notes: string[] = []
  if (payload && typeof payload === 'object') {
    const p = payload as Record<string, unknown>
    const fmt = (n: number) => new Date(n * 1000).toLocaleString()
    if (typeof p.exp === 'number') {
      const expired = p.exp * 1000 < Date.now()
      notes.push(
        i18n.t('toolbox.jwt.exp', { time: fmt(p.exp) }) +
          (expired ? i18n.t('toolbox.jwt.expired') : ''),
      )
    }
    if (typeof p.iat === 'number') notes.push(i18n.t('toolbox.jwt.iat', { time: fmt(p.iat) }))
    if (typeof p.nbf === 'number') notes.push(i18n.t('toolbox.jwt.nbf', { time: fmt(p.nbf) }))
  }
  return notes
}

/* ───────────────────────── 消息摘要 ───────────────────────── */

export type DigestAlgo = 'MD5' | 'SHA-1' | 'SHA-256'

function toHex(buf: ArrayBuffer): string {
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

export async function digest(algo: DigestAlgo, input: string): Promise<string> {
  if (algo === 'MD5') return md5(input)
  const data = new TextEncoder().encode(input)
  const buf = await crypto.subtle.digest(algo, data)
  return toHex(buf)
}

/* —— MD5（SubtleCrypto 不提供，内置实现：Joseph Myers 公有领域版本，作用于 UTF-8 字节） —— */

function md5(input: string): string {
  return rhex(md51(utf8(input)))
}

function utf8(s: string): string {
  return unescape(encodeURIComponent(s))
}

function add32(a: number, b: number): number {
  return (a + b) & 0xffffffff
}

function cmn(q: number, a: number, b: number, x: number, s: number, t: number): number {
  a = add32(add32(a, q), add32(x, t))
  return add32((a << s) | (a >>> (32 - s)), b)
}
function ff(a: number, b: number, c: number, d: number, x: number, s: number, t: number) {
  return cmn((b & c) | (~b & d), a, b, x, s, t)
}
function gg(a: number, b: number, c: number, d: number, x: number, s: number, t: number) {
  return cmn((b & d) | (c & ~d), a, b, x, s, t)
}
function hh(a: number, b: number, c: number, d: number, x: number, s: number, t: number) {
  return cmn(b ^ c ^ d, a, b, x, s, t)
}
function ii(a: number, b: number, c: number, d: number, x: number, s: number, t: number) {
  return cmn(c ^ (b | ~d), a, b, x, s, t)
}

function md5cycle(x: number[], k: number[]) {
  let a = x[0]
  let b = x[1]
  let c = x[2]
  let d = x[3]

  a = ff(a, b, c, d, k[0], 7, -680876936)
  d = ff(d, a, b, c, k[1], 12, -389564586)
  c = ff(c, d, a, b, k[2], 17, 606105819)
  b = ff(b, c, d, a, k[3], 22, -1044525330)
  a = ff(a, b, c, d, k[4], 7, -176418897)
  d = ff(d, a, b, c, k[5], 12, 1200080426)
  c = ff(c, d, a, b, k[6], 17, -1473231341)
  b = ff(b, c, d, a, k[7], 22, -45705983)
  a = ff(a, b, c, d, k[8], 7, 1770035416)
  d = ff(d, a, b, c, k[9], 12, -1958414417)
  c = ff(c, d, a, b, k[10], 17, -42063)
  b = ff(b, c, d, a, k[11], 22, -1990404162)
  a = ff(a, b, c, d, k[12], 7, 1804603682)
  d = ff(d, a, b, c, k[13], 12, -40341101)
  c = ff(c, d, a, b, k[14], 17, -1502002290)
  b = ff(b, c, d, a, k[15], 22, 1236535329)

  a = gg(a, b, c, d, k[1], 5, -165796510)
  d = gg(d, a, b, c, k[6], 9, -1069501632)
  c = gg(c, d, a, b, k[11], 14, 643717713)
  b = gg(b, c, d, a, k[0], 20, -373897302)
  a = gg(a, b, c, d, k[5], 5, -701558691)
  d = gg(d, a, b, c, k[10], 9, 38016083)
  c = gg(c, d, a, b, k[15], 14, -660478335)
  b = gg(b, c, d, a, k[4], 20, -405537848)
  a = gg(a, b, c, d, k[9], 5, 568446438)
  d = gg(d, a, b, c, k[14], 9, -1019803690)
  c = gg(c, d, a, b, k[3], 14, -187363961)
  b = gg(b, c, d, a, k[8], 20, 1163531501)
  a = gg(a, b, c, d, k[13], 5, -1444681467)
  d = gg(d, a, b, c, k[2], 9, -51403784)
  c = gg(c, d, a, b, k[7], 14, 1735328473)
  b = gg(b, c, d, a, k[12], 20, -1926607734)

  a = hh(a, b, c, d, k[5], 4, -378558)
  d = hh(d, a, b, c, k[8], 11, -2022574463)
  c = hh(c, d, a, b, k[11], 16, 1839030562)
  b = hh(b, c, d, a, k[14], 23, -35309556)
  a = hh(a, b, c, d, k[1], 4, -1530992060)
  d = hh(d, a, b, c, k[4], 11, 1272893353)
  c = hh(c, d, a, b, k[7], 16, -155497632)
  b = hh(b, c, d, a, k[10], 23, -1094730640)
  a = hh(a, b, c, d, k[13], 4, 681279174)
  d = hh(d, a, b, c, k[0], 11, -358537222)
  c = hh(c, d, a, b, k[3], 16, -722521979)
  b = hh(b, c, d, a, k[6], 23, 76029189)
  a = hh(a, b, c, d, k[9], 4, -640364487)
  d = hh(d, a, b, c, k[12], 11, -421815835)
  c = hh(c, d, a, b, k[15], 16, 530742520)
  b = hh(b, c, d, a, k[2], 23, -995338651)

  a = ii(a, b, c, d, k[0], 6, -198630844)
  d = ii(d, a, b, c, k[7], 10, 1126891415)
  c = ii(c, d, a, b, k[14], 15, -1416354905)
  b = ii(b, c, d, a, k[5], 21, -57434055)
  a = ii(a, b, c, d, k[12], 6, 1700485571)
  d = ii(d, a, b, c, k[3], 10, -1894986606)
  c = ii(c, d, a, b, k[10], 15, -1051523)
  b = ii(b, c, d, a, k[1], 21, -2054922799)
  a = ii(a, b, c, d, k[8], 6, 1873313359)
  d = ii(d, a, b, c, k[15], 10, -30611744)
  c = ii(c, d, a, b, k[6], 15, -1560198380)
  b = ii(b, c, d, a, k[13], 21, 1309151649)
  a = ii(a, b, c, d, k[4], 6, -145523070)
  d = ii(d, a, b, c, k[11], 10, -1120210379)
  c = ii(c, d, a, b, k[2], 15, 718787259)
  b = ii(b, c, d, a, k[9], 21, -343485551)

  x[0] = add32(a, x[0])
  x[1] = add32(b, x[1])
  x[2] = add32(c, x[2])
  x[3] = add32(d, x[3])
}

function md5blk(s: string): number[] {
  const blks: number[] = []
  for (let i = 0; i < 64; i += 4) {
    blks[i >> 2] =
      s.charCodeAt(i) +
      (s.charCodeAt(i + 1) << 8) +
      (s.charCodeAt(i + 2) << 16) +
      (s.charCodeAt(i + 3) << 24)
  }
  return blks
}

function md51(s: string): number[] {
  const n = s.length
  const state = [1732584193, -271733879, -1732584194, 271733878]
  let i: number
  for (i = 64; i <= n; i += 64) {
    md5cycle(state, md5blk(s.substring(i - 64, i)))
  }
  s = s.substring(i - 64)
  const tail = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]
  for (i = 0; i < s.length; i++) tail[i >> 2] |= s.charCodeAt(i) << ((i % 4) << 3)
  tail[i >> 2] |= 0x80 << ((i % 4) << 3)
  if (i > 55) {
    md5cycle(state, tail)
    for (i = 0; i < 16; i++) tail[i] = 0
  }
  tail[14] = n * 8
  md5cycle(state, tail)
  return state
}

function rhex(x: number[]): string {
  const hexchars = '0123456789abcdef'
  let str = ''
  for (let j = 0; j < x.length; j++) {
    for (let i = 0; i < 4; i++) {
      const byte = (x[j] >> (i * 8)) & 0xff
      str += hexchars[(byte >> 4) & 0x0f] + hexchars[byte & 0x0f]
    }
  }
  return str
}

/* ───────────────────────── 生成 ───────────────────────── */

export function genUuid(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  // 兜底：RFC4122 v4
  const buf = new Uint8Array(16)
  crypto.getRandomValues(buf)
  buf[6] = (buf[6] & 0x0f) | 0x40
  buf[8] = (buf[8] & 0x3f) | 0x80
  const h = [...buf].map((b) => b.toString(16).padStart(2, '0'))
  return `${h[0]}${h[1]}${h[2]}${h[3]}-${h[4]}${h[5]}-${h[6]}${h[7]}-${h[8]}${h[9]}-${h[10]}${h[11]}${h[12]}${h[13]}${h[14]}${h[15]}`
}

export interface TimestampInfo {
  unix: number
  unixMs: number
  iso: string
  local: string
  utc: string
}

export function timestampNow(): TimestampInfo {
  return timestampFrom(new Date())
}

function timestampFrom(d: Date): TimestampInfo {
  const ms = d.getTime()
  return {
    unix: Math.floor(ms / 1000),
    unixMs: ms,
    iso: d.toISOString(),
    local: d.toLocaleString(),
    utc: d.toUTCString(),
  }
}

/** 解析输入：纯数字按 Unix（10 位=秒，13 位=毫秒）；否则按日期字符串。 */
export function parseTimestamp(input: string): TimestampInfo {
  const trimmed = input.trim()
  if (!trimmed) throw new Error(i18n.t('toolbox.timestamp.errEmpty'))
  if (/^\d+$/.test(trimmed)) {
    const num = Number(trimmed)
    const ms = trimmed.length <= 10 ? num * 1000 : num
    const d = new Date(ms)
    if (Number.isNaN(d.getTime())) throw new Error(i18n.t('toolbox.timestamp.errInvalidTime'))
    return timestampFrom(d)
  }
  const d = new Date(trimmed)
  if (Number.isNaN(d.getTime())) throw new Error(i18n.t('toolbox.timestamp.errInvalidDate'))
  return timestampFrom(d)
}
