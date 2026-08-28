import test from 'node:test'
import assert from 'node:assert/strict'
import {
  b64FromBytes,
  b64FromText,
  bytesFromB64,
  bytesFromEscaped,
  decodeHeaderValue,
  encodeHeaderValue,
  escapeBytes,
  escapeForBytesMode,
  hasByteEscape,
  isValidUtf8,
  latin1FromBytes,
  unescapeForTextMode,
  utf8Bytes,
  utf8Lossy,
} from './headerBytes.ts'

const bytes = (...b: number[]) => Uint8Array.from(b)
const b64 = (...b: number[]) => Buffer.from(b).toString('base64')

/* ── base64 ── */

test('base64 与字节互为逆运算', () => {
  const raw = bytes(0x63, 0x61, 0x66, 0xe9, 0x2e, 0x70, 0x64, 0x66)
  const encoded = b64FromBytes(raw)
  assert.equal(encoded, 'Y2Fm6S5wZGY=')
  assert.deepEqual(bytesFromB64(encoded), raw)
})

test('非标准 base64 一律返回 null，调用方据此降级', () => {
  for (const bad of ['', 'Y2Fm6S5wZGY', 'Y2Fm-S5wZGY=', '****', 'Y2Fm6S5wZGY==', '你好']) {
    assert.equal(bytesFromB64(bad), null, bad)
  }
})

/* ── UTF-8 判定与有损解码 ── */

test('裸 0xE9 不是合法 UTF-8，解码后成 U+FFFD', () => {
  const raw = bytes(0x63, 0x61, 0x66, 0xe9)
  assert.equal(isValidUtf8(raw), false)
  assert.equal(utf8Lossy(raw), 'caf\uFFFD')
  assert.equal(latin1FromBytes(raw), 'café')
})

test('合法 UTF-8 的 é 与裸字节 0xE9 在 Latin-1 下同形、在字节层不同', () => {
  const utf8 = utf8Bytes('café')
  assert.equal(isValidUtf8(utf8), true)
  assert.equal(latin1FromBytes(utf8), 'cafÃ©')
  assert.equal(b64FromText('café'), b64FromBytes(utf8))
})

/* ── \xNN 转义 ── */

test('转义只碰不可打印字节、高位字节与字面反斜杠', () => {
  assert.equal(escapeBytes(bytes(0x63, 0xe9, 0x5c, 0x20, 0x7f, 0x0d, 0x0a)), 'c\\xE9\\\\ \\x7F\\x0D\\x0A')
  assert.equal(escapeBytes(utf8Bytes('C:/路径')), 'C:/\\xE8\\xB7\\xAF\\xE5\\xBE\\x84')
})

test('转义 → 字节 → 转义 往返稳定', () => {
  for (let i = 0; i < 256; i++) {
    const raw = bytes(i)
    const esc = escapeBytes(raw)
    const back = bytesFromEscaped(esc)
    assert.equal(back.error, undefined, esc)
    assert.deepEqual(back.bytes, raw, esc)
  }
})

test('转义串里的普通字符按 UTF-8 编码，中文可与 \\xNN 混写', () => {
  const r = bytesFromEscaped('中\\xE9文')
  assert.equal(r.error, undefined)
  assert.deepEqual(r.bytes, Uint8Array.from([...utf8Bytes('中'), 0xe9, ...utf8Bytes('文')]))
})

test('星际平面字符按码点整体编码，不被拆成两个代理', () => {
  const r = bytesFromEscaped('😀')
  assert.deepEqual(r.bytes, utf8Bytes('😀'))
})

test('孤立反斜杠按字面处理，Windows 路径无需转义即可写', () => {
  const r = bytesFromEscaped('C:\\path\\to')
  assert.equal(r.error, undefined)
  assert.deepEqual(r.bytes, utf8Bytes('C:\\path\\to'))
})

test('\\x 后不足两位十六进制判为 badEscape', () => {
  for (const bad of ['a\\x', 'a\\xZZ', 'a\\x9', '\\xg1']) {
    assert.equal(bytesFromEscaped(bad).error, 'badEscape', bad)
  }
})

test('小写十六进制可读入，输出统一为大写', () => {
  const r = bytesFromEscaped('\\xe9')
  assert.deepEqual(r.bytes, bytes(0xe9))
  assert.equal(escapeBytes(r.bytes), '\\xE9')
})

