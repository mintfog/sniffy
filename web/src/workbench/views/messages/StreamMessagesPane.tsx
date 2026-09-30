import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowDown, ArrowUp } from 'lucide-react'
import type { StreamMessage, StreamSession } from '@/types'
import { detectContentKind, formatClock, formatDuration, formatSize } from '../../lib/format'
import { cx } from '../../ui/primitives'
import { KVTable } from '../../ui/controls'
import { BodyViewer, RawCode } from '../BodyViewer'
import { CopyIcon, Pill, SplitBar } from './Bits'
import { hexDumpFromBase64 } from './hexdump'
import { streamKindLabel } from './labels'
import { useVerticalSplit } from './split'

const isBinary = (m: StreamMessage) => m.binary === true

function msgTag(m: StreamMessage): string {
  if (m.kind === 'sse') return (m.sseType || m.eventType || 'message').toUpperCase()
  if (m.kind === 'grpc') return 'GRPC'
  return 'DATA'
}

const sseTagPalette = [
  'bg-info/15 text-info',
  'bg-warn/15 text-warn',
  'bg-ok/15 text-ok',
  'bg-accent/15 text-accent',
  'bg-mark-cyan/15 text-mark-cyan',
  'bg-iris/15 text-iris',
]

/** 事件名经稳定哈希映射到分类色，保证实时追加消息时已有事件不会换色。 */
function msgTagClass(m: StreamMessage): string {
  if (m.kind !== 'sse' || m.sseType || !m.eventType) return 'bg-fg-muted/15 text-fg-muted'
  let hash = 0
  for (let i = 0; i < m.eventType.length; i++) hash = (hash * 31 + m.eventType.charCodeAt(i)) >>> 0
  return sseTagPalette[hash % sseTagPalette.length]
}

function previewText(m: StreamMessage): string {
  if (isBinary(m)) return ''
  return m.data.replace(/\s+/g, ' ').trim().slice(0, 400)
}

function MessageBody({ msg }: { msg: StreamMessage }) {
  const { t } = useTranslation()
  if (!msg.data) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('body.empty')}</div>
  if (isBinary(msg)) return <RawCode text={hexDumpFromBase64(msg.data)} wrap={false} />
  if (msg.sseType) return <RawCode text={msg.data} />
  return <BodyViewer body={msg.data} kind={detectContentKind('text/plain', '', msg.data)} />
}

function MessageRow({ msg, selected, onClick }: { msg: StreamMessage; selected: boolean; onClick: () => void }) {
  const { t } = useTranslation()
  const outbound = msg.direction === 'outbound'
  const binary = isBinary(msg)
  const Arrow = outbound ? ArrowUp : ArrowDown
  return (
    <button
      type="button"
      onClick={onClick}
      title={outbound ? t('detail.ws.sent') : t('detail.ws.received')}
      className={cx(
        'flex w-full items-center gap-2 border-b border-line/50 px-2 py-1 text-left transition-colors',
        selected ? 'wb-row-selected bg-sel' : 'hover:bg-elevated/60',
      )}
    >
      <Arrow className={cx('h-3.5 w-3.5 shrink-0', selected ? '' : outbound ? 'text-method-post' : 'text-info')} />
      <span className={cx('shrink-0 rounded px-1 font-mono text-[10px] font-semibold', msgTagClass(msg), selected && 'ring-1 ring-inset ring-sel-fg/60')}>
        {msgTag(msg)}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-muted">
        {binary ? <span className="italic text-fg-faint">{t('detail.ws.binary')} · {formatSize(msg.size)}</span> : previewText(msg)}
      </span>
      <span className="shrink-0 font-mono text-[10px] tabular-nums text-fg-faint">{formatSize(msg.size)}</span>
      <span className="shrink-0 font-mono text-[10px] tabular-nums text-fg-faint">{formatClock(Date.parse(msg.timestamp) || undefined)}</span>
    </button>
  )
}

function MessageList({ messages, selectedId, onSelect, viewportHeight }: {
  messages: StreamMessage[]
  selectedId?: string
  onSelect: (id: string) => void
  viewportHeight: number
}) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const atBottomRef = useRef(true)
  const lastMessageId = messages[messages.length - 1]?.id

  // 在 scroll 事件更新触底状态前完成定位，避免消息裁剪或分栏缩放中断跟随。
  useLayoutEffect(() => {
    if (selectedId || !atBottomRef.current) return
    const list = scrollRef.current
    if (list) list.scrollTop = list.scrollHeight
  }, [messages.length, lastMessageId, selectedId, viewportHeight])

  if (messages.length === 0) {
    return <div className="flex h-full items-center justify-center px-3 text-2xs text-fg-faint">{t('detail.ws.empty')}</div>
  }
  return (
    <div
      ref={scrollRef}
      className="h-full overflow-auto"
      onScroll={(event) => {
        const list = event.currentTarget
        const distanceFromBottom = list.scrollHeight - list.clientHeight - list.scrollTop
        // scrollTop 可含小数，而高度会取整，判断触底时容许 1px 误差。
        atBottomRef.current = distanceFromBottom <= 1
      }}
    >
      {messages.map((m) => (
        <MessageRow key={m.id} msg={m} selected={m.id === selectedId} onClick={() => onSelect(m.id)} />
      ))}
    </div>
  )
}

