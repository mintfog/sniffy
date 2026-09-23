import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { bridgeName, deferred, resetWails, wails } from '@/test/wails'
import type { HttpSession, StreamMessage, StreamSession } from '@/types'
import { formatClock } from '../lib/format'
import { ComposeView } from './ComposeView'
import { useOutboundSessions } from './compose/useOutbound'

const id = 'compose-events'
const timestamp = '2026-09-23T08:00:00Z'
const http: HttpSession = {
  id,
  status: 'pending',
  request: {
    id,
    method: 'POST',
    url: 'https://example.test/events',
    headers: {},
    timestamp,
    clientIP: '',
    host: 'example.test',
    path: '/events',
    protocol: 'HTTP/1.1',
  },
  response: {
    id,
    requestId: id,
    status: 200,
    statusText: 'OK',
    headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
    timestamp,
    size: 0,
    responseTime: 10,
  },
}
const stream: StreamSession = {
  id,
  url: http.request.url,
  kind: 'sse',
  status: 'open',
  method: 'POST',
  statusCode: 200,
  startTime: timestamp,
  messageCount: 1,
  totalSize: 12,
  messages: [
    {
      id: 'event-1',
      sessionId: id,
      direction: 'inbound',
      kind: 'sse',
      eventType: 'progress',
      data: '收到请求',
      timestamp,
      seq: 0,
      size: 12,
    },
  ],
}

function emit(name: string, data: unknown) {
  wails.listeners.get(name)?.forEach(listener => listener({ data }))
}

beforeEach(() => {
  resetWails()
  localStorage.clear()
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    }
  )
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    value: vi.fn(),
  })
  wails.handlers.set('SendRequest', () => id)
  wails.handlers.set('GetSession', () => ({ ...http, response: undefined }))
  wails.handlers.set('GetStreamSession', () => null)
})

afterEach(() => vi.unstubAllGlobals())

async function sendCurl() {
  const user = userEvent.setup()
  render(<ComposeView />)
  fireEvent.paste(screen.getByRole('textbox', { name: 'URL' }), {
    clipboardData: {
      getData: () =>
        `curl 'https://example.test/events' -H 'Accept: */*' -H 'Content-Type: application/json' --data '{"prompt":"生成脚本"}'`,
    },
  })
  await user.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('GetSession'), id)
  )
  return user
}

test('cURL 导入的 POST 按响应实时显示 SSE，停止后可再次发送', async () => {
  const user = await sendCurl()
  expect(wails.call).toHaveBeenCalledWith(
    bridgeName('SendRequest'),
    expect.objectContaining({
      kind: 'http',
      method: 'POST',
      body: '{"prompt":"生成脚本"}',
      headers: expect.arrayContaining([
        ['Accept', '*/*'],
        ['Content-Type', 'application/json'],
      ]),
    })
  )
  act(() => {
    emit('flow_updated', http)
    emit('stream_message', {
      session: { ...stream, messages: [] },
      message: stream.messages[0],
      retained: 1,
    })
  })
  expect(await screen.findByText('PROGRESS')).toBeVisible()
  expect(screen.getByText('收到请求')).toBeVisible()
  expect(screen.getByText('1 条记录')).toBeVisible()
  expect(screen.getByRole('button', { name: '停止' })).toBeEnabled()

  wails.handlers.set('StopStream', () => {
    emit('stream_message', {
      session: { ...stream, messages: [], status: 'closed' },
      retained: 1,
    })
    emit('flow_updated', { ...http, status: 'completed' })
    return true
  })
  await user.click(screen.getByRole('button', { name: '停止' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('StopStream'), id)
  expect(await screen.findByRole('button', { name: '发送' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: '发送' }))
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('SendRequest'))
  ).toHaveLength(2)
})

