import React from 'react'
import { ArrowDownToLine, FilterX, Search, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Chip, cx, IconButton, Tooltip } from '../ui/primitives'

export interface FilterChip {
  key: string
  label: string
  count: number
}

interface ToolbarProps {
  chips: FilterChip[]
  activeChip: string
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

export function Toolbar({
  chips,
  activeChip,
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
  return (
    <div className="flex flex-col border-b border-line bg-surface">
      {/* 第一行：过滤芯片 + 工具按钮 */}
      <div className="flex h-9 items-center gap-2 px-2">
        <div className="wb-scroll flex items-center gap-1.5 overflow-x-auto">
          {chips.map((c) => (
            <Chip key={c.key} active={activeChip === c.key} onClick={() => onChip(c.key)} count={c.count}>
              {c.label}
            </Chip>
          ))}
        </div>

        <div className="flex-1" />

        {filterActive && (
          <Tooltip label={t('toolbar.clearFilter')} placement="bottom">
            <IconButton onClick={onClearFilter} aria-label={t('toolbar.clearFilter')}>
              <FilterX className="h-4 w-4" />
            </IconButton>
          </Tooltip>
        )}

        <Tooltip label={follow ? t('toolbar.followLatestOn') : t('toolbar.followLatest')} placement="bottom">
          <IconButton active={follow} onClick={onToggleFollow} aria-label={t('toolbar.followScroll')}>
            <ArrowDownToLine className="h-4 w-4" />
          </IconButton>
        </Tooltip>

        <Tooltip label={searchVisible ? t('toolbar.hideSearch') : t('toolbar.search')} placement="bottom">
          <IconButton active={searchVisible || !!search} onClick={onToggleSearch} aria-label={t('toolbar.searchAria')}>
            <Search className="h-4 w-4" />
          </IconButton>
        </Tooltip>
      </div>

      {/* 第二行：整行搜索框（默认隐藏，记忆上次展开状态） */}
      {searchVisible && (
        <div className="flex items-center gap-2 border-t border-line/60 px-2 pb-2 pt-1.5">
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
                'h-8 w-full rounded-wb border border-line bg-inset pl-9 pr-9 text-[13px] text-fg placeholder:text-fg-faint',
                'outline-none transition-colors focus:border-accent focus:bg-surface',
              )}
            />
            {search && (
              <button
                type="button"
                onClick={() => onSearch('')}
                aria-label={t('toolbar.clearSearch')}
                className="absolute right-2 top-1/2 flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded-sm text-fg-faint hover:bg-elevated hover:text-fg"
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
