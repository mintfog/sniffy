import { type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowDown, ArrowUp, Check, Copy, X } from 'lucide-react'
import { useAppStore } from '@/store'
import type { WebSocketMessage, WebSocketSession } from '@/types'
import { usePrefs } from '../prefs'
import { useElementSize } from '../lib/useElementSize'
import { detectContentKind, formatClock, formatDuration, formatSize } from '../lib/format'
import type { TrafficRow, Tone } from '../lib/types'
import { cx, StatusDot, ProcessAvatar } from '../ui/primitives'
import { KVTable } from '../ui/controls'
import { BodyViewer, RawCode, UrlHighlight } from './BodyViewer'

/* ───────────────────────── 小组件 ───────────────────────── */

const tonePill: Record<Tone, string> = {
  ok: 'bg-ok/15 text-ok',
  info: 'bg-info/15 text-info',
  warn: 'bg-warn/15 text-warn',
  danger: 'bg-danger/15 text-danger',
  pending: 'bg-warn/15 text-warn',
  neutral: 'bg-fg-faint/15 text-fg-muted',
}

function Pill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={cx('rounded-full px-2 py-[1px] font-mono text-2xs font-semibold', tonePill[tone])}>{children}</span>
}

function ActionIcon({ title, onClick, children }: { title: string; onClick?: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg"
    >
      {children}
    </button>
  )
}

function CopyIcon({ text, title }: { text: string; title: string }) {
  const [done, setDone] = useState(false)
  return (
    <ActionIcon title={title} onClick={() => navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1100) })}>
      {done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
    </ActionIcon>
  )
}

/* ───────────────────────── 帧数据解码 ───────────────────────── */

/** 把 base64 二进制帧渲染成 hex dump 文本（offset | hex | ascii）。 */
function hexDumpFromBase64(b64: string): string {
  let bytes: Uint8Array
  try {
    const bin = atob(b64)
    bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  } catch {
    return b64
  }
  const rows: string[] = []
  for (let off = 0; off < bytes.length; off += 16) {
    const slice = bytes.slice(off, off + 16)
    const hex = Array.from(slice).map((x) => x.toString(16).padStart(2, '0')).join(' ')
    const ascii = Array.from(slice).map((x) => (x >= 32 && x < 127 ? String.fromCharCode(x) : '.')).join('')
    rows.push(`${off.toString(16).padStart(8, '0')}  ${hex.padEnd(48, ' ')}  ${ascii}`)
  }
  return rows.join('\n')
}

function isBinary(msg: WebSocketMessage): boolean {
  return msg.binary === true || msg.type === 'binary'
}

/** 单行预览：文本帧折叠空白后截断；二进制帧不在此处展示内容（列表里用尺寸占位）。 */
function previewText(msg: WebSocketMessage): string {
  if (isBinary(msg)) return ''
  return msg.data.replace(/\s+/g, ' ').trim().slice(0, 400)
}

/* ───────────────────────── 帧内容查看 ───────────────────────── */

function FrameBody({ msg }: { msg: WebSocketMessage }) {
  const { t } = useTranslation()
  if (!msg.data) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('body.empty')}</div>
  if (isBinary(msg)) return <RawCode text={hexDumpFromBase64(msg.data)} />
  return <BodyViewer body={msg.data} kind={detectContentKind('text/plain', '', msg.data)} />
}

/* ───────────────────────── 消息列表 ───────────────────────── */

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
        selected ? 'wb-row-selected bg-accent' : 'hover:bg-elevated/60',
      )}
    >
      <Arrow className={cx('h-3.5 w-3.5 shrink-0', selected ? '' : outbound ? 'text-method-post' : 'text-info')} />
      <span className={cx('shrink-0 rounded px-1 font-mono text-[10px] font-semibold', selected ? 'bg-white/15' : binary ? 'bg-warn/15 text-warn' : 'bg-fg-faint/15 text-fg-muted')}>
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

/* ───────────────────────── 会话概览（未选中帧时展示） ───────────────────────── */

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

/* ───────────────────────── 选中帧详情 ───────────────────────── */

