/**
 * 已保存的筛选：把快捷芯片与多条件搜索存成命名快照，日后一键套用。
 *
 * 快照只记生效的部分：取值为空的文本条件不参与过滤，存下来套用时只会多出空行。
 * 条件 id 只在界面内用于定位行，不入库，套用时重新生成。
 */

import type { TFunction } from 'i18next'
import {
  BIZ_FIELDS,
  isConditionEffective,
  MARK_COLORS,
  newCondition,
  opsForField,
  SEARCH_FIELDS,
  type SearchCondition,
  type SearchField,
  type SearchLogic,
  type SearchOp,
} from './searchFilter.ts'
import {
  BIZ_FAILED_CHIP,
  FILTER_CHIPS,
  FILTER_GROUPS,
  GROUP_OF,
  isFilterEmpty,
  type FilterSelection,
} from './trafficFilter.ts'

export interface SavedCondition {
  field: SearchField
  op: SearchOp
  value: string
}

export interface SavedFilter {
  id: string
  name: string
  chips: FilterSelection
  conditions: SavedCondition[]
  logic: SearchLogic
}

/** 工作台当前的筛选状态。 */
export interface FilterState {
  chips: FilterSelection
  conditions: SearchCondition[]
  logic: SearchLogic
}

export type FilterSnapshot = Omit<SavedFilter, 'id' | 'name'>

export function snapshotFilter(state: FilterState): FilterSnapshot {
  const conditions = state.conditions
    .filter(isConditionEffective)
    .map((c) => ({ field: c.field, op: c.op, value: c.value.trim() }))
  return { chips: { ...state.chips }, conditions, logic: state.logic }
}

export function isSnapshotEmpty(s: FilterSnapshot): boolean {
  return s.conditions.length === 0 && isFilterEmpty(s.chips)
}

/** 两个快照是否筛出同样的结果；只有一个条件时且 / 或无所谓。 */
export function sameSnapshot(a: FilterSnapshot, b: FilterSnapshot): boolean {
  if (FILTER_GROUPS.some((g) => (a.chips[g] ?? '') !== (b.chips[g] ?? ''))) return false
  if (a.conditions.length !== b.conditions.length) return false
  if (a.conditions.length > 1 && a.logic !== b.logic) return false
  return a.conditions.every((c, i) => {
    const d = b.conditions[i]
    return c.field === d.field && c.op === d.op && c.value === d.value
  })
}

/**
 * 把快照还原成工作台状态。存档可能出自更新或更旧的版本，认不出的字段、匹配方式、芯片丢弃；
 * 没有配置业务码规则时界面上没有业务码字段与芯片，留着会成为看不见也关不掉的筛选，一并丢弃。
 */
export function restoreFilter(saved: FilterSnapshot, bizEnabled: boolean): FilterState {
  const chips: FilterSelection = {}
  for (const g of FILTER_GROUPS) {
    const k = saved.chips[g]
    if (!k || GROUP_OF.get(k) !== g) continue
    if (k === BIZ_FAILED_CHIP && !bizEnabled) continue
    chips[g] = k
  }
  const conditions: SearchCondition[] = []
  for (const c of saved.conditions) {
    if (!SEARCH_FIELDS.includes(c.field) || !opsForField(c.field).includes(c.op)) continue
    if (!bizEnabled && BIZ_FIELDS.has(c.field)) continue
    if (c.field === 'mark' && !(MARK_COLORS as readonly string[]).includes(c.value)) continue
    conditions.push({ ...newCondition(), field: c.field, op: c.op, value: c.value })
  }
  return {
    chips,
    conditions: conditions.length ? conditions : [newCondition()],
    logic: saved.logic === 'or' ? 'or' : 'and',
  }
}

let seq = 0

// 序号随页面重载归零，拼上时间戳才不会与已存条目撞 id。
function newId(): string {
  return `f${Date.now().toString(36)}${(++seq).toString(36)}`
}

/** 按名称保存：同名覆盖原条目（位置与 id 不变），否则追加到末尾。 */
export function upsertFilter(
  list: readonly SavedFilter[],
  name: string,
  snap: FilterSnapshot,
): { list: SavedFilter[]; id: string } {
  const n = name.trim()
  const existing = list.find((f) => f.name === n)
  if (existing) {
    return { list: list.map((f) => (f.id === existing.id ? { ...snap, id: f.id, name: n } : f)), id: existing.id }
  }
  const id = newId()
  return { list: [...list, { ...snap, id, name: n }], id }
}

export type RenameError = 'empty' | 'taken'

export function renameError(list: readonly SavedFilter[], id: string, name: string): RenameError | null {
  const n = name.trim()
  if (!n) return 'empty'
  if (list.some((f) => f.id !== id && f.name === n)) return 'taken'
  return null
}

/** 调用方先用 renameError 校验新名称。 */
export function renameFilter(list: readonly SavedFilter[], id: string, name: string): SavedFilter[] {
  return list.map((f) => (f.id === id ? { ...f, name: name.trim() } : f))
}

export function removeFilter(list: readonly SavedFilter[], id: string): SavedFilter[] {
  return list.filter((f) => f.id !== id)
}

/** 默认名按新条目的序号编号，编号已被占用时顺延。 */
export function defaultFilterName(list: readonly SavedFilter[], base: (n: number) => string): string {
  const names = new Set(list.map((f) => f.name))
  for (let n = list.length + 1; ; n++) {
    const name = base(n)
    if (!names.has(name)) return name
  }
}

/** 一行可读的筛选描述：芯片在前，搜索条件按且 / 或连接。 */
export function describeFilter(f: FilterSnapshot, t: TFunction): string {
  const parts: string[] = []
  for (const def of FILTER_CHIPS) {
    if (f.chips[def.group] === def.key) parts.push(def.labelKey ? t(def.labelKey) : (def.label ?? def.key))
  }
  const joiner = ` ${t(f.logic === 'or' ? 'toolbar.logic.or' : 'toolbar.logic.and')} `
  const conds = f.conditions
    .map((c) => {
      const value = c.field === 'mark' ? t(`workbench.mark.${c.value}`) : c.value
      return `${t(`toolbar.field.${c.field}`)} ${t(`toolbar.op.${c.op}`)} ${value}`
    })
    .join(joiner)
  if (conds) parts.push(conds)
  return parts.join(' · ')
}
