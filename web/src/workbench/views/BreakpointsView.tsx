import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowDownToLine, ArrowUpFromLine, CircleDot, Clock, PenSquare, Plus, Trash2 } from 'lucide-react'
import { Bridge, type BreakRule } from '@/lib/bridge'
import { useAppStore, useGlobalBreak, usePausedFlows } from '@/store'
import { Button, Field, Panel, TextInput, Toggle } from '../ui/controls'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { Chip, cx, EmptyState, IconButton, MethodTag } from '../ui/primitives'
import { PageShell } from './PageShell'
import { useBreakpointActions } from './breakpoints/actions'
import type { PausedFlow } from './breakpoints/model'

/** 新增规则时的占位：带 * 通配且只匹配 example.com。 */
const RULE_PLACEHOLDER = 'https://example.com/*'

export function BreakpointsView() {
  const { t } = useTranslation()
  const globalBreak = useGlobalBreak()
  const paused = usePausedFlows()
  const [rules, setRules] = useState<BreakRule[]>([])
  const [confirmAbortAll, setConfirmAbortAll] = useState(false)
  const [notice, setNotice] = useState('')
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  // 提示紧挨着实时变化的暂停列表，挂久了会被读成对当前列表的描述，因此给它一个寿命。
  const showNotice = useCallback((text: string) => {
    setNotice(text)
    clearTimeout(noticeTimer.current)
    noticeTimer.current = setTimeout(() => setNotice(''), 6000)
  }, [])
  useEffect(() => () => clearTimeout(noticeTimer.current), [])
  // 列表行上没有编辑器那样的错误条，失败必须落到提示区，否则用户只会反复点。
  const reportFailure = useCallback(
    (err: unknown) => showNotice(err instanceof Error ? err.message : String(err)),
    [showNotice],
  )
  // 每秒重算一次倒计时。断点最多百来条，重渲染代价可忽略。
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    let alive = true
    Bridge.getBreakRules()
      .then((rs) => alive && rs && setRules(rs))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  const setGlobal = (onRequest: boolean, onResponse: boolean) => {
    useAppStore.getState().setGlobalBreak({ onRequest, onResponse })
    Bridge.setGlobalBreak(onRequest, onResponse).catch(() => {})
  }

  /* ── 暂停项处置 ── */

  const { resume, abort } = useBreakpointActions()

  // 返回 false = 这一条已不在暂停中（超时或被另一个窗口处置过）：行会消失，
  // 但要说明缘由，否则「点了放行、我的修改却没生效」会被当成功能坏了。
  const dispose = useCallback(
    async (run: () => Promise<boolean>) => {
      if (!(await run())) showNotice(t('breakpoints.paused.stale'))
    },
    [showNotice, t],
  )

  const bulk = (run: () => Promise<number>) => {
    run()
      .then((n) => showNotice(t('breakpoints.paused.bulkDone', { count: n })))
      .catch(reportFailure)
  }

  return (
    <PageShell icon={CircleDot} title={t('breakpoints.title')} subtitle={t('breakpoints.subtitle')}>
      {/* 全局断点 */}
      <Panel title={t('breakpoints.global.title')} icon={<CircleDot className="h-4 w-4" />}>
        <Field label={t('breakpoints.global.requestLabel')} hint={t('breakpoints.global.requestHint')}>
          <Toggle checked={globalBreak.onRequest} onChange={(v) => setGlobal(v, globalBreak.onResponse)} />
        </Field>
        <Field label={t('breakpoints.global.responseLabel')} hint={t('breakpoints.global.responseHint')}>
          <Toggle checked={globalBreak.onResponse} onChange={(v) => setGlobal(globalBreak.onRequest, v)} />
        </Field>
      </Panel>

      {/* 断点规则 */}
      <RulesPanel rules={rules} onRules={setRules} />

      {/* 暂停的请求 */}
      <Panel
        title={t('breakpoints.paused.title')}
        icon={<CircleDot className="h-4 w-4" />}
        right={
          <>
            <Chip count={paused.length}>{t('breakpoints.paused.waiting')}</Chip>
            {/* 批量处置只作用于本面板这份列表，跟着列表走而不是挂在页头。
                用 ghost：正下方就是逐条的放行/阻断，同样分量会让人分不清点的是哪一个。 */}
            {paused.length > 0 && (
              <>
                <Button size="sm" variant="ghost" onClick={() => bulk(Bridge.resumeAllBreakpoints)}>
                  {t('breakpoints.paused.resumeAll')}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-danger hover:bg-danger/15 hover:text-danger"
                  onClick={() => setConfirmAbortAll(true)}
                >
                  {t('breakpoints.paused.abortAll')}
                </Button>
              </>
            )}
          </>
        }
      >
        {notice && <div className="px-3 py-1.5 text-2xs text-fg-muted">{notice}</div>}
        {paused.length === 0 ? (
          <div className="px-3 py-8">
            <EmptyState
              icon={<CircleDot className="h-7 w-7" />}
              title={t('breakpoints.paused.emptyTitle')}
              hint={t('breakpoints.paused.emptyHint')}
            />
          </div>
        ) : (
          <div role="list" aria-live="polite">
            {paused.map((item) => (
              <PausedRow
                key={item.id}
                item={item}
                remaining={remainingSeconds(item, now)}
                onEdit={() => useAppStore.getState().setEditingBreakpoint(item.id)}
                onResume={() => void dispose(() => resume(item.id, null)).catch(reportFailure)}
                onAbort={() => void dispose(() => abort(item.id)).catch(reportFailure)}
              />
            ))}
          </div>
        )}
      </Panel>

      {confirmAbortAll && (
        <ConfirmDialog
          title={t('breakpoints.paused.abortAll')}
          message={t('breakpoints.paused.abortAllConfirm', { count: paused.length })}
          confirmLabel={t('breakpoints.paused.abortAll')}
          cancelLabel={t('breakpoints.rules.cancelAdd')}
          onConfirm={() => {
            setConfirmAbortAll(false)
            bulk(Bridge.abortAllBreakpoints)
          }}
          onClose={() => setConfirmAbortAll(false)}
        />
      )}
    </PageShell>
  )
}

