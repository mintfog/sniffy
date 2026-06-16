/**
 * Wails v3 后端桥接层。
 *
 * 桌面端经 `@wailsio/runtime` 的 Call.ByName 直接调用 Go 侧 Bridge 的导出方法,
 * 完整方法名为 `<包导入路径>.Bridge.<方法名>`。这取代了旧的 HTTP/REST 调用,
 * 不再需要进程内的本地 API 服务。
 *
 * 注意:方法名字符串必须与 internal/desktop/app.go 中 Bridge 的导出方法严格对应。
 */
import { Call } from '@wailsio/runtime'
import type { HttpSession, InterceptRule, Statistics, WebSocketSession } from '@/types'

/** Bridge 类型的完整限定名前缀(= Go 包导入路径 + 结构体名)。 */
const NS = 'github.com/mintfog/sniffy/internal/desktop.Bridge'

/** 调用一个 Bridge 方法并按 T 解析返回值。Call.ByName 返回 CancellablePromise。 */
function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(`${NS}.${method}`, ...args) as unknown as Promise<T>
}

export interface SessionPage {
  data: HttpSession[]
  total: number
}

export interface WSSessionPage {
  data: WebSocketSession[]
  total: number
}

export interface AppConfig {
  port: number
  enableHTTPS: boolean
  recording: boolean
  maxFlows?: number
  upstream?: boolean
  upstreamAddr?: string
}

/** 代理实际监听的绑定地址/端口（对应 Go 侧 ListenInfo，只读）。 */
export interface ListenInfo {
  host: string
  port: number
}

export type PluginMeta = Record<string, unknown>

/** 全局断点开关状态（对应 Go 侧 GlobalBreakState）。 */
export interface GlobalBreakState {
  onRequest: boolean
  onResponse: boolean
}

/** URL 断点规则（对应 Go 侧 pipeline.BreakRule）。 */
export interface BreakRule {
  id: string
  url: string
  onRequest: boolean
  onResponse: boolean
  enabled: boolean
}

/** 桥接 API:每个方法对应 Go 侧 Bridge 的一个导出方法。 */
export const Bridge = {
  // 会话
  getSessions: (page: number, pageSize: number) => call<SessionPage>('GetSessions', page, pageSize),
  getSession: (id: string) => call<HttpSession | null>('GetSession', id),
  deleteSession: (id: string) => call<void>('DeleteSession', id),
  clearSessions: () => call<void>('ClearSessions'),

  // WebSocket 会话（实时帧仍经 ws_message 事件推送；这里用于启动/重连时回填历史会话）
  getWSSessions: (page: number, pageSize: number) => call<WSSessionPage>('GetWSSessions', page, pageSize),
  getWSSession: (id: string) => call<WebSocketSession | null>('GetWSSession', id),

  // 统计
  getStatistics: () => call<Statistics>('GetStatistics'),

  // 配置
  getConfig: () => call<AppConfig>('GetConfig'),
  updateConfig: (patch: Record<string, unknown>) => call<AppConfig>('UpdateConfig', patch),
  /** 代理实际监听的绑定地址/端口（只读，启动期确定，不可经 updateConfig 修改）。 */
  getListenInfo: () => call<ListenInfo>('GetListenInfo'),
  /** 本机内网 IPv4(用于展示代理监听地址)；非 Wails 环境会 reject。 */
  getLanIP: () => call<string>('GetLANIP'),

  // 录制
  startRecording: () => call<void>('StartRecording'),
  stopRecording: () => call<void>('StopRecording'),
  isRecording: () => call<boolean>('IsRecording'),

  // 证书
  getCertificatePEM: () => call<string>('GetCertificatePEM'),

  // 拦截规则
  getRules: () => call<InterceptRule[]>('GetRules'),
  createRule: (rule: InterceptRule) => call<InterceptRule>('CreateRule', rule),
  updateRule: (id: string, rule: InterceptRule) => call<InterceptRule | null>('UpdateRule', id, rule),
  toggleRule: (id: string, enabled: boolean) => call<boolean>('ToggleRule', id, enabled),
  deleteRule: (id: string) => call<void>('DeleteRule', id),

  // 重发 / 证书
  resendFlow: (id: string) => call<boolean>('ResendFlow', id),
  regenerateCA: () => call<string>('RegenerateCA'),

  // 插件
  getPlugins: () => call<PluginMeta[]>('GetPlugins'),
  enablePlugin: (id: string, enabled: boolean) => call<void>('EnablePlugin', id, enabled),
  getPluginSource: (id: string) => call<string>('GetPluginSource', id),
  savePluginSource: (id: string, source: string) => call<void>('SavePluginSource', id, source),

  // 断点（暂停的 flow）
  getBreakpoints: () => call<unknown[]>('GetBreakpoints'),
  resumeBreakpoint: (id: string, edited: unknown) => call<boolean>('ResumeBreakpoint', id, edited),
  abortBreakpoint: (id: string) => call<boolean>('AbortBreakpoint', id),
  setGlobalBreak: (onRequest: boolean, onResponse: boolean) =>
    call<void>('SetGlobalBreak', onRequest, onResponse),
  getGlobalBreak: () => call<GlobalBreakState>('GetGlobalBreak'),

  // URL 断点规则
  getBreakRules: () => call<BreakRule[]>('GetBreakRules'),
  addBreakRule: (url: string, onRequest: boolean, onResponse: boolean) =>
    call<BreakRule>('AddBreakRule', url, onRequest, onResponse),
  updateBreakRule: (id: string, url: string, onRequest: boolean, onResponse: boolean, enabled: boolean) =>
    call<boolean>('UpdateBreakRule', id, url, onRequest, onResponse, enabled),
  toggleBreakRule: (id: string, enabled: boolean) => call<boolean>('ToggleBreakRule', id, enabled),
  deleteBreakRule: (id: string) => call<void>('DeleteBreakRule', id),

  // 窗口（桌面外壳）
  /** 打开（或聚焦已存在的）独立系统窗口承载某个页面：settings | tools | about。 */
  openWindow: (view: string, query = '') => call<void>('OpenWindow', view, query),
  /** 把主窗口带到前台。 */
  focusMain: () => call<void>('FocusMain'),
  /** 用菜单模型重建 macOS 顶部系统菜单栏（仅 mac 调用；见 workbench/shell/nativeMenu.ts）。 */
  setMenu: (items: unknown[]) => call<void>('SetMenu', items),
  /** 弹系统「保存文件」对话框并由 Go 写盘；返回是否已保存。 */
  saveTextFile: (defaultName: string, content: string) => call<boolean>('SaveTextFile', defaultName, content),
}
