import { type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Copy, Download, X } from 'lucide-react'
import { usePrefs } from '../prefs'
import { useElementSize } from '../lib/useElementSize'
import { saveFile } from '../lib/download'
import {
  buildRawRequest,
  buildRawResponse,
  detectContentKind,
  formatClock,
  formatDuration,
  formatSize,
  getHeader,
  headerEntries,
  parseCookies,
  parseFormParams,
  parseQueryParams,
  statusLabel,
  statusTone,
} from '../lib/format'
import type { TrafficRow, Tone } from '../lib/types'
import { cx } from '../ui/primitives'
import { KVTable } from '../ui/controls'
import { BodyViewer, RawCode, UrlHighlight } from './BodyViewer'

/* ───────────────────────── 小组件 ───────────────────────── */

const methodPill: Record<string, string> = {
  GET: 'bg-method-get/15 text-method-get',
  POST: 'bg-method-post/15 text-method-post',
  PUT: 'bg-method-put/15 text-method-put',
  DELETE: 'bg-method-delete/15 text-method-delete',
  PATCH: 'bg-method-patch/15 text-method-patch',
}

function MethodPill({ method }: { method: string }) {
  return (
    <span className={cx('rounded-full px-2 py-[1px] font-mono text-2xs font-bold', methodPill[method.toUpperCase()] ?? 'bg-fg-faint/15 text-fg-muted')}>
      {method.toUpperCase()}
    </span>
  )
}

const tonePill: Record<Tone, string> = {
  ok: 'bg-ok/15 text-ok',
  info: 'bg-info/15 text-info',
  warn: 'bg-warn/15 text-warn',
  danger: 'bg-danger/15 text-danger',
  pending: 'bg-warn/15 text-warn',
  neutral: 'bg-fg-faint/15 text-fg-muted',
}

function Pill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={cx('rounded-full px-2 py-[1px] font-mono text-2xs font-semibold', tonePill[tone])}>{children}</span>
}

function ActionIcon({
  title,
  onClick,
  disabled,
  children,
}: {
  title: string
  onClick?: () => void
  disabled?: boolean
  children: ReactNode
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      disabled={disabled}
      className="flex h-6 w-6 items-center justify-center rounded-control text-fg-faint transition hover:bg-elevated hover:text-fg hover:shadow-raise disabled:pointer-events-none disabled:opacity-40 disabled:shadow-none"
    >
      {children}
    </button>
  )
}

function CopyIcon({ text, title }: { text: string; title: string }) {
  const [done, setDone] = useState(false)
  return (
    <ActionIcon title={title} onClick={() => navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1100) })}>
      {done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
    </ActionIcon>
  )
}

interface SubTab {
  key: string
  label: string
  count?: number
}

function TabRow({
  tabs,
  active,
  onChange,
  right,
}: {
  tabs: SubTab[]
  active: string
  onChange: (k: string) => void
  right?: ReactNode
}) {
  return (
    <div className="flex h-8 shrink-0 items-center border-b border-line bg-surface px-1">
      <div className="flex items-center gap-0.5 overflow-x-auto">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => onChange(t.key)}
            className={cx(
              'inline-flex h-6 items-center whitespace-nowrap rounded-control px-2.5 text-[12px] transition outline-none',
              active === t.key ? 'bg-elevated text-fg shadow-raise' : 'text-fg-muted hover:bg-elevated/60 hover:text-fg',
            )}
          >
            {t.label}
            {t.count != null && t.count > 0 && <span className="ml-1 text-2xs tabular-nums text-fg-faint">{t.count}</span>}
          </button>
        ))}
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-1 pr-1.5 pl-2">{right}</div>
    </div>
  )
}

function rowToCurl(row: TrafficRow): string {
  let curl = `curl -X ${row.method} '${row.url}'`
  for (const [k, v] of headerEntries(row.reqHeaders)) curl += ` \\\n  -H '${k}: ${v}'`
  if (row.reqBody) curl += ` \\\n  --data-raw '${row.reqBody}'`
  return curl
}

/** 内容类型 → 保存文件的扩展名 */
const kindExt: Partial<Record<TrafficRow['contentKind'], string>> = {
  json: 'json',
  html: 'html',
  js: 'js',
  css: 'css',
  text: 'txt',
  form: 'txt',
}

/** 二进制类内容：行模型里 body 是字符串，按字符串落盘必然损坏，禁用保存 */
const binaryKinds: TrafficRow['contentKind'][] = ['image', 'video', 'audio', 'font', 'binary', 'doc']

function canSaveBody(row: TrafficRow): boolean {
  return !!row.resBody && !binaryKinds.includes(row.contentKind)
}

/** 把响应体保存为本地文件（系统原生保存对话框；文件名取 URL 最后一段，缺省按内容类型补扩展名） */
function downloadResponseBody(row: TrafficRow) {
  if (!canSaveBody(row)) return
  const last = row.path.split('?')[0].split('/').filter(Boolean).pop() || 'response'
  const name = last.includes('.') ? last : `${last}.${kindExt[row.contentKind] ?? 'txt'}`
  void saveFile(row.resBody!, name)
}