test('流事件早于发送返回时，通过回填显示已结束的 SSE', async () => {
  wails.handlers.set('GetSession', () => ({ ...http, status: 'completed' }))
  wails.handlers.set('GetStreamSession', () => ({
    ...stream,
    status: 'closed',
  }))
  await sendCurl()
  expect(await screen.findByText('PROGRESS')).toBeVisible()
  expect(screen.getByText('1 条记录')).toBeVisible()
  expect(screen.getByRole('button', { name: '发送' })).toBeEnabled()
})

test('首轮回填后漏掉流事件，可根据完成响应头补回 SSE', async () => {
  await sendCurl()
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('GetStreamSession'), id)
  )
  wails.handlers.set('GetStreamSession', () => ({
    ...stream,
    status: 'closed',
  }))
  act(() => emit('flow_updated', { ...http, status: 'completed' }))
  expect(await screen.findByText('PROGRESS')).toBeVisible()
})

test('心跳、数据事件和控制块按接收顺序实时显示，可查看心跳原文', async () => {
  const user = await sendCurl()
  const heartbeat: StreamMessage = {
    id: 'heartbeat',
    sessionId: id,
    direction: 'inbound',
    kind: 'sse',
    sseType: 'comment',
    data: ': ping\n\n',
    timestamp,
    seq: 0,
    size: 8,
  }
  act(() => {
    emit('flow_updated', http)
    emit('stream_message', {
      session: { ...stream, messages: [], totalSize: 8 },
      message: heartbeat,
      retained: 1,
    })
  })
  const row = await screen.findByRole('button', { name: /COMMENT : ping/ })
  expect(within(row).getByText('8B')).toBeVisible()
  expect(
    within(row).getByText(formatClock(Date.parse(timestamp)))
  ).toBeVisible()
  expect(screen.getByText('1 条记录')).toBeVisible()
  expect(screen.getByRole('button', { name: '停止' })).toBeEnabled()
  await user.click(row)
  expect(screen.getAllByText(': ping')).toHaveLength(2)
  await user.click(screen.getByRole('button', { name: '复制' }))
  expect(await navigator.clipboard.readText()).toBe(': ping\n\n')

  act(() =>
    emit('stream_message', {
      session: { ...stream, messages: [], messageCount: 2, totalSize: 20 },
      message: { ...stream.messages[0], seq: 1 },
      retained: 2,
    })
  )
  expect(await screen.findByText('PROGRESS')).toBeVisible()
  expect(screen.getByText('2 条记录')).toBeVisible()

  act(() =>
    emit('stream_message', {
      session: { ...stream, messages: [], messageCount: 3, totalSize: 33 },
      message: {
        ...heartbeat,
        id: 'retry',
        sseType: 'control',
        data: 'retry: 3000\n\n',
        seq: 2,
        size: 13,
      },
      retained: 3,
    })
  )
  expect(await screen.findByText('CONTROL')).toBeVisible()
  expect(screen.getByText('3 条记录')).toBeVisible()
  const records = screen.getAllByRole('button', {
    name: /COMMENT|PROGRESS|CONTROL/,
  })
  expect(records).toHaveLength(3)
  expect(records[0]).toHaveAccessibleName(/COMMENT : ping/)
  expect(records[1]).toHaveAccessibleName(/PROGRESS 收到请求/)
  expect(records[2]).toHaveAccessibleName(/CONTROL retry: 3000/)
})

test('HTTP 已完成但首轮流回填返回空时，定期对账仍会补回事件', async () => {
  vi.useFakeTimers()
  const firstFetch = deferred<StreamSession | null>()
  wails.handlers.set('GetSession', () => ({ ...http, status: 'completed' }))
  wails.handlers.set('GetStreamSession', () => firstFetch.promise)
  const { result } = renderHook(useOutboundSessions)
  await act(async () => result.current.track(id, 'http'))
  await act(async () => firstFetch.resolve(null))
  expect(result.current.stream[id]).toBeUndefined()
  wails.handlers.set('GetStreamSession', () => ({
    ...stream,
    status: 'closed',
  }))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000)
  })
  expect(result.current.stream[id].messages[0].data).toBe('收到请求')
})

