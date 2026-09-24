/**
 * 请求构造器：空白构造与「编辑后重发」共用的窗口。
 *
 * 窗口支持多个页签，每个页签保存一份草稿与对应的发送状态，便于并行比较同一接口的多个变体。
 *
 * 这里只做装配：草稿状态、蓝本认领、发送分派、把四块子视图拼起来。
 */
import { useCallback, useEffect, useMemo, useRef, useState, type ClipboardEvent as ReactClipboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Events } from '@wailsio/runtime'
import { Send } from 'lucide-react'
import { Bridge } from '@/lib/bridge'
import { dropComposeSeed, takeComposeSeed, type ComposeSeedRequest } from '../lib/windows'
import { methodText, toRowFromHttp } from '../lib/format'
import { useElementSize } from '../lib/useElementSize'
import { Button, Select } from '../ui/controls'
import { cx, LoadingSpinner } from '../ui/primitives'
import { CurlImportDialog } from './compose/CurlImportDialog'
import { DraftTabs } from './compose/DraftTabs'
import { RequestPane } from './compose/RequestPane'
import { ResponseSide } from './compose/ResponseSide'
import { StatusStrip } from './compose/StatusStrip'
import { looksLikeCurl, parseCurl, type CurlWarning } from './compose/curl'
import { connOf, useOutboundSessions } from './compose/useOutbound'
import { headerBytesError, resolveWire, toRequestSpec, toWSSpec } from './compose/wire'
import {
  METHODS,
  applyWsPatch,
  defaultMethod,
  draftDiff,
  draftFromSeed,
  gqlOf,
  newDraft,
  normalizeHeaders,
  wsOf,
  type Draft,
  type DraftKind,
  type WsPatch,
} from './compose/model'

/** 判断草稿是否仍为空白且未开始发送，用于接收新的蓝本。 */
function isPristine(d: Draft): boolean {
  if (d.url.trim() || d.body || d.sentFlowId) return false
  if (d.method !== defaultMethod(d.kind) || d.viaPipeline) return false
  if (d.headers.some((h) => h.name !== '' || h.value !== '')) return false
  const g = d.graphql
  if (g && (g.query || g.variables || g.operationName)) return false
  if (d.ws?.outgoing || d.ws?.outgoingBinary) return false
  // importWarnings 仅为提示字段，不参与空白草稿判定。
  return true
}

function blankDraft(kind: DraftKind = 'http'): Draft {
  const d = newDraft(kind)
  d.headers = normalizeHeaders(d.headers)
  return d
}

/** 可编辑字段变化时清理上一次导入提示。 */
const EDIT_KEYS: (keyof Draft)[] = ['method', 'url', 'headers', 'body', 'graphql', 'ws']

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** 将当前方法加入选项列表，支持 cURL 导入的扩展方法。 */
function methodOptions(d: Draft): { value: string; label: string }[] {
  const list: string[] = d.kind === 'sse' ? ['GET', 'POST'] : [...METHODS]
  if (!list.includes(d.method)) list.push(d.method)
  return list.map((m) => ({ value: m, label: m }))
}

/** 粘贴识别与显式导入共用的提示条，记录导入结果与撤销数据。 */
type ComposeToast =
  | { kind: 'imported'; restore: { drafts: Draft[]; activeId: string } }
  | { kind: 'error'; message: string }

