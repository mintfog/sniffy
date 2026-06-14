import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { ChevronDown, Lock } from 'lucide-react'
import { useElementSize } from '../lib/useElementSize'
import { usePrefs } from '../prefs'
import { formatClock, formatDuration, formatSize, statusLabel, statusTone, toneText } from '../lib/format'
import type { MarkColor, TrafficRow } from '../lib/types'
import { ContentKindIcon, cx, MethodTag, ProcessAvatar, StatusDot } from '../ui/primitives'

/** 高亮标记 → 行背景（非选中态） */
const markBg: Record<MarkColor, string> = {
  red: 'bg-rose-500/15 hover:bg-rose-500/25',
  yellow: 'bg-amber-400/15 hover:bg-amber-400/25',
  green: 'bg-emerald-500/15 hover:bg-emerald-500/25',
  blue: 'bg-sky-500/15 hover:bg-sky-500/25',
  cyan: 'bg-cyan-400/15 hover:bg-cyan-400/25',
}

/** 高亮标记 → 左侧色条 */
const markStripe: Record<MarkColor, string> = {
  red: '#F43F5E',
  yellow: '#F59E0B',
  green: '#10B981',
  blue: '#0EA5E9',
  cyan: '#22D3EE',
}

const HEADER_H = 28
const OVERSCAN = 8

type Align = 'left' | 'right' | 'center'

interface ColDef {
  key: string
  /** 列头文案：传入 t 以随界面语言更新（空串表示无表头，如图标列） */
  header: (t: TFunction) => string
  width: number
  flex?: boolean
  align?: Align
  /** 容器宽度小于该值时隐藏该列 */
  hideBelow?: number
  cell: (row: TrafficRow) => React.ReactNode
}

const COLS: ColDef[] = [
  {
    key: 'status',
    header: (t) => t('traffic.col.status'),
    width: 62,
    cell: (row) => {
      const tone = statusTone(row)
      return (
        <span className="flex items-center gap-1.5">
          <StatusDot tone={tone} pulse={row.state === 'pending'} />
          <span className={cx('font-mono text-2xs tabular-nums', toneText[tone])}>{statusLabel(row)}</span>
        </span>
      )
    },
  },
  {
    key: 'seq',
    header: () => '#',
    width: 46,
    align: 'right',
    hideBelow: 560,
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-faint">{row.seq}</span>,
  },
  {
    key: 'kind',
    header: () => '',
    width: 28,
    align: 'center',
    cell: (row) => <ContentKindIcon kind={row.contentKind} />,
  },
  {
    key: 'method',
    header: (t) => t('traffic.col.method'),
    width: 54,
    cell: (row) => <MethodTag method={row.method} />,
  },
  {
    key: 'url',
    header: () => 'URL',
    width: 200,
    flex: true,
    cell: (row) => (
      <span className="flex min-w-0 items-center gap-1">
        {row.scheme === 'https' || row.scheme === 'wss' ? (
          <Lock className="h-3 w-3 shrink-0 text-ok/70" />
        ) : (
          <span className="h-3 w-3 shrink-0" />
        )}
        <span className="min-w-0 truncate">
          <span className="text-fg">{row.host}</span>
          <span className="text-fg-muted">{row.path}</span>
        </span>
      </span>
    ),
  },
  {
    key: 'size',
    header: (t) => t('traffic.col.size'),
    width: 70,
    align: 'right',
    hideBelow: 720,
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-muted">{formatSize(row.sizeBytes)}</span>,
  },
  {
    key: 'dur',
    header: (t) => t('traffic.col.duration'),
    width: 64,
    align: 'right',
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-muted">{formatDuration(row.durationMs)}</span>,
  },
  {
    key: 'clock',
    header: (t) => t('traffic.col.time'),
    width: 92,
    align: 'right',
    hideBelow: 900,
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-faint">{formatClock(row.startedAt)}</span>,
  },
  {
    key: 'client',
    header: (t) => t('traffic.col.client'),
    width: 112,
    hideBelow: 1040,
    cell: (row) => <span className="truncate font-mono text-2xs text-fg-faint">{row.clientIP || '—'}</span>,
  },
  {
    key: 'process',
    header: (t) => t('traffic.col.process'),
    width: 148,
    hideBelow: 820,
    cell: (row) => (
      <span className="flex min-w-0 items-center gap-1.5">
        <ProcessAvatar name={row.process} iconData={row.iconData} iconType={row.iconType} />
        <span className="truncate text-2xs text-fg-muted">{row.process || '—'}</span>
      </span>
    ),
  },
]