test.each(['completed', 'error'] as const)(
  'SSE 关闭通知丢失时，HTTP %s 通知补回流终态并恢复发送',
  async status => {
    await sendCurl()
    act(() => {
      emit('flow_updated', http)
      emit('stream_message', {
        session: { ...stream, messages: [] },
        message: stream.messages[0],
        retained: 1,
      })
    })
    expect(screen.getByRole('button', { name: '停止' })).toBeEnabled()
    const finalMessage: StreamMessage = {
      ...stream.messages[0],
      id: 'event-2',
      eventType: 'done',
      data: '完成',
      seq: 1,
      size: 6,
    }
    wails.handlers.set('GetStreamSession', () => ({
      ...stream,
      status: 'closed',
      endTime: timestamp,
      messageCount: 2,
      totalSize: 18,
      messages: [...stream.messages, finalMessage],
    }))
    act(() => emit('flow_updated', { ...http, status }))
    expect(await screen.findByRole('button', { name: '发送' })).toBeEnabled()
    expect(screen.getByText('收到请求')).toBeVisible()
    expect(screen.getByText('完成')).toBeVisible()
    expect(
      screen.getAllByRole('button', { name: /PROGRESS|DONE/ })
    ).toHaveLength(2)
  }
)

test('SSE 关闭通知丢失且首轮回填在途时，轮询补回流终态并停止重拉', async () => {
  vi.useFakeTimers()
  const firstFetch = deferred<StreamSession | null>()
  wails.handlers.set('GetStreamSession', () => firstFetch.promise)
  const { result } = renderHook(useOutboundSessions)
  await act(async () => result.current.track(id, 'http'))
  await act(async () => {
    emit('stream_message', {
      session: { ...stream, messages: [] },
      message: stream.messages[0],
      retained: 1,
    })
    emit('flow_updated', { ...http, status: 'completed' })
  })
  await act(async () => firstFetch.resolve(stream))
  const refetch = vi
    .fn()
    .mockRejectedValueOnce(new Error('暂时无法读取流快照'))
    .mockResolvedValueOnce(null)
    .mockResolvedValue({ ...stream, status: 'closed', endTime: timestamp })
  wails.handlers.set('GetStreamSession', refetch)
  for (let i = 0; i < 3; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })
  }
  expect(result.current.stream[id].status).toBe('closed')
  expect(result.current.stream[id].messages).toEqual(stream.messages)
  expect(refetch).toHaveBeenCalledTimes(3)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(6000)
  })
  expect(refetch).toHaveBeenCalledTimes(3)
})

test('SSE 已建立时每十秒核对 HTTP 状态，事件时间线按需回填', async () => {
  vi.useFakeTimers()
  wails.handlers.set('GetSession', () => http)
  wails.handlers.set('GetStreamSession', () => stream)
  const { result } = renderHook(useOutboundSessions)
  await act(async () => result.current.track(id, 'http'))
  wails.call.mockClear()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(6000)
  })
  expect(result.current.stream[id].status).toBe('open')
  expect(wails.call).not.toHaveBeenCalled()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(24000)
  })
  expect(wails.call.mock.calls).toEqual(
    Array.from({ length: 3 }, () => [bridgeName('GetSession'), id])
  )
})

test('SSE 关闭与 HTTP 结束通知同时丢失时，自动恢复发送并补回最后事件', async () => {
  vi.useFakeTimers()
  render(<ComposeView />)
  fireEvent.change(screen.getByRole('textbox', { name: 'URL' }), {
    target: { value: http.request.url },
  })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
  })
  await act(async () => {
    emit('flow_updated', http)
    emit('stream_message', {
      session: { ...stream, messages: [] },
      message: stream.messages[0],
      retained: 1,
    })
  })
  expect(screen.getByRole('button', { name: '停止' })).toBeEnabled()
  wails.handlers.set('GetSession', () => ({ ...http, status: 'completed' }))
  wails.handlers.set('GetStreamSession', () => ({
    ...stream,
    status: 'closed',
    endTime: timestamp,
    messageCount: 2,
    messages: [
      ...stream.messages,
      {
        ...stream.messages[0],
        id: 'final',
        seq: 1,
        data: '完成',
      },
    ],
  }))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  expect(screen.getByRole('button', { name: '发送' })).toBeEnabled()
  expect(screen.getByText('完成')).toBeVisible()
  wails.call.mockClear()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20000)
  })
  expect(wails.call).not.toHaveBeenCalled()
})

