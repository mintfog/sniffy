import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test } from 'vitest'
import {
  bridgeName,
  emitUpdate,
  resetWails,
  updateState,
  wails,
} from '@/test/wails'
import { usePrefs } from '../prefs'
import { SettingsView } from './SettingsView'

beforeEach(() => {
  resetWails()
  usePrefs.getState().reset()
})

test('自动检查开关以后端状态为准，并跟随其它窗口的改动', async () => {
  wails.handlers.set('GetConfig', () => ({}))
  const user = userEvent.setup()
  render(<SettingsView />)
  const field = screen.getByText('自动检查更新').parentElement!.parentElement!
  const toggle = within(field).getByRole('switch')
  await waitFor(() => expect(toggle).toBeChecked())
  wails.handlers.set('SetUpdateAutoCheck', () =>
    updateState({ revision: 2, autoCheck: false })
  )
  await user.click(toggle)
  expect(wails.call).toHaveBeenCalledWith(
    bridgeName('SetUpdateAutoCheck'),
    false
  )
  await waitFor(() => expect(toggle).not.toBeChecked())
  act(() => emitUpdate(updateState({ revision: 3, autoCheck: true })))
  expect(toggle).toBeChecked()
})

test.each(['清除按钮', '删空后失焦', '删空后关闭设置窗口'] as const)(
  '%s 清除本地代理密码后保留认证状态',
  async (action) => {
    usePrefs.getState().set({ proxyAuth: true, proxyUsername: 'sniffy' })
    wails.handlers.set('GetConfig', () => ({ proxyAuth: true, proxyPasswordSet: true }))
    wails.handlers.set('UpdateConfig', () => ({ proxyAuth: true, proxyPasswordSet: false }))
    const user = userEvent.setup()
    const view = render(<SettingsView />)
    const authField = screen.getByText('Sniffy 代理认证').parentElement!.parentElement!
    const toggle = within(authField).getByRole('switch')
    const passwordField = screen.getByText('Sniffy 代理密码').parentElement!.parentElement!
    const password = passwordField.querySelector('input')!
    const clear = within(passwordField).getByRole('button', { name: '清除已保存的 Sniffy 代理密码' })
    await waitFor(() => expect(clear).toBeEnabled())

    if (action === '清除按钮') {
      await user.click(clear)
    } else {
      await user.type(password, 'temporary-password')
      await user.clear(password)
      if (action === '删空后关闭设置窗口') view.unmount()
      else await user.tab()
    }

    await waitFor(() => expect(wails.call).toHaveBeenCalledWith(
      bridgeName('UpdateConfig'),
      { proxyPassword: '' },
    ))
    expect(usePrefs.getState().proxyAuth).toBe(true)
    if (action === '删空后关闭设置窗口') return
    expect(toggle).toBeChecked()
    expect(password).toBeVisible()
    expect(clear).toBeDisabled()
    expect(screen.getByText('账号与密码均为必填')).toBeVisible()
  },
)

test('首次配置时清除密码后可继续填写并启用认证', async () => {
  usePrefs.getState().set({ proxyUsername: 'sniffy' })
  wails.handlers.set('GetConfig', () => ({ proxyAuth: false, proxyPasswordSet: false }))
  wails.handlers.set('UpdateConfig', () => ({ proxyAuth: false, proxyPasswordSet: false }))
  const user = userEvent.setup()
  render(<SettingsView />)
  const authField = screen.getByText('Sniffy 代理认证').parentElement!.parentElement!
  const toggle = within(authField).getByRole('switch')
  await user.click(toggle)
  const passwordField = screen.getByText('Sniffy 代理密码').parentElement!.parentElement!
  const password = passwordField.querySelector('input')!
  await user.type(password, 'temporary-password')
  await user.click(within(passwordField).getByRole('button', { name: '清除已保存的 Sniffy 代理密码' }))
  await waitFor(() => expect(wails.call).toHaveBeenCalledWith(
    bridgeName('UpdateConfig'),
    { proxyPassword: '' },
  ))
  expect(toggle).toBeChecked()
  expect(password).toBeVisible()
  expect(usePrefs.getState().proxyAuth).toBe(false)
  expect(screen.getByText('账号与密码均为必填')).toBeVisible()

  wails.handlers.set('UpdateConfig', () => ({ proxyAuth: true, proxyPasswordSet: true }))
  await user.type(password, 'new-password')
  await user.tab()
  await waitFor(() => expect(wails.call).toHaveBeenCalledWith(
    bridgeName('UpdateConfig'),
    { proxyAuth: true, proxyUsername: 'sniffy', proxyPassword: 'new-password' },
  ))
  await waitFor(() => expect(password).toHaveAttribute('placeholder', '••••••••'))
  expect(usePrefs.getState().proxyAuth).toBe(true)
  expect(toggle).toBeChecked()
  expect(screen.queryByText('账号与密码均为必填')).not.toBeInTheDocument()

  wails.handlers.set('UpdateConfig', () => ({ proxyAuth: false, proxyPasswordSet: false }))
  await user.click(toggle)
  await waitFor(() => expect(wails.call).toHaveBeenCalledWith(
    bridgeName('UpdateConfig'),
    { proxyAuth: false },
  ))
  expect(toggle).not.toBeChecked()
  expect(usePrefs.getState().proxyAuth).toBe(false)
  expect(password).not.toBeInTheDocument()
})
