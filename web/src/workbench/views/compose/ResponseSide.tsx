/**
 * 构造器的响应侧：按草稿类型分流到四种展示。
 *
 * 刻意写成纯 switch 返回子组件——分支里内联 hook 会踩 rules-of-hooks，
 * 而这四种展示各自需要的状态（选中帧、页签）差别很大，只能各自持有。
 */
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Radio, Send, Zap } from 'lucide-react'
import type { StreamSession, WebSocketSession } from '@/types'
import type { TrafficRow } from '../../lib/types'
import { cx, EmptyState } from '../../ui/primitives'
import { ResponsePane } from '../DetailPanel'
import { StreamMessagesPane } from '../messages/StreamMessagesPane'
import { WsMessagesPane } from '../messages/WsMessagesPane'
import type { Draft, WsPatch } from './model'
import { connOf } from './useOutbound'
import { WsComposer } from './WsComposer'

export function ResponseSide({
  draft,
  http,
  stream,
  ws,
  waiting,
  failure,
  partial,
  topFrac,
  onTopFracChange,
  onPatchWs,
}: {
  draft: Draft
  http?: TrafficRow
  stream?: StreamSession
  ws?: WebSocketSession
  waiting: boolean
  failure?: string
  /**
   * 这次发送失败了，但上游已经交回了一部分内容（超上限 / 超时 / 读到一半断了）。
   * 与 failure 互斥：那截内容正是用户判断「少了多少」的依据，只能在它上方明说，不能顶掉。
   */
  partial?: string
  /** 上下分栏占比由窗口保管：usePrefs.detailTopFrac 是全窗口共享的，拖这里不该动主窗。 */
  topFrac: number
  onTopFracChange: (frac: number) => void
  onPatchWs: (patch: WsPatch) => void
}) {
  if (failure !== undefined) return <ResponseFailure message={failure} />

  switch (draft.kind) {
    case 'sse':
      return (
        <SseResponse
          session={stream}
          row={http}
          waiting={waiting}
          partial={partial}
          started={!!draft.sentFlowId}
          topFrac={topFrac}
          onTopFracChange={onTopFracChange}
        />
      )
    case 'ws':
      return (
        <WsResponse
          draft={draft}
          session={ws}
          topFrac={topFrac}
          onTopFracChange={onTopFracChange}
          onPatchWs={onPatchWs}
        />
      )
    case 'http':
    case 'graphql':
      return (
        <HttpResponse
          row={http}
          graphql={draft.kind === 'graphql'}
          waiting={waiting}
          partial={partial}
          started={!!draft.sentFlowId}
        />
      )
  }
}

/** 正文上方的提示条：GraphQL 的 errors、非 SSE 应答、以及「有内容但这次是失败的」都用它。 */
function Banner({ tone = 'warn', children }: { tone?: 'warn' | 'danger'; children: ReactNode }) {
  return (
    <div
      className={cx(
        'shrink-0 border-b border-line px-3 py-1.5 text-2xs leading-relaxed',
        tone === 'danger' ? 'bg-danger/10 text-danger' : 'bg-warn/10 text-warn',
      )}
    >
      {children}
    </div>
  )
}

/** 部分失败的提示条。message 为空串时后端没给原因，退回一句通用说明总比什么都不说强。 */
function PartialBanner({ message }: { message: string }) {
  const { t } = useTranslation()
  return <Banner tone="danger">{t('compose.res.partial', { reason: message || t('compose.res.failedTitle') })}</Banner>
}

function ResponseFailure({ message }: { message: string }) {
  const { t } = useTranslation()
  return (
    <div className="min-h-0 flex-1 bg-surface">
      <EmptyState
        icon={<AlertTriangle className="h-7 w-7 text-danger" />}
        title={t('compose.res.failedTitle')}
        hint={message || t('compose.res.failedHint')}
      />
    </div>
  )
}

/* ───────────────────────── http / graphql ───────────────────────── */

