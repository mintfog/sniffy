/**
 * 请求构造器的草稿模型。
 *
 * 草稿故意用「有序的 [名, 值] 列表」而不是 Record 存头部：构造器承诺所见即所发，
 * 而 Record 会静默丢掉重复头（多条 Cookie / Accept）与用户键入的顺序，
 * 后端也正是按这个顺序原样写线的（见 internal/app/resend.go 的 splitComposedHeaders）。
 */
import type { ComposeSeed } from '@/lib/bridge'
import type { CurlWarning } from './curl'

/** 收发模式。取值与 Go 侧 flow.SpecKind* 常量逐字相同，两侧之间不留映射表。 */
export type DraftKind = 'http' | 'graphql' | 'sse' | 'ws'

export interface HeaderRow {
  /** 本地行标识：头名可重复、可为空，不能拿来做 React key。 */
  id: string
  name: string
  value: string
}

export interface DraftSeed {
  flowId: string
  method: string
  url: string
  headers: [string, string][]
  body: string
  /** 原请求体不是文本（图片 / protobuf 等），无法在文本编辑器里往返，只能如实告知。 */
  bodyBinary: boolean
  /** 原请求体过大，同样载不进编辑器；理由与 bodyBinary 不同，提示文案也不同。 */
  bodyTooLarge: boolean
  bodySize: number
}

/** GraphQL 是纯前端模式：这三项在发送前合成 JSON body。 */
export interface GraphQLPart {
  query: string
  /** 变量的 JSON 原文——保留用户排版与编辑中的非法中间态，发送时才 parse。 */
  variables: string
  operationName: string
}

/** 出站 WebSocket：连接活在后端，这里只留可编辑与可展示的部分。 */
export interface WebSocketPart {
  outgoing: string
  outgoingBinary: boolean
  /** idle 从未连过；connecting 已发 OpenWebSocket 未回；open/closed 由会话 DTO 推。 */
  conn: 'idle' | 'connecting' | 'open' | 'closed'
  connError?: string
}

export interface Draft {
  id: string
  kind: DraftKind
  method: string
  url: string
  headers: HeaderRow[]
  body: string
  /** 是否让这次请求经过插件 / 重写规则 / 断点，默认关闭（见 RequestSpec.ViaPipeline）。 */
  viaPipeline: boolean
  seed?: DraftSeed
  /** 最近一次发出的 flow id，响应据此认领。 */
  sentFlowId?: string
  /** 上一次发出的 flow id：重发期间继续展示它的响应，好让「改一处、比一次」有参照。 */
  prevFlowId?: string
  sending: boolean
  /** 发送被后端拒绝的原因（URL 解析失败等），下一次发送时清空。 */
  sendError?: string
  /** 仅 kind==='graphql' 有值。 */
  graphql?: GraphQLPart
  /** 仅 kind==='ws' 有值。 */
  ws?: WebSocketPart
  /** cURL 导入时的提示，用户首次编辑或点关闭即清空；isPristine 必须忽略它。 */
  importWarnings?: CurlWarning[]
}

export const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const

export const GQL_EMPTY: GraphQLPart = { query: '', variables: '', operationName: '' }
export const WS_EMPTY: WebSocketPart = { outgoing: '', outgoingBinary: false, conn: 'idle' }

/**
 * 子对象按类型可选（而非按 kind 拆判别联合）：页签是 Draft[]，patch(id, Partial<Draft>)
 * 只碰公共字段，Partial<联合> 会当场退化成没有类型安全的东西。
 * 可选还有一个好处——用户就地改类型时，HTTP→GraphQL→HTTP 往返不丢编辑器内容。
 */
export const gqlOf = (d: Draft): GraphQLPart => d.graphql ?? GQL_EMPTY
export const wsOf = (d: Draft): WebSocketPart => d.ws ?? WS_EMPTY

/**
 * ws 分片的补丁：给函数形态时能读到合并那一刻的最新值。
 * 发帧是异步的，回调里拿到的闭包快照已经过期，只有函数形态才判得出
 * 「输入框里现在还是不是刚发出去的那份内容」。
 */
export type WsPatch = Partial<WebSocketPart> | ((prev: WebSocketPart) => Partial<WebSocketPart>)

/**
 * 把补丁合进草稿的 ws 分片。只读 d 自身的当前值，不吃调用方闭包里的旧快照——
 * 连接状态回调与发帧回调都是异步的，用旧快照整片替换会把用户在等待期间键入的
 * 下一帧连同文本/二进制开关一起回滚。
 */
export function applyWsPatch(d: Draft, p: WsPatch): Draft {
  const cur = wsOf(d)
  return { ...d, ws: { ...cur, ...(typeof p === 'function' ? p(cur) : p) } }
}

let uid = 0
function nextId(prefix: string): string {
  uid += 1
  return `${prefix}-${uid}`
}

export function newHeaderRow(name = '', value = ''): HeaderRow {
  return { id: nextId('h'), name, value }
}

function stripTrailingBlank(rows: HeaderRow[]): HeaderRow[] {
  const out = [...rows]
  while (out.length > 0) {
    const last = out[out.length - 1]
    if (last.name === '' && last.value === '') out.pop()
    else break
  }
  return out
}

/**
 * 尾部永远留一行空行：在它上面开始打字即视为新增一条头，因此界面上不需要「添加」按钮。
 * 只规整尾部，中间被清空的行原地保留，避免用户删名字时行序跳动。
 */
