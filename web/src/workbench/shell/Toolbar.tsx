import React, { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { ArrowDownToLine, FilterX, LayoutGrid, List, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Chip, IconButton, Tooltip } from '../ui/primitives'

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

  follow: boolean
  onToggleFollow: () => void

  /** 搜索行是否展开（持久化偏好驱动；默认隐藏） */
  searchVisible: boolean
  onToggleSearch: () => void
  /** 搜索行内容，仅在 searchVisible 时渲染 */
  searchRow: React.ReactNode
  /** 搜索条件是否构成筛选；搜索行收起时用它点亮搜索按钮 */
  searchActive: boolean
  /** 当前是否存在筛选（非「全部」芯片或有生效的搜索条件），用于显示「清除筛选」 */
  filterActive: boolean
  onClearFilter: () => void
  /** 列表 / 缩略图网格切换；缺省时不显示切换按钮。 */
  layout?: { grid: boolean; onChange: (grid: boolean) => void }
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
  follow,
  onToggleFollow,
  searchVisible,
  onToggleSearch,
  searchRow,
  searchActive,
  filterActive,
  onClearFilter,
  layout,
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

        {layout && (
          <div className="mr-1 flex items-center gap-px rounded-control bg-hover/60 p-px">
            <Tooltip label={t('toolbar.listView')} placement="bottom">
              <IconButton size="xs" active={!layout.grid} onClick={() => layout.onChange(false)} aria-label={t('toolbar.listView')}>
                <List className="h-4 w-4" />
              </IconButton>
            </Tooltip>
            <Tooltip label={t('toolbar.gridView')} placement="bottom">
              <IconButton size="xs" active={layout.grid} onClick={() => layout.onChange(true)} aria-label={t('toolbar.gridView')}>
                <LayoutGrid className="h-4 w-4" />
              </IconButton>
            </Tooltip>
          </div>
        )}

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
          <IconButton size="xs" active={searchVisible || searchActive} onClick={onToggleSearch} aria-label={t('toolbar.searchAria')}>
            <Search className="h-4 w-4" />
          </IconButton>
        </Tooltip>
      </div>

      {searchVisible && searchRow}
    </div>
  )
}
