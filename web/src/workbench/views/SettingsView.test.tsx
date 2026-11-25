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
  deferred,
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

test('TUN 模式的参数直接写入后端配置', async () => {
  wails.handlers.set('GetConfig', () => ({ tunIPv6: true, tunBlockQUIC: false }))
  const user = userEvent.setup()
  render(<SettingsView />)
  const ipv6 = screen.getByRole('switch', { name: '接管 IPv6' })
  const quic = screen.getByRole('switch', { name: '拦截 QUIC' })
  await waitFor(() => expect(ipv6).toBeEnabled())
  expect(ipv6).toBeChecked()
  expect(quic).not.toBeChecked()

  await user.click(ipv6)
  expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), { tunIPv6: false })
  expect(ipv6).not.toBeChecked()
})

test('不接管的网段失焦时保存并显示规范化的结果，有无效项时指出且不保存', async () => {
  wails.handlers.set('GetConfig', () => ({ tunIPv6: true, tunBlockQUIC: true, tunBypass: ['10.0.0.0/8'] }))
  wails.handlers.set('UpdateConfig', () => ({ tunBypass: ['10.0.0.0/8', '192.168.1.0/24'] }))
  const user = userEvent.setup()
  render(<SettingsView />)
  const box = await screen.findByRole('textbox', { name: '不接管的网段' })
  expect(box).toHaveValue('10.0.0.0/8')

  await user.type(box, '\nexample.com')
  await user.tab()
  expect(screen.getByRole('alert')).toHaveTextContent('「example.com」不是有效的 CIDR 或 IP 地址，未保存')
  expect(box).toHaveAttribute('aria-invalid', 'true')
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('UpdateConfig'), expect.objectContaining({ tunBypass: expect.anything() }))

  await user.clear(box)
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  await user.type(box, '10.0.0.0/8\n192.168.1.5/24')
  await user.tab()
  expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), { tunBypass: ['10.0.0.0/8', '192.168.1.5/24'] })
  await waitFor(() => expect(box).toHaveValue('10.0.0.0/8\n192.168.1.0/24'))
})

test('抓取的应用：选了按名单抓取才显示名单，模式即时保存，名单失焦时保存并显示规范化的结果', async () => {
  wails.handlers.set('GetConfig', () => ({ tunIPv6: true, tunBlockQUIC: true, tunApps: ['chrome'] }))
  wails.handlers.set('UpdateConfig', (patch) =>
    patch && typeof patch === 'object' && 'tunApps' in patch ? { tunAppMode: 'include', tunApps: ['chrome', '/opt/app'] } : patch
  )
  const user = userEvent.setup()
  render(<SettingsView />)
  const mode = await screen.findByRole('combobox', { name: '抓取的应用' })
  expect(mode).toHaveValue('all')
  expect(screen.queryByRole('textbox', { name: '只抓这些应用' })).not.toBeInTheDocument()

  await user.selectOptions(mode, 'include')
  expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), { tunAppMode: 'include' })
  const box = screen.getByRole('textbox', { name: '只抓这些应用' })
  expect(box).toHaveValue('chrome')
  // 测试环境按 Linux 处理，占位符与校验都用本机平台的写法。
  expect(box).toHaveAttribute('placeholder', 'chrome\ncurl\n/opt/postman')

  await user.type(box, '\nbin/curl')
  await user.tab()
  expect(screen.getByRole('alert')).toHaveTextContent('「bin/curl」不是完整路径：填程序名，或从根目录（/）写起的完整路径，未保存')

  await user.clear(box)
  await user.type(box, 'C:\\Tools\\app.exe')
  await user.tab()
  expect(screen.getByRole('alert')).toHaveTextContent('「C:\\Tools\\app.exe」是 Windows 的路径写法，本机用不了，未保存')
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('UpdateConfig'), expect.objectContaining({ tunApps: expect.anything() }))

  await user.clear(box)
  await user.type(box, 'chrome\n/opt/x/../app\nchrome')
  await user.tab()
  expect(wails.call).toHaveBeenCalledWith(bridgeName('UpdateConfig'), { tunApps: ['chrome', '/opt/x/../app'] })
  await waitFor(() => expect(box).toHaveValue('chrome\n/opt/app'))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()

  await user.selectOptions(mode, 'exclude')
  expect(screen.getByRole('textbox', { name: '不抓这些应用' })).toHaveValue('chrome\n/opt/app')
})

test('直通名单显示主机、原因与生效时间，可单条或全部重新尝试解密', async () => {
  const entries = [
    { host: 'pinned.example:443', reason: 'closed_after_handshake', since: '2026-11-19T10:00:00Z' },
    { host: 'mail.example:993', reason: 'not_http', since: '2026-11-19T10:05:00Z' },
    { host: 'odd.example:443', reason: 'future_reason', since: '2026-11-19T10:06:00Z' },
  ]
  wails.handlers.set('GetDecryptBypass', () => entries)
  wails.handlers.set('ForgetDecryptBypass', (host) => entries.filter((e) => e.host !== host))
  wails.handlers.set('ClearDecryptBypass', () => [])
  const user = userEvent.setup()
  render(<SettingsView />)

  const pinned = (await screen.findByText('pinned.example:443')).closest('li')!
  expect(pinned).toHaveTextContent('握手后即断开（疑似证书固定）')
  expect(pinned).toHaveTextContent(new Date(entries[0].since).toLocaleString('zh-Hans'))
  expect(screen.getByText('mail.example:993').closest('li')).toHaveTextContent('不是 HTTP 流量')
  expect(screen.getByText('odd.example:443').closest('li')).toHaveTextContent('future_reason')

  await user.click(within(pinned).getByRole('button', { name: '重新尝试解密 pinned.example:443' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('ForgetDecryptBypass'), 'pinned.example:443')
  await waitFor(() => expect(screen.queryByText('pinned.example:443')).not.toBeInTheDocument())

  await user.click(screen.getByRole('button', { name: '全部重新尝试' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('ClearDecryptBypass'))
  expect(await screen.findByText('暂无直通的主机')).toBeVisible()
  expect(screen.getByRole('button', { name: '全部重新尝试' })).toBeDisabled()
})

test('窗口获得焦点时刷新直通名单，晚到的刷新结果不覆盖随后重试的结果', async () => {
  const entries = [
    { host: 'pinned.example:443', reason: 'cert_rejected', since: '2026-11-19T10:00:00Z' },
    { host: 'mail.example:993', reason: 'not_http', since: '2026-11-19T10:05:00Z' },
  ]
  wails.handlers.set('GetDecryptBypass', () => entries)
  wails.handlers.set('ForgetDecryptBypass', (host) => entries.filter((e) => e.host !== host))
  const user = userEvent.setup()
  render(<SettingsView />)
  const pinned = (await screen.findByText('pinned.example:443')).closest('li')!

  const late = deferred<typeof entries>()
  wails.handlers.set('GetDecryptBypass', () => late.promise)
  act(() => {
    window.dispatchEvent(new Event('focus'))
  })
  expect(wails.call).toHaveBeenLastCalledWith(bridgeName('GetDecryptBypass'))
  await user.click(within(pinned).getByRole('button', { name: '重新尝试解密 pinned.example:443' }))
  await waitFor(() => expect(screen.queryByText('pinned.example:443')).not.toBeInTheDocument())
  await act(async () => late.resolve(entries))
  expect(screen.queryByText('pinned.example:443')).not.toBeInTheDocument()
  expect(screen.getByText('mail.example:993')).toBeVisible()
})