function remainingSeconds(item: PausedFlow, now: number): number {
  if (!item.pausedUntil) return 0
  return Math.max(0, Math.round((item.pausedUntil - now) / 1000))
}

/* ───────────────────────── 规则区 ───────────────────────── */

function RulesPanel({ rules, onRules }: { rules: BreakRule[]; onRules: (rs: BreakRule[]) => void }) {
  const { t } = useTranslation()
  const [draftUrl, setDraftUrl] = useState<string | null>(null)

  const commitAdd = () => {
    const url = (draftUrl ?? '').trim()
    if (!url) return
    Bridge.addBreakRule(url, true, false)
      .then((created) => {
        if (created) onRules([...rules, created])
        setDraftUrl(null)
      })
      .catch(() => setDraftUrl(null))
  }

  const patchRule = (rule: BreakRule, patch: Partial<BreakRule>) => {
    const next = { ...rule, ...patch }
    onRules(rules.map((r) => (r.id === rule.id ? next : r)))
    Bridge.updateBreakRule(next.id, next.url, next.onRequest, next.onResponse, next.enabled)
      .then((found) => {
        // 规则可能已被另一个窗口删掉：回滚，免得界面上留着一条后端并不认识的规则。
        if (!found) onRules(rules.filter((r) => r.id !== rule.id))
      })
      .catch(() => {})
  }

  const removeRule = (id: string) => {
    onRules(rules.filter((r) => r.id !== id))
    Bridge.deleteBreakRule(id).catch(() => {})
  }

  return (
    <Panel
      title={t('breakpoints.rules.title')}
      icon={<CircleDot className="h-4 w-4" />}
      right={
        <Button
          size="sm"
          variant="secondary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setDraftUrl(RULE_PLACEHOLDER)}
        >
          {t('breakpoints.rules.add')}
        </Button>
      }
    >
      {draftUrl !== null && (
        <DraftRuleRow
          url={draftUrl}
          onChange={setDraftUrl}
          onCommit={commitAdd}
          onCancel={() => setDraftUrl(null)}
        />
      )}
      {rules.length === 0 && draftUrl === null ? (
        <div className="px-3 py-6">
          <EmptyState
            icon={<CircleDot className="h-7 w-7" />}
            title={t('breakpoints.rules.emptyTitle')}
            hint={t('breakpoints.rules.emptyHint')}
          />
        </div>
      ) : (
        rules.map((rule) => (
          <RuleRow
            key={rule.id}
            rule={rule}
            onPatch={(patch) => patchRule(rule, patch)}
            onRemove={() => removeRule(rule.id)}
          />
        ))
      )}
    </Panel>
  )
}

/**
 * 新增规则先让用户把 URL 写对再落库。
 * 直接建一条占位规则是危险的：不含 * 的模式退化为子串包含匹配，一条 `https://` 就能
 * 把全部 HTTPS 流量按住并瞬间占满断点名额。
 */
function DraftRuleRow({
  url,
  onChange,
  onCommit,
  onCancel,
}: {
  url: string
  onChange: (v: string) => void
  onCommit: () => void
  onCancel: () => void
}) {
  const { t } = useTranslation()
  const ref = useRef<HTMLInputElement | null>(null)
  useEffect(() => ref.current?.select(), [])
  const empty = url.trim() === ''
  const noWildcard = !empty && !url.includes('*')

  return (
    <div className="flex flex-col gap-1 bg-elevated/40 px-3 py-2">
      <div className="flex items-center gap-2.5">
        <input
          ref={ref}
          value={url}
          spellCheck={false}
          autoFocus
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') onCommit()
            if (e.key === 'Escape') onCancel()
          }}
          placeholder={RULE_PLACEHOLDER}
          aria-label={t('breakpoints.rules.title')}
          className={cx(
            'min-w-0 flex-1 rounded-control border bg-inset px-2 py-1 font-mono text-[11.5px] text-fg outline-none',
            empty ? 'border-danger' : 'border-line focus:border-accent',
          )}
        />
        <Button size="sm" variant="primary" disabled={empty} onClick={onCommit}>
          {t('breakpoints.rules.confirmAdd')}
        </Button>
        <Button size="sm" onClick={onCancel}>
          {t('breakpoints.rules.cancelAdd')}
        </Button>
      </div>
      {empty && <span className="text-2xs text-danger">{t('breakpoints.rules.urlRequired')}</span>}
      {noWildcard && <span className="text-2xs text-warn">{t('breakpoints.rules.wildcardHint')}</span>}
    </div>
  )
}

