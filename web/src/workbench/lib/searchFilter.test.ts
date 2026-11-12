import test from 'node:test'
import assert from 'node:assert/strict'
import type { TrafficRow } from './types.ts'
import {
  compileSearch,
  globMatch,
  newCondition,
  valueOptions,
  withField,
  type SearchCondition,
  type SearchField,
  type SearchOp,
} from './searchFilter.ts'

function row(over: Partial<TrafficRow>): TrafficRow {
  return {
    id: '1',
    seq: 1,
    kind: 'http',
    method: 'GET',
    scheme: 'https',
    host: 'api.example.com',
    path: '/v1/users?id=7',
    url: 'https://api.example.com/v1/users?id=7',
    state: 'completed',
    status: 200,
    contentType: 'application/json',
    contentKind: 'json',
    startedAt: 0,
    ...over,
  }
}

function cond(field: SearchField, op: SearchOp, value: string): SearchCondition {
  return { ...newCondition(), field, op, value }
}

function matches(r: TrafficRow, conds: SearchCondition[], logic: 'and' | 'or' = 'and', marks = {}): boolean {
  const { filter } = compileSearch(conds, logic)
  return filter ? filter([r], marks).length === 1 : true
}

test('空值条件与写错的正则不构成筛选；只有标记色条件才依赖标记', () => {
  const empty = [newCondition(), cond('url', 'contains', '  ')]
  assert.equal(compileSearch(empty, 'and').filter, null)
  assert.equal(compileSearch([cond('url', 'regex', '(')], 'and').filter, null)
  assert.ok(compileSearch([cond('url', 'contains', 'x')], 'and').filter)
  assert.ok(!compileSearch([cond('url', 'contains', 'x')], 'and').usesMarks)
  const mark = compileSearch([withField(newCondition(), 'mark')], 'and')
  assert.ok(mark.filter && mark.usesMarks)
})

test('文本匹配方式不区分大小写', () => {
  const r = row({})
  assert.ok(matches(r, [cond('host', 'contains', 'EXAMPLE')]))
  assert.ok(!matches(r, [cond('host', 'notContains', 'example')]))
  assert.ok(matches(r, [cond('method', 'equals', 'get')]))
  assert.ok(!matches(r, [cond('host', 'equals', 'example.com')]))
  assert.ok(matches(r, [cond('host', 'notEquals', 'example.com')]))
  assert.ok(matches(r, [cond('path', 'startsWith', '/v1/')]))
  assert.ok(matches(r, [cond('url', 'endsWith', 'id=7')]))
})

test('通配符整串匹配，? 匹配单字符，元字符按字面', () => {
  const r = row({})
  assert.ok(matches(r, [cond('host', 'wildcard', '*.example.com')]))
  assert.ok(!matches(r, [cond('host', 'wildcard', '*.example')]))
  assert.ok(matches(r, [cond('path', 'wildcard', '/v?/users?id=7')]))
  assert.ok(matches(r, [cond('host', 'notWildcard', '*.github.com')]))
})

test('正则匹配；写错的正则标为无效且不参与过滤', () => {
  const r = row({ status: 503 })
  assert.ok(matches(r, [cond('status', 'regex', '^50[23]$')]))
  assert.ok(!matches(r, [cond('status', 'notRegex', '^5')]))

  const bad = cond('url', 'regex', '(')
  const res = compileSearch([bad, cond('host', 'contains', 'nomatch')], 'and')
  assert.deepEqual([...res.invalid], [bad.id])
  assert.ok(res.filter && res.filter([r], {}).length === 0)
})

test('字段缺失时正向不命中、取反命中', () => {
  const r = row({ status: undefined, process: undefined, resBody: undefined })
  assert.ok(!matches(r, [cond('status', 'contains', '2')]))
  assert.ok(matches(r, [cond('process', 'notContains', 'chrome')]))
  assert.ok(matches(r, [cond('resBody', 'notEquals', 'x')]))
})

