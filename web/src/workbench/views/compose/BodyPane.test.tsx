import { useState } from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { copyLineDown, redo, undo, undoDepth } from '@codemirror/commands'
import { EditorView } from '@codemirror/view'
import { expect, test, vi } from 'vitest'
import { BodyPane } from './BodyPane'
import { draftDiff, newDraft, type Draft } from './model'
import { toRequestSpec } from './wire'

function renderBody(body: string) {
  let current: Draft = {
    ...newDraft(),
    method: 'POST',
    url: 'https://example.com',
    body,
  }
  const onPatch = vi.fn()
  function StatefulBodyPane() {
    const [draft, setDraft] = useState(current)
    current = draft
    return (
      <BodyPane
        draft={draft}
        diff={draftDiff(draft)}
        onPatch={patch => {
          onPatch(patch)
          setDraft(previous => ({ ...previous, ...patch }))
        }}
      />
    )
  }
  render(<StatefulBodyPane />)
  const view = EditorView.findFromDOM(
    screen.getByRole('textbox', { name: '请求体' })
  )!
  expect(view).not.toBeNull()
  return { view, onPatch, requestBody: () => toRequestSpec(current).body }
}

test('切换正文格式保留原文，并更新对应的 Content-Type', async () => {
  const user = userEvent.setup()
  const original = '{"message":"保留正文"}\r\n'
  const { onPatch, requestBody } = renderBody(original)
  const select = screen.getByRole('combobox', { name: '正文格式' })
  expect(select).toHaveValue('json')
  expect(onPatch).not.toHaveBeenCalled()
  expect(requestBody()).toBe(original)

  for (const [mode, contentType] of [
    ['xml', 'application/xml'],
    ['text', 'text/plain'],
    ['html', 'text/html'],
    ['json', 'application/json'],
  ]) {
    await user.selectOptions(select, mode)
    expect(select).toHaveValue(mode)
    expect(onPatch).toHaveBeenLastCalledWith(
      expect.objectContaining({
        body: original,
        headers: expect.arrayContaining([
          expect.objectContaining({ name: 'Content-Type', value: contentType }),
        ]),
      })
    )
    expect(requestBody()).toBe(original)
  }
})

test('编辑多行表单参数后发送完整内容，新增字段保持输入焦点', async () => {
  const user = userEvent.setup()
  const { requestBody } = renderBody('{}')
  await user.click(screen.getByRole('radio', { name: 'x-www-form-urlencoded' }))
  const name = screen.getByRole('textbox', { name: '参数名 1' })
  await user.type(name, 'text')
  expect(name).toHaveFocus()
  const value = screen.getByRole('textbox', { name: '参数值 1' })
  await user.type(value, 'first{Enter}second')
  expect(new URLSearchParams(requestBody()).get('text')).toBe('first\nsecond')

  const tail = screen.getByRole('textbox', { name: '参数名 2' })
  await user.type(tail, 'next')
  expect(tail).toHaveValue('next')
  expect(tail).toHaveFocus()
  expect(screen.getByRole('textbox', { name: '参数名 3' })).toHaveValue('')
})

test('正文格式化支持撤销与重做，发送正文随历史恢复且保留原文换行', () => {
  const original = '{"name":"原文",\r\n"count":1}\r\n'
  const { view, onPatch, requestBody } = renderBody(original)

  fireEvent.click(screen.getByRole('button', { name: '格式化' }))

  const formatted = '{\r\n  "name": "原文",\r\n  "count": 1\r\n}'
  expect(view.state.sliceDoc()).toBe(formatted.replace(/\r\n/g, '\n'))
  expect(view.state.doc.lines).toBe(4)
  expect(view.state.doc.line(2).text).toBe('  "name": "原文",')
  expect(requestBody()).toBe(formatted)
  expect(onPatch).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({ body: formatted })
  )

  act(() => {
    expect(undo(view)).toBe(true)
  })
  expect(view.state.sliceDoc()).toBe(original.replace(/\r\n/g, '\n'))
  expect(requestBody()).toBe(original)

  act(() => {
    expect(redo(view)).toBe(true)
  })
  expect(view.state.sliceDoc()).toBe(formatted.replace(/\r\n/g, '\n'))
  expect(requestBody()).toBe(formatted)
})

test('正文格式化独立于前一次编辑，重复格式化不增加撤销步骤', () => {
  const original = '{"name":"原文",\r\n"count":1}\r\n'
  const edited = original.replace('原文', '编辑')
  const { view, requestBody } = renderBody(original)
  act(() => {
    const from = view.state.doc.toString().indexOf('原文')
    view.dispatch({
      changes: { from, to: from + 2, insert: '编辑' },
      userEvent: 'input.type',
    })
  })
  expect(requestBody()).toBe(edited)
  expect(undoDepth(view.state)).toBe(1)

  fireEvent.click(screen.getByRole('button', { name: '格式化' }))
  const formatted = '{\r\n  "name": "编辑",\r\n  "count": 1\r\n}'
  expect(requestBody()).toBe(formatted)
  expect(undoDepth(view.state)).toBe(2)
  fireEvent.click(screen.getByRole('button', { name: '格式化' }))
  expect(view.state.sliceDoc()).toBe(formatted.replace(/\r\n/g, '\n'))
  expect(requestBody()).toBe(formatted)
  expect(undoDepth(view.state)).toBe(2)

  act(() => {
    expect(undo(view)).toBe(true)
  })
  expect(view.state.sliceDoc()).toBe(edited.replace(/\r\n/g, '\n'))
  expect(requestBody()).toBe(edited)
  act(() => {
    expect(undo(view)).toBe(true)
  })
  expect(view.state.sliceDoc()).toBe(original.replace(/\r\n/g, '\n'))
  expect(requestBody()).toBe(original)
})

test('CRLF 正文复制多行后保留行结构和发送换行，撤销恢复原文', () => {
  const original = 'first\r\nsecond\r\nthird'
  const duplicated = 'first\r\nsecond\r\nfirst\r\nsecond\r\nthird'
  const { view, onPatch, requestBody } = renderBody(original)

  act(() => {
    view.dispatch({
      selection: { anchor: 0, head: view.state.doc.line(2).to },
    })
    expect(copyLineDown(view)).toBe(true)
  })

  expect(view.state.doc.lines).toBe(5)
  expect(view.state.sliceDoc()).toBe('first\nsecond\nfirst\nsecond\nthird')
  expect(requestBody()).toBe(duplicated)
  expect(onPatch).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({ body: duplicated })
  )

  act(() => {
    expect(undo(view)).toBe(true)
  })
  expect(view.state.doc.lines).toBe(3)
  expect(requestBody()).toBe(original)
  expect(onPatch).toHaveBeenLastCalledWith(
    expect.objectContaining({ body: original })
  )
})
