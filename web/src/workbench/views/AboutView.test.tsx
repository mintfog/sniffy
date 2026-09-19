import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18n from 'i18next'
import { beforeEach, expect, test } from 'vitest'
import {
  bridgeName,
  emitUpdate,
  resetWails,
  updateState,
  wails,
} from '@/test/wails'
import type { UpdateState } from '@/lib/bridge'
import { DOWNLOAD_URL, REPO_URL } from '../lib/links'
import { AboutView } from './AboutView'

const available = updateState({
  status: 'available',
  latest: '2.0.0',
  notify: true,
  publishedAt: '2026-09-18',
  notesUrl: 'https://gosniffy.com/notes/',
  asset: {
    name: 'sniffy.exe',
    url: 'https://cdn.gosniffy.com/sniffy.exe',
    size: 1_048_576,
  },
})

beforeEach(() => resetWails())

test('检查、阅读说明、跳过与恢复提醒走真实 Bridge', async () => {
  const user = userEvent.setup()
  render(<AboutView />)
  expect(await screen.findByText('尚未检查更新')).toBeVisible()
  wails.handlers.set('CheckUpdate', () => ({ ...available, revision: 2 }))
  await user.click(screen.getByRole('button', { name: '检查更新' }))
  expect(await screen.findByText('发现新版本 2.0.0')).toBeVisible()
  expect(screen.getByText('2026-09-18 · 1.0 MB · sniffy.exe')).toBeVisible()
  await user.click(screen.getByRole('button', { name: '更新说明' }))
  expect(wails.openURL).toHaveBeenCalledWith(available.notesUrl)
  wails.handlers.set('SkipUpdateVersion', () => ({
    ...available,
    revision: 3,
    skippedVersion: '2.0.0',
    notify: false,
  }))
  await user.click(screen.getByRole('button', { name: '跳过此版本' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('SkipUpdateVersion'), '')
  expect(
    screen.queryByRole('button', { name: '跳过此版本' })
  ).not.toBeInTheDocument()
  wails.handlers.set('ClearSkippedUpdateVersion', () => ({
    ...available,
    revision: 4,
  }))
  await user.click(screen.getByRole('button', { name: '恢复提醒' }))
  expect(
    await screen.findByRole('button', { name: '跳过此版本' })
  ).toBeVisible()
})

test('下载期间展示进度、禁止再次检查，取消后可以重试', async () => {
  resetWails(available)
  const user = userEvent.setup()
  render(<AboutView />)
  wails.handlers.set('DownloadUpdate', () => ({
    ...available,
    revision: 2,
    status: 'downloading',
    downloaded: 524_288,
    total: 1_048_576,
  }))
  await user.click(await screen.findByRole('button', { name: '下载新版本' }))
  expect(await screen.findByText('0.5 MB / 1.0 MB')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: '检查更新' })
  ).not.toBeInTheDocument()
  wails.handlers.set('CancelUpdateDownload', () => ({
    ...available,
    revision: 3,
  }))
  await user.click(screen.getByRole('button', { name: '取消下载' }))
  expect(
    await screen.findByRole('button', { name: '下载新版本' })
  ).toBeEnabled()
  expect(wails.call).toHaveBeenCalledWith(bridgeName('CancelUpdateDownload'))
})