test('SSE 完整 mock 在终态后确认流不存在，停止查询并随会话回收清理', async () => {
  vi.useFakeTimers()
  const getStream = vi.fn(() => null)
  wails.handlers.set('GetStreamSession', getStream)
  const { result } = renderHook(useOutboundSessions)
  await act(async () => result.current.track(id, 'http'))
  getStream.mockClear()
  await act(async () => emit('flow_updated', { ...http, status: 'completed' }))
  expect(getStream).toHaveBeenCalledTimes(1)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20000)
    emit('flow_updated', { ...http, status: 'completed' })
  })
  expect(getStream).toHaveBeenCalledTimes(1)
  expect(result.current.stream[id]).toBeUndefined()
  await act(async () => result.current.retain(new Set()))
  wails.handlers.set('GetSession', () => ({ ...http, status: 'completed' }))
  wails.handlers.set('GetStreamSession', () => ({
    ...stream,
    status: 'closed',
  }))
  await act(async () => result.current.track(id, 'http'))
  expect(result.current.stream[id].status).toBe('closed')
})

test('终态后的快照查询失败时继续重试，成功返回空后停止', async () => {
  vi.useFakeTimers()
  const { result } = renderHook(useOutboundSessions)
  await act(async () => result.current.track(id, 'http'))
  const getStream = vi
    .fn()
    .mockRejectedValueOnce(new Error('查询失败'))
    .mockResolvedValue(null)
  wails.handlers.set('GetStreamSession', getStream)
  await act(async () => emit('flow_updated', { ...http, status: 'completed' }))
  expect(getStream).toHaveBeenCalledTimes(1)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000)
  })
  expect(getStream).toHaveBeenCalledTimes(2)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000)
  })
  expect(getStream).toHaveBeenCalledTimes(2)
})

test('SSE 已收到关闭通知时，在途的同号 open 快照不会重新打开流', async () => {
  wails.handlers.set('GetSession', () => http)
  wails.handlers.set('GetStreamSession', () => stream)
  const { result } = renderHook(useOutboundSessions)
  await act(async () => result.current.track(id, 'http'))
  const staleFetch = deferred<StreamSession | null>()
  const refetch = vi.fn(() => staleFetch.promise)
  wails.handlers.set('GetStreamSession', refetch)
  await act(async () => emit('flow_updated', { ...http, status: 'completed' }))
  expect(refetch).toHaveBeenCalledTimes(1)
  await act(async () =>
    emit('stream_message', {
      session: {
        ...stream,
        messages: [],
        status: 'closed',
        endTime: timestamp,
      },
      retained: 1,
    })
  )
  await act(async () => staleFetch.resolve(stream))
  expect(result.current.stream[id].status).toBe('closed')
  expect(result.current.stream[id].endTime).toBe(timestamp)
})

test.each(['等待响应头', '事件流进行中'])(
  '关闭页签会取消请求：%s',
  async phase => {
    const user = await sendCurl()
    if (phase === '事件流进行中') {
      act(() =>
        emit('stream_message', {
          session: { ...stream, messages: [] },
          message: stream.messages[0],
          retained: 1,
        })
      )
      expect(await screen.findByRole('button', { name: '停止' })).toBeEnabled()
    }
    await user.click(screen.getByRole('button', { name: '关闭页签' }))
    await waitFor(() =>
      expect(wails.call).toHaveBeenCalledWith(bridgeName('StopStream'), id)
    )
  }
)