function FrameDetail({ msg }: { msg: WebSocketMessage }) {
  const { t } = useTranslation()
  const outbound = msg.direction === 'outbound'
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-line bg-surface px-2.5">
        <Pill tone={outbound ? 'info' : 'ok'}>{outbound ? t('detail.ws.sent') : t('detail.ws.received')}</Pill>
        <Pill tone={isBinary(msg) ? 'warn' : 'neutral'}>{isBinary(msg) ? t('detail.ws.binary') : 'TEXT'}</Pill>
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

/* ───────────────────────── 容器：消息列表(上)/帧详情(下) 垂直分栏 ───────────────────────── */

export function WsDetailPanel({ row, onClose }: { row: TrafficRow; onClose: () => void }) {
  const { t } = useTranslation()
  // 直接订阅 store 中的完整 WS 会话（含 messages），新帧到达即实时刷新。
  const session = useAppStore((s) => s.webSocketSessions.find((x) => x.id === row.id))
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined)

  const { ref: containerRef, height } = useElementSize<HTMLDivElement>()
  const frac = usePrefs((s) => s.detailTopFrac)
  const setPref = usePrefs((s) => s.set)
  const topH = height > 280 ? Math.min(height - 140, Math.max(120, Math.round(frac * height))) : Math.round(frac * 700)

  const startResize = useCallback(
    (e: ReactPointerEvent) => {
      e.preventDefault()
      const rect = containerRef.current?.getBoundingClientRect()
      if (!rect || rect.height <= 0) return
      const onMove = (ev: PointerEvent) => {
        const px = Math.min(rect.height - 140, Math.max(120, ev.clientY - rect.top))
        setPref({ detailTopFrac: px / rect.height })
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }
      document.body.style.cursor = 'row-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [containerRef, setPref],
  )

  const messages = session?.messages ?? []
  const selected = useMemo(() => messages.find((m) => m.id === selectedId), [messages, selectedId])
  const open = session?.status === 'open'
  const url = session?.url || row.url

  return (
    <div ref={containerRef} className="flex h-full min-h-0 flex-col border-l border-line bg-base">
      {/* 头部：状态 + URL + 关闭 */}
      <div className="flex shrink-0 items-start gap-2 border-b border-line bg-surface px-3 py-2.5">
        <StatusDot tone={open ? 'info' : 'neutral'} pulse={open} />
        <Pill tone={open ? 'info' : 'neutral'}>{open ? t('detail.ws.statusOpen') : t('detail.ws.statusClosed')}</Pill>
        <div className="min-w-0 flex-1">
          <UrlHighlight url={url} />
          <div className="mt-1 flex items-center gap-2 text-2xs text-fg-faint">
            {session?.processName && (
              <span className="flex items-center gap-1">
                <ProcessAvatar name={session.processName} iconData={session.iconData} iconType={session.iconType} />
                <span className="truncate">{session.processName}</span>
              </span>
            )}
            <span className="tabular-nums">{t('detail.ws.messages')}: {session?.messageCount ?? 0}</span>
            <span className="tabular-nums">{formatSize(session?.totalSize)}</span>
          </div>
        </div>
        <ActionIcon title={t('detail.req.close')} onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </ActionIcon>
      </div>

      {/* 消息列表 */}
      <div
        className="relative flex min-h-0 flex-col"
        style={{ height: topH }}
        data-find-region="messages"
        data-find-label={t('find.scopeMessages')}
      >
        <MessageList messages={messages} selectedId={selectedId} onSelect={setSelectedId} />
      </div>

      {/* 拖拽分隔条 */}
      <div
        onPointerDown={startResize}
        className="group/vd flex h-[5px] shrink-0 cursor-row-resize items-center justify-center bg-line transition-colors hover:bg-accent"
      >
        <span className="h-[3px] w-8 rounded-full bg-fg-faint/40 group-hover/vd:bg-accent-fg/60" />
      </div>

      {/* 下：选中帧详情，未选中则展示会话概览 */}
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
          <div className="flex h-full items-center justify-center px-3 text-2xs text-fg-faint">{t('detail.ws.frameEmpty')}</div>
        )}
      </div>
    </div>
  )
}
