/**
 * 流量表快捷筛选：芯片按维度分组，组内互斥（一行在同一维度只可能落在一个取值上），
 * 组间按「与」叠加。每个维度把一行归入若干芯片 key。
 */

import type { TrafficRow } from './types'

export type FilterGroup = 'protocol' | 'version' | 'content' | 'status'

/** 每个维度至多选中一个芯片；空对象即「全部」。 */
export type FilterSelection = Partial<Record<FilterGroup, string>>

export interface FilterChipDef {
  key: string
  group: FilterGroup
  /** 固定文案（协议名、状态码段等无需翻译） */
  label?: string
  /** 需翻译的文案 i18n key */
  labelKey?: string
}

export const FILTER_CHIPS: readonly FilterChipDef[] = [
  { key: 'https', group: 'protocol', label: 'HTTPS' },
  { key: 'http', group: 'protocol', label: 'HTTP' },
  { key: 'ws', group: 'protocol', label: 'WS' },

  { key: 'h1', group: 'version', label: 'HTTP/1' },
  { key: 'h2', group: 'version', label: 'HTTP/2' },

  { key: 'json', group: 'content', label: 'JSON' },
  { key: 'xml', group: 'content', label: 'XML' },
  { key: 'text', group: 'content', labelKey: 'workbench.filter.text' },
  { key: 'html', group: 'content', label: 'HTML' },
  { key: 'js', group: 'content', label: 'JS' },
  { key: 'css', group: 'content', label: 'CSS' },
  { key: 'image', group: 'content', labelKey: 'workbench.filter.image' },
  { key: 'media', group: 'content', labelKey: 'workbench.filter.media' },
  { key: 'binary', group: 'content', labelKey: 'workbench.filter.binary' },

  { key: '1xx', group: 'status', label: '1xx' },
  { key: '2xx', group: 'status', label: '2xx' },
  { key: '3xx', group: 'status', label: '3xx' },
  { key: '4xx', group: 'status', label: '4xx' },
  { key: '5xx', group: 'status', label: '5xx' },
  { key: 'err', group: 'status', labelKey: 'workbench.filter.error' },
]

const GROUP_OF: ReadonlyMap<string, FilterGroup> = new Map(FILTER_CHIPS.map((c) => [c.key, c.group]))
const GROUPS: readonly FilterGroup[] = ['protocol', 'version', 'content', 'status']
const NONE: readonly string[] = []

function protocolKeys(r: TrafficRow): readonly string[] {
  if (r.kind === 'ws') return ['ws']
  if (r.scheme === 'https') return ['https']
  if (r.scheme === 'http') return ['http']
  return NONE
}

// 优先取上游响应的版本；请求尚未拿到响应时退回客户端侧版本。
function versionKeys(r: TrafficRow): readonly string[] {
  const v = r.resHttpVersion || r.reqHttpVersion || ''
  if (v.startsWith('HTTP/1')) return ['h1']
  if (v.startsWith('HTTP/2')) return ['h2']
  return NONE
}

const XML_PATH = /\.xml(?:$|\?)/i

function contentKeys(r: TrafficRow): readonly string[] {
  switch (r.contentKind) {
    case 'json':
    case 'html':
    case 'js':
    case 'css':
    case 'image':
      return [r.contentKind]
    // ContentKind 把 XML 并在 text 里（共用文本查看器），筛选时再按类型拆开。
    case 'text':
      return r.contentType.toLowerCase().includes('xml') || XML_PATH.test(r.path) ? ['xml'] : ['text']
    case 'video':
    case 'audio':
      return ['media']
    case 'binary':
    case 'font':
    case 'doc':
      return ['binary']
    default:
      return NONE
  }
}

const STATUS_CLASS = ['1xx', '2xx', '3xx', '4xx', '5xx'] as const

// 「错误」与 4xx/5xx 有交集，仍放在状态组内互斥，避免选出「2xx 且错误」这类自相矛盾的组合。
function statusKeys(r: TrafficRow): readonly string[] {
  const s = r.status
  const cls = s && s >= 100 && s < 600 ? STATUS_CLASS[Math.floor(s / 100) - 1] : undefined
  const isErr = r.blocked === true || r.state === 'error' || (!!s && s >= 400)
  if (cls && isErr) return [cls, 'err']
  if (cls) return [cls]
  return isErr ? ['err'] : NONE
}

const CLASSIFY: Record<FilterGroup, (r: TrafficRow) => readonly string[]> = {
  protocol: protocolKeys,
  version: versionKeys,
  content: contentKeys,
  status: statusKeys,
}

/** 点击芯片：`all` 清空全部；已选中则取消；否则替换同组已选项，其余维度保持不变。 */
export function toggleFilterChip(sel: FilterSelection, key: string): FilterSelection {
  if (key === 'all') return {}
  const group = GROUP_OF.get(key)
  if (!group) return sel
  const next = { ...sel }
  if (next[group] === key) delete next[group]
  else next[group] = key
  return next
}

export function isFilterEmpty(sel: FilterSelection): boolean {
  return GROUPS.every((g) => !sel[g])
}

export function matchFilter(r: TrafficRow, sel: FilterSelection): boolean {
  for (const g of GROUPS) {
    const want = sel[g]
    if (want && !CLASSIFY[g](r).includes(want)) return false
  }
  return true
}