function RuleRow({
  rule,
  onPatch,
  onRemove,
}: {
  rule: BreakRule
  onPatch: (patch: Partial<BreakRule>) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const [url, setUrl] = useState(rule.url)
  useEffect(() => setUrl(rule.url), [rule.url])

  // 失焦/回车才提交，不用防抖：防抖会把 `https://` 这种键入中间态直接下发，
  // 而不含 * 的模式按子串匹配，那一瞬间全部流量都会被按住。
  const commit = () => {
    const next = url.trim()
    if (next === '' || next === rule.url) {
      setUrl(rule.url)
      return
    }
    onPatch({ url: next })
  }

  return (
    <div className={cx('flex items-center gap-2.5 px-3 py-2', !rule.enabled && 'opacity-70')}>
      <Toggle checked={rule.enabled} onChange={(v) => onPatch({ enabled: v })} />
      <TextInput
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') e.currentTarget.blur()
          if (e.key === 'Escape') setUrl(rule.url)
        }}
        width="100%"
        placeholder={RULE_PLACEHOLDER}
        title={url}
        className="flex-1 font-mono text-[11.5px]"
      />
      <button
        type="button"
        onClick={() => onPatch({ onRequest: !rule.onRequest })}
        className={cx(
          'shrink-0 rounded-full px-2 py-px text-[10px] font-medium transition-colors',
          rule.onRequest ? 'bg-info/15 text-info' : 'bg-fg-faint/10 text-fg-faint hover:text-fg',
        )}
        title={t('breakpoints.rules.requestPhaseTitle')}
      >
        {t('breakpoints.phase.request')}
      </button>
      <button
        type="button"
        onClick={() => onPatch({ onResponse: !rule.onResponse })}
        className={cx(
          'shrink-0 rounded-full px-2 py-px text-[10px] font-medium transition-colors',
          rule.onResponse ? 'bg-iris/15 text-iris' : 'bg-fg-faint/10 text-fg-faint hover:text-fg',
        )}
        title={t('breakpoints.rules.responsePhaseTitle')}
      >
        {t('breakpoints.phase.response')}
      </button>
      <IconButton size="sm" tone="danger" onClick={onRemove} title={t('breakpoints.rules.delete')}>
        <Trash2 className="h-3.5 w-3.5" />
      </IconButton>
    </div>
  )
}

/* ───────────────────────── 暂停项行 ───────────────────────── */

function PausedRow({
  item,
  remaining,
  onEdit,
  onResume,
  onAbort,
}: {
  item: PausedFlow
  remaining: number
  onEdit: () => void
  onResume: () => void
  onAbort: () => void
}) {
  const { t } = useTranslation()
  const isRequest = item.phase === 'request'
  const PhaseIcon = isRequest ? ArrowUpFromLine : ArrowDownToLine
  const phaseLabel = isRequest ? t('breakpoints.phase.request') : t('breakpoints.phase.response')

  return (
    <div role="listitem" className="flex items-center gap-2.5 px-3 py-2.5">
      <MethodTag method={item.method} className="w-12 shrink-0 text-center" />
      <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-muted" title={item.url}>
        {item.url}
      </span>
      {remaining > 0 && (
        <span
          className={cx(
            'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-px text-[10px] font-medium tabular-nums',
            remaining <= 30 ? 'bg-danger/15 text-danger' : remaining <= 60 ? 'bg-warn/15 text-warn' : 'text-fg-faint',
          )}
          title={t('breakpoints.paused.countdownTitle')}
        >
          <Clock className="h-3 w-3" />
          {remaining}s
        </span>
      )}
      <span
        className={cx(
          'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-px text-[10px] font-medium',
          isRequest ? 'bg-info/15 text-info' : 'bg-iris/15 text-iris',
        )}
        title={t('breakpoints.paused.pausedAtTitle', { phase: phaseLabel })}
      >
        <PhaseIcon className="h-3 w-3" />
        {t('breakpoints.paused.pausedAt', { phase: phaseLabel })}
      </span>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button size="sm" icon={<PenSquare className="h-3.5 w-3.5" />} onClick={onEdit}>
          {t('breakpoints.paused.edit')}
        </Button>
        <Button variant="primary" size="sm" onClick={onResume}>
          {t('breakpoints.paused.resume')}
        </Button>
        <Button variant="danger" size="sm" onClick={onAbort}>
          {t('breakpoints.paused.abort')}
        </Button>
      </div>
    </div>
  )
}
