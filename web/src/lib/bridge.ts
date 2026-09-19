/**
 * Wails v3 后端桥接层。
 *
 * 桌面端经 `@wailsio/runtime` 的 Call.ByName 直接调用 Go 侧 Bridge 的导出方法,
 * 完整方法名为 `<包导入路径>.Bridge.<方法名>`，桌面进程通过 Bridge 完成管理操作。
 *
 * 注意:方法名字符串必须与 internal/desktop/app.go 中 Bridge 的导出方法严格对应。
 */
import { Call } from '@wailsio/runtime'
import type { HttpSession, InterceptRule, Statistics, WebSocketSession, StreamSession } from '@/types'
import type { ResumePatch } from '@/workbench/views/breakpoints/model'

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

/** 消息体元信息（对应 Go 侧 service.BodyInfoDTO）：只有 MIME 与大小，内容另经 sessionBodyUrl 取。 */
export interface SessionBodyInfo {
  mime: string
  size: number
}

/** 请求构造器发送一次请求的入参（对应 Go 侧 flow.RequestSpec）。 */
export interface RequestSpec {
  /** 空或缺省按 http 解释；graphql 与 http 共用处理路径，并附加后端标签。 */
  kind?: 'http' | 'graphql' | 'sse' | 'ws'
  method: string
  url: string
  /** 有序头部；保留大小写与重复项，后端按此顺序原样写线。 */
  headers: [string, string][]
  /**
   * 与 headers 下标对齐的值字节旁路；非空项按解码后的字节写线，空项采用对应 headers 值。
   * 列表存在时按整份头解析；全部头值为合法 UTF-8 时省略该字段。
   */
  headersB64?: string[]
  body: string
  /** 蓝本 flow id，仅作溯源标记；空串表示空白构造。 */
  fromId: string
  /** 是否让这次请求经过插件 / 重写规则 / 断点。 */
  viaPipeline: boolean
}

/** 以某条已捕获请求为蓝本预填构造器时的保真快照（对应 Go 侧 service.ComposeSeedDTO）。 */
export interface ComposeSeed {
  flowId: string
  method: string
  url: string
  headers: [string, string][]
  /**
   * 与 headers 下标对齐的值字节旁路；构造器按这里的字节值生成出站头部。
   */
  headersB64?: string[]
  body?: string
  bodySize: number
  /** 原体采用二进制形态，构造器显示体积信息。 */
  bodyBinary?: boolean
  /** 原体超过 service.MaxComposeSeedBytes，构造器显示大小限制信息。 */
  bodyTooLarge?: boolean
}

/**
 * 消息体字节流的地址：指向 Go 侧挂在资源服务器上的 /body 路由（见 internal/desktop/bodyroute.go），
 * 支持 Range，可直接作为 <video>/<audio> 的 src；大体积媒体正文由 Go 侧资源路由提供。
 */
export function sessionBodyUrl(id: string, source: 'request' | 'response'): string {
  return `/body/${encodeURIComponent(id)}?source=${source}`
}

export interface StreamSessionPage {
  data: StreamSession[]
  total: number
}

