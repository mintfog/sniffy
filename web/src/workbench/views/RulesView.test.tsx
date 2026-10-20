import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { bridgeName, deferred, resetWails, wails } from '@/test/wails'
import type { InterceptRule } from '@/types'
import { openRulesWindow } from '../lib/windows'
import { RulesView } from './RulesView'

const rules: InterceptRule[] = [
  {
    id: 'first',
    name: '首个规则',
    enabled: true,
    conditions: [],
    actions: [],
    priority: 0,
    logicOperator: 'AND',
    createdAt: '',
    updatedAt: '',
  },
  {
    id: 'target',
    name: '目标规则',
    enabled: true,
    conditions: [],
    actions: [],
    priority: 10,
    logicOperator: 'AND',
    createdAt: '',
    updatedAt: '',
  },
]

function recordRuleUpdates() {
  const updates: InterceptRule[] = []
  wails.handlers.set('UpdateRule', (_id, value) => {
    updates.push(value as InterceptRule)
    return value
  })
  return updates
}

function mockRuleActions(actions: InterceptRule['actions']) {
  wails.handlers.set('GetRules', () => [{ ...rules[0], actions }])
}

beforeEach(() => {
  resetWails()
  localStorage.removeItem('sniffy-rule-selection')
  window.history.replaceState({}, '', '/')
  wails.handlers.set('GetRules', () => rules)
})

afterEach(() => {
  localStorage.removeItem('sniffy-rule-selection')
  window.history.replaceState({}, '', '/')
})

test('新窗口根据待查看规则选中对应配置', async () => {
  await openRulesWindow('target')
  render(<RulesView />)
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
  expect(localStorage.getItem('sniffy-rule-selection')).toBeNull()
})

test('URL 中的规则标识可定位配置', async () => {
  window.history.replaceState({}, '', '/?w=rules&rule=target')
  render(<RulesView />)
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
})

test('选择条件逻辑、字段、匹配方式和动作后保存对应规则', async () => {
  const user = userEvent.setup()
  const updates = recordRuleUpdates()
  wails.handlers.set('GetRules', () => [
    {
      ...rules[0],
      conditions: [
        { type: 'url_host', operator: 'equals', value: 'example.com' },
      ],
      actions: [
        {
          type: 'redirect',
          parameters: { url: 'https://example.com' },
          enabled: true,
        },
      ],
    },
  ])
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  await user.click(screen.getByRole('button', { name: '任一满足' }))
  await user.click(screen.getByRole('combobox', { name: '条件字段' }))
  await user.click(screen.getByRole('option', { name: '请求头' }))
  await user.type(screen.getByPlaceholderText('头名称'), 'Accept')
  await user.click(screen.getByRole('combobox', { name: '匹配方式' }))
  await user.click(screen.getByRole('option', { name: '包含' }))
  await user.click(screen.getByRole('button', { name: '动作类型: 重定向' }))
  await user.click(screen.getByRole('option', { name: /阻断请求/ }))
  expect(
    screen.getByRole('button', { name: '动作类型: 阻断请求' })
  ).toBeInTheDocument()
  await act(async () => {
    fireEvent.click(screen.getByRole('switch'))
  })
  expect(updates.at(-1)).toMatchObject({
    id: 'first',
    logicOperator: 'OR',
    conditions: [
      {
        type: 'request_header',
        operator: 'contains',
        headerName: 'Accept',
        value: 'example.com',
      },
    ],
    actions: [{ type: 'block' }],
  })
})

test('已打开的窗口接收定位事件，并清空搜索以显示目标规则', async () => {
  render(<RulesView />)
  expect(await screen.findByDisplayValue('首个规则')).toBeInTheDocument()
  fireEvent.change(screen.getByPlaceholderText('搜索规则…'), {
    target: { value: '首个' },
  })
  await act(async () => {
    await openRulesWindow('target')
  })
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
  expect(screen.getByPlaceholderText('搜索规则…')).toHaveValue('')
})