/* ───────────────────────── 请求区（上） ───────────────────────── */

type ReqTab = 'overview' | 'params' | 'headers' | 'body' | 'cookies' | 'raw'

function RequestPane({ row, onClose }: { row: TrafficRow; onClose: () => void }) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<ReqTab>('overview')
  const tone = statusTone(row)
  // row.contentKind 仅由响应推断;请求体的类型(表单解析、图片预览)须按请求自身的 Content-Type 判定。
  const reqKind = detectContentKind(getHeader(row.reqHeaders, 'content-type') || '', row.path, row.reqBody)
  const query = parseQueryParams(row.url)
  const form = reqKind === 'form' ? parseFormParams(row.reqBody) : []
  const params = [...query, ...form]
  const headers = headerEntries(row.reqHeaders)
  const cookies = parseCookies(getHeader(row.reqHeaders, 'cookie'))

  const tabs: SubTab[] = [
    { key: 'overview', label: t('detail.req.tab.overview') },
    ...(params.length > 0 ? [{ key: 'params', label: t('detail.req.tab.params'), count: params.length } as SubTab] : []),
    { key: 'headers', label: t('detail.req.tab.headers'), count: headers.length },
    { key: 'body', label: t('detail.req.tab.body') },
    { key: 'cookies', label: 'Cookies', count: cookies.length },
    { key: 'raw', label: t('detail.req.tab.raw') },
  ]

  return (
    <div className="relative flex min-h-0 flex-1 flex-col" data-find-region="request" data-find-label={t('find.scopeRequest')}>
      <TabRow
        tabs={tabs}
        active={tab}
        onChange={(k) => setTab(k as ReqTab)}
        right={
          <>
            <MethodPill method={row.method} />
            <Pill tone={tone}>{statusLabel(row)}</Pill>
            <CopyIcon text={row.url} title={t('detail.req.copyUrl')} />
            <CopyIcon text={rowToCurl(row)} title={t('detail.req.copyCurl')} />
            <ActionIcon title={t('detail.req.close')} onClick={onClose}>
              <X className="h-3.5 w-3.5" />
            </ActionIcon>
          </>
        }
      />
      <div className="min-h-0 flex-1">
        {tab === 'overview' && <RequestOverview row={row} />}
        {tab === 'params' && params.length > 0 && (
          <div className="h-full overflow-auto">
            {query.length > 0 && (
              <>
                <GroupLabel>{t('detail.req.params.queryGroup')}</GroupLabel>
                <KVTable rows={query} colLabels={[t('detail.req.params.paramCol'), t('detail.common.valueCol')]} emptyText={t('detail.req.params.emptyQuery')} />
              </>
            )}
            {form.length > 0 && (
              <>
                <GroupLabel>{t('detail.req.params.formGroup')}</GroupLabel>
                <KVTable rows={form} colLabels={[t('detail.req.params.fieldCol'), t('detail.common.valueCol')]} emptyText={t('detail.req.params.emptyForm')} />
              </>
            )}
          </div>
        )}
        {tab === 'headers' && (
          <div className="h-full overflow-auto">
            <KVTable rows={headers} colLabels={[t('detail.common.nameCol'), t('detail.common.valueCol')]} emptyText={t('detail.req.headers.empty')} />
          </div>
        )}
        {tab === 'body' && <BodyViewer body={row.reqBody} kind={reqKind} rowId={row.id} source="request" />}
        {tab === 'cookies' && (
          <div className="h-full overflow-auto">
            <KVTable rows={cookies} colLabels={['Cookie', t('detail.common.valueCol')]} emptyText={t('detail.req.cookies.empty')} />
          </div>
        )}
        {tab === 'raw' && <RawCode text={buildRawRequest(row)} />}
      </div>
    </div>
  )
}

function RequestOverview({ row }: { row: TrafficRow }) {
  const { t } = useTranslation()
  const general: [string, string][] = [
    [t('detail.overview.state'), row.state === 'pending' ? t('detail.overview.statePending') : row.state === 'error' ? t('detail.overview.stateError') : t('detail.overview.stateDone')],
    [t('detail.overview.method'), row.method],
    [t('detail.overview.scheme'), row.scheme.toUpperCase()],
    [t('detail.overview.statusCode'), `${statusLabel(row)} ${row.statusText ?? ''}`.trim()],
    [t('detail.overview.clientIP'), row.clientIP || '—'],
    ['Content-Type', row.contentType || '—'],
    [t('detail.overview.process'), row.process || '—'],
    [t('detail.overview.startedAt'), formatClock(row.startedAt)],
    [t('detail.overview.duration'), formatDuration(row.durationMs)],
    [t('detail.overview.size'), formatSize(row.sizeBytes)],
  ]
  return (
    <div className="h-full overflow-auto">
      <div className="border-b border-line px-3 py-2.5">
        <UrlHighlight url={row.url} />
      </div>
      {row.error && (
        <div className="border-b border-line bg-danger/[0.06] px-3 py-2 text-[12px] leading-snug text-danger">
          {row.error}
        </div>
      )}
      <KVTable rows={general} />
    </div>
  )
}

