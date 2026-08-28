/**
 * HTTP 头值的字节层编解码。
 *
 * RFC 7230 的 obs-text 允许头值出现 0x80-0xFF（历史语义是 ISO-8859-1），典型例子是
 * Content-Disposition 的 Latin-1 文件名。JSON 编码会将非 UTF-8 字节规范化为 U+FFFD，
 * 后端同时发送 base64 旁路，由本文件据此还原精确字节。
 *
 * 同一份字节有两种展示约定：详情页按 Latin-1 逐字节渲染，断点编辑器与构造器用 \xNN 转义编辑。
 *
 * 本文件不引入任何依赖，只用浏览器与 Node 都有的全局（atob/btoa/TextEncoder/TextDecoder）：
 * npm test 以 node --test 直接加载本文件与同名测试，不经打包器与路径别名。
 */

/** 头值行的编码模式。'text' 按 UTF-8 出线；'bytes' 按 \xNN 转义规则出线。 */
export type HeaderEnc = 'text' | 'bytes'

/** 转义串里 `\x` 后不足两位十六进制。调用方据此拦住发送。 */
export type HeaderEncodeError = 'badEscape'

/** 一个头值的字节视图：解码后的规范形态，渲染 / diff / 回程都读它。 */
export interface HeaderValueBytes {
  /** 线上确切字节 */
  bytes: Uint8Array
  /** 标准 base64（带填充），原样回传用 */
  b64: string
  /** 每字节一个码位的 Latin-1 渲染 */
  latin1: string
  /** \xNN 转义（大写十六进制，字面反斜杠写作 \\） */
  escaped: string
}

/** 标准 base64（StdEncoding，带填充），与 Go 侧旁路编码一致。 */
const STD_B64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/

/** 长字节序列按块调用 String.fromCharCode。 */
const CHUNK = 0x8000

/**
 * 标准 base64 → 字节。非法编码和空串返回 null。
 * 平行数组中的空串表示该行不携带旁路。
 */
