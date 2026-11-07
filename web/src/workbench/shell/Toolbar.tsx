import React, { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { ArrowDownToLine, FilterX, Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Chip, cx, IconButton, Tooltip } from '../ui/primitives'

export interface FilterChip {
  key: string
  /** 同组芯片连续排列，组与组之间画分隔线 */
  group: string
  label: string
  active: boolean
}

interface ToolbarProps {
  chips: FilterChip[]
  onChip: (key: string) => void

  search: string
  onSearch: (v: string) => void

  follow: boolean
  onToggleFollow: () => void

  /** 搜索行是否展开（持久化偏好驱动；默认隐藏） */
  searchVisible: boolean
  onToggleSearch: () => void
  /** 当前是否存在筛选（非「全部」芯片或非空搜索），用于显示「清除筛选」 */
  filterActive: boolean
  onClearFilter: () => void

  searchRef?: React.RefObject<HTMLInputElement>
}

const FADE = '24px'

/** 芯片行左右是否还有被滚出视野的内容，用于渐隐提示；隐藏滚动条后这是唯一的可滚动线索。 */
function useScrollEdges(deps: unknown) {
  const ref = useRef<HTMLDivElement>(null)
  const [edges, setEdges] = useState({ left: false, right: false })
  const update = useCallback(() => {
    const el = ref.current
    if (!el) return
    const left = el.scrollLeft > 0
    const right = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    setEdges((prev) => (prev.left === left && prev.right === right ? prev : { left, right }))
  }, [])
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [update])
  // 计数位数变化会改变内容宽度而不触发容器 resize。
  useLayoutEffect(update, [update, deps])
  const { left, right } = edges
  const maskImage =
    left || right
      ? `linear-gradient(to right, ${left ? 'transparent' : '#000'} 0, #000 ${left ? FADE : '0px'}, #000 calc(100% - ${right ? FADE : '0px'}), ${right ? 'transparent' : '#000'} 100%)`
      : undefined
  return { ref, update, maskImage }
}

export function Toolbar({
  chips,
  onChip,
  search,
  onSearch,
  follow,
  onToggleFollow,
  searchVisible,
  onToggleSearch,
  filterActive,
  onClearFilter,
  searchRef,
}: ToolbarProps) {
  const { t } = useTranslation()
  const chipScroll = useScrollEdges(chips)
  return (
    <div className="flex flex-col border-b border-line bg-inset">
      {/* 第一行：过滤芯片 + 工具按钮 */}
      <div className="flex h-[var(--wb-filterbar-h)] items-center gap-1 px-1.5">
        {/* 芯片多于可用宽度时横向滚动；隐藏滚动条以免挤占 30px 高的筛选栏，滚轮纵向滚动映射为横向。 */}
        <div
          ref={chipScroll.ref}
          className="flex min-w-0 items-center gap-0.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          style={{ maskImage: chipScroll.maskImage }}
          onScroll={chipScroll.update}
          onWheel={(e) => {
            if (e.deltaY !== 0 && e.deltaX === 0) e.currentTarget.scrollLeft += e.deltaY
          }}
        >
          {chips.map((c, i) => (
            <React.Fragment key={c.key}>
              {i > 0 && chips[i - 1].group !== c.group && <span aria-hidden className="mx-1.5 h-4 w-px shrink-0 bg-line-strong" />}
              <Chip ghost active={c.active} onClick={() => onChip(c.key)}>
                {c.label}
              </Chip>
            </React.Fragment>
          ))}
        </div>

        <div className="flex-1" />

        {filterActive && (
          <Tooltip label={t('toolbar.clearFilter')} placement="bottom">
            <IconButton size="xs" onClick={onClearFilter} aria-label={t('toolbar.clearFilter')}>
              <FilterX className="h-4 w-4" />
            </IconButton>
          </Tooltip>
        )}

        <Tooltip label={follow ? t('toolbar.followLatestOn') : t('toolbar.followLatest')} placement="bottom">
          <IconButton size="xs" active={follow} onClick={onToggleFollow} aria-label={t('toolbar.followScroll')}>
            <ArrowDownToLine className="h-4 w-4" />
          </IconButton>
        </Tooltip>

        <Tooltip label={searchVisible ? t('toolbar.hideSearch') : t('toolbar.search')} placement="bottom">
          <IconButton size="xs" active={searchVisible || !!search} onClick={onToggleSearch} aria-label={t('toolbar.searchAria')}>
            <Search className="h-4 w-4" />
          </IconButton>
        </Tooltip>
      </div>

      {/* 第二行：整行搜索框（默认隐藏，记忆上次展开状态） */}
      {searchVisible && (
        <div className="flex items-center gap-2 border-t border-line px-1.5 py-1">
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-fg-faint" />
            <input
              ref={searchRef}
              value={search}
              onChange={(e) => onSearch(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== 'Escape') return
                // Esc：先清空搜索；已为空则收起搜索栏
                e.stopPropagation()
                if (search) onSearch('')
                else {
                  e.currentTarget.blur()
                  onToggleSearch()
                }
              }}
              placeholder={t('toolbar.searchPlaceholder')}
              aria-label={t('toolbar.searchTraffic')}
              spellCheck={false}
              autoFocus
              className={cx(
                'h-control-md w-full rounded-control border border-line bg-surface pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint',
                'outline-none transition-shadow focus:shadow-focus',
              )}
            />
            {search && (
              <button
                type="button"
                onClick={() => onSearch('')}
                aria-label={t('toolbar.clearSearch')}
                className="absolute right-2 top-1/2 flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded-wb-sm text-fg-faint hover:bg-hover hover:text-fg"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
