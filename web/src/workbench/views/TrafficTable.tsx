import React, { useCallback, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { Lock } from 'lucide-react'
import i18n from '@/i18n'
import { useElementSize } from '../lib/useElementSize'
import { useFollowScroll } from '../lib/useFollowScroll'
import { usePrefs } from '../prefs'
import { hasBizRules } from '../lib/bizStatus'
import { formatClock, formatDuration, formatSize, isAwaitingResponse, statusLabel, statusTone, toneText } from '../lib/format'
import type { MarkColor, TrafficRow } from '../lib/types'
import { ContentKindIcon, cx, MethodTag, ProcessAvatar, StatusDot, StatusSpinner } from '../ui/primitives'
import { JumpToLatestButton } from '../ui/controls'
import { markColor } from '../ui/styles'

/** 高亮标记 → 行背景（非选中态） */
const markBg: Record<MarkColor, string> = {
  red: 'bg-mark-red/15 hover:bg-mark-red/25',
  yellow: 'bg-mark-yellow/15 hover:bg-mark-yellow/25',
  green: 'bg-mark-green/15 hover:bg-mark-green/25',
  blue: 'bg-mark-blue/15 hover:bg-mark-blue/25',
  cyan: 'bg-mark-cyan/15 hover:bg-mark-cyan/25',
}

const HEADER_H = 28
const OVERSCAN = 8

/** 拖拽框选（橡皮筋多选）参数 */
const MARQUEE_THRESHOLD = 4 // 位移超过该值才视为框选
const AUTOSCROLL_EDGE = 28 // 指针进入上/下边缘该范围触发自动滚动
const AUTOSCROLL_MAX = 22 // 自动滚动最大速度（px/frame）

function edgeSpeed(over: number): number {
  return Math.min(AUTOSCROLL_MAX, Math.max(2, Math.round(over / 2)))
}

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
  cell: (row: TrafficRow, read: boolean) => React.ReactNode
}

/** 仅在配置了业务状态码规则时出现的列。 */
const BIZ_COL = 'biz'

