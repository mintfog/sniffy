/**
 * 断点改包编辑器：把一条被按住的 flow 摊开成可编辑的方法 / URL / 头 / 正文，
 * 改完再决定放行还是阻断。
 *
 * 不可编辑字段以只读状态呈现，并通过提示说明其当前语义。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Clock, X } from 'lucide-react'
import { formatSize, prettyJson } from '../../lib/format'
import { ConfirmDialog } from '../../ui/ConfirmDialog'
import { Button, Select, TextInput } from '../../ui/controls'
import { cx, MethodTag } from '../../ui/primitives'
import { badEscapeName, METHODS } from '../compose/model'
import { TabRow } from '../DetailPanel'
import { HeaderTable } from '../compose/HeaderTable'
import { PluginEditor } from '../plugins/editor'
import {
  bodyEditable,
  buildResumePatch,
  byteLength,
  changedRows,
  draftFrom,
  MANAGED_HEADERS,
  type BreakDraft,
  type PausedFlow,
  type ResumePatch,
} from './model'

type EditTab = 'headers' | 'body'

export interface BreakpointEditorProps {
  item: PausedFlow
  /** 剩余秒数；0 表示后端没给截止时刻。 */
  remaining: number
  /** 该条已被后端解除（超时 / 别处处置），编辑器显示为只读并给出说明。 */
  goneReason?: 'expired' | 'resolved'
  onResume: (patch: ResumePatch | null) => Promise<void>
  onAbort: () => Promise<void>
  onExtend: () => void
  onClose: () => void
}

