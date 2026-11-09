import React, { type MutableRefObject, useEffect, useState } from 'react'
import { ImageOff } from 'lucide-react'
import { sessionBodyUrl } from '@/lib/bridge'
import { useElementSize } from '../lib/useElementSize'
import { useFollowScroll } from '../lib/useFollowScroll'
import { formatSize, isAwaitingResponse, statusLabel, statusTone, toneText } from '../lib/format'
import type { MarkColor, TrafficRow } from '../lib/types'
import { JumpToLatestButton } from '../ui/controls'
import { cx, LoadingSpinner } from '../ui/primitives'
import { checkerStyle, markColor } from '../ui/styles'
import type { TrafficListProps } from './TrafficTable'

const CARD_MIN = 132
const GAP = 8
const PAD = 8
const CAPTION_H = 38
const OVERSCAN_ROWS = 2
/** 距底部不足半行最小卡片即视为贴底。 */
const BOTTOM_SLACK = (CARD_MIN + CAPTION_H) / 2

function fileName(row: TrafficRow): string {
  const last = row.path.split(/[?#]/)[0].split('/').filter(Boolean).pop()
  if (!last) return row.host
  try {
    return decodeURIComponent(last)
  } catch {
    return last
  }
}

function Thumb({ row, onDims }: { row: TrafficRow; onDims: (d: { w: number; h: number }) => void }) {
  const [failed, setFailed] = useState(false)
  if (isAwaitingResponse(row)) return <LoadingSpinner className="h-5 w-5 text-fg-faint" />
  const hasBody = row.state === 'completed' && !row.blocked && (row.sizeBytes ?? 0) > 0
  if (!hasBody || failed) return <ImageOff className="h-6 w-6 text-fg-faint" />
  return (
    // 只用 <img> 渲染：抓到的 SVG 在 <img> 里不执行脚本、不加载外部资源。
    <img
      src={sessionBodyUrl(row.id, 'response')}
      alt=""
      loading="lazy"
      decoding="async"
      draggable={false}
      onLoad={(e) => onDims({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
      onError={() => setFailed(true)}
      className="max-h-full max-w-full object-contain"
    />
  )
}

function Card({
  row,
  size,
  selected,
  focused,
  read,
  mark,
  onClick,
  onDoubleClick,
  onContextMenu,
}: {
  row: TrafficRow
  size: number
  selected: boolean
  focused: boolean
  read: boolean
  mark?: MarkColor
  onClick: (e: React.MouseEvent) => void
  onDoubleClick: () => void
  onContextMenu: (e: React.MouseEvent) => void
}) {
  const [dims, setDims] = useState<{ w: number; h: number } | null>(null)
  const tone = row.paused ? 'warn' : statusTone(row)
  const showStatus = row.paused || row.state === 'error' || row.blocked || (row.status ?? 200) >= 300
  return (
    <div
      onClick={onClick}
      onDoubleClick={onDoubleClick}
      onContextMenu={onContextMenu}
      title={row.url}
      style={{
        width: size,
        height: size + CAPTION_H,
        boxShadow: mark && !selected ? `0 0 0 2px ${markColor(mark)}` : undefined,
      }}
      className={cx(
        'flex cursor-default select-none flex-col overflow-hidden rounded-control border transition-colors',
        selected
          ? cx('border-row-indicator', focused ? 'wb-traffic-row-focused ring-1 ring-row-indicator' : 'wb-traffic-row-selected')
          : 'border-line hover:bg-hover',
      )}
    >
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden" style={checkerStyle}>
        {/* 行状态变化（完成、体积更新）时重挂载，让先前 404 的缩略图重新请求。 */}
        <Thumb key={`${row.state}:${row.sizeBytes ?? 0}`} row={row} onDims={setDims} />
        {showStatus && (
          <span
            className={cx(
              'absolute right-1 top-1 rounded-control bg-popover/90 px-1 font-mono text-2xs tabular-nums shadow-wb',
              toneText[tone],
            )}
          >
            {row.paused ? 'HOLD' : statusLabel(row)}
          </span>
        )}
      </div>
      <div className="flex shrink-0 flex-col justify-center gap-0.5 px-1.5" style={{ height: CAPTION_H }}>
        <span className={cx('truncate text-2xs', read && !selected ? 'text-fg-muted' : 'text-fg')}>{fileName(row)}</span>
        <span className="flex items-center gap-1 font-mono text-2xs tabular-nums text-fg-faint">
          <span className="min-w-0 flex-1 truncate">{dims ? `${dims.w}×${dims.h}` : row.host}</span>
          <span className="shrink-0">{formatSize(row.sizeBytes)}</span>
        </span>
      </div>
    </div>
  )
}

interface ImageGalleryProps extends TrafficListProps {
  /** 写入当前每行卡片数，供键盘 ↑/↓ 按整行移动焦点。 */
  colsRef: MutableRefObject<number>
}

/** 流量列表的缩略图网格形态：与表格共用同一份已筛选行、选择集与详情面板，按行虚拟化。 */
export function ImageGallery({
  rows,
  focusedId,
  selectedIds,
  readIds,
  marks,
  onRowClick,
  onRowDoubleClick,
  onRowContextMenu,
  colsRef,
  follow,
}: ImageGalleryProps) {
  const { ref: scrollRef, width, height } = useElementSize<HTMLDivElement>()
  const { scrollTop, atBottom, syncScroll, scrollToBottom } = useFollowScroll(scrollRef, {
    count: rows.length,
    follow,
    focusedId,
    bottomSlack: BOTTOM_SLACK,
  })

  const inner = Math.max(0, width - PAD * 2)
  const cols = Math.max(1, Math.floor((inner + GAP) / (CARD_MIN + GAP)))
  const cardW = Math.max(CARD_MIN, Math.floor((inner - (cols - 1) * GAP) / cols))
  const pitch = cardW + CAPTION_H + GAP
  const rowCount = Math.ceil(rows.length / cols)
  const totalH = rowCount > 0 ? PAD * 2 + rowCount * pitch - GAP : 0

  useEffect(() => {
    colsRef.current = cols
  }, [cols, colsRef])

  useEffect(() => {
    if (!focusedId) return
    const el = scrollRef.current
    if (!el) return
    const idx = rows.findIndex((r) => r.id === focusedId)
    if (idx < 0) return
    const top = PAD + Math.floor(idx / cols) * pitch
    const bottom = top + pitch - GAP
    if (top < el.scrollTop) el.scrollTop = top - PAD
    else if (bottom > el.scrollTop + el.clientHeight) el.scrollTop = bottom - el.clientHeight + PAD
    syncScroll(el)
    // 开合详情面板、拖动分隔条会改变列数或卡片尺寸，进而改变每行高度，同样要把焦点卡片留在视野内。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusedId, pitch])

  const startRow = Math.max(0, Math.floor((scrollTop - PAD) / pitch) - OVERSCAN_ROWS)
  const endRow = Math.min(rowCount, Math.ceil((scrollTop + height) / pitch) + OVERSCAN_ROWS)
  const slice = rows.slice(startRow * cols, endRow * cols)

  return (
    <div className="relative flex min-h-0 min-w-0 flex-1 flex-col bg-surface">
      <div
        ref={scrollRef}
        onScroll={(e) => syncScroll(e.currentTarget)}
        className="relative min-h-0 flex-1 overflow-y-auto overflow-x-hidden"
        style={{ scrollbarGutter: 'stable' }}
      >
        <div style={{ height: totalH, position: 'relative' }}>
          <div
            className="grid"
            style={{
              gridTemplateColumns: `repeat(${cols}, ${cardW}px)`,
              gap: GAP,
              padding: PAD,
              transform: `translateY(${startRow * pitch}px)`,
            }}
          >
            {slice.map((row) => (
              <Card
                key={row.id}
                row={row}
                size={cardW}
                selected={selectedIds.has(row.id)}
                focused={row.id === focusedId}
                read={readIds.has(row.id)}
                mark={marks[row.id]}
                onClick={(e) => onRowClick(row, e)}
                onDoubleClick={() => onRowDoubleClick(row)}
                onContextMenu={(e) => onRowContextMenu(row, e)}
              />
            ))}
          </div>
        </div>
      </div>

      {!atBottom && rows.length > 0 && <JumpToLatestButton onClick={scrollToBottom} />}
    </div>
  )
}
