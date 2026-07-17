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
import type { HttpSession, InterceptRule, Statistics, WebSocketSession, StreamSession } from '@/types'

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

/** 按需拉取的原始消息体（对应 Go 侧 service.BodyDTO，用于图片等二进制内容预览）。 */
export interface SessionBody {
  mime: string
  size: number
  /** 原始字节的标准 base64；tooLarge 时为空。 */
  base64?: string
  /** 超过预览上限：不返回字节，仅元信息。 */
  tooLarge?: boolean
}

export interface StreamSessionPage {
  data: StreamSession[]
  total: number
}

export interface AppConfig {
  port: number
  enableHTTPS: boolean
  recording: boolean
  maxFlows?: number
  upstream?: boolean
  upstreamAddr?: string
  systemProxy?: boolean
  autoSystemProxy?: boolean
  /** 关闭主窗口后是否留在系统托盘;false 则关闭 = 完全退出。 */
  runInBackground?: boolean
}

/** 代理实际监听的绑定地址/端口（对应 Go 侧 ListenInfo，只读）。 */
export interface ListenInfo {
  host: string
  port: number
}

/** 一张本机网卡上的可用内网 IPv4 候选（对应 Go 侧 netinfo.LANAddr）。 */
export interface LANAddr {
  ip: string
  /** 网卡设备名（en0/eth0/Windows 友好名）。 */
  interface: string
  /** 人类可读名（如 macOS 的 Wi-Fi/以太网）；取不到时同 interface。 */
  label: string
  /** 是否 RFC1918 私有网段。 */
  private: boolean
  /** 是否内核默认出站网卡的源地址。 */
  preferred: boolean
}

export type PluginMeta = Record<string, unknown>

/** 一条导入的服务端证书摘要（对应 Go 侧 service.ServerCertDTO，不含私钥）。 */
export interface ServerCert {
  /** 证书指纹（SHA-256 hex），删除时按它引用。 */
  id: string
  /** 从证书 SAN（无 SAN 时回退 CN）提取的匹配域名。 */
  hosts: string[]
  subject: string
  issuer: string
  /** 证书有效期截止（RFC3339）。 */
  notAfter: string
}

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
  /** 按需拉取请求/响应体原始字节（base64），用于预览图片等二进制内容。 */
  getSessionBody: (id: string, source: 'request' | 'response') =>
    call<SessionBody | null>('GetSessionBody', id, source),
  /** 把请求/响应体原始字节另存为本地文件（系统保存对话框；不受预览大小上限约束）。 */
  saveSessionBody: (id: string, source: 'request' | 'response') =>
    call<boolean>('SaveSessionBody', id, source),
  deleteSession: (id: string) => call<void>('DeleteSession', id),
  clearSessions: () => call<void>('ClearSessions'),

  // WebSocket 会话（实时帧仍经 ws_message 事件推送；这里用于启动/重连时回填历史会话）
  getWSSessions: (page: number, pageSize: number) => call<WSSessionPage>('GetWSSessions', page, pageSize),
  getWSSession: (id: string) => call<WebSocketSession | null>('GetWSSession', id),

  // 流式会话(SSE / gRPC / 分块流;实时消息经 stream_message 事件推送,这里回填历史)
  getStreamSessions: (page: number, pageSize: number) => call<StreamSessionPage>('GetStreamSessions', page, pageSize),
  getStreamSession: (id: string) => call<StreamSession | null>('GetStreamSession', id),

  // 统计
  getStatistics: () => call<Statistics>('GetStatistics'),

  // 配置
  getConfig: () => call<AppConfig>('GetConfig'),
  updateConfig: (patch: Record<string, unknown>) => call<AppConfig>('UpdateConfig', patch),
  /** 代理实际监听的绑定地址/端口（只读，启动期确定，不可经 updateConfig 修改）。 */
  getListenInfo: () => call<ListenInfo>('GetListenInfo'),
  /** 本机所有可用内网 IPv4 候选(推荐项在前)；多网卡时供用户自选。非 Wails 环境会 reject。 */
  getLanIPs: () => call<LANAddr[]>('GetLANIPs'),

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
  /** 把根证书装入本机系统信任库;授权对话框由后端按平台触发。 */
  installCAToSystem: () => call<void>('InstallCAToSystem'),
  /** 按格式弹保存对话框写盘根证书;format ∈ pem|crt|der|p12|bundle;password 仅 p12 生效。 */
  exportCACertAs: (format: 'pem' | 'crt' | 'der' | 'p12' | 'bundle', password: string) =>
    call<boolean>('ExportCACertAs', format, password),
  /** 弹打开对话框选择要导入的根证书文件(.p12/.pfx/.pem/.crt),返回绝对路径或空串。 */
  pickImportCAFile: () => call<string>('PickImportCAFile'),
  /** 从给定路径导入根证书(自动分流 PKCS12 与 PEM Bundle),返回新根 PEM;失败 reject。 */
  importCAFromFile: (path: string, password: string) => call<string>('ImportCAFromFile', path, password),

  // 导入的服务端证书(应对固定证书场景:用真实证书 + 私钥替代 MITM 现签的伪造证书)
  /** 列出已按主机导入的服务端证书摘要(不含私钥)。 */
  getServerCerts: () => call<ServerCert[]>('GetServerCerts'),
  /** 校验并导入一条服务端证书 + 私钥(PEM),匹配域名从证书自身提取;不匹配或无可用域名时 reject。 */
  importServerCert: (certPEM: string, keyPEM: string) =>
    call<ServerCert>('ImportServerCert', certPEM, keyPEM),
  /** 按证书指纹删除导入证书。 */
  deleteServerCert: (id: string) => call<void>('DeleteServerCert', id),

  // 插件
  getPlugins: () => call<PluginMeta[]>('GetPlugins'),
  enablePlugin: (id: string, enabled: boolean) => call<void>('EnablePlugin', id, enabled),
  getPluginSource: (id: string) => call<string>('GetPluginSource', id),
  savePluginSource: (id: string, source: string) => call<void>('SavePluginSource', id, source),
  createPlugin: (meta: PluginMeta, source: string) => call<PluginMeta>('CreatePlugin', meta, source),
  deletePlugin: (id: string) => call<void>('DeletePlugin', id),
  updatePluginManifest: (id: string, patch: PluginMeta) => call<void>('UpdatePluginManifest', id, patch),
  clearPluginLogs: (id: string) => call<void>('ClearPluginLogs', id),

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
