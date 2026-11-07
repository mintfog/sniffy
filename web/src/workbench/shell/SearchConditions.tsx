import React, { useEffect, useImperativeHandle, useRef } from 'react'
import { Minus, Plus, Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { SelectMenu } from '../ui/SelectMenu'
import { IconButton, Tooltip } from '../ui/primitives'
import {
  MARK_COLORS,
  newCondition,
  opsForField,
  SEARCH_FIELDS,
  withField,
  type SearchCondition,
  type SearchField,
  type SearchLogic,
  type SearchOp,
  valueOptions,
} from '../lib/searchFilter'
import { SuggestInput } from './SuggestInput'

// 字段与匹配方式列表较长，按下拉默认高度要滚动才看得全
const MENU_MAX_HEIGHT = 540

interface SearchConditionsProps {
  conditions: SearchCondition[]
  logic: SearchLogic
  invalid: ReadonlySet<string>
  onConditions: (next: SearchCondition[]) => void
  onLogic: (next: SearchLogic) => void
  /** 首行为空时再按 Esc：收起搜索栏 */
  onCollapse: () => void
  /** Ctrl+F 的聚焦入口：首行的取值控件可能是输入框，也可能是标记色下拉 */
  focusRef?: React.Ref<SearchFocusHandle>
}

export interface SearchFocusHandle {
  focus: () => void
}

function focusValue(scope: ParentNode | null | undefined) {
  const el = scope?.querySelector<HTMLElement>('[data-search-value] input, [data-search-value] [role="combobox"]')
  el?.focus()
  if (el instanceof HTMLInputElement) el.select()
}

function placeholderKey(c: SearchCondition): string {
  if (c.field === 'reqBody' || c.field === 'resBody') return 'toolbar.placeholder.body'
  if (c.op === 'regex' || c.op === 'notRegex') return 'toolbar.placeholder.regex'
  if (c.op === 'wildcard' || c.op === 'notWildcard') return 'toolbar.placeholder.wildcard'
  if (c.field === 'all') return 'toolbar.searchPlaceholder'
  return 'toolbar.placeholder.value'
}

export function SearchConditions({
  conditions,
  logic,
  invalid,
  onConditions,
  onLogic,
  onCollapse,
  focusRef,
}: SearchConditionsProps) {
  const { t } = useTranslation()
  const rootRef = useRef<HTMLDivElement>(null)
  const focusIdRef = useRef<string>()

  useEffect(() => {
    const id = focusIdRef.current
    if (!id) return
    focusIdRef.current = undefined
    focusValue(rootRef.current?.querySelector(`[data-cond="${id}"]`))
  }, [conditions])

  useImperativeHandle(focusRef, () => ({ focus: () => focusValue(rootRef.current) }), [])

  const fieldOptions = SEARCH_FIELDS.map((f) => ({ value: f, label: t(`toolbar.field.${f}`) }))
  const markOptions = MARK_COLORS.map((m) => ({ value: m, label: t(`workbench.mark.${m}`) }))

  const update = (id: string, fn: (c: SearchCondition) => SearchCondition) =>
    onConditions(conditions.map((c) => (c.id === id ? fn(c) : c)))

  const addAfter = (index: number) => {
    const c = newCondition()
    focusIdRef.current = c.id
    onConditions([...conditions.slice(0, index + 1), c, ...conditions.slice(index + 1)])
  }

  const remove = (id: string) => onConditions(conditions.filter((c) => c.id !== id))

  // Esc：先清空本行（标记色行恢复成空的「全部」条件）；已为空则移除本行并聚焦相邻行，只剩一行时收起搜索栏。
  // 取值下拉展开时 Esc 已被下拉自己拦下，用于收起下拉。
  const onValueKeyDown = (e: React.KeyboardEvent<HTMLElement>, c: SearchCondition, i: number) => {
    if (e.key !== 'Escape' || e.nativeEvent.isComposing) return
    e.stopPropagation()
    if (c.field === 'mark') {
      focusIdRef.current = c.id
      update(c.id, (x) => ({ ...x, field: 'all', op: 'contains', value: '' }))
    } else if (c.value) {
      update(c.id, (x) => ({ ...x, value: '' }))
    } else if (conditions.length > 1) {
      focusIdRef.current = conditions[i > 0 ? i - 1 : 1].id
      remove(c.id)
    } else {
      if (e.target instanceof HTMLElement) e.target.blur()
      onCollapse()
    }
  }

  return (
    <div ref={rootRef} className="flex flex-col gap-1 border-t border-line px-1.5 py-1">
      {conditions.map((c, i) => {
        const bad = invalid.has(c.id)
        return (
          <div key={c.id} data-cond={c.id} className="flex items-center gap-1.5">
            {i === 0 ? (
              <span className="flex w-9 shrink-0 justify-center text-fg-faint">
                <Search className="h-4 w-4" />
              </span>
            ) : (
              <button
                type="button"
                onClick={() => onLogic(logic === 'and' ? 'or' : 'and')}
                aria-label={logic === 'and' ? t('toolbar.logic.hintAnd') : t('toolbar.logic.hintOr')}
                className="h-control-sm w-9 shrink-0 rounded-control border border-line-strong text-[12px] text-fg-muted outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-focus"
              >
                {logic === 'and' ? t('toolbar.logic.and') : t('toolbar.logic.or')}
              </button>
            )}

            <SelectMenu<SearchField>
              label={t('toolbar.searchField')}
              value={c.field}
              options={fieldOptions}
              onChange={(f) => update(c.id, (x) => withField(x, f))}
              className="w-[8.5rem]"
              maxHeight={MENU_MAX_HEIGHT}
            />
            <SelectMenu<SearchOp>
              label={t('toolbar.searchOp')}
              value={c.op}
              options={opsForField(c.field).map((o) => ({ value: o, label: t(`toolbar.op.${o}`) }))}
              onChange={(op) => update(c.id, (x) => ({ ...x, op }))}
              className="w-[8.5rem]"
              maxHeight={MENU_MAX_HEIGHT}
            />

            <div data-search-value className="contents" onKeyDown={(e) => onValueKeyDown(e, c, i)}>
              {c.field === 'mark' ? (
                <SelectMenu
                  label={t('toolbar.searchMark')}
                  value={c.value}
                  options={markOptions}
                  onChange={(value) => update(c.id, (x) => ({ ...x, value }))}
                  className="min-w-0 flex-1"
                />
              ) : (
                <SuggestInput
                  value={c.value}
                  onChange={(value) => update(c.id, (x) => ({ ...x, value }))}
                  options={valueOptions(c)}
                  placeholder={t(placeholderKey(c))}
                  aria-label={t('toolbar.searchTraffic')}
                  invalid={bad}
                  title={bad ? t('toolbar.invalidRegex') : undefined}
                  clearLabel={t('toolbar.clearSearch')}
                  autoFocus={i === 0}
                />
              )}
            </div>

            <Tooltip label={t('toolbar.addCondition')} placement="bottom">
              <IconButton size="sm" onClick={() => addAfter(i)} aria-label={t('toolbar.addCondition')}>
                <Plus className="h-4 w-4" />
              </IconButton>
            </Tooltip>
            {conditions.length > 1 ? (
              <Tooltip label={t('toolbar.removeCondition')} placement="bottom">
                <IconButton size="sm" onClick={() => remove(c.id)} aria-label={t('toolbar.removeCondition')}>
                  <Minus className="h-4 w-4" />
                </IconButton>
              </Tooltip>
            ) : (
              // 只剩一行时保留占位，避免增删条件时输入框宽度跳动
              <span aria-hidden className="w-control-sm shrink-0" />
            )}
          </div>
        )
      })}
    </div>
  )
}
