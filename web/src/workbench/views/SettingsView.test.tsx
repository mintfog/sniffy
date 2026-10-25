import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
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
  await user.click(screen.getByRole('button', { name: '关于与更新' }))
  const toggle = screen.getByRole('switch', { name: '自动检查更新' })
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

test('按分类切换设置并通过关联入口管理解密范围', async () => {
  const user = userEvent.setup()
  render(<SettingsView />)
  expect(screen.getByRole('spinbutton', { name: '监听端口' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: '常规' }))
  expect(
    screen.queryByRole('spinbutton', { name: '监听端口' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('switch', { name: '关闭窗口后保留在托盘' }))
  expect(usePrefs.getState().runInBackground).toBe(false)
  await user.click(screen.getByRole('button', { name: '代理与网络' }))
  await user.click(screen.getByRole('button', { name: '前往 HTTPS 解密' }))
  expect(screen.getByRole('button', { name: 'HTTPS 解密' })).toHaveAttribute(
    'aria-current',
    'page'
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: '解密范围' }),
    'allow'
  )
  await user.type(
    screen.getByRole('textbox', { name: '白名单域名' }),
    '*.example.com'
  )
  expect(usePrefs.getState().decryptAllow).toBe('*.example.com')
})

test('搜索跨分类匹配设置，清除搜索后恢复当前分类', async () => {
  const user = userEvent.setup()
  render(<SettingsView />)
  const search = screen.getByRole('searchbox', { name: '搜索设置' })
  await user.type(search, '  证书 HTTPS  ')
  expect(screen.getByRole('button', { name: '管理证书' })).toBeVisible()
  expect(
    screen.queryByRole('spinbutton', { name: '监听端口' })
  ).not.toBeInTheDocument()
  await user.clear(search)
  await user.type(search, '不存在的配置')
  expect(screen.getByText('未找到相关设置')).toBeVisible()
  await user.click(screen.getByRole('button', { name: '常规' }))
  expect(search).toHaveValue('')
  await user.keyboard('{Control>}k{/Control}')
  expect(search).toHaveFocus()
  await user.type(search, 'HTTPS')
  expect(screen.getByRole('button', { name: '管理证书' })).toBeVisible()
  fireEvent.keyDown(search, { key: 'Escape', isComposing: true })
  expect(search).toHaveValue('HTTPS')
  await user.keyboard('{Escape}')
  expect(search).toHaveValue('')
  expect(
    screen.getByRole('switch', { name: '关闭窗口后保留在托盘' })
  ).toBeVisible()
})

test('切换分类保留待补齐账号的代理认证草稿', async () => {
  wails.handlers.set('GetConfig', () => ({
    proxyAuth: false,
    proxyPasswordSet: false,
  }))
  const user = userEvent.setup()
  render(<SettingsView />)
  await user.click(screen.getByRole('switch', { name: 'Sniffy 代理认证' }))
  await user.type(
    screen.getByLabelText('Sniffy 代理密码', { selector: 'input' }),
    'draft-password'
  )
  await user.click(screen.getByRole('button', { name: '常规' }))
  await user.click(screen.getByRole('button', { name: '代理与网络' }))
  expect(screen.getByRole('switch', { name: 'Sniffy 代理认证' })).toBeChecked()
  expect(
    screen.getByLabelText('Sniffy 代理密码', { selector: 'input' })
  ).toHaveValue('draft-password')
  expect(usePrefs.getState().proxyAuth).toBe(false)
})

test.each(['清除按钮', '删空后失焦', '删空后关闭设置窗口'] as const)(
  '%s 清除本地代理密码后保留认证状态',
  async action => {
    usePrefs.getState().set({ proxyAuth: true, proxyUsername: 'sniffy' })
    wails.handlers.set('GetConfig', () => ({
      proxyAuth: true,
      proxyPasswordSet: true,
    }))
    wails.handlers.set('UpdateConfig', () => ({
      proxyAuth: true,
      proxyPasswordSet: false,
    }))
    const user = userEvent.setup()
    const view = render(<SettingsView />)
    const toggle = screen.getByRole('switch', { name: 'Sniffy 代理认证' })
    const passwordField = screen.getByRole('group', { name: 'Sniffy 代理密码' })
    const password = within(passwordField).getByLabelText('Sniffy 代理密码')
    const clear = within(passwordField).getByRole('button', {
      name: '清除已保存的 Sniffy 代理密码',
    })
    await waitFor(() => expect(clear).toBeEnabled())

    if (action === '清除按钮') {
      await user.click(clear)
    } else {
      await user.type(password, 'temporary-password')
      await user.clear(password)
      if (action === '删空后关闭设置窗口') view.unmount()
      else await user.tab()
    }

    await waitFor(() =>
      expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), {
        proxyPassword: '',
      })
    )
    expect(usePrefs.getState().proxyAuth).toBe(true)
    if (action === '删空后关闭设置窗口') return
    expect(toggle).toBeChecked()
    expect(password).toBeVisible()
    expect(clear).toBeDisabled()
    expect(screen.getByText('账号与密码均为必填')).toBeVisible()
  }
)

test('首次配置时清除密码后可继续填写并启用认证', async () => {
  usePrefs.getState().set({ proxyUsername: 'sniffy' })
  wails.handlers.set('GetConfig', () => ({
    proxyAuth: false,
    proxyPasswordSet: false,
  }))
  wails.handlers.set('UpdateConfig', () => ({
    proxyAuth: false,
    proxyPasswordSet: false,
  }))
  const user = userEvent.setup()
  render(<SettingsView />)
  const toggle = screen.getByRole('switch', { name: 'Sniffy 代理认证' })
  await user.click(toggle)
  const passwordField = screen.getByRole('group', { name: 'Sniffy 代理密码' })
  const password = within(passwordField).getByLabelText('Sniffy 代理密码')
  await user.type(password, 'temporary-password')
  await user.click(
    within(passwordField).getByRole('button', {
      name: '清除已保存的 Sniffy 代理密码',
    })
  )
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), {
      proxyPassword: '',
    })
  )
  expect(toggle).toBeChecked()
  expect(password).toBeVisible()
  expect(usePrefs.getState().proxyAuth).toBe(false)
  expect(screen.getByText('账号与密码均为必填')).toBeVisible()

  wails.handlers.set('UpdateConfig', () => ({
    proxyAuth: true,
    proxyPasswordSet: true,
  }))
  await user.type(password, 'new-password')
  await user.tab()
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), {
      proxyAuth: true,
      proxyUsername: 'sniffy',
      proxyPassword: 'new-password',
    })
  )
  await waitFor(() =>
    expect(password).toHaveAttribute('placeholder', '••••••••')
  )
  expect(usePrefs.getState().proxyAuth).toBe(true)
  expect(toggle).toBeChecked()
  expect(screen.queryByText('账号与密码均为必填')).not.toBeInTheDocument()

  wails.handlers.set('UpdateConfig', () => ({
    proxyAuth: false,
    proxyPasswordSet: false,
  }))
  await user.click(toggle)
  await waitFor(() =>
    expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), {
      proxyAuth: false,
    })
  )
  expect(toggle).not.toBeChecked()
  expect(usePrefs.getState().proxyAuth).toBe(false)
  expect(password).not.toBeInTheDocument()
})