const alignCls: Record<Align, string> = {
  left: 'justify-start text-left',
  right: 'justify-end text-right',
  center: 'justify-center text-center',
}

interface TrafficTableProps {
  rows: TrafficRow[]
  /** 焦点行（详情面板展示的行） */
  focusedId?: string
  /** 多选集合（含焦点行） */
  selectedIds: ReadonlySet<string>
  /** 已查看（已阅）行集合，置灰显示 */
  readIds: ReadonlySet<string>
  /** 行高亮标记 */
  marks: Readonly<Partial<Record<string, MarkColor>>>
  onRowClick: (row: TrafficRow, e: React.MouseEvent) => void
  onRowContextMenu: (row: TrafficRow, e: React.MouseEvent) => void
  follow: boolean
}

export function TrafficTable({
  rows,
  focusedId,
  selectedIds,
  readIds,
  marks,
  onRowClick,
  onRowContextMenu,
  follow,
}: TrafficTableProps) {
  const { t } = useTranslation()
  const { ref: outerRef, width, height } = useElementSize<HTMLDivElement>()
  const compact = usePrefs((s) => s.compact)
  const rowH = compact ? 22 : 26
  const scrollRef = useRef<HTMLDivElement>(null)
  const [scrollTop, setScrollTop] = useState(0)
  // 是否贴近底部：决定「跟随最新」是否生效，以及「回到最新」按钮是否显示
  const [atBottom, setAtBottom] = useState(true)
  const atBottomRef = useRef(true)

  const visibleCols = useMemo(() => COLS.filter((c) => !c.hideBelow || width >= c.hideBelow), [width])
  const template = useMemo(
    () => visibleCols.map((c) => (c.flex ? 'minmax(160px,1fr)' : `${c.width}px`)).join(' '),
    [visibleCols],
  )

  const syncScroll = useCallback(
    (el: HTMLDivElement) => {
      setScrollTop(el.scrollTop)
      const nearBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - rowH * 2
      atBottomRef.current = nearBottom
      setAtBottom(nearBottom)
    },
    [rowH],
  )

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    syncScroll(el)
  }, [syncScroll])

  // 显式开启「跟随最新」（含首次挂载）时，立刻跳到底部
  useEffect(() => {
    if (follow) scrollToBottom()
  }, [follow, scrollToBottom])

  // 跟随最新：仅当本就贴在底部、且未选中行查看详情时，新数据才自动滚到底部；
  // 上滚查看历史或选中行时自动暂停，回到底部（或取消选中）即恢复。
  useEffect(() => {
    if (follow && atBottomRef.current && !focusedId) scrollToBottom()
  }, [rows.length, follow, focusedId, scrollToBottom])

  // 键盘导航（↑/↓）时把焦点行滚入可视区
  useEffect(() => {
    if (!focusedId) return
    const el = scrollRef.current
    if (!el) return
    const idx = rows.findIndex((r) => r.id === focusedId)
    if (idx < 0) return
    const rowTop = HEADER_H + idx * rowH
    const rowBottom = rowTop + rowH
    if (rowTop < el.scrollTop + HEADER_H) el.scrollTop = rowTop - HEADER_H
    else if (rowBottom > el.scrollTop + el.clientHeight) el.scrollTop = rowBottom - el.clientHeight
    syncScroll(el)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusedId])

  const bodyH = Math.max(0, height - HEADER_H)
  const totalH = rows.length * rowH
  const start = Math.max(0, Math.floor((scrollTop - HEADER_H) / rowH) - OVERSCAN)
  const end = Math.min(rows.length, Math.ceil((scrollTop - HEADER_H + bodyH) / rowH) + OVERSCAN)
  const slice = start < end ? rows.slice(start, end) : []

  return (
    <div ref={outerRef} className="relative flex min-h-0 flex-1 flex-col bg-base">
      <div
        ref={scrollRef}
        onScroll={(e) => syncScroll(e.currentTarget)}
        className="wb-scroll relative min-h-0 flex-1 overflow-auto"
        style={{ scrollbarGutter: 'stable' }}
      >
        {/* 表头（粘附顶部，随滚动条对齐） */}
        <div
          className="sticky top-0 z-10 grid h-7 items-center border-b border-line bg-inset text-2xs font-semibold uppercase tracking-wide text-fg-faint"
          style={{ gridTemplateColumns: template }}
        >
          {visibleCols.map((c) => (
            <div key={c.key} className={cx('flex items-center px-2', alignCls[c.align ?? 'left'])}>
              {c.header(t)}
            </div>
          ))}
        </div>

        {/* 虚拟行 */}
        <div style={{ height: totalH, position: 'relative' }}>
          <div style={{ transform: `translateY(${start * rowH}px)` }}>
            {slice.map((row) => {
              const selected = selectedIds.has(row.id)
              const focused = row.id === focusedId
              const isRead = readIds.has(row.id)
              const mark = marks[row.id]
              const tinted = row.blocked || row.state === 'error'
              return (
                <div
                  key={row.id}
                  onClick={(e) => onRowClick(row, e)}
                  onContextMenu={(e) => onRowContextMenu(row, e)}
                  style={{ gridTemplateColumns: template, height: rowH }}
                  className={cx(
                    'group/row relative grid cursor-default select-none items-center border-b text-[12px] transition-colors',
                    selected
                      ? // 选中：实色 accent 高亮（wb-row-selected 强制前景高对比色），一眼可辨
                        'wb-row-selected border-accent-hover/40 bg-accent'
                      : cx(
                          'border-line/50',
                          mark
                            ? markBg[mark]
                            : tinted
                              ? 'bg-danger/[0.04] hover:bg-elevated/70'
                              : 'hover:bg-elevated/70',
                          // 已阅置灰（选中行除外；有高亮标记时不整体压暗，否则 15% 标记色×0.55 几乎不可辨）
                          isRead && !mark && 'wb-row-read',
                        ),
                    // 「已修改」指示条选中时也保留（焦点条会盖在其上层）
                    row.modified &&
                      'before:absolute before:left-0 before:top-0 before:h-full before:w-[2px] before:bg-info',
                  )}
                >
                  {/* 高亮标记色条：选中态下行底色被 accent 覆盖，这里用独立色条保证切色即时可见 */}
                  {mark && (
                    <span
                      className="absolute left-0 top-0 z-[2] h-full w-[3px]"
                      style={{ backgroundColor: markStripe[mark] }}
                    />
                  )}
                  {focused && selected && !mark && (
                    <span className="absolute left-0 top-0 z-[1] h-full w-[2px] bg-accent-fg/80" />
                  )}
                  {visibleCols.map((c) => (
                    <div
                      key={c.key}
                      className={cx('flex min-w-0 items-center overflow-hidden px-2', alignCls[c.align ?? 'left'])}
                    >
                      {c.cell(row)}
                    </div>
                  ))}
                </div>
              )
            })}
          </div>
        </div>
      </div>

      {/* 上滚查看历史时的「回到最新」悬浮按钮 */}
      {!atBottom && rows.length > 0 && (
        <button
          onClick={scrollToBottom}
          className="wb-fade absolute bottom-3 right-4 z-20 flex h-7 items-center gap-1 rounded-full border border-line bg-elevated px-2.5 text-2xs font-medium text-fg shadow-wb transition-colors hover:bg-inset"
        >
          <ChevronDown className="h-3.5 w-3.5" />
          {t('traffic.jumpToLatest')}
        </button>
      )}
    </div>
  )
}