export function ComposeView() {
  const { t } = useTranslation()
  const [drafts, setDrafts] = useState<Draft[]>(() => [blankDraft()])
  const [activeId, setActiveId] = useState<string>(() => '')
  // 分栏占比使用窗口本地 state，各构造器窗口独立维护布局。
  const [topFrac, setTopFrac] = useState(0.5)
  const [curlOpen, setCurlOpen] = useState(false)
  const [toast, setToast] = useState<ComposeToast | null>(null)
  const sessions = useOutboundSessions()

  const active = drafts.find((d) => d.id === activeId) ?? drafts[0]
  const activeRef = useRef(active)
  activeRef.current = active
  const draftsRef = useRef(drafts)
  draftsRef.current = drafts
  const sessionsRef = useRef(sessions)
  sessionsRef.current = sessions
  /** 本窗口打开的出站 WebSocket 连接，连接生命周期由后端维护。 */
  const openWs = useRef<Set<string>>(new Set())
  /** 页签关闭时取消其 HTTP 请求，涵盖等待响应头和持续接收 SSE 的阶段。 */
  const outboundRequests = useRef<Set<string>>(new Set())

  /* ── 草稿增删改 ── */

  const patch = useCallback((id: string, p: Partial<Draft>) => {
    setDrafts((prev) =>
      prev.map((d) => {
        if (d.id !== id) return d
        const next = { ...d, ...p }
        if (d.importWarnings && !('importWarnings' in p) && EDIT_KEYS.some((k) => k in p)) {
          next.importWarnings = undefined
        }
        return next
      }),
    )
  }, [])

  /** 在异步回调中按草稿当前值更新 ws 分片，函数形态补丁可读取合并时的最新状态。 */
  const patchWs = useCallback((id: string, p: WsPatch) => {
    setDrafts((prev) => prev.map((d) => (d.id === id ? applyWsPatch(d, p) : d)))
  }, [])

  const openDraft = useCallback((d: Draft) => {
    // 只有新开窗口留下的那张空白草稿会被蓝本接管：已经分出多个页签说明用户在并行对比，
    // 而页签类型本身也是一次选择，换类型的蓝本另开页签而非顶掉它。
    const takeOver = (prev: Draft[]) => prev.length === 1 && prev[0].kind === d.kind && isPristine(prev[0])
    setDrafts((prev) => (takeOver(prev) ? [d] : [...prev, d]))
    setActiveId(d.id)
  }, [])

  const closeDraft = useCallback((id: string) => {
    setDrafts((prev) => {
      const next = prev.filter((d) => d.id !== id)
      // 关闭最后一个页签时创建一份空白草稿，保持窗口可继续操作。
      if (next.length === 0) {
        const fresh = blankDraft()
        setActiveId(fresh.id)
        return [fresh]
      }
      setActiveId((cur) => (cur === id ? next[Math.min(prev.findIndex((d) => d.id === id), next.length - 1)].id : cur))
      return next
    })
  }, [])

  /* ── 预填：新窗口读 localStorage，已开着的窗口靠事件（见 lib/windows.ts） ── */

  const consumed = useRef<Set<string>>(new Set())
  const takeSeed = useCallback(
    async (req: ComposeSeedRequest) => {
      if (!req?.token || consumed.current.has(req.token)) return
      consumed.current.add(req.token)
      dropComposeSeed(req.token)
      if (!req.flowId) {
        openDraft(blankDraft())
        return
      }
      const seed = await Bridge.composeSeed(req.flowId).catch(() => null)
      if (!seed) {
        openDraft(blankDraft())
        return
      }
      const draft = draftFromSeed(seed)
      draft.headers = normalizeHeaders(draft.headers)
      openDraft(draft)
    },
    [openDraft],
  )

  useEffect(() => {
    const pending = takeComposeSeed()
    if (pending.length > 0) {
      // 按队列顺序处理窗口挂载前积累的蓝本请求。
      for (const req of pending) void takeSeed(req)
    } else {
      // 冷启动时从窗口 URL 读取蓝本标识。
      const fromUrl = new URLSearchParams(window.location.search).get('from')
      if (fromUrl) void takeSeed({ token: `url:${fromUrl}`, flowId: fromUrl })
    }

    let off = () => {}
    try {
      off = Events.On('compose_seed', (e: { data?: unknown }) => void takeSeed(e?.data as ComposeSeedRequest))
    } catch {
      /* 非 Wails 环境 */
    }
    return () => {
      try {
        off()
      } catch {
        /* ignore */
      }
    }
  }, [takeSeed])

  /* ── 会话回收 ── */

  // 页签与会话集合保持同步；长连接时间线在前端累加，已脱离页签的会话及时释放。
  const { retain } = sessions
  useEffect(() => {
    const live = new Set<string>()
    for (const d of drafts) {
      if (d.sentFlowId) live.add(d.sentFlowId)
      if (d.prevFlowId) live.add(d.prevFlowId)
    }
    for (const id of openWs.current) {
      if (live.has(id)) continue
      openWs.current.delete(id)
      void Bridge.closeWebSocket(id).catch(() => {
        /* 连接关闭结果由后端会话状态统一收敛。 */
      })
    }
    for (const id of outboundRequests.current) {
      if (live.has(id)) continue
      outboundRequests.current.delete(id)
      void Bridge.stopStream(id).catch(() => {
        /* 流状态由后端会话统一收敛。 */
      })
    }
    retain(live)
  }, [drafts, retain])

  // pagehide 与 Go 侧 WindowClosing 共同收口窗口关闭时的长连接。
  useEffect(() => {
    const onHide = () => {
      for (const id of openWs.current) {
        void Bridge.closeWebSocket(id).catch(() => {
          /* 窗口关闭阶段由后端完成连接收口。 */
        })
      }
      openWs.current.clear()
      for (const id of outboundRequests.current) {
        void Bridge.stopStream(id).catch(() => {
          /* 窗口关闭阶段由后端完成流收口。 */
        })
      }
      outboundRequests.current.clear()
    }
    window.addEventListener('pagehide', onHide)
    return () => window.removeEventListener('pagehide', onHide)
  }, [])

  /* ── 发送：内部按类型分派，对外仍是一个 callback，好让 Ctrl/⌘+Enter 只认一个 ref ── */

  const send = useCallback(async () => {
    const d = activeRef.current
    if (!d) return
    const url = d.url.trim()
    const { track } = sessionsRef.current

    if (d.kind === 'ws') {
      const conn = connOf(d, d.sentFlowId ? sessionsRef.current.ws[d.sentFlowId] : undefined)
      if (conn === 'connecting') return
      if (conn === 'open') {
        const id = d.sentFlowId
        if (!id) return
        try {
          await Bridge.closeWebSocket(id)
        } catch {
          /* 连接状态由后续会话事件回填。 */
        }
        openWs.current.delete(id)
        patchWs(d.id, { conn: 'closed' })
        track(id, 'ws')
        return
      }
      if (!url || headerBytesError(d)) return
      patch(d.id, { sending: true, sendError: undefined })
      patchWs(d.id, { conn: 'connecting', connError: undefined })
      try {
        const id = await Bridge.openWebSocket(toWSSpec(d))
        openWs.current.add(id)
        track(id, 'ws')
        patch(d.id, { sentFlowId: id, prevFlowId: undefined, sending: false })
        patchWs(d.id, { conn: 'open' })
      } catch (err) {
        patch(d.id, { sending: false })
        patchWs(d.id, { conn: 'idle', connError: errText(err) })
      }
      return
    }

    const stream = d.sentFlowId ? sessionsRef.current.stream[d.sentFlowId] : undefined
    if (d.sentFlowId && stream?.status === 'open') {
      const id = d.sentFlowId
      try {
        await Bridge.stopStream(id)
      } catch {
        /* 流状态由后端会话事件回填。 */
      }
      outboundRequests.current.delete(id)
      track(id, d.kind)
      return
    }

    if (!url || d.sending) return
    const session = d.sentFlowId ? sessionsRef.current.http[d.sentFlowId] : undefined
    if (!stream && d.sentFlowId && (session?.status ?? 'pending') === 'pending') return
    if (d.kind === 'graphql' && gqlOf(d).query.trim() === '') return
    if (resolveWire(d).varsError) return
    // 头部字节转义必须有效，才能生成确定的出站字节。
    if (headerBytesError(d)) return

    patch(d.id, { sending: true, sendError: undefined })
    try {
      const flowId = await Bridge.sendRequest(toRequestSpec(d))
      // 每个 HTTP 请求都可能返回 SSE，响应头到达前也要能随页签关闭而取消。
      outboundRequests.current.add(flowId)
      track(flowId, d.kind)
      // 一次性往返保留上一次 flow 作为对照，SSE 连接仅记录当前 flow。
      patch(d.id, { sentFlowId: flowId, prevFlowId: d.kind === 'sse' || stream ? undefined : d.sentFlowId, sending: false })
    } catch (err) {
      patch(d.id, { sending: false, sendError: errText(err) })
    }
  }, [patch, patchWs])

  const sendRef = useRef(send)
  sendRef.current = send
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
        e.preventDefault()
        void sendRef.current()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  /* ── cURL 导入 ── */

  const importCurl = useCallback(
    (imported: Draft, warnings: CurlWarning[]) => {
      const restore = { drafts: draftsRef.current, activeId: activeRef.current.id }
      openDraft(warnings.length > 0 ? { ...imported, importWarnings: warnings } : imported)
      setCurlOpen(false)
      setToast({ kind: 'imported', restore })
    },
    [openDraft],
  )

  useEffect(() => {
    if (!toast) return
    const timer = window.setTimeout(() => setToast(null), 3000)
    return () => window.clearTimeout(timer)
  }, [toast])

  const onUrlPaste = (e: ReactClipboardEvent<HTMLInputElement>) => {
    const text = e.clipboardData.getData('text')
    if (!looksLikeCurl(text)) return
    e.preventDefault()
    const result = parseCurl(text)
    if (result.ok) {
      importCurl(result.draft, result.warnings)
      return
    }
    // cURL 识别失败时将原文按普通文本插入光标位置。
    const el = e.currentTarget
    const start = el.selectionStart ?? active.url.length
    const end = el.selectionEnd ?? start
    patch(active.id, { url: active.url.slice(0, start) + text + active.url.slice(end) })
    setToast({ kind: 'error', message: t(`compose.curl.err.${result.error.code}`, result.error.params) })
  }

  /* ── 派生状态 ── */

  const diff = useMemo(() => draftDiff(active), [active])
  const varsError = useMemo(() => resolveWire(active).varsError, [active])
  const headerError = useMemo(() => headerBytesError(active), [active])
  const httpSession = active.sentFlowId ? sessions.http[active.sentFlowId] : undefined
  const prevSession = active.prevFlowId ? sessions.http[active.prevFlowId] : undefined
  const streamSession = active.sentFlowId ? sessions.stream[active.sentFlowId] : undefined
  const wsSession = active.sentFlowId ? sessions.ws[active.sentFlowId] : undefined
  const wsConn = connOf(active, wsSession)
  const sseOpen = streamSession?.status === 'open'

  const httpPending = !!active.sentFlowId && (httpSession?.status ?? 'pending') === 'pending'
  const waiting =
    active.kind === 'ws'
      ? wsConn === 'connecting'
      : active.sending || (!streamSession && httpPending)

  const currentRow = useMemo(() => (httpSession?.response ? toRowFromHttp(httpSession, 1) : undefined), [httpSession])
  const prevRow = useMemo(() => (prevSession?.response ? toRowFromHttp(prevSession, 1) : undefined), [prevSession])
  // 新请求进行期间继续显示上一次响应，作为编辑对照。
  const paneRow = currentRow ?? (waiting ? prevRow : undefined)

  // 被阻断或转发出错的 flow 显示错误原因，不展示上一轮响应。
  const flowFailed = !waiting && !!httpSession && !httpSession.response && !streamSession
  // 超限、超时或中途断流时，后端保留部分正文并写入 Flow.Error；界面同时展示正文与错误原因。
  const partialFailure =
    !waiting && !flowFailed && httpSession?.status === 'error' ? (httpSession.error ?? '') : undefined
  const wsConnError = wsOf(active).connError
  const failure = headerError
    ? t('bytes.badEscape', { name: headerError })
    : active.kind === 'ws'
      ? wsConnError !== undefined
        ? t('compose.ws.connectFailed', { reason: wsConnError })
        : active.sendError
      : (active.sendError ?? (flowFailed && httpSession ? httpSession.error || '' : undefined))

  const gqlReady = active.kind !== 'graphql' || (!varsError && gqlOf(active).query.trim() !== '')
  const primary = mainButton(active.kind, {
    waiting,
    sseOpen,
    wsConn,
    hasUrl: !!active.url.trim(),
    gqlReady,
    headersBroken: !!headerError,
    t,
  })

  return (
    <div className="relative flex h-full min-h-0 flex-col bg-base">
      <DraftTabs
        drafts={drafts}
        activeId={active.id}
        onSelect={setActiveId}
        onClose={closeDraft}
        onAdd={(kind) => openDraft(blankDraft(kind))}
        onImportCurl={() => setCurlOpen(true)}
      />

      <div className="flex h-11 shrink-0 items-center gap-2 border-b border-line bg-surface px-2">
        {active.kind === 'graphql' ? (
          <span className="flex h-8 w-[92px] shrink-0 items-center justify-center rounded-wb border border-line bg-inset font-mono text-[12.5px] font-semibold text-method-post">
            POST
          </span>
        ) : active.kind === 'ws' ? null : (
          <Select
            value={active.method}
            onChange={(e) => patch(active.id, { method: e.target.value })}
            options={methodOptions(active)}
            aria-label={t('compose.method')}
            className={cx('h-8 w-[92px] font-mono font-semibold', methodText(active.method))}
          />
        )}
        <div className="relative min-w-0 flex-1">
          {diff.url && <span aria-hidden className="absolute left-0 top-1 z-10 h-[calc(100%-8px)] w-[2px] rounded-full bg-accent" />}
          <input
            value={active.url}
            spellCheck={false}
            onChange={(e) => patch(active.id, { url: e.target.value })}
            onPaste={onUrlPaste}
            placeholder={active.kind === 'ws' ? t('compose.ws.urlPlaceholder') : t('compose.urlPlaceholder')}
            aria-label="URL"
            className="h-8 w-full rounded-wb border border-line bg-inset px-3 font-mono text-[12.5px] text-fg outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:border-accent focus:bg-surface"
          />
        </div>
        <Button
          variant="primary"
          onClick={() => void send()}
          disabled={primary.disabled}
          title={t('compose.sendTip')}
          icon={waiting ? <LoadingSpinner className="h-3.5 w-3.5" /> : <Send className="h-3.5 w-3.5" />}
          className="h-8 shrink-0 px-4"
        >
          {primary.label}
        </Button>
      </div>

      <SplitPanes
        left={<RequestPane draft={active} diff={diff} onPatch={(p) => patch(active.id, p)} />}
        right={
          <ResponseSide
            draft={active}
            http={paneRow}
            stream={streamSession}
            ws={wsSession}
            waiting={waiting}
            failure={failure}
            partial={partialFailure}
            topFrac={topFrac}
            onTopFracChange={setTopFrac}
            onPatchWs={(p) => patchWs(active.id, p)}
          />
        }
      />

      <StatusStrip
        draft={active}
        row={currentRow}
        ws={wsSession}
        stream={streamSession}
        waiting={waiting}
        failure={failure ?? partialFailure}
        onPatch={(p) => patch(active.id, p)}
      />

      {toast && (
        <div className="pointer-events-none absolute inset-x-0 bottom-10 z-40 flex justify-center px-4">
          <div
            className={cx(
              'pointer-events-auto flex max-w-full items-center gap-3 rounded-wb border px-3 py-1.5 text-2xs shadow-wb',
              toast.kind === 'error' ? 'border-danger/40 bg-danger/15 text-danger' : 'border-line bg-elevated text-fg',
            )}
          >
            <span className="min-w-0 truncate">{toast.kind === 'error' ? toast.message : t('compose.curl.detected')}</span>
            {toast.kind === 'imported' && (
              <button
                type="button"
                onClick={() => {
                  setDrafts(toast.restore.drafts)
                  setActiveId(toast.restore.activeId)
                  setToast(null)
                }}
                className="shrink-0 font-semibold text-accent outline-none hover:underline focus-visible:underline"
              >
                {t('compose.curl.undo')}
              </button>
            )}
          </div>
        </div>
      )}

      {curlOpen && <CurlImportDialog onClose={() => setCurlOpen(false)} onImport={importCurl} />}
    </div>
  )
}