test('头部逐行匹配「名: 值」，请求类型取 Content-Type 不分大小写', () => {
  const r = row({
    reqHeaders: { 'Content-Type': 'application/x-www-form-urlencoded', Authorization: 'Bearer abc' },
    resHeaders: { 'set-cookie': 'sid=1' },
  })
  assert.ok(matches(r, [cond('reqHeader', 'equals', 'authorization: bearer abc')]))
  assert.ok(matches(r, [cond('reqHeader', 'startsWith', 'authorization:')]))
  assert.ok(!matches(r, [cond('resHeader', 'contains', 'authorization')]))
  assert.ok(matches(r, [cond('reqType', 'contains', 'form')]))
  assert.ok(matches(r, [cond('resType', 'equals', 'application/json')]))
})

test('带字节旁路的头值按 Latin-1 还原后匹配，与详情页展示一致', () => {
  // 0xE9 不是合法 UTF-8，JSON 里的文本值已被规范化成 U+FFFD
  const r = row({
    resHeaders: { 'Content-Disposition': 'attachment; filename="caf\uFFFD.txt"' },
    resHeadersB64: { 'Content-Disposition': btoa('attachment; filename="caf\xE9.txt"') },
  })
  assert.ok(matches(r, [cond('resHeader', 'contains', 'café.txt')]))
  assert.ok(!matches(r, [cond('resHeader', 'contains', '\uFFFD')]))
})

test('「全部」覆盖 URL、方法、状态码、类型、进程与头部，不含正文', () => {
  const r = row({ method: 'POST', process: 'Chrome', reqHeaders: { 'x-trace': 'abc123' }, resBody: 'secret-token' })
  assert.ok(matches(r, [cond('all', 'contains', 'example')]))
  assert.ok(matches(r, [cond('all', 'equals', 'api.example.com')]))
  assert.ok(matches(r, [cond('all', 'wildcard', '*.example.com')]))
  assert.ok(matches(r, [cond('all', 'equals', 'post')]))
  assert.ok(matches(r, [cond('all', 'equals', '200')]))
  assert.ok(matches(r, [cond('all', 'contains', 'chrome')]))
  assert.ok(matches(r, [cond('all', 'contains', 'abc123')]))
  assert.ok(!matches(r, [cond('all', 'contains', 'secret')]))
  assert.ok(matches(r, [cond('resBody', 'contains', 'SECRET')]))
})

test('正文通配符可跨行', () => {
  const r = row({ reqBody: '{\n  "a": 1\n}' })
  assert.ok(matches(r, [cond('reqBody', 'wildcard', '{*"a"*}')]))
})

test('命中规则 / 插件、客户端地址与错误信息', () => {
  const r = row({
    clientIP: '192.168.1.20:50112',
    error: 'tls: handshake failure',
    executions: [{ kind: 'rule', id: 'r1', name: 'Mock 登录', phase: 'request' }],
  })
  assert.ok(matches(r, [cond('execution', 'contains', 'mock')]))
  assert.ok(matches(r, [cond('clientIP', 'startsWith', '192.168.')]))
  assert.ok(matches(r, [cond('error', 'contains', 'TLS')]))
  assert.ok(matches(row({}), [cond('execution', 'notContains', 'mock')]))
})

test('业务码与业务消息可单独筛选，「全部」也覆盖它们', () => {
  const r = row({ biz: { code: '40001', message: 'token expired', failed: true } })
  assert.ok(matches(r, [cond('bizCode', 'equals', '40001')]))
  assert.ok(matches(r, [cond('bizCode', 'notEquals', '0')]))
  assert.ok(matches(r, [cond('bizMessage', 'contains', 'TOKEN')]))
  assert.ok(matches(r, [cond('all', 'contains', '40001')]))
  assert.ok(!matches(row({}), [cond('bizCode', 'equals', '40001')]))
  assert.ok(matches(row({}), [cond('bizCode', 'notEquals', '0')]))
})

