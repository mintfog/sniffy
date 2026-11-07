import { createRef, useState } from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { compileSearch, newCondition, type SearchCondition, type SearchLogic } from '../lib/searchFilter'
import { SearchConditions, type SearchFocusHandle } from './SearchConditions'

function Harness({
  onCollapse = () => {},
  focusRef,
}: {
  onCollapse?: () => void
  focusRef?: React.Ref<SearchFocusHandle>
}) {
  const [conditions, setConditions] = useState<SearchCondition[]>(() => [newCondition()])
  const [logic, setLogic] = useState<SearchLogic>('and')
  return (
    <SearchConditions
      conditions={conditions}
      logic={logic}
      invalid={compileSearch(conditions, logic).invalid}
      onConditions={setConditions}
      onLogic={setLogic}
      onCollapse={onCollapse}
      focusRef={focusRef}
    />
  )
}

test('添加条件后聚焦新行，行间且 / 或统一切换，Esc 逐级清空、移除、收起', async () => {
  const user = userEvent.setup()
  const onCollapse = vi.fn()
  render(<Harness onCollapse={onCollapse} />)

  expect(screen.queryByRole('button', { name: '移除条件' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '添加条件' }))
  const inputs = screen.getAllByRole('textbox', { name: '搜索流量' })
  expect(inputs).toHaveLength(2)
  expect(inputs[1]).toHaveFocus()

  const toggle = screen.getByRole('button', { name: /满足全部条件/ })
  expect(toggle).toHaveTextContent('且')
  await user.click(toggle)
  expect(screen.getByRole('button', { name: /满足任一条件/ })).toHaveTextContent('或')

  await user.type(inputs[1], 'abc')
  await user.keyboard('{Escape}')
  expect(inputs[1]).toHaveValue('')
  await user.keyboard('{Escape}')
  expect(screen.getAllByRole('textbox', { name: '搜索流量' })).toHaveLength(1)
  // 移除后焦点落到相邻行，可继续按 Esc
  expect(screen.getByRole('textbox', { name: '搜索流量' })).toHaveFocus()

  await user.keyboard('{Escape}')
  expect(onCollapse).toHaveBeenCalledOnce()
})

test('正则写错时输入框标为无效；选标记色字段时取值改为颜色下拉', async () => {
  const user = userEvent.setup()
  render(<Harness />)

  await user.click(screen.getByRole('combobox', { name: '匹配方式' }))
  await user.click(screen.getByRole('option', { name: '匹配正则' }))
  const input = screen.getByRole('textbox', { name: '搜索流量' })
  await user.type(input, '(')
  expect(input).toHaveAttribute('aria-invalid', 'true')

  await user.click(screen.getByRole('combobox', { name: '搜索字段' }))
  await user.click(screen.getByRole('option', { name: '标记色' }))
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '标记色' })).toHaveTextContent('红色')
  await user.click(screen.getByRole('combobox', { name: '匹配方式' }))
  expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['等于', '不等于'])
})

test('方法与请求 / 响应类型给出固定候选，可过滤、键盘选择；其他字段与正则不弹；且 / 或按钮无悬停提示', async () => {
  const user = userEvent.setup()
  render(<Harness />)

  await user.click(screen.getByRole('combobox', { name: '搜索字段' }))
  await user.click(screen.getByRole('option', { name: '域名' }))
  expect(screen.getByRole('textbox', { name: '搜索流量' })).toBeInTheDocument()

  await user.click(screen.getByRole('combobox', { name: '搜索字段' }))
  await user.click(screen.getByRole('option', { name: '请求类型' }))
  const input = screen.getByRole('combobox', { name: '搜索流量' })
  await user.click(input)
  expect(screen.getAllByRole('option')[0]).toHaveTextContent('application/json')

  await user.type(input, 'multi')
  expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['multipart/form-data'])
  await user.keyboard('{ArrowDown}{Enter}')
  expect(input).toHaveValue('multipart/form-data')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()

  // 值正好是候选时重新展开列出全部，可直接换选
  await user.click(input)
  await user.click(screen.getByRole('option', { name: 'text/plain' }))
  expect(input).toHaveValue('text/plain')

  await user.click(screen.getByRole('combobox', { name: '匹配方式' }))
  await user.click(screen.getByRole('option', { name: '匹配正则' }))
  expect(screen.getByRole('textbox', { name: '搜索流量' })).toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: '添加条件' }))
  const toggle = screen.getByRole('button', { name: /满足全部条件/ })
  await user.hover(toggle)
  expect(screen.queryByText('满足全部条件（点击切换为任一）')).not.toBeInTheDocument()
})

test('外部滚动不打断候选选择，输入框所在容器滚动并带走输入框时才收起', async () => {
  const user = userEvent.setup()
  render(
    <>
      <div data-testid="traffic" />
      <Harness />
    </>,
  )
  await user.click(screen.getByRole('combobox', { name: '搜索字段' }))
  await user.click(screen.getByRole('option', { name: '方法' }))
  const input = screen.getByRole('combobox', { name: '搜索流量' })
  await user.click(input)
  expect(screen.getByRole('listbox')).toBeInTheDocument()

  fireEvent.scroll(screen.getByTestId('traffic'))
  expect(screen.getByRole('listbox')).toBeInTheDocument()

  fireEvent.scroll(document)
  expect(screen.getByRole('listbox')).toBeInTheDocument()

  vi.spyOn(input.parentElement!, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 40, 100, 32))
  fireEvent.scroll(document)
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

test('聚焦入口：首行为输入框时聚焦并全选，为标记色时聚焦颜色下拉', async () => {
  const user = userEvent.setup()
  const ref = createRef<SearchFocusHandle>()
  render(<Harness focusRef={ref} />)

  const input = screen.getByRole<HTMLInputElement>('textbox', { name: '搜索流量' })
  await user.type(input, 'api')
  input.blur()
  act(() => ref.current?.focus())
  expect(input).toHaveFocus()
  expect([input.selectionStart, input.selectionEnd]).toEqual([0, 3])

  await user.click(screen.getByRole('combobox', { name: '搜索字段' }))
  await user.click(screen.getByRole('option', { name: '标记色' }))
  ;(document.activeElement as HTMLElement | null)?.blur()
  act(() => ref.current?.focus())
  expect(screen.getByRole('combobox', { name: '标记色' })).toHaveFocus()
})

test('标记色行按 Esc 恢复成空的文本条件并聚焦输入框；下拉展开时 Esc 只收起下拉', async () => {
  const user = userEvent.setup()
  const onCollapse = vi.fn()
  render(<Harness onCollapse={onCollapse} />)

  await user.click(screen.getByRole('combobox', { name: '搜索字段' }))
  await user.click(screen.getByRole('option', { name: '标记色' }))
  const mark = screen.getByRole('combobox', { name: '标记色' })
  await user.click(mark)
  expect(screen.getByRole('listbox')).toBeInTheDocument()
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '标记色' })).toBeInTheDocument()

  await user.keyboard('{Escape}')
  expect(screen.queryByRole('combobox', { name: '标记色' })).not.toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '搜索字段' })).toHaveTextContent('全部')
  expect(screen.getByRole('textbox', { name: '搜索流量' })).toHaveFocus()
  expect(onCollapse).not.toHaveBeenCalled()
})
