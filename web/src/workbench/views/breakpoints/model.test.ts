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
