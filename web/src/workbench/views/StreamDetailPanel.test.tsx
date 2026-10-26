import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { useAppStore } from '@/store'
import { resetWails } from '@/test/wails'
import type { StreamSession } from '@/types'
import type { TrafficRow } from '../lib/types'
import { StreamDetailPanel } from './StreamDetailPanel'

const session: StreamSession = {
  id: 'events',
  url: 'https://example.test/events',
  kind: 'sse',
  status: 'open',
  statusCode: 200,
  startTime: '2026-10-28T08:00:00Z',
  messageCount: 0,
  totalSize: 0,
  messages: [],
}

const row: TrafficRow = {
  id: session.id,
  seq: 1,
  kind: 'http',
  method: 'GET',
  scheme: 'https',
  host: 'example.test',
  path: '/events',
  url: session.url,
  state: 'pending',
  status: 200,
  reqHttpVersion: 'HTTP/1.1',
  resHttpVersion: 'HTTP/2.0',
  contentType: 'text/event-stream',
  contentKind: 'stream',
  startedAt: Date.parse(session.startTime),
}

beforeEach(() => {
  resetWails()
  useAppStore.setState({ streamSessions: [session] })
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    }
  )
})

afterEach(() => {
  useAppStore.setState({ streamSessions: [] })
  vi.unstubAllGlobals()
})

test.each(['sse', 'grpc', 'chunk'] as const)(
  '%s 流在消息和请求页签持续显示响应状态码与版本',
  kind => {
    useAppStore.setState({ streamSessions: [{ ...session, kind }] })
    const { rerender } = render(
      <StreamDetailPanel row={row} onClose={() => {}} />
    )
    expect(screen.getByText('200')).toHaveClass('text-ok')
    expect(screen.getByText('HTTP/2.0')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '请求' }))
    expect(screen.getByText('200')).toBeInTheDocument()
    expect(screen.getByText('HTTP/2.0')).toBeInTheDocument()
    expect(screen.getByText('HTTP/1.1')).toBeInTheDocument()

    rerender(
      <StreamDetailPanel
        row={{ ...row, state: 'error', status: 503 }}
        onClose={() => {}}
      />
    )
    expect(screen.getByText('503')).toHaveClass('text-danger')
    expect(screen.queryByText('200')).not.toBeInTheDocument()
    expect(screen.getByText('HTTP/2.0')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '消息' }))
    expect(screen.getByText('503')).toBeInTheDocument()
    expect(screen.getByText('HTTP/2.0')).toBeInTheDocument()
  }
)

test('HTTP 快照尚未收到状态码时使用流会话的状态码', () => {
  useAppStore.setState({
    streamSessions: [{ ...session, status: 'closed', statusCode: 401 }],
  })
  render(
    <StreamDetailPanel
      row={{
        ...row,
        state: 'completed',
        status: undefined,
        resHttpVersion: undefined,
      }}
      onClose={() => {}}
    />
  )
  expect(screen.getByText('401')).toHaveClass('text-warn')
  expect(screen.queryByText(/HTTP\//)).not.toBeInTheDocument()
})

test('响应头到达后实时更新摘要', () => {
  useAppStore.setState({ streamSessions: [] })
  const { rerender } = render(
    <StreamDetailPanel
      row={{ ...row, status: undefined, resHttpVersion: undefined }}
      onClose={() => {}}
    />
  )
  expect(screen.queryByText('200')).not.toBeInTheDocument()
  expect(screen.queryByText(/HTTP\//)).not.toBeInTheDocument()

  rerender(<StreamDetailPanel row={row} onClose={() => {}} />)
  expect(screen.getByText('200')).toBeInTheDocument()
  expect(screen.getByText('HTTP/2.0')).toBeInTheDocument()
})
