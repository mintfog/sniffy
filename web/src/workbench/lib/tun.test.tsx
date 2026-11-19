import { act, render, renderHook, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { bridgeName, deferred, resetWails, wails } from '@/test/wails'
import type { TunStatus } from '@/lib/bridge'
import { TunDialogs } from '../views/tun/TunDialogs'
import {
  TUN_STABLE_SYNC_MS,
  TUN_TRANSIENT_SYNC_MS,
  acceptTunStatus,
  dismissTunNotice,
  toggleTun,
  tunPhaseOf,
  useTun,
  useTunSync,
} from './tun'

function tun(revision: number, state: TunStatus['state'], patch: Partial<TunStatus> = {}): TunStatus {
  return { revision, state, ...patch }
}

function emitTun(status: TunStatus) {
  act(() => {
    wails.listeners.get('tun_state')?.forEach((listener) => listener({ data: status }))
  })
}

beforeEach(() => {
  resetWails()
  useTun.setState(useTun.getInitialState(), true)
})

test('查询结果与推送交错时只采纳 revision 更高的快照', async () => {
  const initial = deferred<TunStatus>()
  wails.handlers.set('GetTunStatus', () => initial.promise)
  renderHook(useTunSync)
  emitTun(tun(3, 'running', { name: 'sniffy0' }))
  await act(async () => initial.resolve(tun(1, 'starting')))
  expect(useTun.getState().status).toEqual(tun(3, 'running', { name: 'sniffy0' }))
  emitTun(tun(2, 'starting'))
  expect(useTun.getState().status.state).toBe('running')
})

test('新出现的出错原因弹窗告知，打开窗口前就有的只常驻显示', () => {
  acceptTunStatus(tun(5, 'failed', { error: '没有 IPv4 默认路由' }))
  expect(useTun.getState().notice).toBeNull()

  acceptTunStatus(tun(6, 'starting'))
  acceptTunStatus(tun(7, 'failed', { error: '没有 IPv4 默认路由' }))
  expect(useTun.getState().notice).toEqual({ error: '没有 IPv4 默认路由', stuck: false })

  // 开启失败而网卡可能还在：先停在带原因的 stopping，网卡消失后以同一原因变为 failed，不再重复弹出。
  const lingering = '等待 helper 开启网卡: i/o timeout'
  useTun.setState({ notice: null })
  acceptTunStatus(tun(8, 'starting'))
  acceptTunStatus(tun(9, 'stopping', { error: lingering }))
  expect(useTun.getState().notice).toEqual({ error: lingering, stuck: true })
  useTun.setState({ notice: null })
  acceptTunStatus(tun(10, 'failed', { error: lingering }))
  expect(useTun.getState().notice).toBeNull()
})

test('开启、关闭期间缩短回查间隔，卸载后停止回查', async () => {
  vi.useFakeTimers()
  let current = tun(1, 'starting')
  wails.handlers.set('GetTunStatus', () => current)
  const { unmount } = renderHook(useTunSync)
  await act(async () => {})
  current = tun(2, 'running')
  await act(async () => {
    await vi.advanceTimersByTimeAsync(TUN_TRANSIENT_SYNC_MS)
  })
  expect(useTun.getState().status.state).toBe('running')

  current = tun(3, 'off')
  await act(async () => {
    await vi.advanceTimersByTimeAsync(TUN_TRANSIENT_SYNC_MS * 2)
  })
  expect(useTun.getState().status.state).toBe('running')
  await act(async () => {
    await vi.advanceTimersByTimeAsync(TUN_STABLE_SYNC_MS)
  })
  expect(useTun.getState().status.state).toBe('off')

  unmount()
  expect(wails.listeners.get('tun_state')?.size).toBe(0)
  const calls = wails.call.mock.calls.length
  await vi.advanceTimersByTimeAsync(TUN_STABLE_SYNC_MS)
  expect(wails.call).toHaveBeenCalledTimes(calls)
})

test('根证书已信任时直接开启，未信任或检查出错时先征求意见', async () => {
  wails.handlers.set('IsCATrusted', () => true)
  wails.handlers.set('StartTun', () => tun(1, 'starting'))
  await act(async () => toggleTun())
  expect(wails.call).toHaveBeenCalledWith(bridgeName('StartTun'))
  expect(useTun.getState().status.state).toBe('starting')

  useTun.setState(useTun.getInitialState(), true)
  wails.call.mockClear()
  wails.handlers.set('IsCATrusted', () => Promise.reject(new Error('根证书尚未就绪')))
  await act(async () => toggleTun())
  expect(useTun.getState().untrustedPrompt).toBe(true)
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('StartTun'))
})