export interface AppConfig {
  port: number
  enableHTTPS: boolean
  tlsInsecureHosts?: string[]
  recording: boolean
  maxFlows?: number
  upstream?: boolean
  upstreamAddr?: string
  upstreamAuth?: boolean
  upstreamUsername?: string
  upstreamPasswordSet?: boolean
  proxyAuth?: boolean
  proxyUsername?: string
  proxyPasswordSet?: boolean
  systemProxy?: boolean
  autoSystemProxy?: boolean
  throttle?: boolean
  throttleKiBps?: number
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

/** 清单里匹配本机平台的下载产物（对应 Go 侧 service.UpdateAssetDTO）。 */
export interface UpdateAsset {
  name: string
  url: string
  size: number
}

/** 对应 Go 侧 service.UpdateStateDTO，经调用返回值与 update_state 事件传递完整快照。 */
export interface UpdateState {
  /** 更新状态与更新偏好的修订号，用于丢弃迟到的旧快照。 */
  revision: number
  status: 'idle' | 'checking' | 'latest' | 'available' | 'downloading' | 'downloaded' | 'error'
  current: string
  latest?: string
  publishedAt?: string
  notesUrl?: string
  asset?: UpdateAsset
  checkedAt?: string
  error?: string
  /** 失败的阶段，用于显示对应提示与重试入口。 */
  errorStage?: 'check' | 'download'
  /** 该不该提醒用户：有新版、未被跳过、且不是开发构建。 */
  notify: boolean
  skippedVersion?: string
  autoCheck: boolean
  devBuild?: boolean
  downloadedPath?: string
  downloaded?: number
  /** 下载期间为清单登记的正数大小。 */
  total?: number
  /**
   * 由产物类型与 Go 构建目标决定：run 启动安装并退出应用，open 打开镜像，reveal 打开所在目录。
   */
  installAction: 'run' | 'open' | 'reveal'
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
  /** 只取消息体的 MIME 与大小（不搬运字节）；体为空或落盘副本已被清理时返回 null。 */
  getSessionBodyInfo: (id: string, source: 'request' | 'response') =>
    call<SessionBodyInfo | null>('GetSessionBodyInfo', id, source),
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
  /** 构建期注入的版本号（对应 Go 侧 internal/version.Get）；浏览器直开时 reject，调用方回退前端常量。 */
  getVersion: () => call<string>('GetVersion'),
  /** 本机所有可用内网 IPv4 候选(推荐项在前)；多网卡时供用户自选。非 Wails 环境会 reject。 */
  getLanIPs: () => call<LANAddr[]>('GetLANIPs'),

  // 更新
  getUpdateState: () => call<UpdateState>('GetUpdateState'),
  /** 立即查一次发布清单；清单源连不上时状态里带失败原因，不会 reject。 */
  checkUpdate: () => call<UpdateState>('CheckUpdate'),
  /** 后台下载本机平台的安装包；本平台没有对应产物时 reject。进度经事件推送。 */
  downloadUpdate: () => call<UpdateState>('DownloadUpdate'),
  cancelUpdateDownload: () => call<UpdateState>('CancelUpdateDownload'),
  /** 开关启动后的静默检查（持久化到 config.json）。 */
  setUpdateAutoCheck: (enabled: boolean) => call<UpdateState>('SetUpdateAutoCheck', enabled),
  /** 记下不再提醒的版本号；空串表示跳过当前查到的最新版。 */
  skipUpdateVersion: (version = '') => call<UpdateState>('SkipUpdateVersion', version),
  clearSkippedUpdateVersion: () => call<UpdateState>('ClearSkippedUpdateVersion'),
  /** 在系统文件管理器里打开安装包所在目录；返回是否已打开。 */
  revealUpdateDownload: () => call<boolean>('RevealUpdateDownload'),
  /** 执行 installAction；Windows 启动安装成功后会退出应用，调用可能无法返回。 */
  installUpdate: () => call<boolean>('InstallUpdate'),

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