/* ───────────────────────── 响应区（下） ───────────────────────── */

type ResTab = 'headers' | 'body' | 'cookies' | 'raw'

function ResponsePane({ row }: { row: TrafficRow }) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<ResTab>('body')
  const headers = headerEntries(row.resHeaders)
  const cookies = parseCookies(getHeader(row.resHeaders, 'set-cookie'))
  const tone = statusTone(row)
  const raw = buildRawResponse(row)

  const tabs: SubTab[] = [
    { key: 'body', label: t('detail.res.tab.body') },
    { key: 'headers', label: t('detail.res.tab.headers'), count: headers.length },
    { key: 'cookies', label: 'Cookies', count: cookies.length },
    { key: 'raw', label: t('detail.res.tab.raw') },
  ]

  return (
    <div className="relative flex min-h-0 flex-1 flex-col" data-find-region="response" data-find-label={t('find.scopeResponse')}>
      <TabRow
        tabs={tabs}
        active={tab}
        onChange={(k) => setTab(k as ResTab)}
        right={
          <>
            <Pill tone="neutral">HTTP/1.1</Pill>
            <Pill tone={tone}>{statusLabel(row)}</Pill>
            <CopyIcon text={raw} title={t('detail.res.copyResponse')} />
            <ActionIcon
              title={
                canSaveBody(row) ? t('detail.res.saveBody') : row.resBody ? t('detail.res.saveBinaryUnsupported') : t('detail.res.saveNoBody')
              }
              disabled={!canSaveBody(row)}
              onClick={() => downloadResponseBody(row)}
            >
              <Download className="h-3.5 w-3.5" />
            </ActionIcon>
          </>
        }
      />
      <div className="min-h-0 flex-1">
        {tab === 'body' && <BodyViewer body={row.resBody} kind={row.contentKind} rowId={row.id} source="response" />}
        {tab === 'headers' && (
          <div className="h-full overflow-auto">
            <KVTable rows={headers} colLabels={[t('detail.common.nameCol'), t('detail.common.valueCol')]} emptyText={t('detail.res.headers.empty')} />
          </div>
        )}
        {tab === 'cookies' && (
          <div className="h-full overflow-auto">
            <KVTable rows={cookies} colLabels={['Cookie', t('detail.common.valueCol')]} emptyText={t('detail.res.cookies.empty')} />
          </div>
        )}
        {tab === 'raw' && <RawCode text={raw} highlight={false} />}
      </div>
    </div>
  )
}

function GroupLabel({ children }: { children: ReactNode }) {
  return (
    <div className="border-b border-line bg-inset/40 px-3 py-1.5 text-2xs font-semibold uppercase tracking-wide text-fg-muted">
      {children}
    </div>
  )
}

/* ───────────────────────── 容器：请求(上)/响应(下) 垂直分栏 ───────────────────────── */

export function DetailPanel({ row, onClose }: { row: TrafficRow; onClose: () => void }) {
  const { ref: containerRef, height } = useElementSize<HTMLDivElement>()
  // 「请求区」占比持久化于偏好（跨行/跨重启记忆，不随 key={row.id} 重挂载而丢失）。
  const frac = usePrefs((s) => s.detailTopFrac)
  const setPref = usePrefs((s) => s.set)

  // 由占比换算像素高度，并夹紧到合理区间（上下各保留最小可视高度）。
  const topH = height > 280 ? Math.min(height - 140, Math.max(120, Math.round(frac * height))) : Math.round(frac * 700)

  const startResize = useCallback(
    (e: ReactPointerEvent) => {
      e.preventDefault()
      const rect = containerRef.current?.getBoundingClientRect()
      if (!rect || rect.height <= 0) return
      const onMove = (ev: PointerEvent) => {
        const px = Math.min(rect.height - 140, Math.max(120, ev.clientY - rect.top))
        setPref({ detailTopFrac: px / rect.height })
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }
      document.body.style.cursor = 'row-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [containerRef, setPref],
  )

  return (
    <div ref={containerRef} className="flex h-full min-h-0 flex-col border-l border-line bg-base">
      <div className="flex min-h-0 flex-col bg-surface" style={{ height: topH }}>
        <RequestPane row={row} onClose={onClose} />
      </div>
      <div
        onPointerDown={startResize}
        className="group/vd flex h-[5px] shrink-0 cursor-row-resize items-center justify-center bg-line transition-colors hover:bg-accent"
      >
        <span className="h-[3px] w-8 rounded-full bg-fg-faint/40 group-hover/vd:bg-accent-fg/60" />
      </div>
      <div className="flex min-h-0 flex-1 flex-col bg-surface">
        <ResponsePane row={row} />
      </div>
    </div>
  )
}
