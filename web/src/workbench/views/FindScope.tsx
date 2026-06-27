import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { CaseSensitive, ChevronDown, ChevronUp, X } from 'lucide-react'
import { cx } from '../ui/primitives'

/*
 * 详情面板内的「就地查找」（Ctrl/⌘+F），按区域作用域。
 *
 * 查找范围跟随光标：最近一次指针/焦点交互落在哪个 [data-find-region] 子树（请求区/响应区/
 * 消息列表/帧内容…），按 Ctrl/⌘+F 就只在那个区域里查；查找条 portal 渲染到该区域内浮出。
 * 光标不在任何区域时（列表、工具条、列表搜索框）不拦截快捷键，放行给工作台的「列表搜索」。
 *
 * 高亮走 CSS Custom Highlight API（不改 DOM，天然兼容语法高亮 span 与 JSON 树）；
 * 老旧 WebView 不支持时回退用 Selection 仅高亮「当前命中」。查找文本以可见文本节点拼接
 * 跨节点匹配，带 [data-find-skip] 的子树（行号槽、查找条自身）不参与匹配。
 */

const HL_ALL = 'sniffy-find'
const HL_ACTIVE = 'sniffy-find-active'

interface HighlightInstance {
  add(range: Range): void
}
interface HighlightRegistry {
  set(name: string, highlight: HighlightInstance): void
  delete(name: string): void
}

const highlightRegistry =
  typeof CSS !== 'undefined'
    ? (CSS as unknown as { highlights?: HighlightRegistry }).highlights
    : undefined
const HighlightClass =
  typeof window !== 'undefined'
    ? (window as unknown as { Highlight?: new () => HighlightInstance }).Highlight
    : undefined
const supportsHighlight = !!highlightRegistry && !!HighlightClass

/** 命中数上限：超大 body（Raw 模式不虚拟化）下，常见短词可命中数十万次，封顶避免卡死。 */
const MAX_MATCHES = 2000

/**
 * 逐 UTF-16 码元做小写折叠，且保证「长度不变」。
 * 直接 String.toLowerCase 对个别字符会改变长度（如土耳其语 İ→i̇），使匹配下标与原文节点偏移错位，
 * 进而漏掉或画错命中。这里只折叠那些折叠后仍是单码元的字符，其余原样保留，从而与原文一一对应。
 */
function foldCaseUnit(s: string): string {
  let out = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    const l = c.toLowerCase()
    out += l.length === 1 ? l : c
  }
  return out
}

/** 在 root 的可见文本里找出 query 的全部命中，返回（可跨文本节点的）Range 列表。 */
function findRanges(
  root: HTMLElement,
  query: string,
  caseSensitive: boolean,
): { ranges: Range[]; capped: boolean } {
  // 「行/块」边界判定：命中不得跨块，否则会拼出跨行假命中，且命中区间会吞掉夹在两行之间的行号槽。
  const blockCache = new Map<Element, boolean>()
  const isBlock = (el: Element): boolean => {
    let v = blockCache.get(el)
    if (v === undefined) {
      const d = getComputedStyle(el).display
      v = !(d === 'inline' || d === 'inline-block' || d === 'inline-flex' || d === 'inline-grid' || d === 'contents')
      blockCache.set(el, v)
    }
    return v
  }
  const nearestBlock = (node: Node): Element | null => {
    let el = node.parentElement
    while (el && el !== root && !isBlock(el)) el = el.parentElement
    return el
  }

  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!node.nodeValue) return NodeFilter.FILTER_REJECT
      // 行号槽、查找条自身等标了 data-find-skip 的子树不参与匹配
      if (node.parentElement?.closest('[data-find-skip]')) return NodeFilter.FILTER_REJECT
      return NodeFilter.FILTER_ACCEPT
    },
  })

  const segs: { node: Text; start: number }[] = []
  let text = ''
  let prevBlock: Element | null = null
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const t = n as Text
    const block = nearestBlock(t)
    // 跨块时插入换行哨兵（不属于任何节点）：query 不含换行，故 indexOf 不会跨块命中
    if (prevBlock !== null && block !== prevBlock) text += '\n'
    segs.push({ node: t, start: text.length })
    text += t.nodeValue
    prevBlock = block
  }
  if (!text) return { ranges: [], capped: false }

  const hay = caseSensitive ? text : foldCaseUnit(text)
  const needle = caseSensitive ? query : foldCaseUnit(query)

  const locate = (abs: number) => {
    let lo = 0
    let hi = segs.length - 1
    let idx = 0
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (segs[mid].start <= abs) {
        idx = mid
        lo = mid + 1
      } else {
        hi = mid - 1
      }
    }
    return { node: segs[idx].node, offset: abs - segs[idx].start }
  }

  const ranges: Range[] = []
  let capped = false
  for (let from = hay.indexOf(needle); from >= 0; from = hay.indexOf(needle, from + needle.length)) {
    if (ranges.length >= MAX_MATCHES) {
      capped = true
      break
    }
    const s = locate(from)
    const e = locate(from + needle.length)
    const r = document.createRange()
    try {
      r.setStart(s.node, s.offset)
      r.setEnd(e.node, e.offset)
      ranges.push(r)
    } catch {
      /* 节点已失效（DOM 刚变更）：跳过，下一轮 recompute 会重建 */
    }
  }
  return { ranges, capped }
}

