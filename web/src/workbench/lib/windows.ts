import { Events } from '@wailsio/runtime'
import { Bridge } from '@/lib/bridge'
import type { ToolId } from '../views/ToolboxView'

const TOOLBOX_TOOL_KEY = 'sniffy-toolbox-tool'

/**
 * 打开设置窗口。返回 Promise；非 Wails 环境（浏览器预览）会 reject，
 * 调用方可据此回退到主窗口内嵌渲染。
 */
export function openSettingsWindow(): Promise<void> {
  return Bridge.openWindow('settings')
}

/** 打开关于窗口。 */
export function openAboutWindow(): Promise<void> {
  return Bridge.openWindow('about')
}

/** 打开（或聚焦）插件工作室窗口。 */
export function openPluginsWindow(): Promise<void> {
  return Bridge.openWindow('plugins')
}

/** 打开（或聚焦）重写规则窗口。 */
export function openRulesWindow(): Promise<void> {
  return Bridge.openWindow('rules')
}

/** 构造器窗口读取「新窗口该预填哪条流量」的 localStorage 键。 */
const COMPOSE_SEED_KEY = 'sniffy-compose-seed'

/** 一次「打开构造器」的请求。token 让接收端能区分重复投递与两次真实点击。 */
export interface ComposeSeedRequest {
  token: string
  /** 蓝本 flow id；空串表示开一份空白草稿。 */
  flowId: string
}

/**
 * 待处理请求按队列存，不是单个值。
 *
 * 窗口尚未挂载时事件没人接，全靠 localStorage；此时若只存最后一个，连点两下
 * 「编辑后重发」就会把前一条静默吃掉——用户看到的是「点了没反应」。
 * 上限只防一件事：窗口始终打不开时这个键无限长下去。
 */
const COMPOSE_SEED_MAX = 8

function readSeedQueue(): ComposeSeedRequest[] {
  try {
    const raw = window.localStorage.getItem(COMPOSE_SEED_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    // localStorage 的内容不由本进程保证形态：非数组按单条处理，形状交给下面的 filter。
    const list = Array.isArray(parsed) ? parsed : [parsed]
    return list.filter((v): v is ComposeSeedRequest => typeof (v as ComposeSeedRequest)?.token === 'string')
  } catch {
    return []
  }
}

function writeSeedQueue(list: ComposeSeedRequest[]): void {
  try {
    if (list.length === 0) window.localStorage.removeItem(COMPOSE_SEED_KEY)
    else window.localStorage.setItem(COMPOSE_SEED_KEY, JSON.stringify(list.slice(-COMPOSE_SEED_MAX)))
  } catch {
    /* ignore */
  }
}

let composeSeq = 0

/**
 * 打开（或聚焦）请求构造器窗口，并让它新开一个页签。
 *
 * 同时写 localStorage 与发事件，因为两条路径的接收端不同：全新窗口挂载时读 localStorage，
 * 而已经开着的窗口只能靠事件——Windows 上关闭子窗口只是隐藏，再次打开走热复用、不会重新
 * 导航，URL 上的 query 仍是上一次的（见 internal/desktop/windowgc.go）。
 * token 让接收端对同一次请求只响应一次，避免新窗口同时命中两条路径开出两个页签。
 */
export function openComposeWindow(flowId = ''): Promise<void> {
  composeSeq += 1
  const req: ComposeSeedRequest = { token: `${Date.now()}-${composeSeq}`, flowId }
  writeSeedQueue([...readSeedQueue(), req])
  const p = Bridge.openWindow('compose', flowId ? `from=${encodeURIComponent(flowId)}` : '')
  p.then(() => {
    try {
      void Events.Emit('compose_seed', req)
    } catch {
      /* ignore */
    }
  }).catch(() => {})
  return p
}

/** 取出并清空全部待处理的预填请求（只该由构造器窗口挂载时调用）。 */
export function takeComposeSeed(): ComposeSeedRequest[] {
  const list = readSeedQueue()
  writeSeedQueue([])
  return list
}

/**
 * 摘掉一条已被事件消费的预填请求，免得下次冷启动又开一个同样的页签。
 * 只摘这一条：队列里可能还压着别的窗口刚投进来的请求。
 */
export function dropComposeSeed(token: string): void {
  writeSeedQueue(readSeedQueue().filter((r) => r.token !== token))
}

/**
 * 打开工具箱窗口并选中指定工具。
 * - 写入 localStorage：新开窗口挂载时据此定位工具；
 * - 触发 toolbox_select 事件：已打开的窗口实时切换。
 */
export function openToolboxWindow(tool?: ToolId): Promise<void> {
  if (tool) {
    try {
      window.localStorage.setItem(TOOLBOX_TOOL_KEY, tool)
    } catch {
      /* ignore */
    }
  }
  const p = Bridge.openWindow('tools', tool ? `tool=${tool}` : '')
  if (tool) {
    // 已打开的窗口经事件切换（新窗口走 localStorage / URL）
    p.then(() => {
      try {
        void Events.Emit('toolbox_select', tool)
      } catch {
        /* ignore */
      }
    }).catch(() => {})
  }
  return p
}

/** 从子窗口请求主窗口切换视图并置前（如设置里的「管理证书」）。 */
export function requestMainNav(view: string): void {
  try {
    void Events.Emit('main_nav', view)
  } catch {
    /* ignore */
  }
  Bridge.focusMain().catch(() => {})
}