test('已删除的规则显示明确提示', async () => {
  await openRulesWindow('deleted')
  render(<RulesView />)
  expect(await screen.findByText('规则已删除或不存在')).toBeInTheDocument()
  expect(screen.queryByDisplayValue('首个规则')).not.toBeInTheDocument()
})

test('配置加载失败时给出提示', async () => {
  wails.handlers.set('GetRules', () => Promise.reject(new Error('读取失败')))
  await openRulesWindow('target')
  render(<RulesView />)
  expect(await screen.findByText('读取规则配置失败')).toBeInTheDocument()
})

test('配置加载期间连续定位会选中最新目标', async () => {
  const loading = deferred<InterceptRule[]>()
  wails.handlers.set('GetRules', () => loading.promise)
  render(<RulesView />)
  await act(async () => {
    await openRulesWindow('first')
  })
  await act(async () => {
    await openRulesWindow('target')
  })
  await act(async () => {
    loading.resolve(rules)
  })
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('GetRules'))
  ).toHaveLength(1)
})

test('定位后切换开关保存尚在防抖等待的编辑', async () => {
  const updates = recordRuleUpdates()
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => {
    await openRulesWindow('target')
  })
  expect(screen.getByDisplayValue('目标规则')).toBeInTheDocument()
  fireEvent.click(screen.getByText('已编辑规则'))
  expect(screen.getByDisplayValue('已编辑规则')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('switch'))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1000)
  })
  expect(updates).toHaveLength(1)
  expect(updates[0]).toMatchObject({
    id: 'first',
    name: '已编辑规则',
    enabled: false,
  })
})

test('定位保留正在保存的编辑，并按编辑顺序保存开关', async () => {
  const firstSave = deferred<InterceptRule>()
  const updates: InterceptRule[] = []
  wails.handlers.set('UpdateRule', (_id, value) => {
    updates.push(value as InterceptRule)
    return updates.length === 1 ? firstSave.promise : value
  })
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(400)
  })
  expect(updates).toHaveLength(1)
  await act(async () => {
    await openRulesWindow('target')
  })
  fireEvent.click(screen.getByText('已编辑规则'))
  fireEvent.click(screen.getByRole('switch'))
  expect(updates).toHaveLength(1)
  await act(async () => {
    firstSave.resolve(updates[0])
  })
  expect(updates).toHaveLength(2)
  expect(updates[1]).toMatchObject({
    id: 'first',
    name: '已编辑规则',
    enabled: false,
  })
})

test('定位到已保存的规则时保留当前编辑', async () => {
  wails.handlers.set('UpdateRule', (_id, value) => value)
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(400)
  })
  await act(async () => {
    await openRulesWindow('target')
  })
  await act(async () => {
    await openRulesWindow('first')
  })
  expect(screen.getByDisplayValue('已编辑规则')).toBeInTheDocument()
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('GetRules'))
  ).toHaveLength(1)
})

test('定位后删除规则会取消防抖保存，并提示规则不存在', async () => {
  const updates = recordRuleUpdates()
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => {
    await openRulesWindow('target')
  })
  fireEvent.click(screen.getByText('已编辑规则'))
  fireEvent.click(screen.getByRole('button', { name: '更多规则操作' }))
  fireEvent.click(screen.getByRole('menuitem', { name: '删除规则' }))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1000)
  })
  expect(screen.queryByText('首个规则')).not.toBeInTheDocument()
  expect(screen.queryByText('已编辑规则')).not.toBeInTheDocument()
  expect(updates).toHaveLength(0)
  await act(async () => {
    await openRulesWindow('first')
  })
  expect(screen.getByText('规则已删除或不存在')).toBeInTheDocument()
})

