/** 构造器底部状态条：左侧是这次发送的开关，右侧按类型报最新结果。 */
import { useTranslation } from 'react-i18next'
import type { StreamSession, WebSocketSession } from '@/types'
import { formatDuration, formatSize, statusLabel, statusTone, toneText } from '../../lib/format'
import type { TrafficRow } from '../../lib/types'
import { Toggle } from '../../ui/controls'
import { cx, StatusDot } from '../../ui/primitives'
import type { Draft } from './model'
import { connOf } from './useOutbound'

export function StatusStrip({
  draft,
  row,
  ws,
  stream,
  waiting,
  failure,
  onPatch,
}: {
  draft: Draft
  row?: TrafficRow
  ws?: WebSocketSession
  stream?: StreamSession
  waiting: boolean
  failure?: string
  onPatch: (patch: Partial<Draft>) => void
}) {
  const { t } = useTranslation()

  return (
    <div className="flex h-7 shrink-0 items-center gap-3 border-t border-line bg-inset px-3 text-2xs">
      <label
        className="flex shrink-0 cursor-pointer items-center gap-1.5"
        title={t(draft.kind === 'ws' ? 'compose.status.pipelineHintWs' : 'compose.status.pipelineHint')}
      >
        <Toggle checked={draft.viaPipeline} onChange={(v) => onPatch({ viaPipeline: v })} />
        <span className={draft.viaPipeline ? 'text-fg' : 'text-fg-muted'}>{t('compose.status.pipeline')}</span>
      </label>

      {draft.seed && (
        <span className="shrink-0 text-fg-faint" title={t('compose.status.fromTip')}>
          {t('compose.status.from')}
        </span>
      )}

      <div className="ml-auto flex min-w-0 items-center gap-2">
        {failure !== undefined ? (
          <span className="truncate text-danger" title={failure}>
            {/* WS 的失败文案已是完整一句（compose.ws.connectFailed），再套一层就成了「发送失败：连不上：…」。 */}
            {draft.kind === 'ws' ? failure : t('compose.status.failed', { reason: failure || t('compose.res.failedTitle') })}
          </span>
        ) : draft.kind === 'ws' ? (
          <WsStatus draft={draft} session={ws} />
        ) : waiting ? (
          <>
            <StatusDot tone="pending" pulse />
            <span className="text-fg-muted">{t('compose.sending')}</span>
          </>
        ) : draft.kind === 'sse' && stream ? (
          <SseStatus session={stream} row={row} />
        ) : row ? (
          <>
            <span className={cx('font-mono font-semibold', toneText[statusTone(row)])}>{statusLabel(row)}</span>
            <span className="wb-tnum text-fg-faint">{formatDuration(row.durationMs)}</span>
            <span className="wb-tnum text-fg-faint">{formatSize(row.sizeBytes)}</span>
          </>
        ) : (
          <span className="text-fg-faint">{t('compose.status.ready')}</span>
        )}
      </div>
    </div>
  )
}

function WsStatus({ draft, session }: { draft: Draft; session?: WebSocketSession }) {
  const { t } = useTranslation()
  const conn = connOf(draft, session)
  const label: Record<typeof conn, string> = {
    idle: t('compose.status.ready'),
    connecting: t('compose.ws.connecting'),
    open: t('compose.ws.open'),
    closed: t('compose.ws.closed'),
  }
  const tone = conn === 'open' ? 'ok' : conn === 'connecting' ? 'pending' : 'neutral'
  const sent = session?.messages.filter((m) => m.direction === 'outbound').length ?? 0
  const received = (session?.messages.length ?? 0) - sent
  // 时间线只保留最近若干条（见 flow.MaxWSMessages 与字节预算），裁剪之后按它算出的
  // 方向计数就只是「窗口内的条数」了。此时改报 messageCount —— 它始终是累计值。
  const windowed = !!session && session.messageCount > session.messages.length

  return (
    <>
      <StatusDot tone={tone} pulse={conn === 'connecting'} />
      <span className={toneText[tone]}>{label[conn]}</span>
      {session && (
        <span className="wb-tnum text-fg-faint">
          {windowed
            ? t('compose.ws.total', { n: session.messageCount })
            : t('compose.ws.counts', { sent, received })}
        </span>
      )}
      {session && <span className="wb-tnum text-fg-faint">{formatSize(session.totalSize)}</span>}
    </>
  )
}

function SseStatus({ session, row }: { session: StreamSession; row?: TrafficRow }) {
  const { t } = useTranslation()
  const open = session.status === 'open'
  return (
    <>
      <StatusDot tone={open ? 'ok' : 'neutral'} pulse={open} />
      {row && <span className={cx('font-mono font-semibold', toneText[statusTone(row)])}>{statusLabel(row)}</span>}
      <span className="wb-tnum text-fg-faint">{t('compose.sse.eventCount', { n: session.messageCount })}</span>
      <span className="wb-tnum text-fg-faint">{formatSize(session.totalSize)}</span>
    </>
  )
}
