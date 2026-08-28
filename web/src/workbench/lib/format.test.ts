/**
 * 验证展示层的 headerEntries、headerEntriesWithBytes 与 getHeader 字节旁路。
 * 原始报文、复制、HAR/JSON 导出和 Cookie 解析共用这些函数。
 * 测试加载 format.ts 时为 `@/i18n` 与无后缀相对 import 提供解析钩子。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import * as nodeModule from 'node:module'
import type { TrafficRow } from './types.ts'

interface ResolveResult {
  url: string
  shortCircuit?: boolean
}
type Resolve = (spec: string, ctx: unknown, next: (s: string, c: unknown) => ResolveResult) => ResolveResult

// registerHooks 在 @types/node 20 中尚未声明，这里补充运行时类型。
const { registerHooks } = nodeModule as unknown as { registerHooks(hooks: { resolve: Resolve }): void }

const SRC = new URL('../../', import.meta.url)

registerHooks({
  resolve(spec, ctx, next) {
    if (spec === '@/i18n') {
      return { url: 'data:text/javascript,export default { t: (k) => k }', shortCircuit: true }
    }
    if (spec.startsWith('@/')) return next(new URL(`${spec.slice(2)}.ts`, SRC).href, ctx)
    return next(spec.startsWith('.') && !spec.endsWith('.ts') ? `${spec}.ts` : spec, ctx)
  },
})

const { getHeader, headerEntries, headerEntriesWithBytes } = await import('./format.ts')
const { buildCurl, headersToText } = await import('./clipboard.ts')

const CD_BYTES = [0x61, 0x74, 0x74, 0x3b, 0x20, 0x66, 0x3d, 0x22, 0x63, 0x61, 0x66, 0xe9, 0x2e, 0x70, 0x64, 0x66, 0x22]
const CD_B64 = Buffer.from(CD_BYTES).toString('base64')
const CD_LOSSY = new TextDecoder().decode(Uint8Array.from(CD_BYTES))
const CD_LATIN1 = 'att; f="café.pdf"'

const HEADERS = { 'Content-Disposition': CD_LOSSY, Accept: '*/*' }
const SIDECAR = { 'Content-Disposition': CD_B64 }

function row(): TrafficRow {
  return {
    id: 'f1',
    seq: 1,
    kind: 'http',
    method: 'GET',
    scheme: 'https',
    host: 'x.com',
    path: '/a',
    url: 'https://x.com/a',
    state: 'completed',
    contentType: '',
    contentKind: 'other',
    startedAt: 0,
    reqHeaders: HEADERS,
    reqHeadersB64: SIDECAR,
  }
}

// 验证带旁路的头值按 Latin-1 逐字节渲染。
test('带旁路的头值按 Latin-1 渲染，不再是 U+FFFD', () => {
  assert.deepEqual(headerEntries(HEADERS, SIDECAR), [
    ['Content-Disposition', CD_LATIN1],
    ['Accept', '*/*'],
  ])
  assert.equal(getHeader(HEADERS, 'content-disposition', SIDECAR), CD_LATIN1)
})

// 验证未携带旁路的键保留明文值。
test('没有旁路的键原样输出', () => {
  assert.deepEqual(headerEntries(HEADERS), [
    ['Content-Disposition', CD_LOSSY],
    ['Accept', '*/*'],
  ])
  assert.deepEqual(headerEntries(), [])
})

// 验证字节行提供转义视图，干净行的 alt 为空。
test('只有含字节的行给出 \\xNN 转义，干净行的 alt 为空', () => {
  const { rows, alt } = headerEntriesWithBytes(HEADERS, SIDECAR)
  assert.deepEqual(rows[0], ['Content-Disposition', CD_LATIN1])
  assert.equal(alt[0], 'att; f="caf\\xE9.pdf"')
  assert.equal(alt[1], undefined)
})

// 验证非法旁路或键不匹配时退回明文。
test('旁路非法或键对不上时静默退回明文', () => {
  assert.deepEqual(headerEntries(HEADERS, { 'Content-Disposition': '!!not base64!!' })[0], [
    'Content-Disposition',
    CD_LOSSY,
  ])
  assert.equal(headerEntriesWithBytes(HEADERS, { 'X-Other': CD_B64 }).alt[0], undefined)
})

// 验证复制头部与 cURL 使用相同的 Latin-1 渲染。
test('复制头部与复制为 cURL 继承 Latin-1 渲染', () => {
  assert.equal(headersToText(HEADERS, SIDECAR), `Content-Disposition: ${CD_LATIN1}\nAccept: */*`)
  assert.ok(buildCurl(row()).includes(`-H 'Content-Disposition: ${CD_LATIN1}'`))
})