export function normalizeHeaders(rows: HeaderRow[]): HeaderRow[] {
  const out = stripTrailingBlank(rows)
  out.push(newHeaderRow())
  return out
}

/**
 * 在末尾追加一条头。必须先摘掉尾部空行再追加，否则新行会落到空行之后，
 * 表格里就会出现夹在数据中间的空行。
 */
export function appendHeader(rows: HeaderRow[], name: string, value: string): HeaderRow[] {
  return normalizeHeaders([...stripTrailingBlank(rows), newHeaderRow(name, value)])
}

/** 新建草稿时该 kind 的默认方法。isPristine 靠它判断用户是否动过方法选择。 */
export function defaultMethod(kind: DraftKind): string {
  return kind === 'graphql' ? 'POST' : 'GET'
}

export function newDraft(kind: DraftKind = 'http'): Draft {
  const base: Draft = {
    id: nextId('d'),
    kind,
    method: defaultMethod(kind),
    url: '',
    headers: [],
    body: '',
    viaPipeline: false,
    sending: false,
  }
  switch (kind) {
    case 'graphql':
      return { ...base, graphql: { ...GQL_EMPTY } }
    case 'ws':
      return { ...base, ws: { ...WS_EMPTY } }
    case 'sse':
      return base
    case 'http':
      return base
  }
}

export function draftFromSeed(seed: ComposeSeed): Draft {
  const headers = seed.headers ?? []
  return {
    id: nextId('d'),
    kind: 'http',
    method: seed.method || 'GET',
    url: seed.url || '',
    headers: headers.map(([name, value]) => newHeaderRow(name, value)),
    body: seed.body ?? '',
    viaPipeline: false,
    sending: false,
    seed: {
      flowId: seed.flowId,
      method: seed.method || 'GET',
      url: seed.url || '',
      headers: headers.map(([n, v]) => [n, v] as [string, string]),
      body: seed.body ?? '',
      bodyBinary: !!seed.bodyBinary,
      bodyTooLarge: !!seed.bodyTooLarge,
      bodySize: seed.bodySize ?? 0,
    },
  }
}

/* ───────────────────────── 改动标记 ───────────────────────── */

/** 与蓝本相比被改动的字段。未从已捕获请求预填时全为假，界面上不显示任何标记。 */
export interface DraftDiff {
  method: boolean
  url: boolean
  body: boolean
  /** 与蓝本对不上的头行 id 集合（含新增行）。 */
  headers: ReadonlySet<string>
}

const NO_DIFF: DraftDiff = { method: false, url: false, body: false, headers: new Set() }

export function draftDiff(draft: Draft): DraftDiff {
  // 蓝本只可能是 HTTP。翻成别的类型后 draft.body 已不是上线字节（上线的是合成 JSON
  // 或干脆没有体），再拿它跟蓝本比，蓝色改动条就是在说谎。
  if (draft.kind !== 'http') return NO_DIFF
  const seed = draft.seed
  if (!seed) return NO_DIFF

  // 逐位置比对而非按名字：头是有序列表，插入一行会让后面整体错位，
  // 逐位置比对能把「这一行和蓝本对应位置不一样」如实标出来。
  const changed = new Set<string>()
  draft.headers.forEach((row, i) => {
    const orig = seed.headers[i]
    if (!orig || orig[0] !== row.name || orig[1] !== row.value) changed.add(row.id)
  })

  return {
    method: draft.method !== seed.method,
    url: draft.url !== seed.url,
    body: draft.body !== seed.body,
    headers: changed,
  }
}

/* ───────────────────────── URL 解析 ───────────────────────── */

export interface ParsedUrl {
  host: string
  /** 请求行里的 origin-form 目标：path + query。 */
  target: string
}

/**
 * 解析 URL 取 Host 与请求目标。与后端一致：不带协议时补加密的那个（https / wss）——
 * 猜错时握手会立刻失败，反过来猜明文会把本该加密的请求裸着发出去。
 */
export function parseUrl(raw: string, defaultScheme: 'https' | 'wss' = 'https'): ParsedUrl {
  const trimmed = raw.trim()
  if (!trimmed) return { host: '', target: '/' }
  const withScheme = trimmed.includes('://') ? trimmed : `${defaultScheme}://${trimmed}`
  try {
    const u = new URL(withScheme)
    return { host: u.host, target: `${u.pathname || '/'}${u.search}` }
  } catch {
    return { host: '', target: '/' }
  }
}

/* ───────────────────────── 页签标签 ───────────────────────── */

/** 页签下拉里的长标签：带主机名，好区分末段路径撞名的几个页签。空草稿返回空串。 */
export function draftMenuLabel(draft: Draft): string {
  const raw = draft.url.trim()
  if (!raw) return ''
  const { host, target } = parseUrl(raw)
  if (!host) return raw
  const full = `${host}${target}`
  // 从左侧截断：尾部的路径与查询串才是区分几个页签的地方。
  return full.length > 64 ? `…${full.slice(-63)}` : full
}

/** 页签上的短标签：优先末段路径，退回主机名。空草稿返回空串，由调用方填「空白请求」。 */
export function draftLabel(draft: Draft): string {
  // GraphQL 的路径恒是同一个 /graphql，只有操作名能把几个页签区分开。
  if (draft.kind === 'graphql') {
    const op = gqlOf(draft).operationName.trim()
    if (op) return op
  }
  const raw = draft.url.trim()
  if (!raw) return ''
  const { host, target } = parseUrl(raw)
  const path = target.split('?')[0]
  const last = path.split('/').filter(Boolean).pop()
  return last || host || raw
}
