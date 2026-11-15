import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { resetWails, wails } from '@/test/wails'
import { usePrefs } from '../../prefs'
import { CertGuideDialog } from './CertGuideDialog'
import { hostCertPlatform } from './platform'

beforeEach(() => {
  resetWails()
  usePrefs.getState().reset()
  wails.handlers.set('GetCertificatePEM', () => '')
})

test('iOS 截图中的代理参数随当前地址和端口更新', async () => {
  const props = {
    platform: 'ios' as const,
    onPlatformChange: vi.fn(),
    onClose: vi.fn(),
    onInstall: vi.fn(),
    installing: false,
    covered: false,
    proxyHost: '192.0.2.10',
    proxyPort: '8080',
  }
  const { rerender } = render(<CertGuideDialog {...props} />)
  const picture = await screen.findByRole('button', {
    name: '点击放大: 设置 Wi-Fi 代理',
  })
  expect(within(picture).getByText('192.0.2.10')).toBeInTheDocument()
  expect(within(picture).getByText('8080')).toBeInTheDocument()

  rerender(
    <CertGuideDialog {...props} proxyHost="198.51.100.20" proxyPort="9090" />
  )
  expect(within(picture).queryByText('192.0.2.10')).not.toBeInTheDocument()
  expect(within(picture).getByText('198.51.100.20')).toBeInTheDocument()
  expect(within(picture).getByText('9090')).toBeInTheDocument()

  rerender(
    <CertGuideDialog
      {...props}
      platform="android"
      proxyHost="198.51.100.20"
      proxyPort="9090"
    />
  )
  expect(
    screen.queryByRole('button', { name: '点击放大: 设置 Wi-Fi 代理' })
  ).not.toBeInTheDocument()
  rerender(
    <CertGuideDialog {...props} proxyHost="198.51.100.20" proxyPort="9090" />
  )
  const restored = screen.getByRole('button', {
    name: '点击放大: 设置 Wi-Fi 代理',
  })
  expect(within(restored).getByText('198.51.100.20')).toBeInTheDocument()
  expect(within(restored).getByText('9090')).toBeInTheDocument()
})

const baseProps = {
  onPlatformChange: vi.fn(),
  onClose: vi.fn(),
  onInstall: vi.fn(),
  installing: false,
  covered: false,
  proxyHost: '192.0.2.10',
  proxyPort: '8080',
}

test('Linux 默认展示本机安装入口，并可展开手动教程', async () => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
    'Mozilla/5.0 (X11; Linux aarch64)'
  )
  const user = userEvent.setup()
  render(<CertGuideDialog {...baseProps} platform={hostCertPlatform()} />)

  expect(screen.getByRole('tab', { name: 'Linux' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(screen.getByRole('button', { name: '安装到本机' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '或手动安装' }))
  await user.click(screen.getByRole('button', { name: /^加入系统信任库/ }))
  expect(screen.getByRole('img', { name: '加入系统信任库' })).toHaveAttribute(
    'src',
    expect.stringContaining('linux-install.zh-Hans.png')
  )
  const footer = within(screen.getByRole('contentinfo'))
  await user.click(footer.getByRole('button', { name: '下一步' }))
  expect(screen.getByRole('img', { name: '验证安装结果' })).toHaveAttribute(
    'src',
    expect.stringContaining('linux-verify.png')
  )
})

test('跨平台查看 Linux 教程时直接显示手动步骤', async () => {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)'
  )
  const user = userEvent.setup()
  const onPlatformChange = vi.fn()
  render(
    <CertGuideDialog
      {...baseProps}
      platform="linux"
      onPlatformChange={onPlatformChange}
    />
  )

  expect(
    screen.queryByRole('button', { name: '安装到本机' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /^验证安装结果/ }))
  await user.click(screen.getByText('浏览器仍提示证书不受信任？'))
  await user.click(
    screen.getByRole('button', { name: '查看 Firefox 安装教程' })
  )
  expect(onPlatformChange).toHaveBeenCalledWith('firefox')
})

test('Android 证书二维码指向代理门户的 CRT 下载地址', async () => {
  const user = userEvent.setup()
  render(<CertGuideDialog {...baseProps} platform="android" />)
  await user.click(screen.getByRole('button', { name: /扫码下载证书/ }))
  expect(
    screen.getByText('http://192.0.2.10:8080/sniffy-ca.crt')
  ).toBeInTheDocument()
  expect(screen.queryByText(/cert\.sniffy/)).not.toBeInTheDocument()
})

test('Tab 焦点在弹窗内循环，不落到背景', async () => {
  const user = userEvent.setup()
  render(
    <>
      <button type="button">背景按钮</button>
      <CertGuideDialog {...baseProps} platform="android" />
    </>
  )
  const dialog = screen.getByRole('dialog')
  const background = screen.getByRole('button', { name: '背景按钮' })
  for (let i = 0; i < 30; i++) {
    await user.tab({ shift: i % 2 === 1 && i > 15 })
    expect(document.activeElement).not.toBe(background)
    expect(dialog).toContainElement(document.activeElement as HTMLElement)
  }
})
