import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { Bridge, type ProxyStatus } from '@/lib/bridge'
import { hasWailsWebView } from '@/lib/platform'
import { ProxyStartup } from './ProxyStartup'
import { usePrefs } from './prefs'

vi.mock('@/lib/bridge', () => ({
  Bridge: { getProxyStatus: vi.fn(), retryProxy: vi.fn(), getConfig: vi.fn() },
}))
vi.mock('@/lib/platform', () => ({ hasWailsWebView: vi.fn() }))
vi.mock('@wailsio/runtime', () => ({ Application: { Quit: vi.fn() } }))
vi.mock('./shell/TitleBar', () => ({ TitleBar: () => <div>Sniffy</div> }))

const conflict: ProxyStatus = {
  port: 8080,
  running: false,
  errorCode: 'address_in_use',
  error: 'listen tcp :8080: bind: address already in use',
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(hasWailsWebView).mockReturnValue(true)
  vi.mocked(Bridge.getProxyStatus).mockResolvedValue(conflict)
  vi.mocked(Bridge.getConfig).mockResolvedValue({
    port: 9090,
    enableHTTPS: true,
    recording: true,
    systemProxy: true,
  })
  usePrefs.getState().set({ port: '8080', systemProxy: false })
})

test('端口冲突时显示原因，重试成功后进入工作台并同步代理状态', async () => {
  const user = userEvent.setup()
  let resolve!: (status: ProxyStatus) => void
  vi.mocked(Bridge.retryProxy).mockReturnValue(
    new Promise(done => {
      resolve = done
    })
  )
  render(
    <ProxyStartup>
      <div>抓包工作台</div>
    </ProxyStartup>
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    '端口 8080 已被其他程序占用'
  )
  expect(screen.queryByText('抓包工作台')).not.toBeInTheDocument()
  const port = screen.getByRole('spinbutton', { name: '代理端口' })
  await user.clear(port)
  await user.type(port, '9090')
  await user.click(screen.getByRole('button', { name: '启动代理' }))
  expect(Bridge.retryProxy).toHaveBeenCalledWith(9090)
  expect(screen.getByRole('button', { name: '正在启动…' })).toBeDisabled()
  await act(async () => resolve({ port: 9090, running: true }))
  expect(await screen.findByText('抓包工作台')).toBeVisible()
  expect(usePrefs.getState().port).toBe('9090')
  expect(usePrefs.getState().systemProxy).toBe(true)
})

test('重试仍失败时保留恢复入口，非法端口不能提交', async () => {
  const user = userEvent.setup()
  vi.mocked(Bridge.retryProxy).mockResolvedValue(conflict)
  render(
    <ProxyStartup>
      <div>抓包工作台</div>
    </ProxyStartup>
  )
  await user.click(await screen.findByRole('button', { name: '启动代理' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    '端口 8080 已被其他程序占用'
  )
  expect(screen.queryByText('抓包工作台')).not.toBeInTheDocument()
  const port = screen.getByRole('spinbutton', { name: '代理端口' })
  await user.clear(port)
  await user.type(port, '65536')
  expect(screen.getByRole('button', { name: '启动代理' })).toBeDisabled()
  expect(screen.getByText('请输入 1 到 65535 之间的整数。')).toBeVisible()
})

test('正常启动直接进入工作台', async () => {
  vi.mocked(Bridge.getProxyStatus).mockResolvedValue({
    port: 8080,
    running: true,
  })
  render(
    <ProxyStartup>
      <div>抓包工作台</div>
    </ProxyStartup>
  )
  expect(await screen.findByText('抓包工作台')).toBeVisible()
  expect(screen.queryByText('代理启动失败')).not.toBeInTheDocument()
})

test('浏览器预览不查询状态，直接进入工作台', async () => {
  vi.mocked(hasWailsWebView).mockReturnValue(false)
  render(
    <ProxyStartup>
      <div>抓包工作台</div>
    </ProxyStartup>
  )
  expect(screen.getByText('抓包工作台')).toBeVisible()
  expect(Bridge.getProxyStatus).not.toHaveBeenCalled()
})

test('桌面端状态查询失败时保留提示，重新检查后恢复', async () => {
  const user = userEvent.setup()
  vi.mocked(Bridge.getProxyStatus)
    .mockRejectedValueOnce(new Error('IPC 调用失败'))
    .mockResolvedValueOnce({ port: 8080, running: true })
  render(
    <ProxyStartup>
      <div>抓包工作台</div>
    </ProxyStartup>
  )
  expect(await screen.findByRole('alert')).toHaveTextContent('IPC 调用失败')
  expect(screen.getByText('无法获取代理状态')).toBeVisible()
  expect(screen.queryByText('抓包工作台')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '重新检查' }))
  expect(await screen.findByText('抓包工作台')).toBeVisible()
  expect(Bridge.getProxyStatus).toHaveBeenCalledTimes(2)
})
