/**
 * 验证线缆预览与发送入参中的头值字节旁路。
 * 测试加载 wire.ts 时为 `@/i18n` 与无后缀相对 import 提供解析钩子。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import * as nodeModule from 'node:module'
import { newHeaderRow, newDraft, draftFromSeed, type Draft, type HeaderRow } from './model.ts'

interface ResolveResult {
  url: string
  shortCircuit?: boolean
}
type Resolve = (spec: string, ctx: unknown, next: (s: string, c: unknown) => ResolveResult) => ResolveResult

// registerHooks 在 @types/node 20 中尚未声明，这里补充运行时类型。
const { registerHooks } = nodeModule as unknown as { registerHooks(hooks: { resolve: Resolve }): void }

registerHooks({
  resolve(spec, ctx, next) {
    if (spec === '@/i18n') {
      return { url: 'data:text/javascript,export default { t: (k) => k }', shortCircuit: true }
    }
    return next(spec.startsWith('.') && !spec.endsWith('.ts') ? `${spec}.ts` : spec, ctx)
  },
})

const { buildWire, byteLength, headerBytesError, headerBytesNeeded, toRequestSpec, toWSSpec, wireText } =
  await import('./wire.ts')

const CD_BYTES = [0x63, 0x61, 0x66, 0xe9, 0x2e, 0x70, 0x64, 0x66]
const CD_B64 = Buffer.from(CD_BYTES).toString('base64')
const CD_LOSSY = new TextDecoder().decode(Uint8Array.from(CD_BYTES))
const CD_ESCAPED = 'caf\\xE9.pdf'

const bytesRow = (name: string, value: string): HeaderRow => ({ ...newHeaderRow(name, value), enc: 'bytes' })

function draft(kind: Draft['kind'], rows: HeaderRow[]): Draft {
  return { ...newDraft(kind), url: 'https://x.com/a', headers: rows }
}

/* ── 发送入参 ── */

// 验证干净草稿省略旁路字段。
test('干净草稿不发旁路', () => {
  const spec = toRequestSpec(draft('http', [newHeaderRow('Accept', '*/*')]))
  assert.deepEqual(spec.headers, [['Accept', '*/*']])
  assert.equal(spec.headersB64, undefined)
})

// 验证合成头占用旁路数组位置并保持逐行对齐。
test('合成头也占一个旁路位，两个数组等长对齐', () => {
  const spec = toRequestSpec(draft('sse', [bytesRow('X-File', CD_ESCAPED)]))
  assert.deepEqual(spec.headers, [
    ['X-File', CD_LOSSY],
    ['Accept', 'text/event-stream'],
    ['Cache-Control', 'no-cache'],
  ])
  assert.deepEqual(spec.headersB64, [CD_B64, '', ''])
})

// 验证蓝本携带旁路时，编辑后的请求继续使用旁路协议。
test('蓝本带过旁路时，用户删光字节行仍发旁路', () => {
  const d = draftFromSeed({
    flowId: 'f1',
    method: 'GET',
    url: 'https://x.com/a',
    headers: [
      ['X-File', CD_LOSSY],
      ['Accept', '*/*'],
    ],
    headersB64: [CD_B64, ''],
    bodySize: 0,
  })
  assert.equal(headerBytesNeeded(d), true)
  const cleaned = { ...d, headers: d.headers.filter((r) => r.name !== 'X-File') }
  assert.equal(headerBytesNeeded(cleaned), true)
  assert.deepEqual(toRequestSpec(cleaned).headersB64, [''])
})

// 验证 WebSocket 握手与 HTTP 请求共用头值旁路。
test('WS 握手共用同一条旁路', () => {
  const spec = toWSSpec(draft('ws', [bytesRow('X-File', CD_ESCAPED)]))
  assert.deepEqual(spec.headers, [['X-File', CD_LOSSY]])
  assert.deepEqual(spec.headersB64, [CD_B64])
})

// 验证非法转义返回头名，供调用方禁用发送。
test('转义写法非法时报出头名，调用方据此禁用发送', () => {
  assert.equal(headerBytesError(draft('http', [bytesRow('X-Bad', 'a\\xZZ')])), 'X-Bad')
  assert.equal(headerBytesError(draft('http', [bytesRow('X-File', CD_ESCAPED)])), '')
})

/* ── 线缆预览 ── */

// 验证预览字节数按解码后的头值计算。
test('字节数按解码后的字节算，不按转义串的长度算', () => {
  const wire = buildWire(draft('http', [bytesRow('X-File', CD_ESCAPED), newHeaderRow('Accept', '*/*')]))
  assert.deepEqual(
    wire.headers.map((h) => [h.name, h.value, h.origin, h.enc]),
    [
      ['Host', 'x.com', 'synthesized', undefined],
      ['X-File', CD_ESCAPED, 'typed', 'bytes'],
      ['Accept', '*/*', 'typed', undefined],
    ],
  )
  // 请求行、头部行和空行的字节数之和。
  assert.equal(wire.totalBytes, 63)
  assert.equal(byteLength(wireText(draft('http', [bytesRow('X-File', CD_ESCAPED), newHeaderRow('Accept', '*/*')]))), 66)
})

// 验证预览文本对字节行使用可复制的转义形态。
test('预览文本对字节行给出 \\xNN 转义', () => {
  assert.equal(
    wireText(draft('http', [bytesRow('X-File', CD_ESCAPED)])),
    'GET /a HTTP/1.1\r\nHost: x.com\r\nX-File: caf\\xE9.pdf\r\n\r\n',
  )
})