test('下载完成后即使检查失败也保留安装入口和路径', async () => {
  const local = {
    ...available,
    status: 'downloaded' as const,
    downloadedPath: '/Downloads/sniffy.exe',
  }
  resetWails(local)
  const user = userEvent.setup()
  render(<AboutView />)
  wails.handlers.set('CheckUpdate', () => ({
    ...local,
    revision: 2,
    error: '连接超时',
  }))
  await user.click(await screen.findByRole('button', { name: '检查更新' }))
  expect(await screen.findByText('检查更新失败：连接超时')).toBeVisible()
  expect(screen.getByText(local.downloadedPath)).toBeVisible()
  expect(screen.getByRole('button', { name: '立即安装' })).toBeEnabled()
  expect(screen.getByText(/确认系统授权后/)).toBeVisible()
  await user.click(screen.getByRole('button', { name: '打开所在文件夹' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('RevealUpdateDownload'))
})

test('下载失败展示对应原因并直接重试下载', async () => {
  resetWails({
    ...available,
    status: 'error',
    errorStage: 'download',
    error: '连接已中断',
  })
  const user = userEvent.setup()
  render(<AboutView />)
  expect(await screen.findByText('下载失败')).toBeVisible()
  expect(screen.getByText('连接已中断')).toBeVisible()
  expect(screen.queryByText('检查更新失败')).not.toBeInTheDocument()
  wails.handlers.set('DownloadUpdate', () => ({
    ...available,
    revision: 2,
    status: 'downloading',
    total: available.asset!.size,
  }))
  await user.click(screen.getByRole('button', { name: '重试下载' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('DownloadUpdate'))
  expect(wails.call).not.toHaveBeenCalledWith(bridgeName('CheckUpdate'))
  expect(await screen.findByText('正在下载安装包…')).toBeVisible()
  expect(screen.queryByText('连接已中断')).not.toBeInTheDocument()
})

test('安装包缺失后重新下载，清掉旧提示并允许再次安装', async () => {
  resetWails({
    ...available,
    status: 'downloaded',
    downloadedPath: '/Downloads/sniffy.exe',
  })
  const user = userEvent.setup()
  render(<AboutView />)
  wails.handlers.set('InstallUpdate', () => {
    emitUpdate({ ...available, revision: 2 })
    throw new Error('安装包已不在原处，请重新下载')
  })
  await user.click(await screen.findByRole('button', { name: '立即安装' }))
  expect(await screen.findByText('安装包已不在原处，请重新下载')).toBeVisible()
  wails.handlers.set('DownloadUpdate', () => ({
    ...available,
    revision: 3,
    status: 'downloading',
    total: available.asset!.size,
  }))
  await user.click(screen.getByRole('button', { name: '下载新版本' }))
  expect(
    screen.queryByText('安装包已不在原处，请重新下载')
  ).not.toBeInTheDocument()
  expect(screen.getByText('0.0 MB / 1.0 MB')).toBeVisible()
  act(() =>
    emitUpdate({
      ...available,
      revision: 4,
      status: 'downloaded',
      downloadedPath: '/Downloads/sniffy.exe',
    })
  )
  wails.handlers.set('InstallUpdate', () => true)
  await user.click(screen.getByRole('button', { name: '立即安装' }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('InstallUpdate'))
})

test.each([
  ['run', '立即安装'],
  ['open', '打开安装镜像'],
  ['reveal', '打开所在文件夹'],
] as const)('%s 平台显示对应安装动作', async (installAction, label) => {
  resetWails({ ...available, status: 'downloaded', installAction })
  const user = userEvent.setup()
  render(<AboutView />)
  await user.click(await screen.findByRole('button', { name: label }))
  expect(wails.call).toHaveBeenCalledWith(
    bridgeName(
      installAction === 'reveal' ? 'RevealUpdateDownload' : 'InstallUpdate'
    )
  )
  if (installAction !== 'run')
    expect(
      screen.queryByRole('button', { name: '立即安装' })
    ).not.toBeInTheDocument()
})

test('当前平台无安装包时提供官网入口', async () => {
  resetWails({ ...available, asset: undefined })
  const user = userEvent.setup()
  render(<AboutView />)
  expect(
    await screen.findByText('当前平台没有对应的安装包，请到下载页自取')
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: '前往下载页' }))
  expect(wails.openURL).toHaveBeenCalledWith(DOWNLOAD_URL)
})

test.each<[Partial<UpdateState>, string]>([
  [{ status: 'checking' }, '正在检查更新…'],
  [{ status: 'latest' }, '已是最新版本'],
  [{ status: 'latest', checkedAt: '2026-09-18T00:00:00Z' }, '已是最新版本'],
  [{ status: 'latest', checkedAt: '日期不可用' }, '上次检查：日期不可用'],
  [{ status: 'error', error: '网络不可用' }, '网络不可用'],
  [{ status: 'error' }, '检查更新失败'],
  [{ status: 'latest', devBuild: true }, '开发构建，不参与版本比较'],
])('正确展示状态 %j', async (patch, text) => {
  resetWails(updateState(patch))
  render(<AboutView />)
  expect(await screen.findByText(text)).toBeVisible()
  if (patch.status === 'checking')
    expect(screen.getByRole('button', { name: '检查更新' })).toBeDisabled()
})

test('项目与文档链接按当前语言打开，浏览器启动失败不会破坏面板', async () => {
  const user = userEvent.setup()
  render(<AboutView />)
  await user.click(screen.getByRole('button', { name: '文档' }))
  expect(wails.openURL).toHaveBeenCalledWith('https://gosniffy.com/docs/')
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  await user.click(screen.getByRole('button', { name: 'Docs' }))
  expect(wails.openURL).toHaveBeenCalledWith('https://gosniffy.com/en/docs/')
  wails.openURL.mockRejectedValue(new Error('浏览器不可用'))
  await user.click(screen.getByRole('button', { name: 'Project Home' }))
  await waitFor(() => expect(wails.openURL).toHaveBeenCalledWith(REPO_URL))
  expect(screen.getByText('Sniffy')).toBeVisible()
})