const COLS: ColDef[] = [
  {
    key: 'status',
    header: (t) => t('traffic.col.status'),
    width: 62,
    cell: (row) => {
      // 状态列仅有 46px 可用宽度，断点使用 HOLD 短记号以保持单行。
      const paused = row.paused
      const loading = isAwaitingResponse(row)
      const tone = paused ? 'warn' : statusTone(row)
      return (
        <span
          className="flex items-center gap-1.5"
          title={paused ? i18n.t('breakpoints.paused.rowTitle') : loading ? i18n.t('detail.overview.statePending') : undefined}
        >
          {loading ? (
            <StatusSpinner since={row.startedAt} />
          ) : (
            <StatusDot tone={tone} pulse={paused || row.state === 'pending'} />
          )}
          <span className={cx('whitespace-nowrap font-mono text-2xs tabular-nums', toneText[tone])}>
            {paused ? 'HOLD' : statusLabel(row)}
          </span>
        </span>
      )
    },
  },
  {
    key: BIZ_COL,
    header: (t) => t('traffic.col.biz'),
    width: 96,
    // 详情面板打开时列表约 580px 宽，业务码要留到比序号列更晚才隐藏。
    hideBelow: 520,
    cell: (row) => {
      const biz = row.biz
      if (!biz) return <span className="font-mono text-2xs text-fg-faint">—</span>
      return (
        <span
          title={biz.message ? `${biz.code} · ${biz.message}` : biz.code}
          className={cx(
            'min-w-0 truncate font-mono text-2xs tabular-nums',
            biz.failed ? 'rounded-[3px] bg-danger/15 px-1 text-danger' : 'text-fg-muted',
          )}
        >
          {biz.code}
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
    // 「已修改」标在序号前而不是贴边竖条：左边缘留给当前行竖条，否则大量被改写的行会让选中失去辨识度
    cell: (row) => (
      <span className="flex items-center gap-1 font-mono text-2xs tabular-nums text-fg-muted">
        {row.modified && (
          <span title={i18n.t('traffic.modified')} className="h-1.5 w-1.5 shrink-0 rounded-full bg-info" />
        )}
        {row.seq}
      </span>
    ),
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
    cell: (row, read) => (
      <span className="flex min-w-0 items-center gap-1">
        {row.scheme === 'https' || row.scheme === 'wss' ? (
          <Lock className="h-3 w-3 shrink-0 text-ok/70" />
        ) : (
          <span className="h-3 w-3 shrink-0" />
        )}
        <span className="min-w-0 truncate">
          <span className={read ? 'text-fg-muted' : 'text-fg'}>{row.host}</span>
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
    cell: (row) => <span className="font-mono text-2xs tabular-nums text-fg-muted">{formatClock(row.startedAt)}</span>,
  },
  {
    key: 'client',
    header: (t) => t('traffic.col.client'),
    width: 112,
    hideBelow: 1040,
    cell: (row) => <span className="truncate font-mono text-2xs text-fg-muted">{row.clientIP || '—'}</span>,
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

/** 流量列表各形态（表格 / 缩略图网格）共用的数据与交互。 */
export interface TrafficListProps {
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
  /** 双击行：被断点按住的行据此就地打开改包编辑器。 */
  onRowDoubleClick: (row: TrafficRow) => void
  onRowContextMenu: (row: TrafficRow, e: React.MouseEvent) => void
  follow: boolean
}

interface TrafficTableProps extends TrafficListProps {
  /** 拖拽框选：报告框内行集合与锚点行 */
  onMarqueeSelect: (ids: ReadonlySet<string>, anchorId?: string) => void
  /** 框选结束：在收尾时校正焦点行 */
  onMarqueeEnd?: () => void
}

export function TrafficTable({
  rows,
  focusedId,
  selectedIds,
  readIds,
  marks,
  onRowClick,
  onRowDoubleClick,
  onRowContextMenu,
  onMarqueeSelect,
  onMarqueeEnd,
  follow,
}: TrafficTableProps) {
  const { t } = useTranslation()
  const { ref: outerRef, width, height } = useElementSize<HTMLDivElement>()
  const compact = usePrefs((s) => s.compact)
  const rowH = compact ? 22 : 26
  const scrollRef = useRef<HTMLDivElement>(null)
  // 框选拖拽进行中；拖拽期间冻结「跟随最新」自动滚动
  const draggingRef = useRef(false)
  const { scrollTop, atBottom, syncScroll, scrollToBottom } = useFollowScroll(scrollRef, {
    count: rows.length,
    follow,
    focusedId,
    bottomSlack: rowH * 2,
    pausedRef: draggingRef,
  })

  const bizEnabled = usePrefs((s) => hasBizRules(s.bizStatusRules))
  const visibleCols = useMemo(
    () => COLS.filter((c) => (!c.hideBelow || width >= c.hideBelow) && (c.key !== BIZ_COL || bizEnabled)),
    [width, bizEnabled],
  )
  const template = useMemo(
    () => visibleCols.map((c) => (c.flex ? 'minmax(160px,1fr)' : `${c.width}px`)).join(' '),
    [visibleCols],
  )

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

  /* 拖拽框选：按「内容 Y / 行高」换算命中行号（适配虚拟列表），矩形随滚动移动、近边缘自动滚动 */
  const marqueeRef = useRef<HTMLDivElement>(null)
  const dragRef = useRef<{
    startX: number
    startY: number
    startCX: number // 起点的内容坐标（不随滚动变化）
    startCY: number
    additive: boolean
    base: Set<string>
    anchorId?: string
  } | null>(null)
  const lastPtrRef = useRef({ x: 0, y: 0 })
  const rafRef = useRef(0)
  const suppressClickRef = useRef(false)
  const lastRangeRef = useRef('')

  // 以 ref 暴露最新值给稳定回调 / raf 循环
  const rowsRef = useRef(rows)
  rowsRef.current = rows
  const rowHRef = useRef(rowH)
  rowHRef.current = rowH
  const selectedIdsRef = useRef(selectedIds)
  selectedIdsRef.current = selectedIds
  const onMarqueeSelectRef = useRef(onMarqueeSelect)
  onMarqueeSelectRef.current = onMarqueeSelect
  const onMarqueeEndRef = useRef(onMarqueeEnd)
  onMarqueeEndRef.current = onMarqueeEnd
  const syncScrollRef = useRef(syncScroll)
  syncScrollRef.current = syncScroll

  // 单帧：自动滚动 + 重算矩形与命中行
  const applyMarquee = useCallback(() => {
    const el = scrollRef.current
    const d = dragRef.current
    if (!el || !d) return
    const rect = el.getBoundingClientRect()
    const { x, y } = lastPtrRef.current

    // 自动滚动：指针贴近上/下边缘时持续滚动
    let dv = 0
    if (y < rect.top + AUTOSCROLL_EDGE) dv = -edgeSpeed(rect.top + AUTOSCROLL_EDGE - y)
    else if (y > rect.bottom - AUTOSCROLL_EDGE) dv = edgeSpeed(y - (rect.bottom - AUTOSCROLL_EDGE))
    if (dv) {
      const max = el.scrollHeight - el.clientHeight
      const nt = Math.max(0, Math.min(max, el.scrollTop + dv))
      if (nt !== el.scrollTop) {
        el.scrollTop = nt
        syncScrollRef.current(el) // 同步虚拟化 scrollTop
      }
    }

    const curCX = x - rect.left + el.scrollLeft
    const curCY = y - rect.top + el.scrollTop
    const minX = Math.min(d.startCX, curCX)
    const maxX = Math.max(d.startCX, curCX)
    const minY = Math.min(d.startCY, curCY)
    const maxY = Math.max(d.startCY, curCY)

    // 绘制橡皮筋矩形（直接改 DOM）
    const m = marqueeRef.current
    if (m) {
      const left = Math.max(0, minX)
      const top = Math.max(0, minY)
      m.style.left = `${left}px`
      m.style.top = `${top}px`
      m.style.width = `${Math.max(0, Math.min(maxX, el.scrollWidth) - left)}px`
      m.style.height = `${Math.max(0, maxY - top)}px`
    }

    // 命中行号区间：行 i 占内容 Y [HEADER_H + i·rh, HEADER_H + (i+1)·rh]
    const rowsNow = rowsRef.current
    const n = rowsNow.length
    const rh = rowHRef.current
    const contentBottom = HEADER_H + n * rh
    let range: string
    if (n === 0 || maxY <= HEADER_H || minY >= contentBottom) {
      range = 'empty' // 矩形在表头之上或所有行之下
    } else {
      let lo = Math.floor((Math.max(HEADER_H, minY) - HEADER_H) / rh)
      let hi = Math.floor((Math.min(contentBottom - 1, maxY) - HEADER_H) / rh)
      lo = Math.max(0, Math.min(n - 1, lo))
      hi = Math.max(0, Math.min(n - 1, hi))
      range = `${lo}:${hi}`
    }
    // 命中区间未变则跳过
    if (range === lastRangeRef.current) return
    lastRangeRef.current = range

    let next: Set<string>
    if (range === 'empty') {
      next = new Set(d.base)
    } else {
      const [lo, hi] = range.split(':').map(Number)
      next = d.additive ? new Set(d.base) : new Set<string>()
      for (let i = lo; i <= hi; i++) next.add(rowsNow[i].id)
    }
    onMarqueeSelectRef.current(next, d.anchorId)
  }, [])

  const tick = useCallback(() => {
    if (!draggingRef.current) {
      rafRef.current = 0
      return
    }
    applyMarquee()
    rafRef.current = requestAnimationFrame(tick)
  }, [applyMarquee])

  const onPtrMove = useCallback(
    (e: PointerEvent) => {
      const d = dragRef.current
      if (!d) return
      lastPtrRef.current = { x: e.clientX, y: e.clientY }
      if (!draggingRef.current) {
        // 未越过阈值前不算框选，留给普通点击 / Ctrl·Shift 点击
        if (Math.hypot(e.clientX - d.startX, e.clientY - d.startY) < MARQUEE_THRESHOLD) return
        draggingRef.current = true
        suppressClickRef.current = true
        document.body.style.userSelect = 'none'
        // 仅在确认框选（越过阈值）后才捕获指针，以便窗口外松开也能收到 pointerup；
        // 若在 pointerdown 即捕获，普通点击的 click 会被改派到捕获元素，行 onClick 收不到。
        try {
          scrollRef.current?.setPointerCapture(e.pointerId)
        } catch {
          /* 不支持则退化为普通 window 监听 */
        }
        if (marqueeRef.current) marqueeRef.current.style.display = 'block'
        if (!rafRef.current) rafRef.current = requestAnimationFrame(tick)
      }
      e.preventDefault()
    },
    [tick],
  )

  // 统一收尾（pointerup / pointercancel）：清理监听、raf、userSelect、橡皮筋
  const endDrag = useCallback(() => {
    window.removeEventListener('pointermove', onPtrMove)
    window.removeEventListener('pointerup', endDrag)
    window.removeEventListener('pointercancel', endDrag)
    const wasDragging = draggingRef.current
    dragRef.current = null
    draggingRef.current = false
    if (rafRef.current) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = 0
    }
    lastRangeRef.current = ''
    document.body.style.userSelect = ''
    if (marqueeRef.current) marqueeRef.current.style.display = 'none'
    if (wasDragging) {
      // 抑制框选后的 click；click 可能不派发，故微任务兜底
      setTimeout(() => (suppressClickRef.current = false), 0)
      onMarqueeEndRef.current?.()
    }
  }, [onPtrMove])

  const onPtrDown = useCallback(
    (e: React.PointerEvent) => {
      if (e.button !== 0) return // 仅左键；右键留给上下文菜单
      const el = scrollRef.current
      if (!el) return
      const rect = el.getBoundingClientRect()
      const startCX = e.clientX - rect.left + el.scrollLeft
      const startCY = e.clientY - rect.top + el.scrollTop
      const additive = e.ctrlKey || e.metaKey || e.shiftKey
      // 起点所在行作为锚点（供后续 Shift 范围选择）
      const rh = rowHRef.current
      const n = rowsRef.current.length
      let anchorId: string | undefined
      if (startCY >= HEADER_H && n > 0) {
        const idx = Math.floor((startCY - HEADER_H) / rh)
        if (idx >= 0 && idx < n) anchorId = rowsRef.current[idx].id
      }
      dragRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        startCX,
        startCY,
        additive,
        base: additive ? new Set(selectedIdsRef.current) : new Set(),
        anchorId,
      }
      draggingRef.current = false
      lastPtrRef.current = { x: e.clientX, y: e.clientY }
      lastRangeRef.current = '__init__' // 强制首帧派发（含清空场景）
      window.addEventListener('pointermove', onPtrMove)
      window.addEventListener('pointerup', endDrag)
      window.addEventListener('pointercancel', endDrag)
    },
    [onPtrMove, endDrag],
  )

  // 捕获阶段拦下框选后的 click，阻止冒泡到行的 onClick
  const onClickCaptureSuppress = useCallback((e: React.MouseEvent) => {
    if (suppressClickRef.current) {
      suppressClickRef.current = false
      e.stopPropagation()
      e.preventDefault()
    }
  }, [])

  // 卸载兜底清理
  useEffect(
    () => () => {
      window.removeEventListener('pointermove', onPtrMove)
      window.removeEventListener('pointerup', endDrag)
      window.removeEventListener('pointercancel', endDrag)
      if (rafRef.current) cancelAnimationFrame(rafRef.current)
      document.body.style.userSelect = ''
    },
    [onPtrMove, endDrag],
  )

  const bodyH = Math.max(0, height - HEADER_H)
  const totalH = rows.length * rowH
  const start = Math.max(0, Math.floor((scrollTop - HEADER_H) / rowH) - OVERSCAN)
  const end = Math.min(rows.length, Math.ceil((scrollTop - HEADER_H + bodyH) / rowH) + OVERSCAN)
  const slice = start < end ? rows.slice(start, end) : []

  return (
    <div ref={outerRef} className="relative flex min-h-0 flex-1 flex-col bg-surface">
      <div
        ref={scrollRef}
        onScroll={(e) => syncScroll(e.currentTarget)}
        onPointerDown={onPtrDown}
        onClickCapture={onClickCaptureSuppress}
        className="relative min-h-0 flex-1 overflow-auto"
        style={{ scrollbarGutter: 'stable' }}
      >
        {/* 橡皮筋矩形：内容坐标定位，随列表滚动 */}
        <div
          ref={marqueeRef}
          className="pointer-events-none absolute z-[5] rounded-[2px] border border-row-indicator/80 bg-row-indicator/15"
          style={{ display: 'none', left: 0, top: 0, width: 0, height: 0 }}
        />
        {/* 表头（粘附顶部，随滚动条对齐） */}
        <div
          className="sticky top-0 z-10 grid h-control-sm items-center border-b border-line bg-inset text-[12px] font-medium text-fg-muted"
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
                  onDoubleClick={() => onRowDoubleClick(row)}
                  onContextMenu={(e) => onRowContextMenu(row, e)}
                  style={{ gridTemplateColumns: template, height: rowH }}
                  className={cx(
                    'group/row relative grid cursor-default select-none items-center text-[12px] transition-colors',
                    selected
                      ? focused
                        ? 'wb-traffic-row-focused'
                        : 'wb-traffic-row-selected'
                      : mark
                        ? markBg[mark]
                        : tinted
                          ? 'bg-danger/[0.08] hover:bg-hover'
                          : 'hover:bg-hover',
                  )}
                >
                  {/* 标记色条优先于当前行竖条，避免批量选择掩盖用户标记。 */}
                  {mark && (
                    <span
                      className="absolute left-0 top-0 z-[2] h-full w-[3px]"
                      style={{ backgroundColor: markColor(mark) }}
                    />
                  )}
                  {focused && selected && !mark && (
                    <span className="absolute left-0 top-0 z-[1] h-full w-[3px] bg-row-indicator" />
                  )}
                  {visibleCols.map((c) => (
                    <div
                      key={c.key}
                      className={cx('flex min-w-0 items-center overflow-hidden px-2', alignCls[c.align ?? 'left'])}
                    >
                      {/* 已读变灰只作用于未选中行，选中行保持原亮度 */}
                      {c.cell(row, isRead && !selected)}
                    </div>
                  ))}
                </div>
              )
            })}
          </div>
        </div>
      </div>

      {!atBottom && rows.length > 0 && <JumpToLatestButton onClick={scrollToBottom} />}
    </div>
  )
}
