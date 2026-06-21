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
  /** 拖拽框选：报告框内行集合与锚点行 */
  onMarqueeSelect: (ids: ReadonlySet<string>, anchorId?: string) => void
  /** 框选结束：在收尾时校正焦点行 */
  onMarqueeEnd?: () => void
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
  onMarqueeSelect,
  onMarqueeEnd,
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
  // 框选拖拽进行中；拖拽期间冻结「跟随最新」自动滚动
  const draggingRef = useRef(false)

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
    if (follow && atBottomRef.current && !focusedId && !draggingRef.current) scrollToBottom()
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
      // 捕获指针：窗口外松开也能收到 pointerup（捕获事件仍冒泡到 window）
      try {
        el.setPointerCapture(e.pointerId)
      } catch {
        /* 不支持则退化为普通 window 监听 */
      }
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
    <div ref={outerRef} className="relative flex min-h-0 flex-1 flex-col bg-base">
      <div
        ref={scrollRef}
        onScroll={(e) => syncScroll(e.currentTarget)}
        onPointerDown={onPtrDown}
        onClickCapture={onClickCaptureSuppress}
        className="wb-scroll relative min-h-0 flex-1 overflow-auto"
        style={{ scrollbarGutter: 'stable' }}
      >
        {/* 橡皮筋矩形：内容坐标定位，随列表滚动 */}
        <div
          ref={marqueeRef}
          className="pointer-events-none absolute z-[5] rounded-[2px] border border-accent/80 bg-accent/20"
          style={{ display: 'none', left: 0, top: 0, width: 0, height: 0 }}
        />
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
