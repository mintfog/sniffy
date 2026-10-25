import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { bridgeName, deferred, resetWails, wails } from '@/test/wails'
import { useAppStore } from '@/store'
import Workbench from './Workbench'
import { backfillImported } from './data/useBackendSync'
import { usePrefs } from './prefs'
import type { HttpSession, WebSocketMessage, WebSocketSession } from '@/types'

beforeEach(() => {
  resetWails()
  usePrefs.getState().set({ skipCertificatePrompt: false })
  useAppStore.getState().clearAllData()
  useAppStore.getState().setConnected(false)
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    }
  )
  for (const name of ['GetSessions', 'GetWSSessions', 'GetStreamSessions']) {
    wails.handlers.set(name, () => ({ data: [], total: 0 }))
  }
  for (const name of ['GetBreakpoints', 'GetBreakRules', 'GetLanIPs']) {
    wails.handlers.set(name, () => [])
  }
  wails.handlers.set('GetConfig', () => ({}))
  wails.handlers.set('GetSession', () => null)
  wails.handlers.set('GetWSSession', () => null)
})
afterEach(() => vi.unstubAllGlobals())

async function openFileMenu() {
  const user = userEvent.setup()
  render(<Workbench />)
  await waitFor(() => expect(useAppStore.getState().isConnected).toBe(true))
  await user.click(screen.getByRole('button', { name: '流量' }))
  return user
}

function httpSession(
  id: string,
  status: HttpSession['status'] = 'pending'
): HttpSession {
  return {
    id,
    status,
    request: {
      id,
      method: 'GET',
      url: `https://example.com/${id}`,
      headers: {},
      timestamp: '2026-10-08T10:00:00Z',
      clientIP: '',
      host: 'example.com',
      path: `/${id}`,
      protocol: 'https',
    },
  }
}

function emitTraffic(name: string, data: unknown) {
  act(() => {
    wails.listeners.get(name)?.forEach(listener => listener({ data }))
  })
}

function wsSession(
  id: string,
  messages: WebSocketMessage[] = []
): WebSocketSession {
  return {
    id,
    url: `wss://example.com/${id}`,
    status: 'open',
    startTime: '2026-10-08T10:00:00Z',
    messageCount: messages.length,
    totalSize: messages.length,
    messages,
  }
}

test('macOS 工作台提供输入框所需的系统编辑角色', async () => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)'
  )
  render(<Workbench />)

  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(
      bridgeName('SetMenu'),
      expect.arrayContaining([
        expect.objectContaining({
          kind: 'submenu',
          label: '编辑',
          items: expect.arrayContaining(
            ['undo', 'redo', 'cut', 'copy', 'paste'].map(role =>
              expect.objectContaining({ kind: 'role', role })
            )
          ),
        }),
      ])
    )
  )
})

test('导入后只按返回的 ID 补齐列表，不恢复本地已删除的会话', async () => {
  const user = await openFileMenu()
  wails.handlers.set('ImportSessions', () => ({
    imported: 2,
    ids: ['http-new', 'ws-new'],
  }))
  wails.handlers.set('GetSession', id =>
    id === 'http-new' ? httpSession('http-new') : null
  )
  wails.handlers.set('GetWSSession', id =>
    id === 'ws-new' ? wsSession('ws-new') : null
  )
  await user.click(screen.getByText('导入 HAR/JSON…'))
  expect(await screen.findByText('导入完成')).toBeVisible()
  expect(screen.getByText(/已导入 2 条会话/)).toBeVisible()
  await waitFor(() =>
    expect(useAppStore.getState().sessions.map(s => s.id)).toEqual(['http-new'])
  )
  await waitFor(() =>
    expect(useAppStore.getState().webSocketSessions.map(s => s.id)).toEqual([
      'ws-new',
    ])
  )
  expect(wails.call).not.toHaveBeenCalledWith(
    bridgeName('GetSession'),
    'deleted-http'
  )
  expect(wails.call).not.toHaveBeenCalledWith(
    bridgeName('GetWSSession'),
    'deleted-ws'
  )
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('GetSessions'))
  ).toHaveLength(1)
  expect(
    wails.call.mock.calls.filter(
      ([name]) => name === bridgeName('GetWSSessions')
    )
  ).toHaveLength(1)
})

test('回填不覆盖已经通过事件到达的会话', async () => {
  const pending = deferred<HttpSession | null>()
  const user = await openFileMenu()
  wails.handlers.set('ImportSessions', () => ({ imported: 1, ids: ['a'] }))
  wails.handlers.set('GetSession', () => pending.promise)
  await user.click(screen.getByText('导入 HAR/JSON…'))
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('GetSession'), 'a')
  )
  emitTraffic('flow_updated', httpSession('a', 'completed'))
  await act(async () => {
    pending.resolve(httpSession('a'))
  })
  const sessions = useAppStore.getState().sessions
  expect(sessions).toHaveLength(1)
  expect(sessions[0].status).toBe('completed')
})

