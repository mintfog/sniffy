import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import {
  bridgeName,
  emitUpdate,
  resetWails,
  updateState,
  wails,
} from '@/test/wails'
import { StatusBar } from './StatusBar'

test('新版通知打开关于窗口，跳过后隐藏，更高版本重新提醒', async () => {
  resetWails()
  const user = userEvent.setup()
  render(
    <StatusBar
      proxyAddr="127.0.0.1:8080"
      capturing
      connected
      total={10}
      filtered={10}
    />
  )
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  act(() =>
    emitUpdate(
      updateState({
        revision: 2,
        status: 'available',
        latest: '2.0.0',
        notify: true,
      })
    )
  )
  await user.click(screen.getByRole('button', { name: /2\.0\.0/ }))
  expect(wails.call).toHaveBeenCalledWith(bridgeName('OpenWindow'), 'about', '')
  act(() =>
    emitUpdate(
      updateState({
        revision: 3,
        latest: '2.0.0',
        skippedVersion: '2.0.0',
        notify: false,
      })
    )
  )
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  act(() =>
    emitUpdate(updateState({ revision: 4, latest: '2.1.0', notify: true }))
  )
  wails.handlers.set('OpenWindow', () => {
    throw new Error('窗口不可用')
  })
  await user.click(screen.getByRole('button', { name: /2\.1\.0/ }))
  expect(screen.getByRole('button', { name: /2\.1\.0/ })).toBeVisible()
})

test('更新提示与暂停、断点及选择数量可以同时展示', async () => {
  resetWails(
    updateState({ status: 'available', latest: '2.0.0', notify: true })
  )
  const user = userEvent.setup()
  const onGoBreakpoints = vi.fn()
  const props = {
    proxyAddr: ':8080',
    capturing: false,
    connected: false,
    total: 10,
    filtered: 2,
  }
  const { rerender } = render(
    <StatusBar
      {...props}
      pausedCount={1}
      onGoBreakpoints={onGoBreakpoints}
      selectedCount={2}
    />
  )
  expect(await screen.findByRole('button', { name: /2\.0\.0/ })).toBeVisible()
  await user.click(screen.getByRole('button', { name: '1 已暂停' }))
  expect(onGoBreakpoints).toHaveBeenCalledOnce()
  rerender(<StatusBar {...props} selectedSeq={3} />)
  expect(screen.getByText('#3')).toBeVisible()
  rerender(<StatusBar {...props} selectedCount={1} />)
  expect(screen.queryByText('#3')).not.toBeInTheDocument()
})
