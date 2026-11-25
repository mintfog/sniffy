import test from 'node:test'
import assert from 'node:assert/strict'
import { newCondition, type SearchCondition } from './searchFilter.ts'
import {
  defaultFilterName,
  describeFilter,
  isSnapshotEmpty,
  removeFilter,
  renameError,
  renameFilter,
  restoreFilter,
  sameSnapshot,
  snapshotFilter,
  upsertFilter,
  type FilterSnapshot,
  type SavedFilter,
} from './savedFilters.ts'

const cond = (over: Partial<SearchCondition>): SearchCondition => ({ ...newCondition(), ...over })

test('快照只保留生效的条件，去掉 id，文本取值去首尾空白', () => {
  const snap = snapshotFilter({
    chips: { status: '2xx', protocol: 'https' },
    conditions: [
      cond({ field: 'host', op: 'contains', value: '  api.example.com ' }),
      cond({ value: '   ' }),
      cond({ field: 'mark', op: 'equals', value: 'red' }),
    ],
    logic: 'or',
  })
  assert.deepEqual(snap, {
    chips: { protocol: 'https', status: '2xx' },
    conditions: [
      { field: 'host', op: 'contains', value: 'api.example.com' },
      { field: 'mark', op: 'equals', value: 'red' },
    ],
    logic: 'or',
  })
  assert.equal(isSnapshotEmpty(snap), false)
  assert.equal(isSnapshotEmpty(snapshotFilter({ chips: {}, conditions: [newCondition()], logic: 'and' })), true)
})

test('比较快照：单条件时忽略且 / 或，多条件时区分；芯片与条件顺序都要一致', () => {
  const one: FilterSnapshot = { chips: {}, conditions: [{ field: 'url', op: 'contains', value: 'a' }], logic: 'and' }
  assert.equal(sameSnapshot(one, { ...one, logic: 'or' }), true)
  const two: FilterSnapshot = { ...one, conditions: [...one.conditions, { field: 'host', op: 'equals', value: 'b' }] }
  assert.equal(sameSnapshot(two, { ...two, logic: 'or' }), false)
  assert.equal(sameSnapshot(two, { ...two, conditions: [...two.conditions].reverse() }), false)
  assert.equal(sameSnapshot(one, { ...one, chips: { status: '4xx' } }), false)
  assert.equal(sameSnapshot({ ...one, chips: { status: '4xx' } }, { ...one, chips: { status: '4xx' } }), true)
})

test('还原时生成新 id，丢弃认不出的字段、匹配方式、颜色与芯片', () => {
  const saved = {
    chips: { status: '2xx', content: 'nope', protocol: '2xx' },
    conditions: [
      { field: 'host', op: 'contains', value: 'a' },
      { field: 'gone', op: 'contains', value: 'x' },
      { field: 'mark', op: 'contains', value: 'red' },
      { field: 'mark', op: 'equals', value: 'purple' },
      { field: 'method', op: 'fuzzy', value: 'GET' },
    ],
    logic: 'or',
  } as unknown as FilterSnapshot
  const st = restoreFilter(saved, true)
  assert.deepEqual(st.chips, { status: '2xx' })
  assert.equal(st.logic, 'or')
  assert.equal(st.conditions.length, 1)
  assert.deepEqual({ ...st.conditions[0], id: undefined }, { id: undefined, field: 'host', op: 'contains', value: 'a' })
  assert.match(st.conditions[0].id, /^c\d+$/)

  const again = restoreFilter(saved, true)
  assert.notEqual(again.conditions[0].id, st.conditions[0].id)
})

