import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, test } from 'vitest'
import { bridgeName, resetWails, wails } from '@/test/wails'
import { useTun } from '../lib/tun'
import type { TrafficRow } from '../lib/types'
import type { MenuNode } from '../ui/Menu'
import { useTunAppMenu } from './tunAppMenu'

beforeEach(() => {
  resetWails()
  useTun.setState({ status: { revision: 1, state: 'running' }, stopPending: false })
})

const row = { id: '1', kind: 'http', process: 'Google Chrome Helper', processPath: '/Applications/Google Chrome.app/x' } as TrafficRow

/** 取「TUN 模式」子菜单里的某一项并点击。 */
function select(items: MenuNode[], label: string) {
  const submenu = items.find((n) => 'submenu' in n && n.submenu)
  const item = submenu && 'submenu' in submenu ? submenu.submenu?.find((n) => 'label' in n && n.label === label) : undefined
  if (!item || !('onSelect' in item) || !item.onSelect) throw new Error(`没有「${label}」`)
  act(() => item.onSelect?.())
}

test('没有进程信息的会话不出这组菜单', () => {
  const { result } = renderHook(useTunAppMenu)
  expect(result.current.menuFor({ id: '2', kind: 'http' } as TrafficRow)).toEqual([])
})

test('只抓这个应用：按会话的进程改名单，提示列出现在只抓的应用', async () => {
  wails.handlers.set('SetTunAppCaptured', () => ({
    app: 'Google Chrome',
    config: { tunAppMode: 'include', tunIncludeApps: ['curl', 'Google Chrome'] },
  }))
  const { result } = renderHook(useTunAppMenu)
  select(result.current.menuFor(row), '只抓这个应用')
  expect(wails.call).toHaveBeenCalledWith(bridgeName('SetTunAppCaptured'), '/Applications/Google Chrome.app/x', 'Google Chrome Helper', true)
  await waitFor(() => expect(result.current.notice?.title).toBe('已设为只抓「Google Chrome」'))
  expect(result.current.notice?.message).toBe(
    '经 TUN 的连接现在只抓：curl、Google Chrome。其余应用照常联网，但不解密、不记录。\n可以在「设置 → 代理与网络 → TUN 模式」里调整。',
  )
})

test('不抓这个应用：TUN 没开时说明开启后生效；名单很长时只列前几个', async () => {
  useTun.setState({ status: { revision: 2, state: 'off' } })
  const apps = ['a', 'b', 'c', 'd', 'e', 'f', 'Google Chrome']
  wails.handlers.set('SetTunAppCaptured', () => ({ app: 'Google Chrome', config: { tunAppMode: 'exclude', tunExcludeApps: apps } }))
  const { result } = renderHook(useTunAppMenu)
  select(result.current.menuFor(row), '不抓这个应用')
  expect(wails.call).toHaveBeenCalledWith(bridgeName('SetTunAppCaptured'), '/Applications/Google Chrome.app/x', 'Google Chrome Helper', false)
  await waitFor(() => expect(result.current.notice?.title).toBe('已设为不抓「Google Chrome」'))
  expect(result.current.notice?.message).toBe(
    '经 TUN 的连接现在不抓：a、b、c、d、e 等 7 个应用，其余照常抓取。\n开启 TUN 模式后生效。\n可以在「设置 → 代理与网络 → TUN 模式」里调整。',
  )
})

test('只抓名单里移出最后一个应用时说明都不抓了；失败时给出原因', async () => {
  wails.handlers.set('SetTunAppCaptured', () => ({ app: 'Google Chrome', config: { tunAppMode: 'include', tunIncludeApps: [] } }))
  const { result } = renderHook(useTunAppMenu)
  select(result.current.menuFor(row), '不抓这个应用')
  await waitFor(() => expect(result.current.notice?.title).toBe('已不再抓「Google Chrome」'))
  expect(result.current.notice?.message).toContain('名单空了：经 TUN 的应用现在都不抓，照常联网。')

  wails.handlers.set('SetTunAppCaptured', () => {
    throw new Error('应用名单有 257 项，超过上限 256')
  })
  select(result.current.menuFor(row), '只抓这个应用')
  await waitFor(() => expect(result.current.notice).toEqual({ tone: 'error', title: '没能修改应用名单', message: '应用名单有 257 项，超过上限 256' }))
})
