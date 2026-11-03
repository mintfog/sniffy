import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { breakChecked, breakTarget, sameTargetRules, toggleBreak } from './fromFlow.ts'
import type { BreakRule } from '@/lib/bridge'

const PATH = { url: 'https://a.com/x', match: 'path' as const }

function rule(over: Partial<BreakRule> = {}): BreakRule {
  return { id: 'b1', url: 'https://a.com/x', match: 'path', onRequest: true, onResponse: false, enabled: true, ...over }
}

interface BreakTargetFixture {
  targets: { url: string; path: string; host: string }[]
  sameTarget: { match: '' | 'path' | 'host'; a: string; b: string; same: boolean }[]
}

/** 与 Go 侧 internal/rules 的 TestFlowTargetMatrix 共用：那边核对这些目标对实际请求的命中。 */
const fixture: BreakTargetFixture = JSON.parse(
  readFileSync(new URL('../../../../internal/pipeline/testdata/break_targets.json', import.meta.url), 'utf8'),
)

test('契约：breakTarget 生成的目标与 break_targets.json 一致', () => {
  for (const c of fixture.targets) {
    assert.deepEqual(breakTarget(c.url, 'path'), { url: c.path, match: 'path' }, c.url)
    assert.deepEqual(breakTarget(c.url, 'host'), { url: c.host, match: 'host' }, c.url)
  }
})

test('契约：sameTargetRules 的同一目标判断与 break_targets.json 一致', () => {
  for (const c of fixture.sameTarget) {
    const got = sameTargetRules([rule({ url: c.b, match: c.match || undefined })], { url: c.a, match: c.match }).length === 1
    assert.equal(got, c.same, `${c.match} ${c.a} / ${c.b}`)
  }
})

test('breakTarget: path 去掉查询串与片段，按路径模式整串比对', () => {
  assert.deepEqual(breakTarget('https://a.com/api/v1/me?tz=1#top', 'path'), { url: 'https://a.com/api/v1/me', match: 'path' })
  // 不经 URL 规范化：点段与编码保持后端看到的原样。
  assert.deepEqual(breakTarget('https://a.com/a/../b%2F?x', 'path'), { url: 'https://a.com/a/../b%2F', match: 'path' })
  // 空路径即根路径，与实际请求 https://a.com/ 一致。
  assert.deepEqual(breakTarget('https://a.com?q=1', 'path'), { url: 'https://a.com/', match: 'path' })
})

test('breakTarget: host 取 scheme://authority 按主机模式比对，保留显式端口与 IPv6', () => {
  assert.deepEqual(breakTarget('https://a.com:443/x?y=1', 'host'), { url: 'https://a.com:443', match: 'host' })
  assert.deepEqual(breakTarget('http://[::1]:8080/x', 'host'), { url: 'http://[::1]:8080', match: 'host' })
  assert.deepEqual(breakTarget('https://a.com?q=https://b.com/x', 'host'), { url: 'https://a.com', match: 'host' })
})

test('sameTargetRules: 模式相同但匹配方式不同不算同一目标', () => {
  assert.deepEqual(sameTargetRules([rule({ match: undefined })], PATH), [])
  assert.deepEqual(sameTargetRules([rule(), rule({ id: 'b2' })], PATH).map((r) => r.id), ['b1', 'b2'])
})

test('sameTargetRules: 按后端匹配语义归一化后比较', () => {
  const host = breakTarget('https://a.test/x', 'host')
  const hostRule = (url: string) => rule({ url, match: 'host' })
  // 域名规则忽略路径、查询串与主机大小写。
  for (const url of ['https://a.test/', 'https://A.Test', ' https://a.test/y?z=1 ']) {
    assert.equal(sameTargetRules([hostRule(url)], host).length, 1, url)
  }
  assert.deepEqual(sameTargetRules([hostRule('https://a.test.evil.com/'), hostRule('http://a.test/')], host), [])
  // 路径规则忽略查询串与片段，路径本身区分大小写。
  assert.equal(sameTargetRules([rule({ url: 'https://a.com/x?id=1#top' })], PATH).length, 1)
  assert.deepEqual(sameTargetRules([rule({ url: 'https://a.com/X' }), rule({ url: 'https://a.com/x/' })], PATH), [])
  const root = breakTarget('https://a.com', 'path')
  assert.equal(sameTargetRules([rule({ url: 'https://a.com' }), rule({ url: 'https://a.com/?x=1' })], root).length, 2)
  // 默认匹配按原串比较（不同写法命中的请求不同）。
  const wild = { url: 'https://a.test/*', match: '' as const }
  assert.deepEqual(sameTargetRules([rule({ url: 'https://A.test/*', match: undefined })], wild), [])
})

test('toggleBreak: 已有等价的域名规则时沿用并能关掉它', () => {
  const host = breakTarget('https://a.test/x', 'host')
  const existing = rule({ url: 'https://a.test/', match: 'host', onRequest: true })
  assert.equal(breakChecked([existing], host, 'request'), true)
  assert.deepEqual(toggleBreak([existing], host, 'request'), [{ kind: 'delete', id: 'b1' }])
  assert.deepEqual(toggleBreak([existing], host, 'response'), [{ kind: 'update', rule: { ...existing, onResponse: true } }])
})

test('toggleBreak: 无同目标规则时新增单阶段', () => {
  assert.deepEqual(toggleBreak([], PATH, 'response'), [{ kind: 'add', target: PATH, onRequest: false, onResponse: true }])
})

test('toggleBreak: 打开另一阶段 / 关掉唯一阶段即删除', () => {
  const rs = [rule()]
  assert.deepEqual(toggleBreak(rs, PATH, 'response'), [{ kind: 'update', rule: rule({ onResponse: true }) }])
  assert.deepEqual(toggleBreak(rs, PATH, 'request'), [{ kind: 'delete', id: 'b1' }])
})

test('toggleBreak: 重新启用停用规则时只保留本次阶段', () => {
  const rs = [rule({ enabled: false, onRequest: true, onResponse: true })]
  assert.equal(breakChecked(rs, PATH, 'request'), false)
  assert.deepEqual(toggleBreak(rs, PATH, 'response'), [
    { kind: 'update', rule: rule({ enabled: true, onRequest: false, onResponse: true }) },
  ])
})

test('重复的同目标规则：勾选态看全部，关闭时处理每一条', () => {
  const reqOnly = rule({ id: 'req', onRequest: true, onResponse: false })
  const resOnly = rule({ id: 'res', onRequest: false, onResponse: true })
  const both = rule({ id: 'both', onRequest: true, onResponse: true })
  const rs = [reqOnly, resOnly, both]
  assert.equal(breakChecked(rs, PATH, 'request'), true)
  assert.equal(breakChecked(rs, PATH, 'response'), true)
  assert.deepEqual(toggleBreak(rs, PATH, 'response'), [
    { kind: 'delete', id: 'res' },
    { kind: 'update', rule: { ...both, onResponse: false } },
  ])
  // 停用的同目标规则不参与勾选，也不在关闭时被改动。
  const withDisabled = [reqOnly, rule({ id: 'off', enabled: false, onResponse: true })]
  assert.equal(breakChecked(withDisabled, PATH, 'response'), false)
  assert.deepEqual(toggleBreak(withDisabled, PATH, 'response'), [{ kind: 'update', rule: { ...reqOnly, onResponse: true } }])
})