test('未配置业务码规则时，还原丢弃业务码条件与芯片；没有条件时给一条空条件', () => {
  const saved: FilterSnapshot = {
    chips: { biz: 'bizFailed' },
    conditions: [{ field: 'bizCode', op: 'equals', value: '0' }],
    logic: 'and',
  }
  const off = restoreFilter(saved, false)
  assert.deepEqual(off.chips, {})
  assert.equal(off.conditions.length, 1)
  assert.equal(off.conditions[0].field, 'all')
  assert.equal(off.conditions[0].value, '')

  const on = restoreFilter(saved, true)
  assert.deepEqual(on.chips, { biz: 'bizFailed' })
  assert.equal(on.conditions[0].field, 'bizCode')
})

test('保存→还原→再快照得到同一份筛选', () => {
  const state = {
    chips: { content: 'json' },
    conditions: [cond({ field: 'path', op: 'regex', value: '^/api/v\\d+' }), cond({ field: 'status', op: 'notEquals', value: '200' })],
    logic: 'and' as const,
  }
  const snap = snapshotFilter(state)
  assert.equal(sameSnapshot(snapshotFilter(restoreFilter(snap, false)), snap), true)
})

const snap: FilterSnapshot = { chips: { status: '5xx' }, conditions: [], logic: 'and' }

test('同名保存覆盖原条目并保留位置与 id，新名称追加到末尾', () => {
  const a = upsertFilter([], ' 失败请求 ', snap)
  assert.equal(a.list.length, 1)
  assert.equal(a.list[0].name, '失败请求')
  const b = upsertFilter(a.list, '其它', snap)
  assert.deepEqual(b.list.map((f) => f.name), ['失败请求', '其它'])
  assert.notEqual(b.id, a.id)

  const next: FilterSnapshot = { chips: { status: '4xx' }, conditions: [], logic: 'and' }
  const c = upsertFilter(b.list, '失败请求', next)
  assert.equal(c.id, a.id)
  assert.deepEqual(c.list.map((f) => f.name), ['失败请求', '其它'])
  assert.deepEqual(c.list[0].chips, { status: '4xx' })
})

test('重命名拒绝空名与重名，允许改回自己的名字；删除按 id', () => {
  const list: SavedFilter[] = [
    { ...snap, id: 'x', name: 'A' },
    { ...snap, id: 'y', name: 'B' },
  ]
  assert.equal(renameError(list, 'x', '  '), 'empty')
  assert.equal(renameError(list, 'x', 'B'), 'taken')
  assert.equal(renameError(list, 'x', ' A '), null)
  assert.deepEqual(renameFilter(list, 'x', ' C ').map((f) => f.name), ['C', 'B'])
  assert.deepEqual(removeFilter(list, 'x').map((f) => f.id), ['y'])
})

test('默认名称跳过已占用的编号', () => {
  const base = (n: number) => `筛选 ${n}`
  assert.equal(defaultFilterName([], base), '筛选 1')
  const list: SavedFilter[] = [
    { ...snap, id: 'a', name: '筛选 2' },
    { ...snap, id: 'b', name: '筛选 3' },
  ]
  assert.equal(defaultFilterName(list, base), '筛选 4')
  assert.equal(defaultFilterName([{ ...snap, id: 'a', name: '别的' }], base), '筛选 2')
})

test('描述：芯片在前，条件按且 / 或连接，标记色显示颜色名', () => {
  const dict: Record<string, string> = {
    'workbench.filter.error': '错误',
    'toolbar.logic.or': '或',
    'toolbar.field.host': '域名',
    'toolbar.field.mark': '标记色',
    'toolbar.op.contains': '包含',
    'toolbar.op.equals': '等于',
    'workbench.mark.red': '红',
  }
  const t = ((k: string) => dict[k] ?? k) as unknown as Parameters<typeof describeFilter>[1]
  const text = describeFilter(
    {
      chips: { status: 'err', protocol: 'https' },
      conditions: [
        { field: 'host', op: 'contains', value: 'api' },
        { field: 'mark', op: 'equals', value: 'red' },
      ],
      logic: 'or',
    },
    t,
  )
  assert.equal(text, 'HTTPS · 错误 · 域名 包含 api 或 标记色 等于 红')
})
