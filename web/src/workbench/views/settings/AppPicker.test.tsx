import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { bridgeName, deferred, resetWails, wails } from '@/test/wails'
import { AppPicker } from './AppPicker'

const platform = vi.hoisted(() => ({ current: 'windows' as 'windows' | 'mac' | 'linux' }))
vi.mock('@/lib/platform', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/platform')>()),
  detectPlatform: () => platform.current,
}))

beforeEach(() => {
  resetWails()
  platform.current = 'windows'
  wails.handlers.set('GetNetworkApps', () => [{ entry: 'curl.exe', path: 'C:\\tools\\curl.exe' }])
  wails.handlers.set('GetInstalledApps', () => [
    { name: 'Google Chrome', entry: 'chrome.exe', path: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe' },
    { name: '微信', entry: 'Weixin.exe', path: 'D:\\Program Files\\Tencent\\WeChat\\Weixin.exe' },
  ])
  wails.handlers.set('GetAppIcons', (paths) =>
    (paths as string[]).filter((p) => p.endsWith('chrome.exe')).map((path) => ({ path, iconData: 'Y2hyb21l', iconType: 'png' })),
  )
})

test('已安装的应用：切换标签页时各列一次，图标随后补上，勾选跨标签页保留', async () => {
  const onAdd = vi.fn()
  const user = userEvent.setup()
  render(<AppPicker listed={['Weixin.exe']} onAdd={onAdd} onClose={() => {}} />)
  await user.click(await screen.findByRole('checkbox', { name: 'curl.exe' }))

  await user.click(screen.getByRole('button', { name: '已安装' }))
  const chrome = await screen.findByRole('checkbox', { name: 'Google Chrome' })
  expect(screen.getByText('C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe')).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: '微信' })).toBeDisabled()
  await waitFor(() => expect(screen.getByRole('img', { name: 'Google Chrome' })).toHaveAttribute('src', 'data:image/png;base64,Y2hyb21l'))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('GetAppIcons'), [
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    'D:\\Program Files\\Tencent\\WeChat\\Weixin.exe',
  ])
  await user.click(chrome)

  await user.click(screen.getByRole('button', { name: '正在联网' }))
  expect(screen.getByRole('checkbox', { name: 'curl.exe' })).toBeChecked()
  await user.click(screen.getByRole('button', { name: '加入名单（2）' }))
  expect(onAdd).toHaveBeenCalledWith([
    { entry: 'curl.exe', path: 'C:\\tools\\curl.exe' },
    expect.objectContaining({ entry: 'chrome.exe', iconData: 'Y2hyb21l', iconType: 'png' }),
  ])
  const fetched = (name: string) => wails.call.mock.calls.filter(([m]) => m === bridgeName(name)).length
  expect([fetched('GetNetworkApps'), fetched('GetInstalledApps')]).toEqual([1, 1])
})

test('已安装的应用读不出时说明原因', async () => {
  wails.handlers.set('GetInstalledApps', () => {
    throw new Error('拒绝访问')
  })
  const user = userEvent.setup()
  render(<AppPicker listed={[]} onAdd={() => {}} onClose={() => {}} />)
  await user.click(screen.getByRole('button', { name: '已安装' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('列不出已安装的应用：拒绝访问')
})

test('Linux 上只有正在联网的程序', async () => {
  platform.current = 'linux'
  render(<AppPicker listed={[]} onAdd={() => {}} onClose={() => {}} />)
  const dialog = screen.getByRole('dialog', { name: '添加应用' })
  await within(dialog).findByRole('checkbox', { name: 'curl.exe' })
  expect(within(dialog).queryByRole('button', { name: '已安装' })).not.toBeInTheDocument()
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('GetInstalledApps'))
})

test('手动刷新：刷新期间旧列表照旧显示、按钮置灰，勾选保留；刷新失败时保留旧列表并说明原因', async () => {
  const user = userEvent.setup()
  render(<AppPicker listed={[]} onAdd={() => {}} onClose={() => {}} />)
  await user.click(await screen.findByRole('checkbox', { name: 'curl.exe' }))

  const next = deferred<unknown>()
  wails.handlers.set('GetNetworkApps', () => next.promise)
  const refresh = screen.getByRole('button', { name: '刷新列表' })
  await user.click(refresh)
  expect(refresh).toBeDisabled()
  expect(screen.getByRole('checkbox', { name: 'curl.exe' })).toBeChecked()

  next.resolve([
    { entry: 'curl.exe', path: 'C:\\tools\\curl.exe' },
    { entry: 'wget.exe', path: 'C:\\tools\\wget.exe' },
  ])
  expect(await screen.findByRole('checkbox', { name: 'wget.exe' })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'curl.exe' })).toBeChecked()
  expect(refresh).toBeEnabled()

  wails.handlers.set('GetNetworkApps', () => {
    throw new Error('扫描失败')
  })
  await user.click(refresh)
  expect(await screen.findByRole('alert')).toHaveTextContent('列不出联网的程序：扫描失败')
  expect(screen.getByRole('checkbox', { name: 'wget.exe' })).toBeInTheDocument()
})
