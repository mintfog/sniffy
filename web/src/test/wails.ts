import { vi } from 'vitest'
import type { UpdateState } from '@/lib/bridge'

type Listener = (event: { data: unknown }) => void

// 替换桌面运行时边界，保留真实 Bridge 的方法名、参数和 Hook/组件之间的数据流。
const runtime = vi.hoisted(() => ({
  call: vi.fn(),
  on: vi.fn(),
  openURL: vi.fn(),
  handlers: new Map<string, (...args: unknown[]) => unknown>(),
  listeners: new Map<string, Set<Listener>>(),
}))

export const wails = runtime

vi.mock('@wailsio/runtime', () => ({
  Call: { ByName: runtime.call },
  Events: { On: runtime.on },
  Browser: { OpenURL: runtime.openURL },
}))

export function updateState(patch: Partial<UpdateState> = {}): UpdateState {
  return {
    revision: 1,
    status: 'idle',
    current: '1.0.0',
    notify: false,
    autoCheck: true,
    installAction: 'run',
    ...patch,
  }
}

export function resetWails(initial = updateState()) {
  wails.handlers.clear()
  wails.listeners.clear()
  wails.call
    .mockReset()
    .mockImplementation(async (name: string, ...args: unknown[]) => {
      const method = name.split('.').at(-1)!
      if (wails.handlers.has(method))
        return wails.handlers.get(method)!(...args)
      if (method === 'GetUpdateState') return initial
      if (method === 'GetVersion') return initial.current
      return true
    })
  wails.on
    .mockReset()
    .mockImplementation((name: string, listener: Listener) => {
      const listeners = wails.listeners.get(name) ?? new Set<Listener>()
      listeners.add(listener)
      wails.listeners.set(name, listeners)
      return () => {
        listeners.delete(listener)
      }
    })
  wails.openURL.mockReset().mockResolvedValue(undefined)
}

export function emitUpdate(state: UpdateState | undefined) {
  wails.listeners
    .get('update_state')
    ?.forEach(listener => listener({ data: state }))
}

export function bridgeName(method: string) {
  return `github.com/mintfog/sniffy/internal/desktop.Bridge.${method}`
}

export function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => {
    resolve = done
  })
  return { promise, resolve }
}
