import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, Lock } from 'lucide-react'
import { useElementSize } from '../lib/useElementSize'
import { formatClock, formatDuration, formatSize, statusLabel, statusTone, toneText } from '../lib/format'
import type { TrafficRow } from '../lib/types'
import { ContentKindIcon, cx, MethodTag, ProcessAvatar, StatusDot } from '../ui/primitives'

const ROW_H = 26
const HEADER_H = 28
const OVERSCAN = 8

type Align = 'left' | 'right' | 'center'

interface ColDef {
  key: string
  header: string
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
    header: '状态',
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
    header: '#',
    width: 46,
    align: 'right',
    hideBelow: 560,
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-faint">{row.seq}</span>,
  },
  {
    key: 'kind',
    header: '',
    width: 28,
    align: 'center',
    cell: (row) => <ContentKindIcon kind={row.contentKind} />,
  },
  {
    key: 'method',
    header: '方法',
    width: 54,
    cell: (row) => <MethodTag method={row.method} />,
  },
  {
    key: 'url',
    header: 'URL',
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
    header: '大小',
    width: 70,
    align: 'right',
    hideBelow: 720,
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-muted">{formatSize(row.sizeBytes)}</span>,
  },
  {
    key: 'dur',
    header: '耗时',
    width: 64,
    align: 'right',
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-muted">{formatDuration(row.durationMs)}</span>,
  },
  {
    key: 'clock',
    header: '时间',
    width: 92,
    align: 'right',
    hideBelow: 900,
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-faint">{formatClock(row.startedAt)}</span>,
  },
  {
    key: 'client',
    header: '客户端',
    width: 112,
    hideBelow: 1040,
    cell: (row) => <span className="truncate font-mono text-2xs text-fg-faint">{row.clientIP || '—'}</span>,
  },
  {
    key: 'process',
    header: '进程',
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
  selectedId?: string
  onSelect: (id: string) => void
  follow: boolean
}

export function TrafficTable({ rows, selectedId, onSelect, follow }: TrafficTableProps) {
  const { ref: outerRef, width, height } = useElementSize<HTMLDivElement>()
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

  const syncScroll = useCallback((el: HTMLDivElement) => {
    setScrollTop(el.scrollTop)
    const nearBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - ROW_H * 2
    atBottomRef.current = nearBottom
    setAtBottom(nearBottom)
  }, [])

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
    if (follow && atBottomRef.current && !selectedId) scrollToBottom()
  }, [rows.length, follow, selectedId, scrollToBottom])

  const bodyH = Math.max(0, height - HEADER_H)
  const totalH = rows.length * ROW_H
  const start = Math.max(0, Math.floor((scrollTop - HEADER_H) / ROW_H) - OVERSCAN)
  const end = Math.min(rows.length, Math.ceil((scrollTop - HEADER_H + bodyH) / ROW_H) + OVERSCAN)
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
              {c.header}
            </div>
          ))}
        </div>

        {/* 虚拟行 */}
        <div style={{ height: totalH, position: 'relative' }}>
          <div style={{ transform: `translateY(${start * ROW_H}px)` }}>
            {slice.map((row) => {
              const selected = row.id === selectedId
              const tinted = row.blocked || row.state === 'error'
              return (
                <div
                  key={row.id}
                  onClick={() => onSelect(row.id)}
                  style={{ gridTemplateColumns: template, height: ROW_H }}
                  className={cx(
                    'group/row relative grid cursor-default items-center border-b border-line/50 text-[12px] transition-colors',
                    selected
                      ? 'bg-accent/12'
                      : tinted
                        ? 'bg-danger/[0.04] hover:bg-elevated/70'
                        : 'hover:bg-elevated/70',
                    row.modified && 'before:absolute before:left-0 before:top-0 before:h-full before:w-[2px] before:bg-info',
                  )}
                >
                  {selected && <span className="absolute left-0 top-0 h-full w-[2px] bg-accent" />}
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
          最新
        </button>
      )}
    </div>
  )
}
