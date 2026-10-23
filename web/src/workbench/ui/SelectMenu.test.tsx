import { useState } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { SelectMenu } from './SelectMenu'

const options = [
  { value: 'url', label: 'URL' },
  { value: 'host', label: 'Host' },
  { value: 'path', label: 'Path' },
]

function Example() {
  const [value, setValue] = useState('host')
  return (
    <>
      <SelectMenu
        label="条件字段"
        value={value}
        options={options}
        onChange={setValue}
      />
      <button>下一项</button>
    </>
  )
}

test('鼠标选择更新值与选中标记，焦点保留在触发器', async () => {
  const user = userEvent.setup()
  render(<Example />)
  const trigger = screen.getByRole('combobox', { name: '条件字段' })
  await user.click(trigger)
  expect(screen.getByRole('option', { name: 'Host' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await user.click(screen.getByRole('option', { name: 'URL' }))
  expect(trigger).toHaveTextContent('URL')
  expect(trigger).toHaveFocus()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  await user.click(trigger)
  expect(screen.getByRole('option', { name: 'URL' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
})

test('键盘浏览可取消、确认、按首字母定位，并通过 Tab 继续填写', async () => {
  const user = userEvent.setup()
  render(<Example />)
  const trigger = screen.getByRole('combobox')
  trigger.focus()
  await user.keyboard('{ArrowDown}{End}{Escape}')
  expect(trigger).toHaveTextContent('Host')
  expect(trigger).toHaveFocus()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  await user.keyboard('{Enter}{Home}{ArrowDown}{ArrowDown}{Enter}')
  expect(trigger).toHaveTextContent('Path')
  await user.keyboard('h{Enter}')
  expect(trigger).toHaveTextContent('Host')
  await user.keyboard(' {End}{Tab}')
  expect(trigger).toHaveTextContent('Path')
  expect(screen.getByRole('button', { name: '下一项' })).toHaveFocus()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

test('外部点击关闭列表，列表自身与无关容器滚动保持展开', async () => {
  const user = userEvent.setup()
  render(
    <>
      <Example />
      <div data-testid="other-scroller" />
    </>
  )
  const trigger = screen.getByRole('combobox')
  await user.click(trigger)
  fireEvent.scroll(screen.getByRole('listbox'))
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  fireEvent.scroll(screen.getByTestId('other-scroller'))
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  await user.click(screen.getByRole('button', { name: '下一项' }))
  expect(trigger).toHaveTextContent('Host')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

test.each([
  ['纵向', 100, 180],
  ['横向', 80, 200],
])(
  '延迟滚动事件保持列表展开，祖先%s滚动使锚点移动时关闭',
  async (_direction, left, top) => {
    const user = userEvent.setup()
    render(
      <div data-testid="scroller">
        <Example />
      </div>
    )
    const trigger = screen.getByRole('combobox')
    const scroller = screen.getByTestId('scroller')
    const bounds = vi
      .spyOn(trigger, 'getBoundingClientRect')
      .mockReturnValue(new DOMRect(100, 200, 160, 28))

    await user.click(trigger)
    fireEvent.scroll(scroller)
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    expect(trigger).toHaveFocus()

    bounds.mockReturnValue(new DOMRect(left, top, 160, 28))
    fireEvent.scroll(scroller)
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()

    await user.click(trigger)
    fireEvent.scroll(scroller)
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Host' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
  }
)

test('靠近窗口底部和右侧时，浮层向上展开并保持在视口内', async () => {
  const user = userEvent.setup()
  render(<Example />)
  const trigger = screen.getByRole('combobox')
  vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(
    new DOMRect(window.innerWidth - 100, window.innerHeight - 40, 90, 28)
  )
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(200)
  await user.click(trigger)
  const list = screen.getByRole('listbox')
  expect(list).toHaveStyle({
    left: `${window.innerWidth - 168}px`,
    top: `${window.innerHeight - 246}px`,
  })
  expect(list.parentElement).toBe(document.body)
})
