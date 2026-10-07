import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { bridgeName, deferred, resetWails, wails } from '@/test/wails'
import type { InterceptRule } from '@/types'
import { openRulesWindow } from '../lib/windows'
import { RulesView } from './RulesView'

const rules: InterceptRule[] = [
  { id: 'first', name: '首个规则', enabled: true, conditions: [], actions: [], priority: 0, logicOperator: 'AND', createdAt: '', updatedAt: '' },
  { id: 'target', name: '目标规则', enabled: true, conditions: [], actions: [], priority: 10, logicOperator: 'AND', createdAt: '', updatedAt: '' },
]

beforeEach(() => {
  resetWails()
  localStorage.removeItem('sniffy-rule-selection')
  window.history.replaceState({}, '', '/')
  wails.handlers.set('GetRules', () => rules)
})

afterEach(() => {
  localStorage.removeItem('sniffy-rule-selection')
  window.history.replaceState({}, '', '/')
})

test('新窗口根据待查看规则选中对应配置', async () => {
  await openRulesWindow('target')
  render(<RulesView />)
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
  expect(localStorage.getItem('sniffy-rule-selection')).toBeNull()
})

test('URL 中的规则标识可定位配置', async () => {
  window.history.replaceState({}, '', '/?w=rules&rule=target')
  render(<RulesView />)
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
})

test('已打开的窗口接收定位事件，并清空搜索以显示目标规则', async () => {
  render(<RulesView />)
  expect(await screen.findByDisplayValue('首个规则')).toBeInTheDocument()
  fireEvent.change(screen.getByPlaceholderText('搜索规则…'), { target: { value: '首个' } })
  await act(async () => { await openRulesWindow('target') })
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
  expect(screen.getByPlaceholderText('搜索规则…')).toHaveValue('')
})

test('已删除的规则显示明确提示', async () => {
  await openRulesWindow('deleted')
  render(<RulesView />)
  expect(await screen.findByText('规则已删除或不存在')).toBeInTheDocument()
  expect(screen.queryByDisplayValue('首个规则')).not.toBeInTheDocument()
})

test('配置加载失败时给出提示', async () => {
  wails.handlers.set('GetRules', () => Promise.reject(new Error('读取失败')))
  await openRulesWindow('target')
  render(<RulesView />)
  expect(await screen.findByText('读取规则配置失败')).toBeInTheDocument()
})

test('配置加载期间连续定位会选中最新目标', async () => {
  const loading = deferred<InterceptRule[]>()
  wails.handlers.set('GetRules', () => loading.promise)
  render(<RulesView />)
  await act(async () => { await openRulesWindow('first') })
  await act(async () => { await openRulesWindow('target') })
  await act(async () => { loading.resolve(rules) })
  expect(await screen.findByDisplayValue('目标规则')).toBeInTheDocument()
  expect(wails.call.mock.calls.filter(([name]) => name === bridgeName('GetRules'))).toHaveLength(1)
})

test('定位后切换开关保存尚在防抖等待的编辑', async () => {
  const updates: InterceptRule[] = []
  wails.handlers.set('UpdateRule', (_id, value) => {
    updates.push(value as InterceptRule)
    return value
  })
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => { await openRulesWindow('target') })
  expect(screen.getByDisplayValue('目标规则')).toBeInTheDocument()
  fireEvent.click(screen.getByText('已编辑规则'))
  expect(screen.getByDisplayValue('已编辑规则')).toBeInTheDocument()
  fireEvent.click(screen.getAllByRole('switch').at(-1)!)
  await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
  expect(updates).toHaveLength(1)
  expect(updates[0]).toMatchObject({ id: 'first', name: '已编辑规则', enabled: false })
})

test('定位保留正在保存的编辑，并按编辑顺序保存开关', async () => {
  const firstSave = deferred<InterceptRule>()
  const updates: InterceptRule[] = []
  wails.handlers.set('UpdateRule', (_id, value) => {
    updates.push(value as InterceptRule)
    return updates.length === 1 ? firstSave.promise : value
  })
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => { await vi.advanceTimersByTimeAsync(400) })
  expect(updates).toHaveLength(1)
  await act(async () => { await openRulesWindow('target') })
  fireEvent.click(screen.getByText('已编辑规则'))
  fireEvent.click(screen.getAllByRole('switch').at(-1)!)
  expect(updates).toHaveLength(1)
  await act(async () => { firstSave.resolve(updates[0]) })
  expect(updates).toHaveLength(2)
  expect(updates[1]).toMatchObject({ id: 'first', name: '已编辑规则', enabled: false })
})

test('保存失败的编辑在再次定位时继续保留', async () => {
  wails.handlers.set('UpdateRule', () => Promise.reject(new Error('保存失败')))
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => { await vi.advanceTimersByTimeAsync(400) })
  await act(async () => { await openRulesWindow('target') })
  fireEvent.click(screen.getByText('已编辑规则'))
  expect(screen.getByDisplayValue('已编辑规则')).toBeInTheDocument()
})

test('定位到已保存的规则时保留当前编辑', async () => {
  wails.handlers.set('UpdateRule', (_id, value) => value)
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => { await vi.advanceTimersByTimeAsync(400) })
  await act(async () => { await openRulesWindow('target') })
  await act(async () => { await openRulesWindow('first') })
  expect(screen.getByDisplayValue('已编辑规则')).toBeInTheDocument()
  expect(wails.call.mock.calls.filter(([name]) => name === bridgeName('GetRules'))).toHaveLength(1)
})

test('定位后删除规则会取消防抖保存，并提示规则不存在', async () => {
  const updates: InterceptRule[] = []
  wails.handlers.set('UpdateRule', (_id, value) => { updates.push(value as InterceptRule); return value })
  render(<RulesView />)
  const input = await screen.findByDisplayValue('首个规则')
  vi.useFakeTimers()
  fireEvent.change(input, { target: { value: '已编辑规则' } })
  await act(async () => { await openRulesWindow('target') })
  fireEvent.click(screen.getByText('已编辑规则'))
  fireEvent.click(screen.getByTitle('删除规则'))
  await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
  expect(screen.queryByText('首个规则')).not.toBeInTheDocument()
  expect(screen.queryByText('已编辑规则')).not.toBeInTheDocument()
  expect(updates).toHaveLength(0)
  await act(async () => { await openRulesWindow('first') })
  expect(screen.getByText('规则已删除或不存在')).toBeInTheDocument()
})
