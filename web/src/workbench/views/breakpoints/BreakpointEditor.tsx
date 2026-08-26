/**
 * 断点改包编辑器：把一条被按住的 flow 摊开成可编辑的方法 / URL / 头 / 正文，
 * 改完再决定放行还是阻断。
 *
 * 界面对"改了不生效"的地方一律如实置灰而不是假装可编辑——断点是用来验证假设的工具，
 * 一个会悄悄吞掉修改的编辑框比没有编辑框更糟。
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Clock, X } from 'lucide-react'
import { formatSize, prettyJson } from '../../lib/format'
import { ConfirmDialog } from '../../ui/ConfirmDialog'
import { Button, Select, TextInput } from '../../ui/controls'
import { cx, MethodTag } from '../../ui/primitives'
import { METHODS } from '../compose/model'
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
  /** 该条已被后端解除（超时 / 别处处置），编辑器转成只读并给出说明。 */
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
  // 编辑中的内容只认打开那一刻的快照：续期会带着新的截止时刻重发一次命中，
  // 跟着列表重建草稿会把用户正在敲的东西冲掉。
  const [base] = useState(item)
  const [draft, setDraft] = useState<BreakDraft>(() => draftFrom(item))
  const [tab, setTab] = useState<EditTab>('headers')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  // 遮罩只认"起落都在遮罩上"的点击：在正文编辑器里往下拖选文本、松手时落到遮罩上，
  // 浏览器同样会派发一次 click，那一下不该把改到一半的包丢掉。
  const downOnOverlay = useRef(false)

  const isRequest = base.phase === 'request'
  const editable = bodyEditable(base)
  const locked = busy || !!goneReason
  const bodyInfo = isRequest ? base.requestBody : base.responseBody

  const patch = useMemo(() => buildResumePatch(draft, base), [draft, base])
  const rows = isRequest ? draft.requestHeaders : draft.responseHeaders
  const baseRows = isRequest ? base.requestHeaders : base.responseHeaders
  const changed = useMemo(() => changedRows(rows, baseRows), [rows, baseRows])

  // 关闭前先问一句：编辑器里可能攒着几 KB 手改的 body 与整份头部，关掉即不可恢复
  // （重开是从后端快照重建的）。没有改动时不打断。
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
      // 校验失败时 flow 仍被按在断点上：留住编辑器，把后端的原话摆出来让用户改回去。
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
                  // 原因短语跟着状态码走:置空后由出线侧按新状态码派生标准短语,
                  // 留着上游那句就会拼出 "HTTP/1.1 404 OK"。
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

        {error && (
          <div className="shrink-0 border-t border-line bg-danger/10 px-4 py-1.5 text-2xs leading-relaxed text-danger">
            {error}
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
            <Button variant="primary" size="sm" disabled={locked || !patch} onClick={() => run(() => onResume(patch))}>
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
    // 正文不可编辑，但状态行与响应头照改不误——两条路径写回客户端时用的都是 Flow.Response 上的值。
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
