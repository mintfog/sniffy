import test from 'node:test'
import assert from 'node:assert/strict'
import {
  bodyEditable,
  buildResumePatch,
  changedRows,
  decodeBody,
  draftFrom,
  MAX_EDITABLE_BODY,
  pairsFrom,
  parsePausedFlow,
  rowsFrom,
  type PausedFlow,
} from './model.ts'
import { newHeaderRow, type HeaderRow } from '../compose/model.ts'

const b64 = (s: string) => Buffer.from(s, 'utf8').toString('base64')

function hit(over: Record<string, unknown> = {}) {
  return {
    id: 'f1',
    pausedAt: 'request',
    pausedUntil: '2026-08-26T10:00:00Z',
    request: { method: 'POST', url: 'https://x.com/a', host: 'x.com', body: b64('{"a":1}') },
    requestHeaders: [
      ['Host', 'x.com'],
      ['content-type', 'application/json'],
    ],
    ...over,
  }
}

function paused(over: Record<string, unknown> = {}): PausedFlow {
  const p = parsePausedFlow(hit(over))
  assert.ok(p)
  return p
}

/* ── 解析 ── */

test('畸形载荷一律返回 null，不拿半个对象去渲染编辑器', () => {
  for (const bad of [null, undefined, 42, 'x', {}, { request: {} }]) {
    assert.equal(parsePausedFlow(bad), null)
  }
})

test('解析出阶段、有序头与截止时刻', () => {
  const p = paused()
  assert.equal(p.phase, 'request')
  assert.equal(p.method, 'POST')
  assert.equal(p.host, 'x.com')
  assert.deepEqual(p.requestHeaders[0], ['Host', 'x.com'])
  assert.equal(p.pausedUntil, Date.parse('2026-08-26T10:00:00Z'))
  assert.equal(p.requestBody.text, '{"a":1}')
})

test('没有 pausedAt 时按有无 response 推断阶段', () => {
  const p = parsePausedFlow({ ...hit(), pausedAt: undefined, response: { status: 200 } })
  assert.equal(p?.phase, 'response')
  assert.equal(p?.status, 200)
})

test('截止时刻缺失或不可解析时归零，界面据此不显示倒计时', () => {
  assert.equal(paused({ pausedUntil: undefined }).pausedUntil, 0)
  assert.equal(paused({ pausedUntil: '不是时间' }).pausedUntil, 0)
})

/* ── base64 与可编辑判定 ── */

test('UTF-8 正文往返，中文与 emoji 都不损坏', () => {
  const body = decodeBody(b64('中文 🎉 ok'))
  assert.equal(body.text, '中文 🎉 ok')
  assert.equal(body.binary, false)
  assert.equal(body.size, Buffer.byteLength('中文 🎉 ok'))
})

test('非 UTF-8 字节流认作二进制，不进文本编辑器', () => {
  const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]).toString('base64')
  const body = decodeBody(png)
  assert.equal(body.binary, true)
  assert.equal(body.text, '')
  assert.equal(body.base64, png, '原始字节要留着，放行时原样保留')
})

test('超大正文不解码，直接标 tooLarge', () => {
  const body = decodeBody('A'.repeat(Math.ceil(((MAX_EDITABLE_BODY + 1024) * 4) / 3)))
  assert.equal(body.tooLarge, true)
  assert.equal(body.text, '')
})

test('流式与透传响应的正文不可编辑：暂停时它根本不在内存里', () => {
  const base = { pausedAt: 'response', response: { status: 200, body: b64('ok') } }
  assert.equal(bodyEditable(paused({ ...base })), true)
  assert.equal(bodyEditable(paused({ ...base, metadata: { stream: 'sse' } })), false)
  assert.equal(bodyEditable(paused({ ...base, metadata: { passthrough: true } })), false)
})

/* ── 头行 ── */

test('头行往返：尾部留一行空行，回传时被丢掉', () => {
  const rows = rowsFrom([
    ['A', '1'],
    ['B', '2'],
  ])
  assert.equal(rows.length, 3, '尾部空行是「在这里开始打字即新增一条」的入口')
  assert.deepEqual(pairsFrom(rows), [
    ['A', '1'],
    ['B', '2'],
  ])
})

test('无名头不回传：写到线上就是一条空的 ": value"', () => {
  const rows = rowsFrom([['', 'orphan']])
  assert.deepEqual(pairsFrom(rows), [])
})

test('改动条按位置比对', () => {
  const base: [string, string][] = [
    ['A', '1'],
    ['B', '2'],
  ]
  const rows = rowsFrom(base)
  assert.equal(changedRows(rows, base).size, 0)
  rows[0] = { ...rows[0], value: '9' }
  assert.deepEqual([...changedRows(rows, base)], [rows[0].id])
})

/* ── 补丁 ── */

test('一处没改就返回 null，调用方据此走原样放行', () => {
  const p = paused()
  assert.equal(buildResumePatch(draftFrom(p), p), null)
})