test('搜索并添加 Mock 后聚焦参数，保存输入的响应体', async () => {
  const user = userEvent.setup()
  const updates = recordRuleUpdates()
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  await user.click(screen.getByRole('button', { name: '添加动作' }))
  const search = screen.getByRole('combobox', { name: '搜索动作' })
  expect(search).toHaveFocus()
  await user.type(search, 'mock')
  expect(screen.getAllByRole('option')).toHaveLength(1)
  await user.keyboard('{Enter}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByLabelText('状态码')).toHaveFocus()
  expect(screen.getByLabelText('状态码')).toHaveValue('200')
  fireEvent.change(screen.getByLabelText('响应体 (JSON)'), {
    target: { value: '{"ok":true}' },
  })
  await user.click(screen.getByRole('switch'))
  expect(updates.at(-1)?.actions).toMatchObject([
    {
      type: 'auto_respond',
      parameters: { response: { status: 200, body: '{"ok":true}' } },
    },
  ])
})

test('动作面板在组合输入期间保持打开，取消或切换规则不添加动作', async () => {
  const user = userEvent.setup()
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  const add = screen.getByRole('button', { name: '添加动作' })
  await user.click(add)
  const search = screen.getByRole('combobox', { name: '搜索动作' })
  await user.type(search, '不存在的动作')
  expect(screen.getByText('没有匹配的动作')).toBeInTheDocument()
  fireEvent.compositionStart(search)
  fireEvent.change(search, { target: { value: 'yan' } })
  expect(fireEvent.keyDown(search, { key: 'Escape', isComposing: true })).toBe(
    true
  )
  expect(screen.getByRole('dialog')).toBeInTheDocument()
  expect(search).toHaveFocus()
  fireEvent.compositionEnd(search)
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(add).toHaveFocus()
  expect(
    screen.queryByRole('group', { name: '动作 1' })
  ).not.toBeInTheDocument()
  await user.click(add)
  await user.click(screen.getByRole('button', { name: /目标规则/ }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('group', { name: '动作 1' })
  ).not.toBeInTheDocument()
})

test('动作排序与删除保留已编辑参数及已有扩展字段', async () => {
  const user = userEvent.setup()
  const updates = recordRuleUpdates()
  mockRuleActions([
    {
      type: 'redirect',
      enabled: true,
      parameters: { url: 'https://example.com' },
    },
    {
      type: 'auto_respond',
      enabled: true,
      parameters: {
        response: {
          status: 201,
          body: 'initial',
          headers: { 'X-Example': 'keep' },
        },
      },
    },
  ])
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  fireEvent.change(screen.getByLabelText('响应体 (JSON)'), {
    target: { value: 'edited' },
  })
  await user.click(
    within(screen.getByRole('group', { name: '动作 2' })).getByRole('button', {
      name: '上移动作',
    })
  )
  expect(
    within(screen.getByRole('group', { name: '动作 1' })).getByLabelText(
      '响应体 (JSON)'
    )
  ).toHaveValue('edited')
  await user.click(
    within(screen.getByRole('group', { name: '动作 2' })).getByRole('button', {
      name: '删除动作',
    })
  )
  await user.click(screen.getByRole('switch'))
  expect(updates.at(-1)?.actions).toMatchObject([
    {
      type: 'auto_respond',
      parameters: {
        response: {
          status: 201,
          body: 'edited',
          headers: { 'X-Example': 'keep' },
        },
      },
    },
  ])
})

test.each([
  { action: '新建规则', expectedName: '未命名规则' },
  { action: '复制规则', expectedName: '首个规则 （副本）' },
])(
  '$action清空搜索并选中名称，显示动作选择入口',
  async ({ action, expectedName }) => {
    const user = userEvent.setup()
    wails.handlers.set('CreateRule', value => ({
      ...(value as InterceptRule),
      id: 'created',
    }))
    render(<RulesView />)
    await screen.findByDisplayValue('首个规则')
    await user.type(screen.getByPlaceholderText('搜索规则…'), '找不到')
    await user.click(screen.getByRole('button', { name: action }))
    const name = screen.getByRole('textbox', {
      name: '规则名称',
    }) as HTMLInputElement
    expect(name).toHaveValue(expectedName)
    expect(name).toHaveFocus()
    expect(name.selectionStart).toBe(0)
    expect(name.selectionEnd).toBe(name.value.length)
    expect(screen.getByPlaceholderText('搜索规则…')).toHaveValue('')
    expect(screen.getByRole('button', { name: '添加动作' })).toBeInTheDocument()
  }
)

test.each([
  { reason: '调用失败', fail: () => Promise.reject(new Error('失败')) },
  { reason: '规则不存在', fail: () => null },
])(
  '保存失败（$reason）后切换规则保留编辑，重试成功后更新状态',
  async ({ fail }) => {
    let failed = true
    wails.handlers.set('UpdateRule', (_id, value) => (failed ? fail() : value))
    render(<RulesView />)
    await screen.findByDisplayValue('首个规则')
    vi.useFakeTimers()
    fireEvent.change(screen.getByRole('textbox', { name: '规则名称' }), {
      target: { value: '修改名称' },
    })
    expect(screen.getByRole('status')).toHaveTextContent('等待保存')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400)
    })
    expect(screen.getByRole('status')).toHaveTextContent('保存失败')
    await act(async () => {
      await openRulesWindow('target')
    })
    fireEvent.click(screen.getByText('修改名称'))
    expect(screen.getByRole('textbox', { name: '规则名称' })).toHaveValue(
      '修改名称'
    )
    expect(screen.getByRole('status')).toHaveTextContent('保存失败')
    failed = false
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '重试' }))
    })
    expect(screen.getByRole('status')).toHaveTextContent('已保存')
  }
)