function SessionOverview({ session }: { session: StreamSession }) {
  const { t } = useTranslation()
  const started = Date.parse(session.startTime) || undefined
  const ended = session.endTime ? Date.parse(session.endTime) || undefined : undefined
  const rows: [string, string][] = [
    ['Type', streamKindLabel[session.kind] || session.kind],
    [t('detail.overview.state'), session.status === 'open' ? t('detail.ws.statusOpen') : t('detail.ws.statusClosed')],
    [t('detail.ws.messages'), String(session.messageCount)],
    [t('detail.overview.size'), formatSize(session.totalSize)],
    [t('detail.overview.startedAt'), formatClock(started)],
    [t('detail.ws.endedAt'), ended ? formatClock(ended) : '—'],
    [t('detail.overview.duration'), ended && started ? formatDuration(ended - started) : '—'],
    [t('detail.overview.process'), session.processName || '—'],
  ]
  return (
    <div className="h-full overflow-auto">
      <KVTable rows={rows} />
    </div>
  )
}

function MessageDetail({ msg }: { msg: StreamMessage }) {
  const { t } = useTranslation()
  const outbound = msg.direction === 'outbound'
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-surface px-2.5">
        <Pill tone={outbound ? 'info' : 'ok'}>{outbound ? t('detail.ws.sent') : t('detail.ws.received')}</Pill>
        <span className={cx('rounded-full px-2 py-[1px] font-mono text-2xs font-semibold', msgTagClass(msg))}>{msgTag(msg)}</span>
        <span className="font-mono text-2xs tabular-nums text-fg-faint">#{msg.seq}</span>
        {msg.truncated && (
          <span title={t('detail.ws.truncatedHint')}>
            <Pill tone="warn">{t('detail.ws.truncated')}</Pill>
          </span>
        )}
        <span className="font-mono text-2xs tabular-nums text-fg-faint">{formatSize(msg.size)}</span>
        <span className="font-mono text-2xs tabular-nums text-fg-faint">{formatClock(Date.parse(msg.timestamp) || undefined)}</span>
        <div className="ml-auto flex items-center gap-1">
          <CopyIcon text={msg.data} title={t('body.copy')} />
        </div>
      </div>
      <div className="min-h-0 flex-1">
        <MessageBody msg={msg} />
      </div>
    </div>
  )
}

/**
 * 主窗口详情面板与请求构造器共用的流式消息分栏。URL、状态与页签由各窗口自行组织。
 */
export function StreamMessagesPane({
  session,
  topFrac,
  onTopFracChange,
  emptyHint,
}: {
  session?: StreamSession
  /** 列表高度占比（0–1）；主窗口与构造器窗口分别管理布局偏好。 */
  topFrac: number
  onTopFracChange: (frac: number) => void
  emptyHint?: string
}) {
  const { t } = useTranslation()
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined)
  const { containerRef, topH, startResize } = useVerticalSplit(topFrac, onTopFracChange)

  const messages = useMemo(() => session?.messages ?? [], [session?.messages])
  const selected = useMemo(() => messages.find((m) => m.id === selectedId), [messages, selectedId])

  return (
    <div ref={containerRef} className="flex min-h-0 flex-1 flex-col">
      <div
        className="relative flex min-h-0 flex-col"
        style={{ height: topH }}
        data-find-region="messages"
        data-find-label={t('find.scopeMessages')}
      >
        <MessageList
          key={session?.id}
          messages={messages}
          selectedId={selectedId}
          onSelect={setSelectedId}
          viewportHeight={topH}
        />
      </div>

      <SplitBar onPointerDown={startResize} />

      <div
        className="relative flex min-h-0 flex-1 flex-col bg-surface"
        data-find-region="body"
        data-find-label={t('find.scopeBody')}
      >
        {selected ? (
          <MessageDetail msg={selected} />
        ) : session ? (
          <SessionOverview session={session} />
        ) : (
          <div className="flex h-full items-center justify-center px-3 text-2xs text-fg-faint">{emptyHint ?? t('detail.ws.frameEmpty')}</div>
        )}
      </div>
    </div>
  )
}