export function FindScope({ children, className }: { children: ReactNode; className?: string }) {
  const { t } = useTranslation()
  const scopeRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const [open, setOpen] = useState(false)
  // 当前查找作用域：命中的 [data-find-region] 元素，同时作为查找条的 portal 容器与高亮根。
  const [region, setRegion] = useState<HTMLElement | null>(null)
  const [query, setQuery] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [count, setCount] = useState(0)
  const [capped, setCapped] = useState(false)
  const [active, setActive] = useState(0)

  const rangesRef = useRef<Range[]>([])
  const activeRef = useRef(0)
  const openRef = useRef(false)
  openRef.current = open
  const regionRef = useRef<HTMLElement | null>(null)
  regionRef.current = region

  // 光标所在区域：最近一次指针/焦点交互落在本详情内哪个 [data-find-region] 子树，
  // 决定下次 Ctrl/⌘+F 的作用域；落在所有区域之外时为 null（此时不拦截，交回列表搜索）。
  const cursorRegionRef = useRef<HTMLElement | null>(null)
  const regionOf = useCallback((node: EventTarget | null): HTMLElement | null => {
    const el = node instanceof Element ? node : node instanceof Node ? node.parentElement : null
    const found = el?.closest<HTMLElement>('[data-find-region]') ?? null
    return found && scopeRef.current?.contains(found) ? found : null
  }, [])

  const clearPaint = useCallback(() => {
    if (supportsHighlight) {
      highlightRegistry!.delete(HL_ALL)
      highlightRegistry!.delete(HL_ACTIVE)
    } else {
      window.getSelection()?.removeAllRanges()
    }
  }, [])

  // 仅刷新「当前命中」的高亮并滚动其入视野（不重建全部命中）
  const applyActive = useCallback((i: number) => {
    activeRef.current = i
    const ranges = rangesRef.current
    const current = ranges[i]
    if (supportsHighlight) {
      const h = new HighlightClass!()
      if (current) h.add(current)
      highlightRegistry!.set(HL_ACTIVE, h)
    } else {
      const sel = window.getSelection()
      sel?.removeAllRanges()
      if (current) sel?.addRange(current)
    }
    current?.startContainer.parentElement?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [])

  const recompute = useCallback(() => {
    const root = regionRef.current
    const result = root && query ? findRanges(root, query, caseSensitive) : { ranges: [], capped: false }
    const ranges = result.ranges
    rangesRef.current = ranges
    setCount(ranges.length)
    setCapped(result.capped)
    if (supportsHighlight) {
      if (ranges.length) {
        const h = new HighlightClass!()
        for (const r of ranges) h.add(r)
        highlightRegistry!.set(HL_ALL, h)
      } else {
        highlightRegistry!.delete(HL_ALL)
      }
    }
    let i = activeRef.current
    if (i >= ranges.length) i = 0
    setActive(i)
    applyActive(i)
  }, [query, caseSensitive, applyActive])

  const recomputeRef = useRef(recompute)
  recomputeRef.current = recompute

  const go = useCallback((delta: number) => {
    const n = rangesRef.current.length
    if (!n) return
    let i = (activeRef.current + delta) % n
    if (i < 0) i += n
    setActive(i)
    applyActive(i)
  }, [applyActive])

  const close = useCallback(() => {
    setOpen(false)
    clearPaint()
  }, [clearPaint])

  // 命中重算：开启时、切换作用域时、以及 query/大小写变化时。防抖合并连续按键（大 body 下逐字全量重扫会卡顿）。
  useEffect(() => {
    if (!open) return
    const id = setTimeout(() => recompute(), 80)
    return () => clearTimeout(id)
  }, [open, region, recompute])

  useEffect(() => {
    if (!open) clearPaint()
  }, [open, clearPaint])

  // CSS.highlights 是文档级全局表，open 态下面板被直接卸载时不会走上面的关闭路径，须在卸载时兜底清理
  useEffect(() => () => clearPaint(), [clearPaint])

  // 内容变化（切页签/换查看模式/展开树/流式新消息）时重算
  useEffect(() => {
    if (!open) return
    const root = regionRef.current
    if (!root) return
    let raf = 0
    const obs = new MutationObserver((records) => {
      // 查找条 portal 在区域内（标 data-find-skip），其计数/状态自更新不应触发重算，否则会自激成环
      const relevant = records.some((r) => {
        const node = r.target
        const el = node instanceof Element ? node : node.parentElement
        return !el?.closest('[data-find-skip]')
      })
      if (!relevant) return
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => recomputeRef.current())
    })
    obs.observe(root, { childList: true, subtree: true, characterData: true })
    return () => {
      obs.disconnect()
      cancelAnimationFrame(raf)
    }
  }, [open, region])

  // 追踪光标所在区域,供 Ctrl/⌘+F 决定查找作用域:指针按下以命中区域(无则 null)为准。
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      cursorRegionRef.current = regionOf(e.target)
    }
    const onFocus = (e: FocusEvent) => {
      const r = regionOf(e.target)
      if (r) cursorRegionRef.current = r
      // 焦点(Tab/程序化)移到区域外的可聚焦控件时清空,使 Ctrl+F 回到列表搜索;
      // 但点击正文会使焦点回落 <body>,那是「点击后就地查找」的常见路径,不能据此清空。
      else if (e.target instanceof HTMLElement && e.target !== document.body) cursorRegionRef.current = null
    }
    window.addEventListener('pointerdown', onDown, true)
    window.addEventListener('focusin', onFocus, true)
    return () => {
      window.removeEventListener('pointerdown', onDown, true)
      window.removeEventListener('focusin', onFocus, true)
    }
  }, [regionOf])

  // 详情打开期间按 Ctrl/⌘+F：仅当光标落在某个 [data-find-region] 时拦截并就地查找该区域；
  // 用 window 捕获阶段先于工作台「列表搜索」的冒泡监听执行并 stopPropagation 拦下。光标不在任何区域
  // 时不拦截（不 preventDefault/stopPropagation），让事件冒泡到工作台触发列表搜索。
  // 不接管焦点（内嵌 WebView 下程序化改焦点会干扰鼠标点击）。掩码与工作台一致，忽略 Alt。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.ctrlKey || e.metaKey
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'f') {
        const target = cursorRegionRef.current
        if (!target || !target.isConnected) {
          // 光标不在详情区域内：放行给列表搜索，并收起可能残留的就地查找条
          if (openRef.current) close()
          return
        }
        e.preventDefault()
        e.stopPropagation()
        setRegion(target)
        setOpen(true)
        requestAnimationFrame(() => {
          inputRef.current?.focus()
          inputRef.current?.select()
        })
        return
      }
      if (!openRef.current) return
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        close()
      } else if (e.key === 'Enter' && document.activeElement === inputRef.current) {
        e.preventDefault()
        go(e.shiftKey ? -1 : 1)
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [go, close, regionOf])

  const onQueryChange = (v: string) => {
    activeRef.current = 0
    setQuery(v)
  }
  const toggleCase = () => {
    activeRef.current = 0
    setCaseSensitive((v) => !v)
  }

  const scopeLabel = region?.dataset.findLabel

  return (
    <div ref={scopeRef} className={cx('flex h-full min-h-0 flex-col', className)}>
      {children}

      {open &&
        region &&
        createPortal(
          <div
            data-find-skip
            className="wb-pop absolute right-3 top-10 z-20 flex items-center gap-0.5 rounded-wb border border-line bg-surface px-1.5 py-1 shadow-wb"
          >
            {scopeLabel && (
              <span className="mr-0.5 border-r border-line/60 pl-0.5 pr-1.5 text-2xs text-fg-faint">{scopeLabel}</span>
            )}
            <input
              ref={inputRef}
              value={query}
              onChange={(e) => onQueryChange(e.target.value)}
              placeholder={t('find.placeholder')}
              spellCheck={false}
              className="h-6 w-44 bg-transparent px-1 text-[12px] text-fg outline-none placeholder:text-fg-faint"
            />
            <span className="min-w-[46px] px-1 text-center text-2xs tabular-nums text-fg-faint">
              {query ? (count ? `${active + 1}/${count}${capped ? '+' : ''}` : t('find.none')) : ''}
            </span>
            <button
              type="button"
              title={t('find.caseSensitive')}
              aria-pressed={caseSensitive}
              onClick={toggleCase}
              className={cx(
                'flex h-6 w-6 items-center justify-center rounded-wb-sm transition hover:bg-elevated',
                caseSensitive ? 'bg-elevated text-accent' : 'text-fg-faint hover:text-fg',
              )}
            >
              <CaseSensitive className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              title={t('find.prev')}
              disabled={!count}
              onClick={() => go(-1)}
              className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg disabled:pointer-events-none disabled:opacity-40"
            >
              <ChevronUp className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              title={t('find.next')}
              disabled={!count}
              onClick={() => go(1)}
              className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg disabled:pointer-events-none disabled:opacity-40"
            >
              <ChevronDown className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              title={t('find.close')}
              onClick={close}
              className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>,
          region,
        )}
    </div>
  )
}
