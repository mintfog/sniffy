import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CaseSensitive, ChevronDown, ChevronUp, X } from 'lucide-react'
import { cx } from '../ui/primitives'

/*
 * 详情面板内的「就地查找」（Ctrl/⌘+F）。
 *
 * 包裹一个详情面板：焦点落在面板内时按 Ctrl/⌘+F 浮出查找条，在当前可见内容
 * （响应体/请求体/头/Raw/Tree…）里高亮命中并支持上/下一个跳转。默认隐藏，纯快捷键唤出。
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
  const wrapRef = useRef<HTMLDivElement>(null)
  const scopeRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [count, setCount] = useState(0)
  const [capped, setCapped] = useState(false)
  const [active, setActive] = useState(0)

  const rangesRef = useRef<Range[]>([])
  const activeRef = useRef(0)
  const openRef = useRef(false)
  openRef.current = open

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
    const root = scopeRef.current
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
    wrapRef.current?.focus()
  }, [clearPaint])

  // 命中重算：开启时、以及 query/大小写变化时。防抖合并连续按键（大 body 下逐字全量重扫会卡顿）。
  useEffect(() => {
    if (!open) return
    const id = setTimeout(() => recompute(), 80)
    return () => clearTimeout(id)
  }, [open, recompute])

  useEffect(() => {
    if (!open) clearPaint()
  }, [open, clearPaint])

  // CSS.highlights 是文档级全局表，open 态下面板被直接卸载时不会走上面的关闭路径，须在卸载时兜底清理
  useEffect(() => () => clearPaint(), [clearPaint])

  // 内容变化（切页签/换查看模式/展开树/流式新消息）时重算
  useEffect(() => {
    if (!open) return
    const root = scopeRef.current
    if (!root) return
    let raf = 0
    const obs = new MutationObserver(() => {
      cancelAnimationFrame(raf)
      raf = requestAnimationFrame(() => recomputeRef.current())
    })
    obs.observe(root, { childList: true, subtree: true, characterData: true })
    return () => {
      obs.disconnect()
      cancelAnimationFrame(raf)
    }
  }, [open])

  // 焦点落在面板内即可用 Ctrl/⌘+F 唤出；捕获阶段拦截并阻止冒泡，避免触发工作台的「列表搜索」。
  useEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const onKey = (e: KeyboardEvent) => {
      // 与工作台全局 Ctrl/⌘+F 用同一掩码（忽略 Alt），避免同一组合在详情聚焦与否时行为分叉
      const mod = e.ctrlKey || e.metaKey
      if (mod && !e.shiftKey && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        e.stopPropagation()
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
    el.addEventListener('keydown', onKey, true)
    return () => el.removeEventListener('keydown', onKey, true)
  }, [go, close])

  // 选中某行后自动把焦点交给面板，使「点行 → Ctrl/⌘+F」可直接查找详情（无需先点详情）。
  useEffect(() => {
    wrapRef.current?.focus()
  }, [])

  const onQueryChange = (v: string) => {
    activeRef.current = 0
    setQuery(v)
  }
  const toggleCase = () => {
    activeRef.current = 0
    setCaseSensitive((v) => !v)
  }

  return (
    <div
      ref={wrapRef}
      tabIndex={-1}
      className={cx('relative flex h-full min-h-0 flex-col outline-none', className)}
    >
      <div ref={scopeRef} className="flex min-h-0 flex-1 flex-col">
        {children}
      </div>

      {open && (
        <div
          data-find-skip
          className="wb-pop absolute right-3 top-2 z-20 flex items-center gap-0.5 rounded-wb border border-line bg-surface px-1.5 py-1 shadow-wb"
        >
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
        </div>
      )}
    </div>
  )
}