test('只改 body 时不回传头部：头是替换语义，多送一份就多一次出错机会', () => {
  const p = paused()
  const patch = buildResumePatch({ ...draftFrom(p), requestBody: '{"a":2}' }, p)
  assert.deepEqual(patch, { request: { body: '{"a":2}' } })
})

test('删掉一条头即整份回传，缺席的那条由后端按替换语义删除', () => {
  const p = paused()
  const d = draftFrom(p)
  const patch = buildResumePatch({ ...d, requestHeaders: d.requestHeaders.filter((r) => r.name !== 'content-type') }, p)
  assert.deepEqual(patch, { request: { headers: [['Host', 'x.com']] } })
})

test('二进制正文永不回传，原体因此在后端原封不动', () => {
  const p = paused({
    request: { method: 'POST', url: 'https://x.com/a', body: Buffer.from([0xff, 0xfe]).toString('base64') },
  })
  assert.equal(p.requestBody.binary, true)
  const patch = buildResumePatch({ ...draftFrom(p), requestBody: '被误改的文本' }, p)
  assert.equal(patch, null)
})

test('响应阶段只产出 response：请求早已发出，改了也不生效', () => {
  const p = paused({ pausedAt: 'response', response: { status: 200, statusText: '200 OK', body: b64('ok') } })
  const patch = buildResumePatch({ ...draftFrom(p), url: 'https://other.com/', responseBody: 'edited' }, p)
  assert.deepEqual(patch, { response: { body: 'edited' } })
})

test('只改状态码时不带 statusText，否则线上会拼出 "404 200 OK"', () => {
  const p = paused({ pausedAt: 'response', response: { status: 200, statusText: '200 OK' } })
  const patch = buildResumePatch({ ...draftFrom(p), status: '404' }, p)
  assert.deepEqual(patch, { response: { status: 404 } })
})

test('用户自己改了状态文本则如实回传', () => {
  const p = paused({ pausedAt: 'response', response: { status: 200, statusText: '200 OK' } })
  const patch = buildResumePatch({ ...draftFrom(p), status: '404', statusText: 'Gone Fishing' }, p)
  assert.deepEqual(patch, { response: { status: 404, statusText: 'Gone Fishing' } })
})

test('状态码输入框的中间态（空串 / 非数字）不产出补丁', () => {
  const p = paused({ pausedAt: 'response', response: { status: 200, statusText: '200 OK' } })
  assert.equal(buildResumePatch({ ...draftFrom(p), status: '' }, p), null)
  assert.equal(buildResumePatch({ ...draftFrom(p), status: 'abc' }, p), null)
})

test('URL 与方法只在非空且真的改过时回传', () => {
  const p = paused()
  assert.equal(buildResumePatch({ ...draftFrom(p), url: '   ' }, p), null)
  assert.deepEqual(buildResumePatch({ ...draftFrom(p), url: 'https://y.com/b' }, p), {
    request: { url: 'https://y.com/b' },
  })
})

/* ── 头值字节旁路 ── */

// Content-Disposition 的 Latin-1 文件名示例，包含非法 UTF-8 字节 0xE9。
const CD_BYTES = [0x61, 0x74, 0x74, 0x3b, 0x20, 0x66, 0x3d, 0x22, 0x63, 0x61, 0x66, 0xe9, 0x2e, 0x70, 0x64, 0x66, 0x22]
const CD_B64 = Buffer.from(CD_BYTES).toString('base64')
/** 明文槽对应的 UTF-8 有损形态。 */
const CD_LOSSY = new TextDecoder().decode(Uint8Array.from(CD_BYTES))
const CD_ESCAPED = 'att; f="caf\\xE9.pdf"'

function bytesPaused(over: Record<string, unknown> = {}): PausedFlow {
  return paused({
    requestHeaders: [
      ['Host', 'x.com'],
      ['Content-Disposition', CD_LOSSY],
      ['Accept', '*/*'],
    ],
    requestHeadersB64: ['', CD_B64, ''],
    ...over,
  })
}

// 验证带旁路的行以字节模式打开并显示转义。
test('含旁路的头解析成按字节编辑的行，值格是 \\xNN 转义', () => {
  const d = draftFrom(bytesPaused())
  assert.equal(d.requestHeaders[1].enc, 'bytes')
  assert.equal(d.requestHeaders[1].value, CD_ESCAPED)
  assert.equal(d.requestHeaders[0].enc, undefined, '干净行不进字节模式')
})

// 验证改动比对使用出线字节，原样打开保持未改动。
test('原样打开不算改动，也不产出补丁', () => {
  const p = bytesPaused()
  const d = draftFrom(p)
  assert.equal(changedRows(d.requestHeaders, p.requestHeaders, p.requestHeadersB64).size, 0)
  assert.equal(buildResumePatch(d, p), null)
})

// 验证头部与旁路数组由同一行列表生成并保持对齐。
test('只改一行时其余行的字节原样带回，两个数组逐位对齐', () => {
  const p = bytesPaused()
  const d = draftFrom(p)
  const rows = d.requestHeaders.map((r) => (r.name === 'Accept' ? { ...r, value: 'application/json' } : r))
  assert.deepEqual(buildResumePatch({ ...d, requestHeaders: rows }, p), {
    request: {
      headers: [
        ['Host', 'x.com'],
        ['Content-Disposition', CD_LOSSY],
        ['Accept', 'application/json'],
      ],
      headersB64: ['', CD_B64, ''],
    },
  })
})