test('导入的 WS 元数据已到达时，回填补齐消息列表', async () => {
  const user = await openFileMenu()
  const full = wsSession('ws-new', [
    {
      id: 'message',
      sessionId: 'ws-new',
      type: 'text',
      direction: 'inbound',
      data: 'hello',
      timestamp: '2026-10-08T10:00:00Z',
      size: 5,
    },
  ])
  wails.handlers.set('ImportSessions', () => {
    wails.listeners.get('ws_message')?.forEach(listener =>
      listener({
        data: { session: { ...full, messages: [] }, retained: 1 },
      })
    )
    return { imported: 1, ids: ['ws-new'] }
  })
  wails.handlers.set('GetWSSession', () => full)
  await user.click(screen.getByText('导入 HAR/JSON…'))
  await waitFor(() =>
    expect(useAppStore.getState().webSocketSessions[0]?.messages).toEqual(
      full.messages
    )
  )
  expect(wails.call).toHaveBeenCalledWith(bridgeName('GetWSSession'), 'ws-new')
})

test('WS 回填保留查询期间收到的更新消息', async () => {
  const pending = deferred<WebSocketSession | null>()
  const user = await openFileMenu()
  const full = wsSession('ws-new', [
    {
      id: 'first',
      sessionId: 'ws-new',
      type: 'text',
      direction: 'inbound',
      data: 'first',
      timestamp: '2026-10-08T10:00:00Z',
      size: 5,
    },
  ])
  act(() => useAppStore.getState().setWebSocketSessions([full]))
  wails.handlers.set('ImportSessions', () => ({ imported: 1, ids: ['ws-new'] }))
  wails.handlers.set('GetWSSession', () => pending.promise)
  await user.click(screen.getByText('导入 HAR/JSON…'))
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(
      bridgeName('GetWSSession'),
      'ws-new'
    )
  )
  const latest = { ...full.messages[0], id: 'latest', data: 'latest' }
  emitTraffic('ws_message', {
    session: { ...full, messageCount: 2, messages: [] },
    message: latest,
    retained: 2,
  })
  await act(async () => {
    pending.resolve(full)
  })
  expect(
    useAppStore.getState().webSocketSessions[0].messages.map(m => m.id)
  ).toEqual(['first', 'latest'])
})

test('导入回填限制同时查询的数量', async () => {
  const gate = deferred<HttpSession | null>()
  const ids = Array.from({ length: 20 }, (_, i) => `imported-${i}`)
  wails.handlers.set('GetSession', () => gate.promise)
  const pending = backfillImported(ids, () => false)
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('GetSession'))
  ).toHaveLength(8)
  gate.resolve(null)
  await pending
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('GetSession'))
  ).toHaveLength(ids.length)
})

test('回填期间清空后，迟到的快照不写回记录', async () => {
  const pending = deferred<HttpSession | null>()
  const user = await openFileMenu()
  wails.handlers.set('ImportSessions', () => ({ imported: 1, ids: ['a'] }))
  wails.handlers.set('GetSession', () => pending.promise)
  await user.click(screen.getByText('导入 HAR/JSON…'))
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('GetSession'), 'a')
  )
  emitTraffic('data_cleared', undefined)
  await act(async () => {
    pending.resolve(httpSession('a'))
  })
  expect(useAppStore.getState().sessions).toEqual([])
})

test('取消导入保持当前列表，解析失败显示原因', async () => {
  wails.handlers.set('ImportSessions', () => null)
  const user = await openFileMenu()
  await user.click(screen.getByText('导入 HAR/JSON…'))
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('ImportSessions'))
  )
  expect(screen.queryByText('导入完成')).not.toBeInTheDocument()
  wails.handlers.set('ImportSessions', () => {
    throw new Error('第 2 条会话: 正文 base64 编码无效')
  })
  await user.click(screen.getByRole('button', { name: '流量' }))
  await user.click(screen.getByText('导入 HAR/JSON…'))
  expect(await screen.findByText('导入失败')).toBeVisible()
  expect(screen.getByText(/第 2 条会话/)).toBeVisible()
})

test('空筛选范围导出空 ID 列表，写盘失败可见', async () => {
  wails.handlers.set('ExportSessions', () => {
    throw new Error('磁盘空间不足')
  })
  const user = await openFileMenu()
  await user.click(screen.getByText('导出 HAR…'))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('ExportSessions'), [])
  expect(await screen.findByText('导出失败')).toBeVisible()
  expect(screen.getByText(/磁盘空间不足/)).toBeVisible()
})

test('桌面 JSON 导出保留流量行数组', async () => {
  const user = await openFileMenu()
  act(() =>
    useAppStore
      .getState()
      .setSessions([httpSession('json-export', 'completed')])
  )
  await user.click(screen.getByText('导出 JSON…'))
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(
      bridgeName('SaveTextFile'),
      expect.stringMatching(/\.json$/),
      expect.any(String)
    )
  )
  const call = wails.call.mock.calls.find(
    ([name]) => name === bridgeName('SaveTextFile')
  )!
  expect(JSON.parse(call[2])).toEqual([
    expect.objectContaining({
      kind: 'http',
      method: 'GET',
      url: 'https://example.com/json-export',
    }),
  ])
})