test('较早的保存完成时，仍提示后续编辑正在等待保存', async () => {
  const firstSave = deferred<InterceptRule>()
  let calls = 0
  wails.handlers.set('UpdateRule', (_id, value) =>
    ++calls === 1 ? firstSave.promise : value
  )
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(screen.getByRole('textbox', { name: '规则名称' }), {
    target: { value: '修改名称' },
  })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(400)
  })
  expect(screen.getByRole('status')).toHaveTextContent('保存中')
  fireEvent.change(screen.getByRole('textbox', { name: '备注' }), {
    target: { value: '补充备注' },
  })
  await act(async () => {
    firstSave.resolve(rules[0])
  })
  expect(screen.getByRole('status')).toHaveTextContent('等待保存')
  await act(async () => {
    await vi.advanceTimersByTimeAsync(400)
  })
  expect(screen.getByRole('status')).toHaveTextContent('已保存')
})

test('拖动动作调整顺序后保存原有参数', async () => {
  const updates = recordRuleUpdates()
  mockRuleActions([
    {
      type: 'redirect',
      enabled: true,
      parameters: { url: 'https://example.com' },
    },
    { type: 'delay', enabled: true, parameters: { milliseconds: 1500 } },
  ])
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  const dataTransfer = {
    effectAllowed: '',
    dropEffect: '',
    setData: vi.fn(),
    setDragImage: vi.fn(),
  }
  fireEvent.dragStart(
    screen.getAllByRole('button', { name: '拖动调整顺序' })[1],
    { dataTransfer }
  )
  fireEvent.dragOver(screen.getByRole('group', { name: '动作 1' }), {
    dataTransfer,
  })
  fireEvent.drop(screen.getByRole('group', { name: '动作 1' }), {
    dataTransfer,
  })
  expect(
    within(screen.getByRole('group', { name: '动作 1' })).getByLabelText(
      '延迟毫秒'
    )
  ).toHaveValue('1500')
  await act(async () => {
    fireEvent.click(screen.getByRole('switch'))
  })
  expect(updates.at(-1)?.actions).toMatchObject([
    { type: 'delay', parameters: { milliseconds: 1500 } },
    { type: 'redirect', parameters: { url: 'https://example.com' } },
  ])
})

