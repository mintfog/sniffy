import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'
import {
  bridgeName,
  deferred,
  emitUpdate,
  resetWails,
  updateState,
  wails,
} from '@/test/wails'
import type { UpdateState } from '@/lib/bridge'
import { useUpdate } from './update'

beforeEach(() => resetWails())

test('迟到的初始快照与操作结果不能覆盖更新事件', async () => {
  const initial = deferred<UpdateState>()
  const checking = deferred<UpdateState>()
  wails.handlers.set('GetUpdateState', () => initial.promise)
  wails.handlers.set('CheckUpdate', () => checking.promise)
  const { result } = renderHook(useUpdate)
  act(() => result.current.actions.check())
  act(() =>
    emitUpdate(
      updateState({ revision: 5, status: 'available', latest: '2.0.0' })
    )
  )
  await act(async () => {
    initial.resolve(updateState({ revision: 1 }))
    checking.resolve(updateState({ revision: 4, status: 'checking' }))
  })
  expect(result.current.state).toMatchObject({
    revision: 5,
    status: 'available',
    latest: '2.0.0',
  })
  expect(wails.call).toHaveBeenCalledWith(bridgeName('CheckUpdate'))
})

test('多个窗口共享跳过版本和自动检查的状态', async () => {
  const first = renderHook(useUpdate)
  const second = renderHook(useUpdate)
  await waitFor(() => expect(first.result.current.state.autoCheck).toBe(true))
  const next = updateState({
    revision: 2,
    autoCheck: false,
    skippedVersion: '2.0.0',
  })
  wails.handlers.set('SetUpdateAutoCheck', () => next)
  await act(async () => first.result.current.actions.setAutoCheck(false))
  expect(wails.call).toHaveBeenCalledWith(
    bridgeName('SetUpdateAutoCheck'),
    false
  )
  act(() => emitUpdate(next))
  expect(first.result.current.state).toEqual(second.result.current.state)
  expect(second.result.current.state.skippedVersion).toBe('2.0.0')
})

test('丢失结束事件后自动回查，卸载时解除订阅和定时器', async () => {
  vi.useFakeTimers()
  const { result, unmount } = renderHook(useUpdate)
  await act(async () => {})
  act(() => emitUpdate(updateState({ revision: 2, status: 'downloading' })))
  wails.handlers.set('GetUpdateState', () =>
    updateState({ revision: 3, status: 'downloaded' })
  )
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000)
  })
  expect(result.current.state.status).toBe('downloaded')
  unmount()
  expect(wails.listeners.get('update_state')?.size).toBe(0)
  const calls = wails.call.mock.calls.length
  await vi.advanceTimersByTimeAsync(60_000)
  expect(wails.call).toHaveBeenCalledTimes(calls)
})

test('稳定状态也能回查到未收到的新版本通知', async () => {
  vi.useFakeTimers()
  const { result } = renderHook(useUpdate)
  await act(async () => {})
  wails.handlers.set('GetUpdateState', () =>
    updateState({ revision: 2, status: 'available', notify: true })
  )
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(result.current.state.notify).toBe(true)
})

test('卸载后初始请求和回查的迟到响应不再更新状态', async () => {
  vi.useFakeTimers()
  const response = deferred<UpdateState>()
  wails.handlers.set('GetUpdateState', () => response.promise)
  const { result, unmount } = renderHook(useUpdate)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  unmount()
  await act(async () =>
    response.resolve(updateState({ revision: 20, status: 'available' }))
  )
  expect(result.current.state.revision).toBe(-1)
})

test('桌面运行时不可用或回查失败时保留最后一份状态', async () => {
  vi.useFakeTimers()
  wails.call.mockRejectedValue(new Error('运行时不可用'))
  const { result } = renderHook(useUpdate)
  await act(async () => {})
  await act(async () => {
    result.current.actions.check()
    result.current.actions.reveal()
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(result.current.state).toMatchObject({
    status: 'idle',
    notify: false,
    revision: -1,
  })
})

test('空更新事件不覆盖已有状态', async () => {
  const { result } = renderHook(useUpdate)
  await waitFor(() => expect(result.current.state.revision).toBe(1))
  act(() => emitUpdate(undefined))
  expect(result.current.state.revision).toBe(1)
})

test('重新下载与重新安装都会清除上一次安装错误', async () => {
  const { result } = renderHook(useUpdate)
  wails.handlers.set('InstallUpdate', () => {
    throw '安装包已不在原处，请重新下载'
  })
  await act(async () => result.current.actions.install())
  expect(result.current.installError).toContain('请重新下载')
  wails.handlers.set('DownloadUpdate', () =>
    updateState({ revision: 2, status: 'downloading' })
  )
  await act(async () => result.current.actions.download())
  expect(result.current.installError).toBe('')
  wails.handlers.set('InstallUpdate', () => {
    throw new Error('已取消安装授权')
  })
  await act(async () => result.current.actions.install())
  expect(result.current.installError).toBe('已取消安装授权')
  wails.handlers.set('InstallUpdate', () => true)
  await act(async () => result.current.actions.install())
  expect(result.current.installError).toBe('')
})
