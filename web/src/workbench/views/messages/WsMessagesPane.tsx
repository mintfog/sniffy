import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowDown, ArrowUp } from 'lucide-react'
import type { WebSocketMessage, WebSocketSession } from '@/types'
import { detectContentKind, formatClock, formatDuration, formatSize } from '../../lib/format'
import { cx } from '../../ui/primitives'
import { KVTable } from '../../ui/controls'
import { BodyViewer, RawCode } from '../BodyViewer'
import { CopyIcon, Pill, SplitBar } from './Bits'
import { hexDumpFromBase64 } from './hexdump'
import { useVerticalSplit } from './split'

function isBinary(msg: WebSocketMessage): boolean {
  return msg.binary === true || msg.type === 'binary'
}

/** 单行预览：文本帧折叠空白后截断；二进制帧不在此处展示内容（列表里用尺寸占位）。 */
function previewText(msg: WebSocketMessage): string {
  if (isBinary(msg)) return ''
  return msg.data.replace(/\s+/g, ' ').trim().slice(0, 400)
}

function FrameBody({ msg }: { msg: WebSocketMessage }) {
  const { t } = useTranslation()
  if (!msg.data) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('body.empty')}</div>
  if (isBinary(msg)) return <RawCode text={hexDumpFromBase64(msg.data)} />
  return <BodyViewer body={msg.data} kind={detectContentKind('text/plain', '', msg.data)} />
}

function MessageRow({
  msg,
  selected,
  onClick,
}: {
  msg: WebSocketMessage
  selected: boolean
  onClick: () => void
}) {
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
      <span className={cx('shrink-0 rounded px-1 font-mono text-[10px] font-semibold', selected ? 'ring-1 ring-inset ring-sel-fg/60' : binary ? 'bg-warn/15 text-warn' : 'bg-fg-muted/15 text-fg-muted')}>
        {binary ? 'BIN' : 'TXT'}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-muted">
        {binary ? <span className="italic text-fg-faint">{t('detail.ws.binary')} · {formatSize(msg.size)}</span> : previewText(msg)}
      </span>
      <span className="shrink-0 font-mono text-[10px] tabular-nums text-fg-faint">{formatSize(msg.size)}</span>
      <span className="shrink-0 font-mono text-[10px] tabular-nums text-fg-faint">{formatClock(Date.parse(msg.timestamp) || undefined)}</span>
    </button>
  )
}

function MessageList({
  messages,
  selectedId,
  onSelect,
}: {
  messages: WebSocketMessage[]
  selectedId?: string
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  // 未选中具体帧时自动跟随到底部（实时尾随）；用户选中某帧查看后停止打扰。
  useEffect(() => {
    if (selectedId) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages.length, selectedId])

  if (messages.length === 0) {
    return <div className="flex h-full items-center justify-center px-3 text-2xs text-fg-faint">{t('detail.ws.empty')}</div>
  }
  return (
    <div ref={scrollRef} className="h-full overflow-auto">
      {messages.map((m) => (
        <MessageRow key={m.id} msg={m} selected={m.id === selectedId} onClick={() => onSelect(m.id)} />
      ))}
    </div>
  )
}

function SessionOverview({ session }: { session: WebSocketSession }) {
  const { t } = useTranslation()
  const started = Date.parse(session.startTime) || undefined
  const ended = session.endTime ? Date.parse(session.endTime) || undefined : undefined
  const rows: [string, string][] = [
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

function FrameDetail({ msg }: { msg: WebSocketMessage }) {
  const { t } = useTranslation()
  const outbound = msg.direction === 'outbound'
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-surface px-2.5">
        <Pill tone={outbound ? 'info' : 'ok'}>{outbound ? t('detail.ws.sent') : t('detail.ws.received')}</Pill>
        <Pill tone={isBinary(msg) ? 'warn' : 'neutral'}>{isBinary(msg) ? t('detail.ws.binary') : 'TEXT'}</Pill>
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
        <FrameBody msg={msg} />
      </div>
    </div>
  )
}

/**
 * WebSocket 帧列表(上)/帧详情(下) 的垂直分栏区，主窗详情面板与请求构造器共用。
 *
 * 顶部的 URL / 状态药丸 / 关闭按钮刻意留在调用方：构造器窗口的「头」是共享的方法+URL 栏。
 */
export function WsMessagesPane({
  session,
  topFrac,
  onTopFracChange,
  emptyHint,
}: {
  session?: WebSocketSession
  /** 上下分栏占比（0–1）。由调用方决定它存在哪儿——主窗用 usePrefs，构造器用本地 state。 */
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
        <MessageList messages={messages} selectedId={selectedId} onSelect={setSelectedId} />
      </div>

      <SplitBar onPointerDown={startResize} />

      <div
        className="relative flex min-h-0 flex-1 flex-col bg-surface"
        data-find-region="body"
        data-find-label={t('find.scopeBody')}
      >
        {selected ? (
          <FrameDetail msg={selected} />
        ) : session ? (
          <SessionOverview session={session} />
        ) : (
          <div className="flex h-full items-center justify-center px-3 text-2xs text-fg-faint">{emptyHint ?? t('detail.ws.frameEmpty')}</div>
        )}
      </div>
    </div>
  )
}
