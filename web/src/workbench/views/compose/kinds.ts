/**
 * 构造器四种类型的展示元数据。
 *
 * 色值一律复用既有语义 token（text-method-* / text-ok / text-info），不另起一套配色词汇：
 * 类型化菜单与页签靠图标色一眼区分协议，这个色必须与流量表里同一协议的颜色对得上。
 */
import { Braces, Globe, Radio, Zap, type LucideIcon } from 'lucide-react'
import type { DraftKind } from './model'

export interface KindMeta {
  kind: DraftKind
  /** HTTP / GraphQL / WebSocket 是专名，不进 i18n。 */
  label: string
  icon: LucideIcon
  iconClass: string
  /** 页签上替代方法名的短标记；http 为空表示继续显示方法名。 */
  tag: string
}

export const KIND_META: Record<DraftKind, KindMeta> = {
  http: { kind: 'http', label: 'HTTP', icon: Globe, iconClass: 'text-method-get', tag: '' },
  sse: { kind: 'sse', label: 'SSE', icon: Radio, iconClass: 'text-ok', tag: 'SSE' },
  ws: { kind: 'ws', label: 'WebSocket', icon: Zap, iconClass: 'text-info', tag: 'WS' },
  graphql: { kind: 'graphql', label: 'GraphQL', icon: Braces, iconClass: 'text-method-patch', tag: 'GQL' },
}

/** 新建菜单与页签里的固定顺序：按用得多少排，不按字母。 */
export const KIND_ORDER: DraftKind[] = ['http', 'sse', 'ws', 'graphql']