/** GraphQL 的坑：状态药丸显示绿色 200，错误却全在 body 的 errors 里。 */
function gqlErrorCount(row?: TrafficRow): number {
  if (!row?.resBody) return 0
  try {
    const parsed = JSON.parse(row.resBody) as { errors?: unknown }
    return Array.isArray(parsed.errors) ? parsed.errors.length : 0
  } catch {
    return 0
  }
}

function HttpResponse({
  row,
  graphql,
  waiting,
  partial,
  started,
}: {
  row?: TrafficRow
  graphql: boolean
  waiting: boolean
  partial?: string
  started: boolean
}) {
  const { t } = useTranslation()
  const errors = graphql ? gqlErrorCount(row) : 0

  // 有响应就交给详情面板：body / 头 / cookies / 原始报文它都已经会渲染。
  if (row) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        {partial !== undefined && <PartialBanner message={partial} />}
        {errors > 0 && <Banner>{t('compose.graphql.responseErrors', { n: errors })}</Banner>}
        <ResponsePane row={row} />
      </div>
    )
  }

  return (
    <div className="min-h-0 flex-1 bg-surface">
      {started && waiting ? (
        <EmptyState icon={<Send className="h-7 w-7 wb-pulse" />} title={t('compose.res.waitingTitle')} hint={t('compose.res.waitingHint')} />
      ) : (
        <EmptyState icon={<Zap className="h-7 w-7" />} title={t('compose.res.emptyTitle')} hint={t('compose.res.emptyHint')} />
      )}
    </div>
  )
}

/* ───────────────────────── sse ───────────────────────── */

function SseResponse({
  session,
  row,
  waiting,
  partial,
  started,
  topFrac,
  onTopFracChange,
}: {
  session?: StreamSession
  row?: TrafficRow
  waiting: boolean
  partial?: string
  started: boolean
  topFrac: number
  onTopFracChange: (frac: number) => void
}) {
  const { t } = useTranslation()

  if (!started) {
    return (
      <div className="min-h-0 flex-1 bg-surface">
        <EmptyState icon={<Radio className="h-7 w-7" />} title={t('compose.res.emptyTitle')} hint={t('compose.res.emptyHint')} />
      </div>
    )
  }
  if (session) {
    return (
      <div className="flex min-h-0 flex-1 flex-col bg-surface">
        {partial !== undefined && <PartialBanner message={partial} />}
        <StreamMessagesPane session={session} topFrac={topFrac} onTopFracChange={onTopFracChange} emptyHint={t('compose.sse.empty')} />
      </div>
    )
  }
  // 服务端没按 text/event-stream 回，后端也就不会建流会话——此时整体展示响应体才是真相。
  if (row) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        {partial !== undefined && <PartialBanner message={partial} />}
        <Banner>{t('compose.sse.notEventStream', { type: row.contentType || '—' })}</Banner>
        <ResponsePane row={row} />
      </div>
    )
  }
  return (
    <div className="min-h-0 flex-1 bg-surface">
      <EmptyState
        icon={<Radio className={waiting ? 'h-7 w-7 wb-pulse' : 'h-7 w-7'} />}
        title={t('compose.sse.waiting')}
        hint={t('compose.res.waitingHint')}
      />
    </div>
  )
}

/* ───────────────────────── ws ───────────────────────── */

function WsResponse({
  draft,
  session,
  topFrac,
  onTopFracChange,
  onPatchWs,
}: {
  draft: Draft
  session?: WebSocketSession
  topFrac: number
  onTopFracChange: (frac: number) => void
  onPatchWs: (patch: WsPatch) => void
}) {
  const { t } = useTranslation()
  const conn = connOf(draft, session)
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      <WsMessagesPane session={session} topFrac={topFrac} onTopFracChange={onTopFracChange} emptyHint={t('compose.ws.notConnected')} />
      <WsComposer draft={draft} conn={conn} onPatchWs={onPatchWs} />
    </div>
  )
}
