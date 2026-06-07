import React from 'react'
import { ArrowDownToLine, Search, X } from 'lucide-react'
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

  searchRef?: React.RefObject<HTMLInputElement>
}

export function Toolbar({ chips, activeChip, onChip, search, onSearch, follow, onToggleFollow, searchRef }: ToolbarProps) {
  return (
    <div className="flex h-9 items-center gap-2 border-b border-line bg-surface px-2">
      {/* 过滤芯片 */}
      <div className="wb-scroll flex items-center gap-1.5 overflow-x-auto">
        {chips.map((c) => (
          <Chip key={c.key} active={activeChip === c.key} onClick={() => onChip(c.key)} count={c.count}>
            {c.label}
          </Chip>
        ))}
      </div>

      <div className="flex-1" />

      {/* 跟随滚动 */}
      <Tooltip label={follow ? '已跟随最新（点击关闭）' : '跟随最新'} placement="bottom">
        <IconButton active={follow} onClick={onToggleFollow} aria-label="跟随滚动">
          <ArrowDownToLine className="h-4 w-4" />
        </IconButton>
      </Tooltip>

      {/* 搜索 */}
      <div className="relative w-64">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
        <input
          ref={searchRef}
          value={search}
          onChange={(e) => onSearch(e.target.value)}
          placeholder="过滤 URL / Host / 状态码…"
          spellCheck={false}
          className={cx(
            'h-7 w-full rounded-wb border border-line bg-inset pl-7 pr-7 text-[12px] text-fg placeholder:text-fg-faint',
            'outline-none transition-colors focus:border-accent focus:bg-surface',
          )}
        />
        {search && (
          <button
            type="button"
            onClick={() => onSearch('')}
            className="absolute right-1.5 top-1/2 flex h-4 w-4 -translate-y-1/2 items-center justify-center rounded-sm text-fg-faint hover:bg-elevated hover:text-fg"
          >
            <X className="h-3 w-3" />
          </button>
        )}
      </div>
    </div>
  )
}
