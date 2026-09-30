import { fireEvent, render, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { StreamSession } from '@/types'
import { StreamMessagesPane } from './StreamMessagesPane'

const ROW_HEIGHT = 30
const VIEWPORT_HEIGHT = 90

beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    }
  )

  // jsdom 不计算布局，也不会约束 scrollTop；这里模拟列表的浏览器滚动行为。
  const scrollPositions = new WeakMap<Element, number>()
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(
    VIEWPORT_HEIGHT
  )
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(
    function (this: Element) {
      return this.children.length * ROW_HEIGHT
    }
  )
  vi.spyOn(Element.prototype, 'scrollTop', 'get').mockImplementation(function (
    this: Element
  ) {
    return scrollPositions.get(this) ?? 0
  })
  vi.spyOn(Element.prototype, 'scrollTop', 'set').mockImplementation(function (
    this: Element,
    value
  ) {
    const maxScrollTop = Math.max(0, this.scrollHeight - this.clientHeight)
    scrollPositions.set(this, Math.max(0, Math.min(value, maxScrollTop)))
  })
})

afterEach(() => vi.unstubAllGlobals())

function createSession(count: number, id = 'events'): StreamSession {
  const timestamp = '2026-09-26T08:00:00Z'
  return {
    id,
    url: 'https://example.test/events',
    kind: 'sse',
    status: 'open',
    startTime: timestamp,
    messageCount: count,
    totalSize: count,
    messages: Array.from({ length: count }, (_, seq) => ({
      id: `${id}-${seq}`,
      sessionId: id,
      direction: 'inbound',
      kind: 'sse',
      data: `消息 ${seq}`,
      timestamp,
      seq,
      size: 1,
    })),
  }
}

function createPane(session: StreamSession) {
  return (
    <StreamMessagesPane
      session={session}
      topFrac={0.5}
      onTopFracChange={() => {}}
    />
  )
}

function getMessageList(container: HTMLElement): HTMLDivElement {
  const list = container.querySelector<HTMLDivElement>(
    '[data-find-region="messages"] > div'
  )
  if (!list) throw new Error('找不到流式消息列表')
  return list
}

test('接收 SSE 时跟随底部，上滚暂停，回到底部恢复', () => {
  const { container, rerender } = render(createPane(createSession(8)))
  const list = getMessageList(container)
  expect(list.scrollTop).toBe(150)

  rerender(createPane(createSession(9)))
  expect(list.scrollTop).toBe(180)

  list.scrollTop -= 3
  fireEvent.scroll(list)
  rerender(createPane(createSession(10)))
  expect(list.scrollTop).toBe(177)
  rerender(createPane(createSession(11)))
  expect(list.scrollTop).toBe(177)

  list.scrollTop = list.scrollHeight
  fireEvent.scroll(list)
  rerender(createPane(createSession(12)))
  expect(list.scrollTop).toBe(270)
})

test('选中消息后，收到新消息仍保持阅读位置', () => {
  const { container, rerender } = render(createPane(createSession(8)))
  const list = getMessageList(container)
  expect(list.scrollTop).toBe(150)

  fireEvent.click(within(list).getAllByRole('button')[0])
  rerender(createPane(createSession(9)))
  expect(list.scrollTop).toBe(150)
})

test('切换 SSE 会话后新列表从底部开始跟随', () => {
  const { container, rerender } = render(createPane(createSession(8)))
  const previousList = getMessageList(container)
  previousList.scrollTop = 60
  fireEvent.scroll(previousList)

  rerender(createPane(createSession(8, 'next-events')))
  const nextList = getMessageList(container)
  expect(nextList.scrollTop).toBe(150)
  rerender(createPane(createSession(9, 'next-events')))
  expect(nextList.scrollTop).toBe(180)
})

test('保留消息数不变时，收到新消息仍跟随底部', () => {
  const { container, rerender } = render(createPane(createSession(8)))
  const list = getMessageList(container)
  const trimmedSession = createSession(9)
  trimmedSession.messages = trimmedSession.messages.slice(1)

  // 浏览器在裁剪首行时会调整滚动锚点；布局阶段结束后才派发 scroll 事件。
  list.scrollTop -= ROW_HEIGHT
  rerender(createPane(trimmedSession))
  expect(list.scrollTop).toBe(150)
})

test('末条消息不变时，回填缺失消息后仍跟随底部', () => {
  const { container, rerender } = render(createPane(createSession(8)))
  const list = getMessageList(container)
  const incompleteSession = createSession(11)
  incompleteSession.messages = incompleteSession.messages.filter(
    msg => msg.seq < 8 || msg.seq === 10
  )

  rerender(createPane(incompleteSession))
  expect(list.scrollTop).toBe(180)

  rerender(createPane(createSession(11)))
  expect(list.scrollTop).toBe(240)
})
