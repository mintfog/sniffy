import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { emitUpdate, resetWails, updateState, wails } from '@/test/wails'
import { useUpdate } from '../lib/update'
import { UpdateDialog } from './UpdateDialog'

const available = updateState({
  status: 'available',
  latest: '2.0.0',
  notify: true,
  asset: {
    name: 'sniffy.exe',
    url: 'https://cdn.gosniffy.com/sniffy.exe',
    size: 1_048_576,
  },
})

function Harness({ onClose }: { onClose: () => void }) {
  const { state, actions, installError } = useUpdate()
  if (!state.latest) return null
  return (
    <UpdateDialog
      state={state}
      actions={actions}
      installError={installError}
      onClose={onClose}
    />
  )
}

beforeEach(() => resetWails())

test('安装包失效、状态退回可下载时，弹窗仍说明原因', async () => {
  resetWails({ ...available, status: 'downloaded' })
  const user = userEvent.setup()
  render(<Harness onClose={() => {}} />)
  wails.handlers.set('InstallUpdate', () => {
    emitUpdate({ ...available, revision: 2 })
    throw new Error('安装包已不在原处，请重新下载')
  })
  await user.click(await screen.findByRole('button', { name: '立即安装' }))
  expect(await screen.findByText('安装包已不在原处，请重新下载')).toBeVisible()
  expect(screen.getByRole('button', { name: '下载更新' })).toBeVisible()
})

test('底栏随下载状态切换后焦点仍在弹窗内，Escape 可关闭', async () => {
  resetWails(available)
  const onClose = vi.fn()
  const user = userEvent.setup()
  render(<Harness onClose={onClose} />)
  wails.handlers.set('DownloadUpdate', () => ({
    ...available,
    revision: 2,
    status: 'downloading',
    downloaded: 0,
    total: available.asset!.size,
  }))
  await user.click(await screen.findByRole('button', { name: '下载更新' }))
  const dialog = await screen.findByRole('dialog')
  expect(await screen.findByText('正在下载安装包…')).toBeVisible()
  expect(dialog).toContainElement(document.activeElement as HTMLElement)
  act(() =>
    emitUpdate({
      ...available,
      revision: 3,
      status: 'downloaded',
    })
  )
  expect(await screen.findByText('2.0.0 安装包已下载')).toBeVisible()
  expect(dialog).toContainElement(document.activeElement as HTMLElement)
  await user.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledTimes(1)
})