/* ── 编码模式 ── */

test('text 模式按 UTF-8 出线，bytes 模式按转义规则出线', () => {
  assert.deepEqual(encodeHeaderValue('caf\\xE9', 'text').bytes, utf8Bytes('caf\\xE9'))
  assert.deepEqual(encodeHeaderValue('caf\\xE9', 'bytes').bytes, bytes(0x63, 0x61, 0x66, 0xe9))
})

test('模式切换：text→bytes 只转义反斜杠，bytes→text 仅在无 \\xNN 时无损', () => {
  assert.equal(escapeForBytesMode('C:\\path 中文'), 'C:\\\\path 中文')
  assert.equal(hasByteEscape('C:\\\\path'), false)
  assert.equal(hasByteEscape('caf\\xE9'), true)
  assert.equal(unescapeForTextMode('C:\\\\path'), 'C:\\path')
  assert.equal(unescapeForTextMode(escapeForBytesMode('a\\b')), 'a\\b')
})

/* ── 旁路解析 ── */

test('旁路解出非法 UTF-8 字节时给出两种渲染', () => {
  const v = decodeHeaderValue(b64(0x63, 0x61, 0x66, 0xe9), 'caf\uFFFD')
  assert.ok(v)
  assert.equal(v.latin1, 'café')
  assert.equal(v.escaped, 'caf\\xE9')
  assert.deepEqual(v.bytes, bytes(0x63, 0x61, 0x66, 0xe9))
})

test('旁路缺失或非法时视同没有旁路，不抛', () => {
  assert.equal(decodeHeaderValue(undefined, 'x'), null)
  assert.equal(decodeHeaderValue('', 'x'), null)
  assert.equal(decodeHeaderValue('!!!not base64!!!', 'x'), null)
})

test('旁路字节是合法 UTF-8 且与明文相同时视同没有旁路，不给干净行挂徽标', () => {
  assert.equal(decodeHeaderValue(b64FromText('application/json'), 'application/json'), null)
  assert.ok(decodeHeaderValue(b64FromText('café'), 'cafe'))
})

/* ── 全字节与长值 ── */

// 验证 0x00-0xFF 全字节经 base64 与 Latin-1 往返保持一致。
test('全字节 0x00-0xFF 经 base64 与 Latin-1 双向往返无损', () => {
  const all = Uint8Array.from({ length: 256 }, (_, i) => i)
  assert.deepEqual(bytesFromB64(b64FromBytes(all)), all)
  const shown = latin1FromBytes(all)
  assert.equal(shown.length, 256)
  for (let i = 0; i < 256; i++) assert.equal(shown.charCodeAt(i), i)
})

// 验证分块拼接边界下的长头值字节数保持一致。
test('超过分块阈值的长值不丢字节', () => {
  const long = Uint8Array.from({ length: 0x8000 * 2 + 7 }, (_, i) => i % 256)
  assert.deepEqual(bytesFromB64(b64FromBytes(long)), long)
  assert.equal(latin1FromBytes(long).length, long.length)
})

/* ── 合法 UTF-8 判定 ── */

// 验证中文、emoji 和合法 UTF-8 文本不触发字节旁路。
test('合法 UTF-8 的中文与 emoji 不被判成需要旁路', () => {
  for (const v of ['文件名.pdf', '报表😀.xlsx', 'café', 'application/json']) {
    assert.equal(decodeHeaderValue(b64FromText(v), v), null, v)
  }
})

// 验证明文槽的 utf8Lossy 形态与旁路并存时仍识别为字节行。
test('明文槽是 U+FFFD 形态时旁路照常生效', () => {
  const raw = bytes(0x63, 0x61, 0x66, 0xe9)
  const v = decodeHeaderValue(b64FromBytes(raw), utf8Lossy(raw))
  assert.ok(v)
  assert.deepEqual(v.bytes, raw)
})

// 验证视觉上同形的 Latin-1 与 UTF-8 文本在字节层保持区分。
test('屏幕上同形的 é 在字节层不相等', () => {
  assert.equal(latin1FromBytes(bytes(0xe9)), 'é')
  assert.equal(utf8Lossy(utf8Bytes('é')), 'é')
  assert.notEqual(b64FromBytes(bytes(0xe9)), b64FromText('é'))
  assert.equal(escapeBytes(utf8Bytes('é')), '\\xC3\\xA9')
})