export function bytesFromB64(b64: string): Uint8Array | null {
  if (typeof b64 !== 'string' || b64.length === 0 || b64.length % 4 !== 0 || !STD_B64.test(b64)) return null
  let bin: string
  try {
    bin = atob(b64)
  } catch {
    return null
  }
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/** 字节 → 标准 base64（带填充）。 */
export function b64FromBytes(bytes: Uint8Array): string {
  let bin = ''
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return btoa(bin)
}

/** 文本按 UTF-8 编码。 */
export function utf8Bytes(text: string): Uint8Array {
  return new TextEncoder().encode(text)
}

/**
 * 字节按 UTF-8 解码，非法字节替换为 U+FFFD。
 *
 * 这就是 Go 侧 `string(b)` 出境后本来会得到的那串，用于回填文本字段；
 * 纯 REST 视角下行为与文本路径逐字相同。
 */
export function utf8Lossy(bytes: Uint8Array): string {
  return new TextDecoder('utf-8').decode(bytes)
}

/** 字节序列是否为合法 UTF-8。 */
export function isValidUtf8(bytes: Uint8Array): boolean {
  try {
    new TextDecoder('utf-8', { fatal: true }).decode(bytes)
    return true
  } catch {
    return false
  }
}

/** 每字节一个码位（ISO-8859-1）。只用于只读渲染。 */
export function latin1FromBytes(bytes: Uint8Array): string {
  let out = ''
  for (let i = 0; i < bytes.length; i += CHUNK) {
    out += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return out
}

/** 文本按 UTF-8 编码后取 base64。diff 归一到字节层时用它把明文值折成同一个域。 */
export function b64FromText(text: string): string {
  return b64FromBytes(utf8Bytes(text))
}

/**
 * 字节 → \xNN 转义串：<0x20、>=0x7F 的字节写成 `\xNN`（大写十六进制），
 * 字面反斜杠写成 `\\`，其余按原字符。与 bash ANSI-C 引法 `$'…'` 的写法一致。
 */
export function escapeBytes(bytes: Uint8Array): string {
  let out = ''
  for (let i = 0; i < bytes.length; i++) {
    const b = bytes[i]
    if (b === 0x5c) out += '\\\\'
    else if (b < 0x20 || b >= 0x7f) out += '\\x' + b.toString(16).toUpperCase().padStart(2, '0')
    else out += String.fromCharCode(b)
  }
  return out
}

/**
 * \xNN 转义串 → 字节：`\xNN` 表示一个字节，`\\` 表示字面反斜杠，其余字符按 UTF-8 编码。
 * `\` 后的普通字符按字面处理；`\x` 后的十六进制不足两位时返回 badEscape。
 * bytes 同时返回尽力解析的结果，供 UI 展示。
 */
export function bytesFromEscaped(text: string): { bytes: Uint8Array; error?: HeaderEncodeError } {
  const enc = new TextEncoder()
  const out: number[] = []
  let error: HeaderEncodeError | undefined
  let i = 0
  while (i < text.length) {
    if (text[i] === '\\') {
      const next = text[i + 1]
      if (next === '\\') {
        out.push(0x5c)
        i += 2
        continue
      }
      if (next === 'x') {
        const hex = text.slice(i + 2, i + 4)
        if (/^[0-9a-fA-F]{2}$/.test(hex)) {
          out.push(parseInt(hex, 16))
          i += 4
          continue
        }
        error = error ?? 'badEscape'
      }
      out.push(0x5c)
      i += 1
      continue
    }
    // 按 Unicode 码点推进，确保代理对作为一个字符编码。
    const ch = String.fromCodePoint(text.codePointAt(i) as number)
    const bs = enc.encode(ch)
    for (let k = 0; k < bs.length; k++) out.push(bs[k])
    i += ch.length
  }
  return { bytes: Uint8Array.from(out), error }
}

/** 一行头值按其编码模式算出线字节。这是回程编码的唯一规则所在地。 */
export function encodeHeaderValue(text: string, enc: HeaderEnc): { bytes: Uint8Array; error?: HeaderEncodeError } {
  if (enc === 'bytes') return bytesFromEscaped(text)
  return { bytes: utf8Bytes(text) }
}

/**
 * 头值旁路的解析入口。b64 缺失或非法，或其合法 UTF-8 文本等于明文值时返回 null。
 */
export function decodeHeaderValue(b64: string | null | undefined, plain: string): HeaderValueBytes | null {
  if (!b64) return null
  const bytes = bytesFromB64(b64)
  if (!bytes) return null
  if (isValidUtf8(bytes) && utf8Lossy(bytes) === plain) return null
  return { bytes, b64, latin1: latin1FromBytes(bytes), escaped: escapeBytes(bytes) }
}

/** 文本模式 → 字节模式：字面反斜杠转义成 `\\`，其余字符原样（中文仍是中文）。 */
export function escapeForBytesMode(text: string): string {
  return text.replace(/\\/g, '\\\\')
}

/** 返回转义串是否包含 `\xNN` 字节写法。 */
export function hasByteEscape(text: string): boolean {
  let i = 0
  while (i < text.length) {
    if (text[i] === '\\') {
      if (text[i + 1] === '\\') {
        i += 2
        continue
      }
      if (text[i + 1] === 'x' && /^[0-9a-fA-F]{2}$/.test(text.slice(i + 2, i + 4))) return true
    }
    i += 1
  }
  return false
}

/** 字节模式 → 文本模式：`\\` 还原成字面反斜杠。 */
export function unescapeForTextMode(text: string): string {
  let out = ''
  let i = 0
  while (i < text.length) {
    if (text[i] === '\\' && text[i + 1] === '\\') {
      out += '\\'
      i += 2
      continue
    }
    out += text[i]
    i += 1
  }
  return out
}