export function BreakpointEditor({
  item,
  remaining,
  goneReason,
  onResume,
  onAbort,
  onExtend,
  onClose,
}: BreakpointEditorProps) {
  const { t } = useTranslation()
  // 草稿固定使用打开时的快照，续期事件不会覆盖用户正在编辑的内容。
  const [base] = useState(item)
  const [draft, setDraft] = useState<BreakDraft>(() => draftFrom(item))
  const [tab, setTab] = useState<EditTab>('headers')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  // 按下与释放均位于遮罩时关闭，文本选择保持编辑器打开。
  const downOnOverlay = useRef(false)

  const isRequest = base.phase === 'request'
  const editable = bodyEditable(base)
  const locked = busy || !!goneReason
  const bodyInfo = isRequest ? base.requestBody : base.responseBody

  const patch = useMemo(() => buildResumePatch(draft, base), [draft, base])
  const rows = isRequest ? draft.requestHeaders : draft.responseHeaders
  const baseRows = isRequest ? base.requestHeaders : base.responseHeaders
  const baseRowsB64 = isRequest ? base.requestHeadersB64 : base.responseHeadersB64
  const changed = useMemo(() => changedRows(rows, baseRows, baseRowsB64), [rows, baseRows, baseRowsB64])
  // 头部字节转义必须有效，放行时才能生成确定的出站字节。
  const badHeader = useMemo(() => badEscapeName(rows), [rows])
  const problem = badHeader ? t('bytes.badEscape', { name: badHeader }) : error

  // 有编辑内容时关闭前确认，空草稿直接关闭。
  const requestClose = useCallback(() => {
    if (busy) return
    if (patch) setConfirmDiscard(true)
    else onClose()
  }, [busy, patch, onClose])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') requestClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [requestClose])

  const run = async (action: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await action()
    } catch (err) {
      // 将后端校验错误显示在编辑器中，flow 保持暂停状态。
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const patchDraft = (p: Partial<BreakDraft>) => setDraft((d) => ({ ...d, ...p }))
  const bodyText = isRequest ? draft.requestBody : draft.responseBody
  const setBodyText = (v: string) => patchDraft(isRequest ? { requestBody: v } : { responseBody: v })
  const setRows = (next: BreakDraft['requestHeaders']) =>
    patchDraft(isRequest ? { requestHeaders: next } : { responseHeaders: next })

  const isJson = useMemo(() => {
    const s = bodyText.trim()
    if (!s || !/^[[{]/.test(s)) return false
    try {
      JSON.parse(s)
      return true
    } catch {
      return false
    }
  }, [bodyText])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onMouseDown={(e) => {
        downOnOverlay.current = e.target === e.currentTarget
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget && downOnOverlay.current) requestClose()
      }}
      role="presentation"
    >
      <div
        className="flex h-[80vh] w-full max-w-5xl flex-col overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={t('breakpoints.edit.title')}
      >
        <header className="flex shrink-0 items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          {isRequest ? (
            <Select
              value={draft.method}
              onChange={(e) => patchDraft({ method: e.target.value })}
              disabled={locked}
              options={METHODS.map((m) => ({ value: m, label: m }))}
              className="w-24 shrink-0 font-mono text-[11.5px]"
              aria-label={t('breakpoints.edit.method')}
            />
          ) : (
            <MethodTag method={draft.method} className="w-12 shrink-0 text-center" />
          )}
          <span
            className={cx(
              'shrink-0 rounded-full px-2 py-px text-[10px] font-medium',
              isRequest ? 'bg-info/15 text-info' : 'bg-iris/15 text-iris',
            )}
          >
            {isRequest ? t('breakpoints.phase.request') : t('breakpoints.phase.response')}
          </span>
          {remaining > 0 && !goneReason && (
            <span
              className={cx(
                'inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-px text-[10px] font-medium tabular-nums',
                remaining <= 30 ? 'bg-danger/15 text-danger' : remaining <= 60 ? 'bg-warn/15 text-warn' : 'bg-fg-faint/10 text-fg-muted',
              )}
              title={t('breakpoints.paused.countdownTitle')}
            >
              <Clock className="h-3 w-3" />
              {t('breakpoints.paused.countdown', { time: formatCountdown(remaining) })}
            </span>
          )}
          {!goneReason && (
            <button
              type="button"
              onClick={onExtend}
              className="shrink-0 rounded-control px-1.5 py-0.5 text-2xs text-fg-muted transition hover:bg-elevated hover:text-fg"
            >
              {t('breakpoints.paused.extend')}
            </button>
          )}
          <button
            type="button"
            onClick={requestClose}
            aria-label={t('breakpoints.edit.close')}
            className="ml-auto shrink-0 text-fg-faint hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        </header>

        {/* URL / 状态码：请求阶段改 URL，响应阶段改状态行 */}
        <div className="flex shrink-0 items-center gap-2 border-b border-line px-4 py-2">
          {isRequest ? (
            <TextInput
              value={draft.url}
              readOnly={locked}
              onChange={(e) => patchDraft({ url: e.target.value })}
              width="100%"
              aria-label={t('breakpoints.edit.url')}
              className="flex-1 font-mono text-[11.5px]"
            />
          ) : (
            <>
              <label className="shrink-0 text-2xs uppercase tracking-wide text-fg-muted" htmlFor="bp-status">
                {t('breakpoints.edit.status')}
              </label>
              <TextInput
                id="bp-status"
                value={draft.status}
                readOnly={locked}
                onChange={(e) =>
                  // 修改状态码时清空状态文本，由出线侧按新状态码派生标准短语。
                  patchDraft({ status: e.target.value.replace(/[^0-9]/g, ''), statusText: '' })
                }
                width="72px"
                className="shrink-0 text-center font-mono text-[11.5px] tabular-nums"
              />
              <TextInput
                value={draft.statusText}
                readOnly={locked}
                onChange={(e) => patchDraft({ statusText: e.target.value })}
                width="100%"
                aria-label={t('breakpoints.edit.statusText')}
                className="flex-1 font-mono text-[11.5px]"
              />
              <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg-faint" title={base.url}>
                {base.url}
              </span>
            </>
          )}
        </div>

        {notice(base, t) && (
          <div className="shrink-0 border-b border-line bg-warn/10 px-4 py-1.5 text-2xs leading-relaxed text-warn">
            {notice(base, t)}
          </div>
        )}
        {goneReason && (
          <div className="flex shrink-0 items-center gap-2 border-b border-line bg-danger/10 px-4 py-1.5 text-2xs leading-relaxed text-danger">
            <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
            {goneReason === 'expired' ? t('breakpoints.paused.expired') : t('breakpoints.paused.stale')}
          </div>
        )}

        <TabRow
          tabs={[
            { key: 'headers', label: t('breakpoints.edit.tabHeaders'), count: rows.filter((r) => r.name.trim()).length },
            { key: 'body', label: t('breakpoints.edit.tabBody') },
          ]}
          active={tab}
          onChange={(k) => setTab(k as EditTab)}
          right={
            tab === 'body' ? (
              <>
                {isJson && editable && !locked && (
                  <button
                    type="button"
                    onClick={() => setBodyText(prettyJson(bodyText))}
                    className="rounded-control px-1.5 py-0.5 text-2xs text-fg-muted transition hover:bg-elevated hover:text-fg"
                  >
                    {t('breakpoints.edit.format')}
                  </button>
                )}
                <span className="tabular-nums text-2xs text-fg-faint">
                  {formatSize(editable ? byteLength(bodyText) : bodyInfo.size)}
                </span>
              </>
            ) : undefined
          }
        />

        <div className="min-h-0 flex-1 overflow-hidden">
          {tab === 'headers' ? (
            <HeaderTable
              rows={rows}
              changed={changed}
              onChange={setRows}
              readOnly={locked}
              managed={MANAGED_HEADERS}
              managedHint={t('breakpoints.edit.managedHeader')}
            />
          ) : (
            <div className="relative h-full min-h-0">
              <PluginEditor
                value={editable ? bodyText : ''}
                onChange={setBodyText}
                readOnly={locked || !editable}
                language="json"
                placeholder={editable ? t('breakpoints.edit.bodyPlaceholder') : ''}
                ariaLabel={t('breakpoints.edit.tabBody')}
                className="h-full"
              />
            </div>
          )}
        </div>

        {problem && (
          <div className="shrink-0 border-t border-line bg-danger/10 px-4 py-1.5 text-2xs leading-relaxed text-danger">
            {problem}
          </div>
        )}
        <footer className="flex shrink-0 items-center gap-2 border-t border-line px-4 py-2.5">
          <span className="text-2xs text-fg-faint">{patch ? '' : t('breakpoints.edit.noChange')}</span>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="danger" size="sm" disabled={locked} onClick={() => run(onAbort)}>
              {t('breakpoints.paused.abort')}
            </Button>
            <Button size="sm" disabled={locked} onClick={() => run(() => onResume(null))}>
              {t('breakpoints.paused.resumeAsIs')}
            </Button>
            <Button
              variant="primary"
              size="sm"
              disabled={locked || !patch || !!badHeader}
              onClick={() => run(() => onResume(patch))}
            >
              {t('breakpoints.paused.resumeEdited')}
            </Button>
          </div>
        </footer>
      </div>

      {confirmDiscard && (
        <ConfirmDialog
          title={t('breakpoints.edit.discardTitle')}
          message={t('breakpoints.edit.discardMessage')}
          confirmLabel={t('breakpoints.edit.discardConfirm')}
          cancelLabel={t('breakpoints.rules.cancelAdd')}
          onConfirm={() => {
            setConfirmDiscard(false)
            onClose()
          }}
          onClose={() => setConfirmDiscard(false)}
        />
      )}
    </div>
  )
}

function notice(p: PausedFlow, t: (k: string, o?: Record<string, unknown>) => string): string {
  if (p.phase === 'response') {
    // 流式与透传正文保持只读，状态行与响应头仍可编辑并写回 Flow.Response。
    if (p.streamed) return t('breakpoints.edit.streamNotice')
    if (p.passthrough) return t('breakpoints.edit.passthroughNotice')
  }
  const body = p.phase === 'request' ? p.requestBody : p.responseBody
  if (body.binary) return t('breakpoints.edit.binaryNotice')
  if (body.tooLarge) return t('breakpoints.edit.oversizeNotice', { size: formatSize(body.size) })
  return ''
}

function formatCountdown(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return m > 0 ? `${m}:${String(s).padStart(2, '0')}` : `${s}s`
}