  // 重发 / 构造器 / 证书
  resendFlow: (id: string) => call<boolean>('ResendFlow', id),
  /** 按给定内容发起一次请求，返回新 flow 的 id；URL 无法解析等输入问题会 reject。 */
  sendRequest: (spec: RequestSpec) => call<string>('SendRequest', spec),
  /** 取一条已捕获请求的保真快照供构造器预填；会话不存在返回 null。 */
  composeSeed: (id: string) => call<ComposeSeed | null>('ComposeSeed', id),
  /** 停止一条构造器发起的 SSE 流；返回是否命中进行中的流。 */
  stopStream: (flowId: string) => call<boolean>('StopStream', flowId),
  /** 建立一条出站 WebSocket，返回会话 id；该 id 即 ws_message 事件里 WebSocketSession.id。 */
  openWebSocket: (spec: RequestSpec) => call<string>('OpenWebSocket', spec),
  /** 往一条活动连接写一帧；type 为 'binary'/'ping' 时 data 是 base64。 */
  sendWSMessage: (flowId: string, type: 'text' | 'binary' | 'ping', data: string) =>
    call<void>('SendWSMessage', flowId, type, data),
  closeWebSocket: (flowId: string) => call<void>('CloseWebSocket', flowId),
  regenerateCA: () => call<string>('RegenerateCA'),
  /** 把根证书装入本机系统信任库;授权对话框由后端按平台触发。 */
  installCAToSystem: () => call<void>('InstallCAToSystem'),
  /** 按格式弹保存对话框写盘根证书;format ∈ pem|crt|der|p12|bundle;password 仅 p12 生效。 */
  exportCACertAs: (format: 'pem' | 'crt' | 'der' | 'p12' | 'bundle', password: string) =>
    call<boolean>('ExportCACertAs', format, password),
  /** 弹打开对话框选择要导入的根证书文件(.p12/.pfx/.pem/.crt),返回绝对路径或空串。 */
  pickImportCAFile: () => call<string>('PickImportCAFile'),
  /** 从给定路径导入根证书（自动分流 PKCS12 与 PEM Bundle），返回新根 PEM。 */
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

  // 断点（暂停的 flow）。载荷形状见 workbench/views/breakpoints/model.ts 的 parsePausedFlow。
  getBreakpoints: () => call<unknown[]>('GetBreakpoints'),
  /**
   * 放行；edit 为 null 表示原样放行。
   * 返回 false 表示该条已解除暂停；reject 表示编辑校验错误，flow 保持暂停。
   */
  resumeBreakpoint: (id: string, edit: ResumePatch | null) => call<boolean>('ResumeBreakpoint', id, edit),
  abortBreakpoint: (id: string) => call<boolean>('AbortBreakpoint', id),
  /** 批量处置全部暂停项，返回实际处置的条数。 */
  resumeAllBreakpoints: () => call<number>('ResumeAllBreakpoints'),
  abortAllBreakpoints: () => call<number>('AbortAllBreakpoints'),
  /** 把自动放行时刻整体推后一个周期；新时刻随 breakpoint_hit 事件下发。 */
  extendBreakpoint: (id: string) => call<boolean>('ExtendBreakpoint', id),
  setGlobalBreak: (onRequest: boolean, onResponse: boolean) =>
    call<void>('SetGlobalBreak', onRequest, onResponse),
  getGlobalBreak: () => call<GlobalBreakState>('GetGlobalBreak'),

  // URL 断点规则
  getBreakRules: () => call<BreakRule[]>('GetBreakRules'),
  addBreakRule: (url: string, onRequest: boolean, onResponse: boolean) =>
    call<BreakRule>('AddBreakRule', url, onRequest, onResponse),
  updateBreakRule: (id: string, url: string, onRequest: boolean, onResponse: boolean, enabled: boolean) =>
    call<boolean>('UpdateBreakRule', id, url, onRequest, onResponse, enabled),
  deleteBreakRule: (id: string) => call<void>('DeleteBreakRule', id),

  // 窗口（桌面外壳）
  /** 打开（或聚焦已存在的）独立系统窗口承载某个页面：settings | tools | about | plugins | rules | compose。 */
  openWindow: (view: string, query = '') => call<void>('OpenWindow', view, query),
  /** 把主窗口带到前台。 */
  focusMain: () => call<void>('FocusMain'),
  /** 用菜单模型重建 macOS 顶部系统菜单栏（仅 mac 调用；见 workbench/shell/nativeMenu.ts）。 */
  setMenu: (items: unknown[]) => call<void>('SetMenu', items),
  /** 弹系统「保存文件」对话框并由 Go 写盘；返回是否已保存。 */
  saveTextFile: (defaultName: string, content: string) => call<boolean>('SaveTextFile', defaultName, content),
}