/* ───────────────────────── 主按钮 ───────────────────────── */

/** 顶部主按钮按类型换语义：一次性往返是 Send，长连接是 Connect / Stop / Disconnect。 */
function mainButton(
  kind: DraftKind,
  s: {
    waiting: boolean
    sseOpen: boolean
    wsConn: ReturnType<typeof connOf>
    hasUrl: boolean
    /** GraphQL 专用：查询为空或变量 JSON 非法时发不出去。 */
    gqlReady: boolean
    /** 头部字节转义错误时禁用发送，WebSocket 断开操作仍可用。 */
    headersBroken: boolean
    t: (key: string) => string
  },
): { label: string; disabled: boolean } {
  if (s.sseOpen) return { label: s.t('compose.sse.stop'), disabled: false }
  switch (kind) {
    case 'ws':
      if (s.wsConn === 'open') return { label: s.t('compose.ws.disconnect'), disabled: false }
      if (s.wsConn === 'connecting') return { label: s.t('compose.ws.connecting'), disabled: true }
      return { label: s.t('compose.ws.connect'), disabled: !s.hasUrl || s.headersBroken }
    case 'sse':
      return {
        label: s.waiting ? s.t('compose.sending') : s.t('compose.sse.connect'),
        disabled: !s.hasUrl || s.waiting || s.headersBroken,
      }
    case 'graphql':
      return {
        label: s.waiting ? s.t('compose.sending') : s.t('compose.send'),
        disabled: !s.hasUrl || s.waiting || !s.gqlReady || s.headersBroken,
      }
    case 'http':
      return {
        label: s.waiting ? s.t('compose.sending') : s.t('compose.send'),
        disabled: !s.hasUrl || s.waiting || s.headersBroken,
      }
  }
}