test('打开已有动作时定位当前类型，浏览其它选项后仍保留选中标记', async () => {
  const user = userEvent.setup()
  mockRuleActions([
    {
      type: 'auto_respond',
      enabled: true,
      parameters: { response: { status: 201, body: '{"kept":true}' } },
    },
  ])
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  const trigger = screen.getByRole('button', {
    name: '动作类型: 替换响应 (Mock)',
  })
  await user.click(trigger)
  const selected = screen.getByRole('option', { selected: true })
  const search = screen.getByRole('combobox', { name: '搜索动作' })
  expect(selected).toHaveTextContent('替换响应 (Mock)')
  expect(search).toHaveAttribute('aria-activedescendant', selected.id)
  await user.hover(screen.getByRole('option', { name: /^重定向/ }))
  expect(selected).toHaveAttribute('aria-selected', 'true')
  await user.type(search, 'redirect')
  expect(
    screen.queryByRole('option', { selected: true })
  ).not.toBeInTheDocument()
  await user.clear(search)
  expect(search).toHaveAttribute(
    'aria-activedescendant',
    screen.getByRole('option', { selected: true }).id
  )
  await user.keyboard('{Escape}')
  await user.click(trigger)
  await user.keyboard('{Enter}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(trigger).toHaveFocus()
  expect(screen.getByLabelText('状态码')).toHaveValue('201')
  expect(screen.getByLabelText('响应体 (JSON)')).toHaveValue('{"kept":true}')
  expect(
    wails.call.mock.calls.some(([name]) => name === bridgeName('UpdateRule'))
  ).toBe(false)
})

test('更多规则操作通过键盘展开、导航与退出后恢复焦点', async () => {
  const user = userEvent.setup()
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  const trigger = screen.getByRole('button', { name: '更多规则操作' })
  trigger.focus()
  await user.keyboard('{Enter}')
  const menu = screen.getByRole('menu', { name: '更多规则操作' })
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  expect(trigger).toHaveAttribute('aria-controls', menu.id)
  expect(screen.getByRole('menuitem', { name: '停用规则' })).toHaveFocus()
  await user.keyboard('{ArrowDown}')
  expect(screen.getByRole('menuitem', { name: '复制规则' })).toHaveFocus()
  await user.keyboard('{End}')
  expect(screen.getByRole('menuitem', { name: '删除规则' })).toHaveFocus()
  await user.keyboard('{Home}')
  expect(screen.getByRole('menuitem', { name: '停用规则' })).toHaveFocus()
  await user.keyboard('{ArrowUp}')
  expect(screen.getByRole('menuitem', { name: '删除规则' })).toHaveFocus()
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  expect(trigger).toHaveFocus()
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
})

test('菜单使用 Tab 继续表单，外部点击保留目标输入框焦点', async () => {
  const user = userEvent.setup()
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  const trigger = screen.getByRole('button', { name: '更多规则操作' })
  const name = screen.getByRole('textbox', { name: '规则名称' })
  trigger.focus()
  await user.keyboard(' ')
  expect(screen.getByRole('menuitem', { name: '停用规则' })).toHaveFocus()
  await user.tab()
  expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  expect(name).toHaveFocus()
  await user.click(trigger)
  await user.tab({ shift: true })
  expect(screen.getByRole('button', { name: '复制规则' })).toHaveFocus()
  await user.click(trigger)
  await user.click(name)
  expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  expect(name).toHaveFocus()
  await user.click(trigger)
  await user.click(trigger)
  expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  expect(trigger).toHaveFocus()
})

test('键盘执行菜单项后返回触发按钮，删除当前规则后聚焦新建入口', async () => {
  const user = userEvent.setup()
  wails.handlers.set('UpdateRule', (_id, value) => value)
  render(<RulesView />)
  await screen.findByDisplayValue('首个规则')
  const trigger = screen.getByRole('button', { name: '更多规则操作' })
  trigger.focus()
  await user.keyboard('{Enter}{Enter}')
  expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'false')
  expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  expect(trigger).toHaveFocus()
  await user.keyboard('{Enter}{End}{Enter}')
  expect(screen.queryByDisplayValue('首个规则')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: '新建规则' })).toHaveFocus()
  expect(wails.call).toHaveBeenCalledWith(bridgeName('DeleteRule'), 'first')
})
