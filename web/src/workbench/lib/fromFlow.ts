/** 从流量行派生断点规则的纯逻辑，供右键菜单与测试共用。 */
import type { BreakRule } from '@/lib/bridge'

export type BreakScope = 'path' | 'host'
export type BreakPhase = 'request' | 'response'

export interface BreakTarget {
  url: string
  match: '' | 'path' | 'host'
}

/**
 * 按 scope 生成断点目标。直接切 URL 字符串：new URL() 会规范化路径、去掉显式默认端口，与后端拿到的原串对不上。
 */
export function breakTarget(url: string, scope: BreakScope): BreakTarget {
  if (scope === 'host') return { url: urlOrigin(url), match: 'host' }
  const origin = urlOrigin(url)
  return { url: origin + routePath(url, origin), match: 'path' }
}

/** 以下与 Go 侧 pipeline.stripQuery / urlOrigin / samePath 保持一致，契约用例见 internal/pipeline/testdata/break_targets.json。 */
function stripQuery(url: string): string {
  return url.split(/[?#]/)[0]
}

/** origin 之后的路径；空路径即 /。 */
function routePath(url: string, origin: string): string {
  return stripQuery(url).slice(origin.length) || '/'
}

function urlOrigin(url: string): string {
  const base = stripQuery(url)
  const sep = base.indexOf('://')
  const slash = base.indexOf('/', sep < 0 ? 0 : sep + 3)
  return slash < 0 ? base : base.slice(0, slash)
}

/** 按后端匹配语义归一化：同一 key 的规则命中的请求完全相同。 */
function targetKey(url: string, match: BreakTarget['match']): string {
  const u = url.trim()
  if (match === 'path') {
    const origin = urlOrigin(u)
    return origin.toLowerCase() + routePath(u, origin)
  }
  if (match === 'host') return urlOrigin(u).toLowerCase()
  return u
}

function phaseKey(phase: BreakPhase): 'onRequest' | 'onResponse' {
  return phase === 'request' ? 'onRequest' : 'onResponse'
}

/** 匹配方式与归一化后的模式都相同的规则视为同一目标；可能有多条，勾选态与开关都要覆盖全部。 */
export function sameTargetRules(rules: BreakRule[], target: BreakTarget): BreakRule[] {
  const key = targetKey(target.url, target.match)
  return rules.filter((r) => (r.match ?? '') === target.match && targetKey(r.url, target.match) === key)
}

/** 该阶段实际会暂停时勾选。 */
export function breakChecked(rules: BreakRule[], target: BreakTarget, phase: BreakPhase): boolean {
  const key = phaseKey(phase)
  return sameTargetRules(rules, target).some((r) => r.enabled && r[key])
}

export type BreakToggle =
  | { kind: 'add'; target: BreakTarget; onRequest: boolean; onResponse: boolean }
  | { kind: 'update'; rule: BreakRule }
  | { kind: 'delete'; id: string }

/**
 * 切换某目标某阶段的断点，返回需依次执行的操作。
 * 重新启用停用规则时只打开本次阶段，避免另一阶段在用户不知情时生效。
 */
export function toggleBreak(rules: BreakRule[], target: BreakTarget, phase: BreakPhase): BreakToggle[] {
  const key = phaseKey(phase)
  const same = sameTargetRules(rules, target)
  if (breakChecked(rules, target, phase)) {
    return same
      .filter((r) => r.enabled && r[key])
      .map((r): BreakToggle => {
        const next = { ...r, [key]: false }
        return next.onRequest || next.onResponse ? { kind: 'update', rule: next } : { kind: 'delete', id: r.id }
      })
  }
  const enabled = same.find((r) => r.enabled)
  if (enabled) return [{ kind: 'update', rule: { ...enabled, [key]: true } }]
  const disabled = same[0]
  if (disabled) {
    return [{ kind: 'update', rule: { ...disabled, enabled: true, onRequest: false, onResponse: false, [key]: true } }]
  }
  return [{ kind: 'add', target, onRequest: phase === 'request', onResponse: phase === 'response' }]
}

/** 与 Go 侧 service.maxMockBody 一致。 */
export const MAX_MOCK_BODY = 1 << 20