// 验证编辑后的字节行按转义生成新字节及明文槽。
test('改了字节行即按用户写的转义出线', () => {
  const p = bytesPaused()
  const d = draftFrom(p)
  const rows = d.requestHeaders.map((r, i) => (i === 1 ? { ...r, value: 'x\\xFF' } : r))
  const patch = buildResumePatch({ ...d, requestHeaders: rows }, p)
  assert.deepEqual(patch?.request?.headers?.[1], ['Content-Disposition', 'x\uFFFD'])
  assert.equal(patch?.request?.headersB64?.[1], Buffer.from([0x78, 0xff]).toString('base64'))
})

// 验证切回文本模式后该行使用明文值。
test('切回文本模式的行不带旁路项，值以明文为准', () => {
  const p = bytesPaused()
  const d = draftFrom(p)
  const rows = d.requestHeaders.map((r, i) => (i === 1 ? { ...r, enc: undefined, value: 'inline' } : r))
  const patch = buildResumePatch({ ...d, requestHeaders: rows }, p)
  assert.deepEqual(patch?.request?.headersB64, ['', '', ''])
  assert.deepEqual(patch?.request?.headers?.[1], ['Content-Disposition', 'inline'])
})

// 验证蓝本携带旁路时，草稿始终保留旁路协议。
test('删掉唯一的字节行后仍带旁路，且其余行不被标成改动', () => {
  const p = bytesPaused()
  const d = draftFrom(p)
  const rows = d.requestHeaders.filter((r) => r.name !== 'Content-Disposition')
  const patch = buildResumePatch({ ...d, requestHeaders: rows }, p)
  assert.deepEqual(patch?.request?.headers, [
    ['Host', 'x.com'],
    ['Accept', '*/*'],
  ])
  assert.deepEqual(patch?.request?.headersB64, ['', ''])
  assert.equal(changedRows(rows, p.requestHeaders, p.requestHeadersB64).size, 0)
})

// 验证插入与重排后旁路仍与对应头行对齐。
test('插入与重排后旁路仍与头逐位对齐', () => {
  const p = bytesPaused()
  const d = draftFrom(p)
  const [host, cd, accept] = d.requestHeaders
  const rows: HeaderRow[] = [accept, newHeaderRow('X-New', 'v'), cd, host]
  const patch = buildResumePatch({ ...d, requestHeaders: rows }, p)
  assert.deepEqual(patch?.request?.headers, [
    ['Accept', '*/*'],
    ['X-New', 'v'],
    ['Content-Disposition', CD_LOSSY],
    ['Host', 'x.com'],
  ])
  assert.deepEqual(patch?.request?.headersB64, ['', '', CD_B64, ''])
})

// 验证旁路长度不匹配时解析、展示和回程均回退到文本路径。
test('旁路长度与头对不上时整条丢弃，展示与回程看同一份数据', () => {
  const p = bytesPaused({ requestHeadersB64: [CD_B64] })
  assert.deepEqual(p.requestHeadersB64, [])
  const d = draftFrom(p)
  assert.equal(d.requestHeaders[1].enc, undefined)
  assert.equal(d.requestHeaders[1].value, CD_LOSSY)
  const rows = d.requestHeaders.map((r) => (r.name === 'Accept' ? { ...r, value: 'application/json' } : r))
  assert.equal(buildResumePatch({ ...d, requestHeaders: rows }, p)?.request?.headersB64, undefined)
})

// 验证缺少旁路字段时沿用文本路径。
test('后端不发旁路时优雅降级，补丁也不带旁路', () => {
  const p = paused()
  assert.deepEqual(p.requestHeadersB64, [])
  const d = draftFrom(p)
  const rows = d.requestHeaders.map((r) => (r.name === 'content-type' ? { ...r, value: 'text/plain' } : r))
  assert.deepEqual(buildResumePatch({ ...d, requestHeaders: rows }, p), {
    request: {
      headers: [
        ['Host', 'x.com'],
        ['content-type', 'text/plain'],
      ],
    },
  })
})

// 验证响应阶段使用独立的头部旁路字段。
test('响应阶段的头同样走旁路，原样打开不产出补丁', () => {
  const p = paused({
    pausedAt: 'response',
    response: { status: 200, statusText: '200 OK' },
    responseHeaders: [['Content-Disposition', CD_LOSSY]],
    responseHeadersB64: [CD_B64],
  })
  const d = draftFrom(p)
  assert.equal(d.responseHeaders[0].value, CD_ESCAPED)
  assert.equal(buildResumePatch(d, p), null)
  const rows = d.responseHeaders.map((r, i) => (i === 0 ? { ...r, name: 'X-File' } : r))
  assert.deepEqual(buildResumePatch({ ...d, responseHeaders: rows }, p)?.response?.headersB64, [CD_B64])
})
