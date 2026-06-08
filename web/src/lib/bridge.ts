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
import type { HttpSession, InterceptRule, Statistics } from '@/types'

/** Bridge 类型的完整限定名前缀(= Go 包导入路径 + 结构体名)。 */
const NS = 'github.com/mintfog/sniffy/internal/desktop.Bridge'

/** 调用一个 Bridge 方法并按 T 解析返回值。Call.ByName 返回 CancellablePromise(可直接 await)。 */
function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return Call.ByName(`${NS}.${method}`, ...args) as unknown as Promise<T>
}

export interface SessionPage {
  data: HttpSession[]
  total: number
}

export interface AppConfig {
  port: number
  host: string
  enableHTTPS: boolean
  recording: boolean
}

export type PluginMeta = Record<string, unknown>

/** 桥接 API:每个方法对应 Go 侧 Bridge 的一个导出方法。 */
export const Bridge = {
  // 会话
  getSessions: (page: number, pageSize: number) => call<SessionPage>('GetSessions', page, pageSize),
  getSession: (id: string) => call<HttpSession | null>('GetSession', id),
  deleteSession: (id: string) => call<void>('DeleteSession', id),
  clearSessions: () => call<void>('ClearSessions'),

  // 统计
  getStatistics: () => call<Statistics>('GetStatistics'),

  // 配置
  getConfig: () => call<AppConfig>('GetConfig'),
  updateConfig: (patch: Record<string, unknown>) => call<AppConfig>('UpdateConfig', patch),

  // 录制
  startRecording: () => call<void>('StartRecording'),
  stopRecording: () => call<void>('StopRecording'),
  isRecording: () => call<boolean>('IsRecording'),

  // 证书
  getCertificatePEM: () => call<string>('GetCertificatePEM'),

  // 拦截规则
  getRules: () => call<InterceptRule[]>('GetRules'),
  createRule: (rule: InterceptRule) => call<InterceptRule>('CreateRule', rule),
  toggleRule: (id: string, enabled: boolean) => call<boolean>('ToggleRule', id, enabled),
  deleteRule: (id: string) => call<void>('DeleteRule', id),

  // 插件
  getPlugins: () => call<PluginMeta[]>('GetPlugins'),
  enablePlugin: (id: string, enabled: boolean) => call<void>('EnablePlugin', id, enabled),
  getPluginSource: (id: string) => call<string>('GetPluginSource', id),
  savePluginSource: (id: string, source: string) => call<void>('SavePluginSource', id, source),

  // 断点
  getBreakpoints: () => call<unknown[]>('GetBreakpoints'),
  resumeBreakpoint: (id: string, edited: unknown) => call<boolean>('ResumeBreakpoint', id, edited),
  abortBreakpoint: (id: string) => call<boolean>('AbortBreakpoint', id),
  setGlobalBreak: (onRequest: boolean, onResponse: boolean) =>
    call<void>('SetGlobalBreak', onRequest, onResponse),

  // 窗口（桌面外壳）
  /** 打开（或聚焦已存在的）独立系统窗口承载某个页面：settings | tools | about。 */
  openWindow: (view: string, query = '') => call<void>('OpenWindow', view, query),
  /** 把主窗口带到前台。 */
  focusMain: () => call<void>('FocusMain'),
  /** 弹系统「保存文件」对话框并由 Go 写盘；返回是否已保存。 */
  saveTextFile: (defaultName: string, content: string) => call<boolean>('SaveTextFile', defaultName, content),
}