test('连接指南请求本机安装时，先在客户端确认再调用系统安装', async () => {
  const user = userEvent.setup()
  wails.handlers.set('InstallCAToSystem', () => undefined)
  render(<Workbench />)
  await waitFor(() => expect(wails.listeners.has('certificate_install_requested')).toBe(true))

  emitTraffic('certificate_install_requested', undefined)

  expect(await screen.findByRole('alertdialog')).toHaveTextContent('安装根证书到本机')
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))
  await user.click(screen.getByRole('button', { name: '安装到本机' }))
  await waitFor(() => expect(wails.call).toHaveBeenCalledWith(bridgeName('InstallCAToSystem')))
})

test('启动时未信任当前根证书则提示安装，取消后本次挂载不再提示', async () => {
  const user = userEvent.setup()
  wails.handlers.set('IsCATrusted', () => false)
  const { rerender } = render(<Workbench />)

  const dialog = await screen.findByRole('alertdialog')
  expect(dialog).toHaveTextContent('安装根证书到本机')
  expect(dialog).toHaveTextContent('本机尚未信任当前 Sniffy 根证书')
  expect(dialog).toHaveTextContent('HTTPS')
  expect(wails.call).toHaveBeenCalledWith(bridgeName('IsCATrusted'))
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))

  await user.click(screen.getByRole('button', { name: '取消' }))
  rerender(<Workbench />)

  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(
    wails.call.mock.calls.filter(([name]) => name === bridgeName('IsCATrusted'))
  ).toHaveLength(1)
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))
})

test.each([
  ['当前根证书已受信任', (): boolean => true],
  [
    '无法检测系统信任状态',
    (): boolean => {
      throw new Error('无法读取系统信任库')
    },
  ],
] as const)('启动时%s不弹安装提示', async (_name, checkTrust) => {
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  wails.handlers.set('IsCATrusted', checkTrust)
  await act(async () => {
    render(<Workbench />)
  })

  expect(wails.call).toHaveBeenCalledWith(bridgeName('IsCATrusted'))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))
})

test('确认启动证书提示后安装，完成时显示成功结果', async () => {
  const user = userEvent.setup()
  const installation = deferred<void>()
  wails.handlers.set('IsCATrusted', () => false)
  wails.handlers.set('InstallCAToSystem', () => installation.promise)
  render(<Workbench />)

  await user.click(await screen.findByRole('button', { name: '安装到本机' }))

  expect(wails.call).toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))
  expect(screen.getByRole('button', { name: '安装到本机' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '不再提示' })).toBeDisabled()
  await user.keyboard('{Escape}')
  expect(screen.getByRole('alertdialog')).toHaveTextContent('安装根证书到本机')
  await act(async () => {
    installation.resolve()
  })

  expect(screen.getByRole('alertdialog')).toHaveTextContent('安装成功')
  expect(screen.getByRole('alertdialog')).toHaveTextContent(
    '已成功安装到系统信任库。'
  )
})

test('启动提示中的证书安装失败时显示失败原因', async () => {
  const user = userEvent.setup()
  wails.handlers.set('IsCATrusted', () => false)
  wails.handlers.set('InstallCAToSystem', () => {
    throw new Error('无法写入系统信任库')
  })
  render(<Workbench />)

  await user.click(await screen.findByRole('button', { name: '安装到本机' }))

  expect(await screen.findByText('安装失败')).toBeVisible()
  expect(screen.getByRole('alertdialog')).toHaveTextContent('无法写入系统信任库')
  expect(screen.queryByRole('button', { name: '安装到本机' })).not.toBeInTheDocument()
})

test('启动检测延迟返回时不重开已手动取消的证书安装提示', async () => {
  const user = userEvent.setup()
  const trust = deferred<boolean>()
  wails.handlers.set('IsCATrusted', () => trust.promise)
  render(<Workbench />)
  await waitFor(() => {
    expect(wails.call).toHaveBeenCalledWith(bridgeName('IsCATrusted'))
    expect(wails.listeners.has('certificate_install_requested')).toBe(true)
  })

  emitTraffic('certificate_install_requested', undefined)
  await user.click(await screen.findByRole('button', { name: '取消' }))
  await act(async () => {
    trust.resolve(false)
  })

  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))
})

test('不再提示会保存偏好并跳过后续启动检测，仍可手动安装', async () => {
  const user = userEvent.setup()
  wails.handlers.set('IsCATrusted', () => false)
  const { unmount } = render(<Workbench />)

  await user.click(await screen.findByRole('button', { name: '不再提示' }))

  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(usePrefs.getState().skipCertificatePrompt).toBe(true)
  const saved = JSON.parse(localStorage.getItem('sniffy-prefs')!)
  expect(saved.state.skipCertificatePrompt).toBe(true)
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))

  unmount()
  wails.call.mockClear()
  await act(async () => {
    render(<Workbench />)
  })

  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('IsCATrusted'))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()

  emitTraffic('certificate_install_requested', undefined)

  expect(screen.getByRole('alertdialog')).toHaveTextContent('安装根证书到本机')
  expect(screen.queryByRole('button', { name: '不再提示' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '安装到本机' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('InstallCAToSystem'))
})
