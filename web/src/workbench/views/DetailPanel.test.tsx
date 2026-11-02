import {
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import i18n from '@/i18n'
import { bridgeName, resetWails, wails } from '@/test/wails'
import { useAppStore } from '@/store'
import type { HttpSession } from '@/types'
import { useTraffic } from '../data/useTraffic'
import { toRowFromHttp } from '../lib/format'
import { parsePausedFlow } from './breakpoints/model'
import { RequestPane, ResponsePane } from './DetailPanel'

vi.mock('./BodyViewer', () => ({
  UrlHighlight: ({ url }: { url: string }) => <span>{url}</span>,
  BodyViewer: () => null,
  RawCode: () => null,
}))

const session: HttpSession = {
  id: 'flow',
  status: 'completed',
  request: {
    id: 'flow',
    method: 'GET',
    url: 'https://example.test/',
    headers: {},
    timestamp: '2026-10-11T08:00:00Z',
    clientIP: '',
    host: 'example.test',
    path: '/',
    protocol: 'https',
  },
  executions: [
    { kind: 'rule', id: 'rule-1', name: '改写请求', phase: 'request' },
    { kind: 'plugin', id: 'plugin-1', name: '调试插件', phase: 'request' },
    { kind: 'plugin', id: 'plugin-1', name: '调试插件', phase: 'response' },
  ],
}

afterEach(() => {
  useAppStore.setState({ sessions: [], webSocketSessions: [], pausedFlows: [] })
})

test('详情分别显示两段 HTTP 版本，状态码显示在响应区', () => {
  resetWails()
  const row = {
    ...toRowFromHttp(session, 1),
    reqHttpVersion: 'HTTP/2.0',
    resHttpVersion: 'HTTP/1.1',
    status: 201,
  }
  const { container } = render(
    <>
      <RequestPane row={row} onClose={() => {}} />
      <ResponsePane row={row} />
    </>
  )
  const request = within(
    container.querySelector<HTMLElement>('[data-find-region="request"]')!
  )
  const response = within(
    container.querySelector<HTMLElement>('[data-find-region="response"]')!
  )
  expect(request.getByText('HTTP/2.0')).toBeInTheDocument()
  expect(request.queryByText('201')).not.toBeInTheDocument()
  expect(response.getByText('HTTP/1.1')).toBeInTheDocument()
  expect(response.getByText('201')).toBeInTheDocument()
})

test('详情显示执行顺序、类型、名称和阶段，标识置于悬停提示', () => {
  resetWails()
  render(<RequestPane row={toRowFromHttp(session, 1)} onClose={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: /执行/ }))
  const rows = within(screen.getByRole('table')).getAllByRole('row').slice(1)
  expect(
    rows.map(row =>
      within(row)
        .getAllByRole('cell')
        .map(cell => cell.getAttribute('aria-label') || cell.textContent)
    )
  ).toEqual([
    ['规则', '改写请求', '请求'],
    ['插件', '调试插件', '请求'],
    ['插件', '调试插件', '响应'],
  ])
  expect(screen.getByRole('cell', { name: '改写请求' })).toHaveAttribute('title', '改写请求\nrule-1')
})

test('未记录执行时显示空状态，插件缺少名称时显示标识', () => {
  resetWails()
  const row = toRowFromHttp({ ...session, executions: undefined }, 1)
  const { rerender } = render(<RequestPane row={row} onClose={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: /执行/ }))
  expect(screen.getByText('暂无执行记录')).toBeInTheDocument()
  rerender(
    <RequestPane
      row={{
        ...row,
        executions: [
          { kind: 'plugin', id: 'native-plugin', name: '', phase: 'response' },
        ],
      }}
      onClose={() => {}}
    />
  )
  expect(
    screen.getByRole('cell', { name: 'native-plugin' })
  ).toBeInTheDocument()
})

test('英文界面展示规则与插件记录', async () => {
  resetWails()
  await i18n.changeLanguage('en')
  render(<RequestPane row={toRowFromHttp(session, 1)} onClose={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: /Execution/ }))
  expect(screen.getByRole('cell', { name: 'Rule' })).toBeInTheDocument()
  expect(screen.getAllByRole('cell', { name: 'Plugin' })).toHaveLength(2)
})

test('点击规则名称打开对应配置窗口', async () => {
  resetWails()
  render(<RequestPane row={toRowFromHttp(session, 1)} onClose={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: /执行/ }))
  fireEvent.click(screen.getByRole('button', { name: '查看规则配置：改写请求' }))
  await waitFor(() => expect(wails.call).toHaveBeenCalledWith(bridgeName('OpenWindow'), 'rules', 'rule=rule-1'))
  await waitFor(() => expect(wails.emit).toHaveBeenCalledWith('rule_select', 'rule-1'))
})

test.each(['打开窗口', '发送定位事件'])('%s失败时给出提示', async (stage) => {
  resetWails()
  const error = new Error(`${stage}失败`)
  if (stage === '打开窗口') {
    wails.handlers.set('OpenWindow', () => Promise.reject(error))
  } else {
    wails.emit.mockRejectedValue(error)
  }
  render(<RequestPane row={toRowFromHttp(session, 1)} onClose={() => {}} />)
  fireEvent.click(screen.getByRole('button', { name: /执行/ }))
  fireEvent.click(screen.getByRole('button', { name: '查看规则配置：改写请求' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('打开规则窗口失败')
  expect(localStorage.getItem('sniffy-rule-selection')).toBeNull()
})

test('断点暂停时从暂停快照显示执行记录', () => {
  resetWails()
  const paused = parsePausedFlow({
    id: session.id,
    pausedAt: 'response',
    request: {},
    executions: [...session.executions!, { kind: 'invalid', phase: 'request' }],
  })!
  useAppStore.setState({
    sessions: [{ ...session, status: 'pending', executions: undefined }],
    webSocketSessions: [],
    pausedFlows: [paused],
  })
  const { result } = renderHook(useTraffic)
  expect(result.current.rows[0].paused).toBe(true)
  expect(result.current.rows[0].executions).toEqual(session.executions)
})