test('标记色按行 id 比较', () => {
  const r = row({ id: 'a' })
  const red = withField(newCondition(), 'mark')
  assert.equal(red.value, 'red')
  assert.ok(matches(r, [red], 'and', { a: 'red' }))
  assert.ok(!matches(r, [red], 'and', { a: 'blue' }))
  assert.ok(matches(r, [{ ...red, op: 'notEquals' }], 'and', {}))
})

test('切换字段收敛匹配方式与取值', () => {
  const c = cond('url', 'regex', 'x')
  const m = withField(c, 'mark')
  assert.equal(m.op, 'equals')
  const back = withField({ ...m, op: 'notEquals' }, 'host')
  assert.deepEqual([back.op, back.value], ['notEquals', ''])
  assert.equal(withField(c, 'host').value, 'x')
})

test('多条件统一按且 / 或组合', () => {
  const r = row({ method: 'POST' })
  const conds = [cond('host', 'contains', 'example'), cond('method', 'equals', 'GET')]
  assert.ok(!matches(r, conds, 'and'))
  assert.ok(matches(r, conds, 'or'))
  assert.ok(matches(r, [conds[0], cond('method', 'equals', 'POST')], 'and'))
})

test('只有方法与请求 / 响应类型给候选，正则与通配符不给', () => {
  assert.ok(valueOptions(cond('method', 'equals', ''))?.includes('POST'))
  assert.ok(valueOptions(cond('reqType', 'contains', ''))?.includes('multipart/form-data'))
  assert.ok(valueOptions(cond('resType', 'notContains', ''))?.includes('text/event-stream'))
  for (const f of ['host', 'status', 'process', 'clientIP', 'execution', 'url'] as const)
    assert.equal(valueOptions(cond(f, 'contains', '')), undefined)
  assert.equal(valueOptions(cond('method', 'regex', '')), undefined)
  assert.equal(valueOptions(cond('resType', 'wildcard', '')), undefined)
})

test('通配符算法：* 可为空、连续 * 等价、? 恰好一个字符', () => {
  assert.ok(globMatch('a*b', 'ab'))
  assert.ok(globMatch('a**b', 'axxb'))
  assert.ok(globMatch('*', ''))
  assert.ok(!globMatch('?', ''))
  assert.ok(globMatch('a?c*', 'abcdef'))
  assert.ok(!globMatch('a*c', 'abcd'))
})

test('病态正则与通配符不会卡住过滤', () => {
  const evil = row({ url: 'a'.repeat(30) + '!', resBody: 'a'.repeat(100_000) + '!' })
  const started = performance.now()
  assert.ok(matches(evil, [cond('url', 'regex', '(a+)+$')]) === false)
  assert.ok(!matches(evil, [cond('resBody', 'wildcard', '*a*a*a*a*b')]))
  assert.ok(!matches(evil, [cond('resBody', 'regex', '^(a|aa)+$')]))
  assert.ok(performance.now() - started < 1000)
})

test('正则用 RE2 语法：反向引用视为无效', () => {
  const bad = cond('url', 'regex', '(a)\\1')
  assert.deepEqual([...compileSearch([bad], 'and').invalid], [bad.id])
})

test('正文结果按行缓存：正文换了重新求值，长正文同长也不退化', () => {
  const len = 20_000
  const rows = Array.from({ length: 3000 }, (_, i) =>
    row({ id: `r${i}`, resBody: 'x'.repeat(len - 8) + String(i).padStart(8, '0') }),
  )
  const { filter } = compileSearch([cond('resBody', 'regex', '00000042$')], 'and')
  assert.ok(filter)
  const started = performance.now()
  for (let pass = 0; pass < 3; pass++) assert.deepEqual(filter(rows, {}).map((r) => r.id), ['r42'])
  assert.ok(performance.now() - started < 2000)

  const changed = rows.map((r) => (r.id === 'r42' ? { ...r, resBody: 'other' } : r))
  assert.deepEqual(filter(changed, {}), [])
})
