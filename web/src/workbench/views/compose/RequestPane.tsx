/**
 * 构造器的请求编辑区：按类型分流的页签 + 请求头表格 + 线缆读数。
 *
 * 排版刻意与详情面板的只读视图同源（等宽 11.5px、iris 头名、发丝线分隔），
 * 二者呈现的是同一件东西，只是这边可写。
 */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import { TabRow } from '../DetailPanel'
import { cx } from '../../ui/primitives'
import { BodyActions, BodyPane } from './BodyPane'
import { GraphQLPane } from './GraphQLPane'
import { HeaderTable } from './HeaderTable'
import { WireReadout } from './WireReadout'
import type { CurlWarning } from './curl'
import type { Draft, DraftDiff, DraftKind } from './model'

type ReqTab = 'headers' | 'body' | 'query' | 'variables'

/** 空白草稿最常要补的几条头。空表是邀请动手的地方，不该只是一片虚无。 */
const COMMON_HEADERS: [string, string][] = [
  ['Content-Type', 'application/json'],
  ['Accept', 'application/json'],
  ['Authorization', 'Bearer '],
  ['User-Agent', 'sniffy'],
]

/** 建议只服务于「从零起手」：预填自已捕获请求的草稿不需要，攒够几条头也不再需要。 */
function headerSuggestions(draft: Draft): [string, string][] {
  const present = new Set(draft.headers.map((h) => h.name.trim().toLowerCase()).filter(Boolean))
  if (draft.seed || present.size >= 4) return []
  return COMMON_HEADERS.filter(([n]) => !present.has(n.toLowerCase()))
}

/** WS 的消息编辑在右侧帧列表旁（所有 WS 客户端都这么放），左边只剩握手头。 */
function tabsFor(kind: DraftKind): ReqTab[] {
  switch (kind) {
    case 'graphql':
      return ['query', 'variables', 'headers']
    case 'ws':
      return ['headers']
    case 'sse':
    case 'http':
      return ['headers', 'body']
  }
}

export function RequestPane({
  draft,
  diff,
  onPatch,
}: {
  draft: Draft
  diff: DraftDiff
  onPatch: (patch: Partial<Draft>) => void
}) {
  const { t } = useTranslation()
  const [wanted, setWanted] = useState<ReqTab>('headers')
  const named = draft.headers.filter((h) => h.name.trim() !== '')

  // 用户可以就地改类型，记住的页签未必还存在；派生而不是用 effect 纠正，免得多一帧错版。
  const available = tabsFor(draft.kind)
  const tab = available.includes(wanted) ? wanted : available[0]

  const label: Record<ReqTab, string> = {
    headers: t('compose.req.headers'),
    body: t('compose.req.body'),
    query: t('compose.graphql.query'),
    variables: t('compose.graphql.variables'),
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      {draft.importWarnings && draft.importWarnings.length > 0 && (
        <ImportWarnings items={draft.importWarnings} onDismiss={() => onPatch({ importWarnings: undefined })} />
      )}
      <TabRow
        tabs={available.map((key) => ({
          key,
          label: label[key],
          count: key === 'headers' ? named.length : undefined,
        }))}
        active={tab}
        onChange={(k) => setWanted(k as ReqTab)}
        right={tab === 'body' ? <BodyActions draft={draft} onPatch={onPatch} /> : undefined}
      />
      <div className="min-h-0 flex-1 overflow-hidden">
        {tab === 'headers' && (
          <HeaderTable
            rows={draft.headers}
            changed={diff.headers}
            onChange={(rows) => onPatch({ headers: rows })}
            suggestions={headerSuggestions(draft)}
          />
        )}
        {tab === 'body' && <BodyPane draft={draft} diff={diff} onPatch={onPatch} />}
        {tab === 'query' && <GraphQLPane draft={draft} mode="query" onPatch={onPatch} />}
        {tab === 'variables' && <GraphQLPane draft={draft} mode="variables" onPatch={onPatch} />}
      </div>
      <WireReadout draft={draft} />
    </div>
  )
}

/* ───────────────────────── 导入提示 ───────────────────────── */

const WARN_TONE: Record<CurlWarning['level'], string> = {
  info: 'bg-accent/10 text-accent',
  warn: 'bg-warn/10 text-warn',
  error: 'bg-danger/10 text-danger',
}

/** cURL 导入时丢失或改写了什么，只说一次；用户动手改任一字段即自动消失。 */
function ImportWarnings({ items, onDismiss }: { items: CurlWarning[]; onDismiss: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="shrink-0 border-b border-line">
      <div className="flex items-center gap-2 bg-inset px-3 py-1">
        <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('compose.curl.warn.title')}</span>
        <button
          type="button"
          onClick={onDismiss}
          title={t('compose.curl.dismiss')}
          aria-label={t('compose.curl.dismiss')}
          className="ml-auto flex h-4 w-4 items-center justify-center rounded-[3px] text-fg-faint transition hover:bg-elevated hover:text-fg"
        >
          <X className="h-3 w-3" />
        </button>
      </div>
      {items.map((w, i) => (
        <div key={`${w.code}-${i}`} className={cx('px-3 py-1 text-2xs leading-relaxed', WARN_TONE[w.level])}>
          {t(`compose.curl.warn.${w.code}`, w.params)}
        </div>
      ))}
    </div>
  )
}
