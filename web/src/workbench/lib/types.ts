/**
 * 工作台行视图模型 —— 流量表/详情面板的单一数据来源。
 * 由 HttpSession / WebSocketSession 适配而来（见 format.ts 的 toRow*），
 * 也可由 demo 生成器直接产出，与后端 DTO 解耦。
 */

export type RowKind = 'http' | 'ws'

export type RowState = 'pending' | 'completed' | 'error'

export type ContentKind =
  | 'json'
  | 'html'
  | 'js'
  | 'css'
  | 'image'
  | 'font'
  | 'video'
  | 'audio'
  | 'text'
  | 'doc'
  | 'form'
  | 'stream'
  | 'binary'
  | 'other'

/** 语义色调，映射到主题 token（ok/info/warn/danger/accent/neutral） */
export type Tone = 'ok' | 'info' | 'warn' | 'danger' | 'pending' | 'neutral'

export interface TrafficRow {
  id: string
  seq: number
  kind: RowKind
  method: string
  scheme: 'http' | 'https' | 'ws' | 'wss'
  host: string
  path: string
  url: string
  /** HTTP 响应状态码；pending / 无响应时为 undefined */
  status?: number
  statusText?: string
  state: RowState
  blocked?: boolean
  modified?: boolean
  contentType: string
  contentKind: ContentKind
  durationMs?: number
  sizeBytes?: number
  clientIP?: string
  process?: string
  iconData?: string
  iconType?: string
  /** 起始时间（epoch ms），用于排序与展示 */
  startedAt: number
  /** 完整请求/响应原始引用（详情面板用；demo 行可缺省） */
  reqHeaders?: Record<string, string>
  resHeaders?: Record<string, string>
  reqBody?: string
  resBody?: string
}
