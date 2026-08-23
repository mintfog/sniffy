/**
 * 构造器页签条。
 *
 * 页签按可用宽度压缩而非横滚，好让「+」始终紧跟在最后一个页签之后（浏览器的做法）；
 * 压到最小宽度仍放不下才横滚，此时靠右侧钉住的下拉兜底。
 */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, Plus, Terminal, X } from 'lucide-react'
import { methodText } from '../../lib/format'
import { cx } from '../../ui/primitives'
import { ContextMenu, type MenuNode } from '../../ui/Menu'
import { KIND_META, KIND_ORDER } from './kinds'
import { draftLabel, draftMenuLabel, type Draft, type DraftKind } from './model'

/** 页签只有 min-w-[104px]，方法名与类型标记二选一；GraphQL 恒是 POST，显示它不携带信息。 */
function kindMark(d: Draft): { text: string; className: string } {
  const meta = KIND_META[d.kind]
  return meta.tag ? { text: meta.tag, className: meta.iconClass } : { text: d.method, className: methodText(d.method) }
}

export function DraftTabs({
  drafts,
  activeId,
  onSelect,
  onClose,
  onAdd,
  onImportCurl,
}: {
  drafts: Draft[]
  activeId: string
  onSelect: (id: string) => void
  onClose: (id: string) => void
  onAdd: (kind: DraftKind) => void
  onImportCurl: () => void
}) {
  const { t } = useTranslation()
  const stripRef = useRef<HTMLDivElement>(null)
  const [listAt, setListAt] = useState<{ x: number; y: number } | null>(null)
  const [addAt, setAddAt] = useState<{ x: number; y: number } | null>(null)

  // 页签压到最小宽度后才会横滚，此时被激活的那个可能在视野外——从流量表连开几条
  // 「编辑后重发」尤其容易撞上。切换页签时把它滚进来。
  const lastId = drafts[drafts.length - 1]?.id
  useEffect(() => {
    const strip = stripRef.current
    if (!strip) return
    // 激活的是最后一个页签时直接滚到底，顺带把跟在它后面的「+」也带进视野。
    if (activeId === lastId) {
      strip.scrollLeft = strip.scrollWidth
      return
    }
    strip.querySelector<HTMLElement>(`[data-tab-id="${activeId}"]`)?.scrollIntoView({
      block: 'nearest',
      inline: 'nearest',
    })
  }, [activeId, lastId])

  const listItems: MenuNode[] = drafts.map((d) => ({
    label: `${kindMark(d).text}  ${draftMenuLabel(d) || t('compose.tab.new')}`,
    checked: d.id === activeId,
    onSelect: () => onSelect(d.id),
  }))

  const addItems: MenuNode[] = [
    { type: 'label', label: t('compose.kind.menu') },
    ...KIND_ORDER.map((kind): MenuNode => {
      const meta = KIND_META[kind]
      return { label: meta.label, icon: meta.icon, iconClass: meta.iconClass, onSelect: () => onAdd(kind) }
    }),
    { type: 'separator' },
    { label: t('compose.kind.curl'), icon: Terminal, onSelect: onImportCurl },
  ]

  return (
    <div className="flex h-8 shrink-0 items-stretch border-b border-line bg-inset">
      <div ref={stripRef} className="flex min-w-0 flex-1 items-stretch overflow-x-auto">
        {drafts.map((d) => {
          const activeTab = d.id === activeId
          const label = draftLabel(d) || t('compose.tab.new')
          const mark = kindMark(d)
          return (
            <div
              key={d.id}
              data-tab-id={d.id}
              className={cx(
                'group/tab relative flex h-full min-w-[104px] shrink basis-[200px] items-center gap-1.5 border-r border-line pl-2.5 pr-1 transition-colors',
                activeTab ? 'bg-surface' : 'hover:bg-elevated/60',
              )}
            >
              {activeTab && <span aria-hidden className="absolute inset-x-0 top-0 h-[2px] bg-accent" />}
              <button
                type="button"
                onClick={() => onSelect(d.id)}
                title={d.url || label}
                className="flex min-w-0 flex-1 items-center gap-1.5 text-left outline-none focus-visible:underline"
              >
                <span className={cx('shrink-0 font-mono text-2xs font-semibold', mark.className)}>{mark.text}</span>
                <span className={cx('truncate text-[12px]', activeTab ? 'text-fg' : 'text-fg-muted')}>{label}</span>
              </button>
              <button
                type="button"
                onClick={() => onClose(d.id)}
                title={t('compose.tab.close')}
                aria-label={t('compose.tab.close')}
                className={cx(
                  'flex h-4 w-4 shrink-0 items-center justify-center rounded-[3px] text-fg-faint transition hover:bg-elevated hover:text-fg focus-visible:opacity-100 focus-visible:outline-none',
                  activeTab ? 'opacity-100' : 'opacity-0 group-hover/tab:opacity-100',
                )}
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          )
        })}
        <button
          type="button"
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect()
            setAddAt({ x: r.left, y: r.bottom + 2 })
          }}
          title={t('compose.tab.add')}
          aria-label={t('compose.tab.add')}
          className="flex w-8 shrink-0 items-center justify-center border-r border-line text-fg-faint transition hover:bg-elevated hover:text-fg focus-visible:outline-none"
        >
          <Plus className="h-3.5 w-3.5" />
        </button>
        <div className="flex-1" />
      </div>
      {drafts.length > 1 && (
        <button
          type="button"
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect()
            setListAt({ x: r.right, y: r.bottom + 2 })
          }}
          title={t('compose.tab.list')}
          aria-label={t('compose.tab.list')}
          className="flex w-7 shrink-0 items-center justify-center border-l border-line text-fg-faint transition hover:bg-elevated hover:text-fg focus-visible:outline-none"
        >
          <ChevronDown className="h-3.5 w-3.5" />
        </button>
      )}
      {listAt && <ContextMenu x={listAt.x} y={listAt.y} items={listItems} onClose={() => setListAt(null)} />}
      {addAt && <ContextMenu x={addAt.x} y={addAt.y} items={addItems} onClose={() => setAddAt(null)} />}
    </div>
  )
}
