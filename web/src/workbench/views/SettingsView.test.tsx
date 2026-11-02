import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { StrictMode } from 'react'
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

test('StrictMode 下新窗口打开指定分类，并在挂载后清除待打开分类', () => {
  window.localStorage.setItem('sniffy-settings-category', 'decrypt')
  render(
    <StrictMode>
      <SettingsView />
    </StrictMode>
  )
  expect(screen.getByRole('button', { name: 'HTTPS 解密' })).toHaveAttribute(
    'aria-current',
    'page'
  )
  expect(window.localStorage.getItem('sniffy-settings-category')).toBeNull()
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

test('搜索或切换分类后保留服务端证书导入草稿', async () => {
  const user = userEvent.setup()
  render(<SettingsView />)
  await user.click(screen.getByRole('button', { name: 'HTTPS 解密' }))
  await user.type(screen.getByLabelText('证书 (PEM)'), 'CERT-DRAFT')
  await user.type(screen.getByLabelText('私钥 (PEM)'), 'KEY-DRAFT')

  const search = screen.getByRole('searchbox', { name: '搜索设置' })
  await user.type(search, '语言')
  expect(screen.queryByLabelText('证书 (PEM)')).not.toBeInTheDocument()
  await user.clear(search)
  expect(screen.getByLabelText('证书 (PEM)')).toHaveValue('CERT-DRAFT')
  expect(screen.getByLabelText('私钥 (PEM)')).toHaveValue('KEY-DRAFT')

  await user.click(screen.getByRole('button', { name: '常规' }))
  await user.click(screen.getByRole('button', { name: 'HTTPS 解密' }))
  expect(screen.getByLabelText('证书 (PEM)')).toHaveValue('CERT-DRAFT')
  expect(screen.getByLabelText('私钥 (PEM)')).toHaveValue('KEY-DRAFT')
})

function delayServerCertImport() {
  let finish = () => {}
  const imported = {
    id: 'a1',
    hosts: ['api.example.com'],
    subject: 'CN=api.example.com',
    issuer: '',
    notAfter: '',
  }
  let list: unknown[] = []
  wails.handlers.set('GetServerCerts', () => list)
  wails.handlers.set(
    'ImportServerCert',
    () =>
      new Promise(resolve => {
        finish = () => {
          list = [imported]
          resolve(imported)
        }
      })
  )
  return () => act(() => finish())
}

async function startServerCertImport(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'HTTPS 解密' }))
  await user.type(screen.getByLabelText('证书 (PEM)'), 'CERT-1')
  await user.type(screen.getByLabelText('私钥 (PEM)'), 'KEY-1')
  await user.click(screen.getByRole('button', { name: '导入' }))
  await user.click(screen.getByRole('button', { name: '常规' }))
  await user.click(screen.getByRole('button', { name: 'HTTPS 解密' }))
  expect(screen.getByRole('button', { name: '导入中…' })).toBeDisabled()
}

test('导入进行中切换分类再改写草稿，导入完成后保留新草稿', async () => {
  const finish = delayServerCertImport()
  const user = userEvent.setup()
  render(<SettingsView />)
  await startServerCertImport(user)

  const cert = screen.getByLabelText('证书 (PEM)')
  await user.clear(cert)
  await user.type(cert, 'CERT-2')
  await finish()

  expect(await screen.findByText('api.example.com')).toBeVisible()
  expect(screen.getByLabelText('证书 (PEM)')).toHaveValue('CERT-2')
  expect(screen.getByLabelText('私钥 (PEM)')).toHaveValue('KEY-1')
  expect(screen.getByRole('button', { name: '导入' })).toBeEnabled()
})

test('导入在设置分组卸载期间完成，返回后草稿已清空且列表已刷新', async () => {
  const finish = delayServerCertImport()
  const user = userEvent.setup()
  render(<SettingsView />)
  await startServerCertImport(user)

  await user.click(screen.getByRole('button', { name: '常规' }))
  await finish()
  await user.click(screen.getByRole('button', { name: 'HTTPS 解密' }))

  expect(await screen.findByText('api.example.com')).toBeVisible()
  expect(screen.getByLabelText('证书 (PEM)')).toHaveValue('')
  expect(screen.getByLabelText('私钥 (PEM)')).toHaveValue('')
  expect(screen.getByRole('button', { name: '导入' })).toBeEnabled()
})

test('搜索跨分类匹配设置，清除搜索后恢复当前分类', async () => {
  const user = userEvent.setup()
  render(<SettingsView />)
  const search = screen.getByRole('searchbox', { name: '搜索设置' })
  await user.type(search, '  证书 HTTPS  ')
  expect(screen.getByRole('button', { name: '安装引导' })).toBeVisible()
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
  expect(screen.getByRole('button', { name: '安装引导' })).toBeVisible()
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