test('开启被拒绝时弹窗显示原因并拉回状态', async () => {
  wails.handlers.set('IsCATrusted', () => true)
  wails.handlers.set('StartTun', () => Promise.reject(new Error('虚拟网卡正在关闭')))
  wails.handlers.set('GetTunStatus', () => tun(4, 'stopping', { error: '关闭超时' }))
  await act(async () => toggleTun())
  expect(useTun.getState().status.revision).toBe(4)
  expect(useTun.getState().notice).toEqual({ error: '虚拟网卡正在关闭', stuck: false })
})

test('关闭成功后复位关闭请求，回到关闭', async () => {
  acceptTunStatus(tun(1, 'running', { name: 'sniffy0' }))
  wails.handlers.set('StopTun', () => tun(3, 'off'))
  await act(async () => toggleTun())
  const { status, stopPending } = useTun.getState()
  expect(stopPending).toBe(false)
  expect(tunPhaseOf(status, stopPending)).toBe('off')
})

test('失败时关闭弹窗即清除失败原因，其它情形只关闭弹窗', async () => {
  wails.handlers.set('StopTun', () => tun(3, 'off'))
  acceptTunStatus(tun(1, 'starting'))
  acceptTunStatus(tun(2, 'failed', { error: '没有 IPv4 默认路由' }))
  await act(async () => dismissTunNotice())
  expect(wails.call).toHaveBeenCalledWith(bridgeName('StopTun'))
  expect(useTun.getState()).toMatchObject({ notice: null, status: tun(3, 'off') })

  wails.call.mockClear()
  acceptTunStatus(tun(4, 'starting'))
  acceptTunStatus(tun(5, 'stopping', { error: '等待 helper 开启网卡: i/o timeout' }))
  await act(async () => dismissTunNotice())
  expect(useTun.getState().notice).toBeNull()
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('StopTun'))
})

test.each([
  ['windows', 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)', '请以管理员身份运行 Sniffy'],
  ['mac', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', '请在已登录的图形界面会话中运行 Sniffy'],
  ['linux', 'Mozilla/5.0 (X11; Linux x86_64)', '运行着 polkit 认证代理'],
])('无法自动提权时弹窗显示本地化指引并附原因（%s）', async (_, ua, guide) => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(ua)
  const cause = '启动 helper: 无法自动提权: 请求管理员权限: 拒绝访问'
  render(<TunDialogs onInstallCert={() => {}} />)
  acceptTunStatus(tun(1, 'starting'))
  act(() => acceptTunStatus(tun(2, 'failed', { error: cause, errorCode: 'elevation_unavailable' })))
  const dialog = screen.getByRole('alertdialog')
  expect(dialog).toHaveTextContent(guide)
  expect(dialog).toHaveTextContent(cause)

  wails.handlers.set('StopTun', () => tun(3, 'off'))
  await userEvent.setup().click(screen.getByRole('button', { name: '关闭' }))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(useTun.getState().status.state).toBe('off')
})

test('开启中要求关闭时按关闭中显示且不能重复操作', async () => {
  const stop = deferred<TunStatus>()
  acceptTunStatus(tun(1, 'starting'))
  wails.handlers.set('StopTun', () => stop.promise)

  act(() => toggleTun())
  const pending = useTun.getState()
  expect(tunPhaseOf(pending.status, pending.stopPending)).toBe('stopping')
  act(() => toggleTun())
  expect(wails.call.mock.calls.filter(([name]) => name === bridgeName('StopTun'))).toHaveLength(1)

  await act(async () => stop.resolve(tun(2, 'off')))
  const after = useTun.getState()
  expect(after.stopPending).toBe(false)
  expect(tunPhaseOf(after.status, after.stopPending)).toBe('off')
})

test('关不掉时复位关闭请求，以回查到的状态为准', async () => {
  acceptTunStatus(tun(1, 'running', { name: 'sniffy0' }))
  wails.handlers.set('StopTun', () => Promise.reject(new Error('helper 没有在 10s 内拆掉虚拟网卡 sniffy0')))
  wails.handlers.set('GetTunStatus', () =>
    tun(3, 'stopping', { name: 'sniffy0', error: 'helper 没有在 10s 内拆掉虚拟网卡 sniffy0' }),
  )
  await act(async () => toggleTun())
  const after = useTun.getState()
  expect(after.stopPending).toBe(false)
  expect(tunPhaseOf(after.status, after.stopPending)).toBe('stuck')
})
