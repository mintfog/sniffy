/**
 * 前端报错落盘：未捕获错误 / 未处理的 Promise 拒绝 / console.error|warn 转发到后端，
 * 写入独立的前端日志（<ConfigDir>/logs/sniffy-web-<日期>.log），与后端日志分文件。
 * 非 Wails 环境（浏览器预览）下转发失败，静默忽略。
 */
import { Call } from '@wailsio/runtime'

const METHOD = 'github.com/mintfog/sniffy/internal/desktop.Bridge.LogFrontend'

type Level = 'error' | 'warn' | 'info'

// 抑制相同消息的短时间重复（React 等可能连刷同一条），避免灌爆日志文件
let lastMessage = ''
let lastAt = 0

function ship(level: Level, message: string) {
  if (!message) return
  const now = Date.now()
  if (message === lastMessage && now - lastAt < 1000) return
  lastMessage = message
  lastAt = now
  try {
    void (Call.ByName(METHOD, level, message) as Promise<unknown>).catch(() => {})
  } catch {
    /* 非 Wails 环境：忽略 */
  }
}

function describe(v: unknown): string {
  if (typeof v === 'string') return v
  if (v instanceof Error) return `${v.message}\n${v.stack ?? ''}`.trim()
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

let installed = false

/** 安装全局报错捕获并转发到后端日志。重复调用无副作用。在 main 入口尽早调用一次。 */
export function installFrontendLog() {
  if (installed || typeof window === 'undefined') return
  installed = true

  window.addEventListener('error', (e) => {
    const where = e.filename ? ` @ ${e.filename}:${e.lineno}:${e.colno}` : ''
    ship('error', `${e.message}${where}\n${e.error?.stack ?? ''}`.trim())
  })

  window.addEventListener('unhandledrejection', (e) => {
    ship('error', `unhandledrejection: ${describe(e.reason)}`)
  })

  // 包裹 console.error/warn：照常打到控制台，同时转发后端。ship 内部不再调用 console，无递归。
  for (const level of ['error', 'warn'] as const) {
    const orig = console[level].bind(console)
    console[level] = (...args: unknown[]) => {
      orig(...args)
      ship(level, args.map(describe).join(' '))
    }
  }
}