/* ───────────────────────── 左右分栏 ───────────────────────── */

function SplitPanes({ left, right }: { left: ReactNode; right: ReactNode }) {
  const { ref, width } = useElementSize<HTMLDivElement>()
  const [frac, setFrac] = useState(0.5)
  // 两侧各保留最小可视宽度；容器尚未测量时按比例估算初始值。
  const leftW = width > 640 ? Math.min(width - 320, Math.max(320, Math.round(frac * width))) : Math.round(frac * 900)

  const startResize = useCallback(
    (e: ReactPointerEvent) => {
      e.preventDefault()
      const rect = ref.current?.getBoundingClientRect()
      if (!rect || rect.width <= 0) return
      const onMove = (ev: PointerEvent) => {
        const px = Math.min(rect.width - 320, Math.max(320, ev.clientX - rect.left))
        setFrac(px / rect.width)
      }
      const onUp = () => {
        window.removeEventListener('pointermove', onMove)
        window.removeEventListener('pointerup', onUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }
      document.body.style.cursor = 'col-resize'
      document.body.style.userSelect = 'none'
      window.addEventListener('pointermove', onMove)
      window.addEventListener('pointerup', onUp)
    },
    [ref],
  )

  return (
    <div ref={ref} className="flex min-h-0 flex-1 items-stretch">
      <div className="flex min-h-0 flex-col" style={{ width: leftW }}>
        {left}
      </div>
      <div
        onPointerDown={startResize}
        className="group/hd flex w-[5px] shrink-0 cursor-col-resize items-center justify-center bg-line transition-colors hover:bg-accent"
      >
        <span className="h-8 w-[3px] rounded-full bg-fg-faint/40 group-hover/hd:bg-accent-fg/60" />
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{right}</div>
    </div>
  )
}
