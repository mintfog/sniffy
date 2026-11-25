import { useState } from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test } from 'vitest'
import { usePrefs } from '../prefs'
import { newCondition } from '../lib/searchFilter'
import { snapshotFilter, type FilterState } from '../lib/savedFilters'
import { SavedFilters } from './SavedFilters'

let latest: FilterState

function Harness({ initial, bizEnabled = false }: { initial: FilterState; bizEnabled?: boolean }) {
  const [state, setState] = useState(initial)
  latest = state
  return (
    <>
      <SavedFilters current={state} bizEnabled={bizEnabled} onApply={setState} />
      <button type="button" onClick={() => setState({ chips: {}, conditions: [newCondition()], logic: 'and' })}>
        reset
      </button>
    </>
  )
}

const hostFilter = (): FilterState => ({
  chips: { status: '2xx' },
  conditions: [{ ...newCondition(), field: 'host', op: 'contains', value: 'api.example.com' }],
  logic: 'and',
})

beforeEach(() => {
  usePrefs.getState().set({ savedFilters: [] })
})

test('保存当前筛选后可从菜单一键套用，当前筛选与之相同时打勾', async () => {
  const user = userEvent.setup()
  render(<Harness initial={hostFilter()} />)

  await user.click(screen.getByRole('button', { name: '已保存的筛选' }))
  expect(screen.getByRole('menuitem', { name: '暂无已保存的筛选' })).toBeDisabled()
  expect(screen.getByRole('menuitem', { name: '管理已保存的筛选…' })).toBeDisabled()
  await user.click(screen.getByRole('menuitem', { name: '保存当前筛选…' }))

  const dialog = screen.getByRole('dialog', { name: '保存筛选' })
  const name = within(dialog).getByRole('textbox', { name: '名称' })
  expect(name).toHaveValue('筛选 1')
  await user.clear(name)
  await user.type(name, 'API 成功请求{Enter}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

  const saved = usePrefs.getState().savedFilters
  expect(saved).toHaveLength(1)
  expect(saved[0]).toMatchObject({
    name: 'API 成功请求',
    chips: { status: '2xx' },
    conditions: [{ field: 'host', op: 'contains', value: 'api.example.com' }],
  })

  await user.click(screen.getByRole('button', { name: 'reset' }))
  await user.click(screen.getByRole('button', { name: '已保存的筛选' }))
  // 空筛选不能保存
  expect(screen.getByRole('menuitem', { name: '保存当前筛选…' })).toBeDisabled()
  await user.click(screen.getByRole('menuitem', { name: 'API 成功请求' }))
  expect(snapshotFilter(latest)).toEqual(snapshotFilter(hostFilter()))

  await user.click(screen.getByRole('button', { name: '已保存的筛选' }))
  // 已套用的条目显示勾选标记
  expect(screen.getByRole('menuitem', { name: 'API 成功请求' }).querySelector('svg')).not.toBeNull()
  expect(screen.getByRole('button', { name: '已保存的筛选' })).toHaveAttribute('aria-expanded', 'true')
})

test('同名保存会提示并覆盖原条目', async () => {
  const user = userEvent.setup()
  usePrefs.getState().set({
    savedFilters: [{ id: 'x', name: '失败', chips: { status: '5xx' }, conditions: [], logic: 'and' }],
  })
  render(<Harness initial={hostFilter()} />)

  await user.click(screen.getByRole('button', { name: '已保存的筛选' }))
  await user.click(screen.getByRole('menuitem', { name: '保存当前筛选…' }))
  const dialog = screen.getByRole('dialog', { name: '保存筛选' })
  const name = within(dialog).getByRole('textbox', { name: '名称' })
  await user.clear(name)
  expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
  await user.type(name, '失败')
  expect(within(dialog).getByText('已有同名的筛选，保存将覆盖它')).toBeInTheDocument()
  await user.click(within(dialog).getByRole('button', { name: '覆盖' }))

  const saved = usePrefs.getState().savedFilters
  expect(saved).toHaveLength(1)
  expect(saved[0]).toMatchObject({ id: 'x', name: '失败', chips: { status: '2xx' } })
})

test('管理对话框可重命名（拒绝重名，Esc 只取消重命名）、删除与套用', async () => {
  const user = userEvent.setup()
  usePrefs.getState().set({
    savedFilters: [
      { id: 'a', name: '甲', chips: { status: '5xx' }, conditions: [], logic: 'and' },
      { id: 'b', name: '乙', chips: {}, conditions: [{ field: 'method', op: 'equals', value: 'POST' }], logic: 'and' },
    ],
  })
  render(<Harness initial={hostFilter()} />)

  await user.click(screen.getByRole('button', { name: '已保存的筛选' }))
  await user.click(screen.getByRole('menuitem', { name: '管理已保存的筛选…' }))
  const dialog = screen.getByRole('dialog', { name: '管理已保存的筛选' })
  expect(within(dialog).getByRole('button', { name: '甲 5xx' })).toHaveFocus()
  expect(within(dialog).getByText('5xx')).toBeInTheDocument()
  expect(within(dialog).getByText('方法 等于 POST')).toBeInTheDocument()

  await user.click(within(dialog).getByRole('button', { name: '重命名 甲' }))
  const input = within(dialog).getByRole('textbox', { name: '名称' })
  await user.clear(input)
  await user.type(input, '乙')
  expect(within(dialog).getByText('已有同名的筛选')).toBeInTheDocument()
  expect(within(dialog).getByRole('button', { name: '确认重命名' })).toBeDisabled()
  await user.keyboard('{Escape}')
  expect(screen.getByRole('dialog', { name: '管理已保存的筛选' })).toBeInTheDocument()
  expect(usePrefs.getState().savedFilters.map((f) => f.name)).toEqual(['甲', '乙'])

  await user.click(within(dialog).getByRole('button', { name: '重命名 甲' }))
  await user.clear(within(dialog).getByRole('textbox', { name: '名称' }))
  await user.type(within(dialog).getByRole('textbox', { name: '名称' }), '服务端错误{Enter}')
  expect(usePrefs.getState().savedFilters.map((f) => f.name)).toEqual(['服务端错误', '乙'])

  await user.click(within(dialog).getByRole('button', { name: '删除 乙' }))
  expect(usePrefs.getState().savedFilters.map((f) => f.id)).toEqual(['a'])

  await user.click(within(dialog).getByRole('button', { name: '服务端错误 5xx' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(latest.chips).toEqual({ status: '5xx' })
  expect(latest.conditions).toHaveLength(1)
  expect(latest.conditions[0].value).toBe('')
})