// 验证重复 Host 折叠成一行：位置与大小写取首行，值取最后一个非空。
test('重复的 Host 只出一行，值取最后一个非空', () => {
  const wire = buildWire(
    draft('http', [
      newHeaderRow('host', 'a.test'),
      newHeaderRow('Accept', '*/*'),
      newHeaderRow('Host', ''),
      newHeaderRow('HOST', 'b.test'),
    ]),
  )
  assert.deepEqual(
    wire.headers.map((h) => [h.name, h.value]),
    [
      ['host', 'b.test'],
      ['Accept', '*/*'],
    ],
  )
  assert.deepEqual(wire.overridden, ['host'])
})

// 验证全空的 Host 行退回 URL 主机。
test('Host 行全为空时退回 URL 主机', () => {
  const wire = buildWire(draft('http', [newHeaderRow('Host', ''), newHeaderRow('Host', '  ')]))
  assert.deepEqual(
    wire.headers.map((h) => [h.name, h.value]),
    [['Host', 'x.com']],
  )
})

/* ── WS 握手预览 ── */

function wsDraft(rows: HeaderRow[]): Draft {
  return { ...newDraft('ws'), url: 'wss://x.com/chat?q=1', headers: rows }
}

// 验证握手预览与 internal/app/compose_ws_test.go 断言的写线报文逐行一致：
// Host 与 User-Agent 在最前，其余按名字字节序，头名规范化，同名头保持键入顺序。
test('WS 握手预览镜像 Dialer 的写线顺序与头名', () => {
  const wire = buildWire(
    wsDraft([
      newHeaderRow('host', 'vhost.test'),
      newHeaderRow('Host', 'vhost2.test'),
      newHeaderRow('x-foo', '1'),
      newHeaderRow('X-Foo', '2'),
      newHeaderRow('authorization', 'Bearer t'),
      newHeaderRow('Keep-Alive', 'timeout=5'),
      newHeaderRow('sec-websocket-protocol', 'graphql-ws'),
      newHeaderRow('Sec-WebSocket-Accept', 'zz'),
      newHeaderRow('Cookie', 'a=b'),
      newHeaderRow('Origin', 'https://app.test'),
      newHeaderRow('Content-Length', '5'),
      newHeaderRow('Transfer-Encoding', 'chunked'),
      newHeaderRow('Trailer', 'X-T'),
    ]),
  )
  assert.equal(wire.requestLine, 'GET /chat?q=1 HTTP/1.1')
  assert.deepEqual(
    wire.headers.map((h) => [h.name, h.value]),
    [
      ['Host', 'vhost2.test'],
      ['User-Agent', 'Go-http-client/1.1'],
      ['Authorization', 'Bearer t'],
      ['Connection', 'Upgrade'],
      ['Cookie', 'a=b'],
      ['Keep-Alive', 'timeout=5'],
      ['Origin', 'https://app.test'],
      ['Sec-WebSocket-Key', 'compose.wire.wsKeyPlaceholder'],
      ['Sec-WebSocket-Protocol', 'graphql-ws'],
      ['Sec-WebSocket-Version', '13'],
      ['Sec-Websocket-Accept', 'zz'],
      ['Upgrade', 'websocket'],
      ['X-Foo', '1'],
      ['X-Foo', '2'],
    ],
  )
  assert.deepEqual(wire.overridden, ['Host'])
  assert.deepEqual(wire.dropped, ['Content-Length', 'Transfer-Encoding', 'Trailer'])
})

// 验证用户写的握手控制头不出线：四个由 Dialer 重算，Sec-WebSocket-Extensions 整行删除。
test('WS 握手控制头不按键入的值出线', () => {
  const wire = buildWire(
    wsDraft([
      newHeaderRow('Connection', 'keep-alive'),
      newHeaderRow('Upgrade', 'h2c'),
      newHeaderRow('Sec-WebSocket-Key', 'aaaaaaaaaaaaaaaaaaaaaa=='),
      newHeaderRow('Sec-WebSocket-Version', '8'),
      newHeaderRow('Sec-WebSocket-Extensions', 'permessage-deflate'),
    ]),
  )
  assert.deepEqual(
    wire.headers.map((h) => [h.name, h.value, h.origin]),
    [
      ['Host', 'x.com', 'synthesized'],
      ['User-Agent', 'Go-http-client/1.1', 'synthesized'],
      ['Connection', 'Upgrade', 'synthesized'],
      ['Sec-WebSocket-Key', 'compose.wire.wsKeyPlaceholder', 'synthesized'],
      ['Sec-WebSocket-Version', '13', 'synthesized'],
      ['Upgrade', 'websocket', 'synthesized'],
    ],
  )
  assert.deepEqual(wire.overridden, ['Connection', 'Upgrade', 'Sec-WebSocket-Key', 'Sec-WebSocket-Version'])
  assert.deepEqual(wire.dropped, ['Sec-WebSocket-Extensions'])
})

// 验证 User-Agent：键入值覆盖默认值，空值让整行不出线，多行只写第一个。
test('WS 握手的 User-Agent 由第一行决定，空值整行不出线', () => {
  const typed = buildWire(wsDraft([newHeaderRow('user-agent', 'sniffy/1')]))
  assert.deepEqual(typed.headers[1], { name: 'User-Agent', value: 'sniffy/1', origin: 'typed', enc: undefined })

  const blank = buildWire(wsDraft([newHeaderRow('User-Agent', '')]))
  assert.equal(
    blank.headers.some((h) => h.name === 'User-Agent'),
    false,
  )

  const twice = buildWire(wsDraft([newHeaderRow('User-Agent', 'first'), newHeaderRow('User-Agent', 'second')]))
  assert.equal(twice.headers.filter((h) => h.name === 'User-Agent').length, 1)
  assert.equal(twice.headers[1].value, 'first')
  assert.deepEqual(twice.overridden, ['User-Agent'])
})
