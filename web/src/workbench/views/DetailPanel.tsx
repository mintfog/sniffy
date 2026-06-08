import { type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useState } from 'react'
import { Check, Copy, Download, X } from 'lucide-react'
import { usePrefs } from '../prefs'
import { useElementSize } from '../lib/useElementSize'
import { saveFile } from '../lib/download'
import {
  buildRawRequest,
  buildRawResponse,
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
      className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg disabled:pointer-events-none disabled:opacity-40"
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
    <div className="flex h-8 shrink-0 items-stretch border-b border-line bg-surface">
      <div className="wb-scroll flex items-stretch overflow-x-auto">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => onChange(t.key)}
            className={cx(
              'relative whitespace-nowrap px-2.5 text-[12px] transition-colors outline-none',
              active === t.key ? 'text-fg' : 'text-fg-muted hover:text-fg',
            )}
          >
            {t.label}
            {t.count != null && t.count > 0 && <span className="ml-1 text-2xs tabular-nums text-fg-faint">{t.count}</span>}
            {active === t.key && <span className="absolute inset-x-1.5 bottom-0 h-[2px] rounded-t bg-accent" />}
          </button>
        ))}
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-1 pr-2 pl-2">{right}</div>
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
  const [tab, setTab] = useState<ReqTab>('overview')
  const query = parseQueryParams(row.url)
  const form = row.contentKind === 'form' ? parseFormParams(row.reqBody) : []
  const params = [...query, ...form]
  const headers = headerEntries(row.reqHeaders)
  const cookies = parseCookies(getHeader(row.reqHeaders, 'cookie'))

  const tabs: SubTab[] = [
    { key: 'overview', label: '总览' },
    { key: 'params', label: '参数', count: params.length },
    { key: 'headers', label: '请求头', count: headers.length },
    { key: 'body', label: '请求体' },
    { key: 'cookies', label: 'Cookies', count: cookies.length },
    { key: 'raw', label: '原始' },
  ]

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <TabRow
        tabs={tabs}
        active={tab}
        onChange={(k) => setTab(k as ReqTab)}
        right={
          <>
            <MethodPill method={row.method} />
            <CopyIcon text={row.url} title="复制 URL" />
            <CopyIcon text={rowToCurl(row)} title="复制为 cURL" />
            <ActionIcon title="关闭 (Esc)" onClick={onClose}>
              <X className="h-3.5 w-3.5" />
            </ActionIcon>
          </>
        }
      />
      <div className="min-h-0 flex-1">
        {tab === 'overview' && <RequestOverview row={row} />}
        {tab === 'params' && (
          <div className="wb-scroll h-full overflow-auto">
            <GroupLabel>查询参数</GroupLabel>
            <KVTable rows={query} colLabels={['参数', '值']} emptyText="无查询参数" />
            <GroupLabel>表单参数</GroupLabel>
            <KVTable rows={form} colLabels={['字段', '值']} emptyText="无表单参数" />
          </div>
        )}
        {tab === 'headers' && (
          <div className="wb-scroll h-full overflow-auto">
            <KVTable rows={headers} colLabels={['名称', '值']} emptyText="无请求头" />
          </div>
        )}
        {tab === 'body' && <BodyViewer body={row.reqBody} kind={row.contentKind} />}
        {tab === 'cookies' && (
          <div className="wb-scroll h-full overflow-auto">
            <KVTable rows={cookies} colLabels={['Cookie', '值']} emptyText="无 Cookie" />
          </div>
        )}
        {tab === 'raw' && <RawCode text={buildRawRequest(row)} />}
      </div>
    </div>
  )
}

function RequestOverview({ row }: { row: TrafficRow }) {
  const tone = statusTone(row)
  const general: [string, string][] = [
    ['状态', row.state === 'pending' ? '进行中' : row.state === 'error' ? '错误' : '完成'],
    ['方法', row.method],
    ['协议', row.scheme.toUpperCase()],
    ['状态码', `${statusLabel(row)} ${row.statusText ?? ''}`.trim()],
    ['客户端 IP', row.clientIP || '—'],
    ['Content-Type', row.contentType || '—'],
    ['进程', row.process || '—'],
    ['开始时间', formatClock(row.startedAt)],
    ['耗时', formatDuration(row.durationMs)],
    ['大小', formatSize(row.sizeBytes)],
  ]
  return (
    <div className="wb-scroll h-full overflow-auto">
      <div className="flex items-start gap-2 border-b border-line px-3 py-2.5">
        <MethodPill method={row.method} />
        <Pill tone={tone}>{statusLabel(row)}</Pill>
        <div className="min-w-0 flex-1">
          <UrlHighlight url={row.url} />
        </div>
      </div>
      <KVTable rows={general} />
    </div>
  )
}

/* ───────────────────────── 响应区（下） ───────────────────────── */

type ResTab = 'headers' | 'body' | 'cookies' | 'raw'

function ResponsePane({ row }: { row: TrafficRow }) {
  const [tab, setTab] = useState<ResTab>('body')
  const headers = headerEntries(row.resHeaders)
  const cookies = parseCookies(getHeader(row.resHeaders, 'set-cookie'))
  const tone = statusTone(row)
  const raw = buildRawResponse(row)

  const tabs: SubTab[] = [
    { key: 'body', label: '响应体' },
    { key: 'headers', label: '响应头', count: headers.length },
    { key: 'cookies', label: 'Cookies', count: cookies.length },
    { key: 'raw', label: '原始' },
  ]

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <TabRow
        tabs={tabs}
        active={tab}
        onChange={(k) => setTab(k as ResTab)}
        right={
          <>
            <Pill tone="neutral">HTTP/1.1</Pill>
            <Pill tone={tone}>{statusLabel(row)}</Pill>
            <CopyIcon text={raw} title="复制响应" />
            <ActionIcon
              title={
                canSaveBody(row) ? '保存响应体' : row.resBody ? '二进制内容暂不支持保存' : '无响应体可保存'
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
        {tab === 'body' && <BodyViewer body={row.resBody} kind={row.contentKind} />}
        {tab === 'headers' && (
          <div className="wb-scroll h-full overflow-auto">
            <KVTable rows={headers} colLabels={['名称', '值']} emptyText="无响应头" />
          </div>
        )}
        {tab === 'cookies' && (
          <div className="wb-scroll h-full overflow-auto">
            <KVTable rows={cookies} colLabels={['Cookie', '值']} emptyText="无 Set-Cookie" />
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
