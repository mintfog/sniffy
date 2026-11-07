import test from 'node:test'
import assert from 'node:assert/strict'
import type { TrafficRow } from './types.ts'
import { isFilterEmpty, matchFilter, toggleFilterChip, type FilterSelection } from './trafficFilter.ts'

function row(over: Partial<TrafficRow>): TrafficRow {
  return {
    id: '1',
    seq: 1,
    kind: 'http',
    method: 'GET',
    scheme: 'https',
    host: 'example.com',
    path: '/',
    url: 'https://example.com/',
    state: 'completed',
    contentType: '',
    contentKind: 'other',
    startedAt: 0,
    ...over,
  }
}

test('同组芯片互斥替换，再点一次取消，all 清空', () => {
  let sel: FilterSelection = {}
  sel = toggleFilterChip(sel, 'https')
  sel = toggleFilterChip(sel, 'json')
  sel = toggleFilterChip(sel, '2xx')
  assert.deepEqual(sel, { protocol: 'https', content: 'json', status: '2xx' })

  sel = toggleFilterChip(sel, 'http')
  assert.equal(sel.protocol, 'http')

  sel = toggleFilterChip(sel, 'json')
  assert.equal(sel.content, undefined)

  assert.ok(isFilterEmpty(toggleFilterChip(sel, 'all')))
})

test('跨组按与叠加', () => {
  const r = row({ scheme: 'https', resHttpVersion: 'HTTP/2.0', contentKind: 'json', status: 200 })
  assert.ok(matchFilter(r, { protocol: 'https', version: 'h2', content: 'json', status: '2xx' }))
  assert.ok(!matchFilter(r, { protocol: 'https', status: '4xx' }))
  assert.ok(!matchFilter(r, { version: 'h1' }))
})

test('未拿到响应时按客户端侧 HTTP 版本归类', () => {
  assert.ok(matchFilter(row({ reqHttpVersion: 'HTTP/1.1', state: 'pending' }), { version: 'h1' }))
})

test('XML 从 text 中拆出', () => {
  assert.ok(matchFilter(row({ contentKind: 'text', contentType: 'application/xml' }), { content: 'xml' }))
  assert.ok(matchFilter(row({ contentKind: 'text', path: '/feed.xml?v=1' }), { content: 'xml' }))
  assert.ok(matchFilter(row({ contentKind: 'text', contentType: 'text/plain' }), { content: 'text' }))
  assert.ok(!matchFilter(row({ contentKind: 'text', contentType: 'text/xml' }), { content: 'text' }))
})

test('错误同时命中 4xx/5xx 与拦截、失败', () => {
  assert.ok(matchFilter(row({ status: 503 }), { status: 'err' }))
  assert.ok(matchFilter(row({ status: 503 }), { status: '5xx' }))
  assert.ok(matchFilter(row({ blocked: true }), { status: 'err' }))
  assert.ok(matchFilter(row({ state: 'error' }), { status: 'err' }))
  assert.ok(!matchFilter(row({ status: 302 }), { status: 'err' }))
})

test('媒体与二进制按内容大类合并', () => {
  assert.ok(matchFilter(row({ contentKind: 'video' }), { content: 'media' }))
  assert.ok(matchFilter(row({ contentKind: 'audio' }), { content: 'media' }))
  assert.ok(matchFilter(row({ contentKind: 'font' }), { content: 'binary' }))
  assert.ok(!matchFilter(row({ contentKind: 'form' }), { content: 'binary' }))
})

test('WebSocket 行归入 WS 而非 HTTPS', () => {
  const ws = row({ kind: 'ws', scheme: 'wss', contentKind: 'stream', status: 101 })
  assert.ok(matchFilter(ws, { protocol: 'ws', status: '1xx' }))
  assert.ok(!matchFilter(ws, { protocol: 'https' }))
})
