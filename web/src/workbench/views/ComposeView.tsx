/**
 * 请求构造器：空白构造与「编辑后重发」共用的窗口。
 *
 * 页签而非单份草稿，是因为窗口可能被反复唤起：从流量表任选一条「编辑后重发」都会
 * 送来一份新蓝本，单份草稿只能覆盖掉手上正在改的东西。页签同时也是这类工具的实际
 * 用法——把同一个接口的几个变体并排放着来回试。
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
import { cx } from '../ui/primitives'
import { CurlImportDialog } from './compose/CurlImportDialog'
import { DraftTabs } from './compose/DraftTabs'
import { RequestPane } from './compose/RequestPane'
import { ResponseSide } from './compose/ResponseSide'
import { StatusStrip } from './compose/StatusStrip'
import { looksLikeCurl, parseCurl, type CurlWarning } from './compose/curl'
import { connOf, useOutboundSessions } from './compose/useOutbound'
import { resolveWire, toRequestSpec, toWSSpec } from './compose/wire'
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

/** 从未动过的空白草稿：新蓝本到来时直接顶替它，而不是在旁边再开一个空页签。 */
function isPristine(d: Draft): boolean {
  if (d.url.trim() || d.body || d.sentFlowId) return false
  if (d.method !== defaultMethod(d.kind) || d.viaPipeline) return false
  if (d.headers.some((h) => h.name !== '' || h.value !== '')) return false
  const g = d.graphql
  if (g && (g.query || g.variables || g.operationName)) return false
  if (d.ws?.outgoing || d.ws?.outgoingBinary) return false
  // importWarnings 是纯提示，算进来会让「空白草稿顶替」在导入之后永久失效。
  return true
}

function blankDraft(kind: DraftKind = 'http'): Draft {
  const d = newDraft(kind)
  d.headers = normalizeHeaders(d.headers)
  return d
}

/** 动了任一可编辑字段就说明用户已经在改了，上一次导入的提示随之过时。 */
const EDIT_KEYS: (keyof Draft)[] = ['method', 'url', 'headers', 'body', 'graphql', 'ws']

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** 表外方法（cURL 导入来的 PROPFIND 一类）必须补进选项，否则下拉空白且一动就丢值。 */
function methodOptions(d: Draft): { value: string; label: string }[] {
  const list: string[] = d.kind === 'sse' ? ['GET', 'POST'] : [...METHODS]
  if (!list.includes(d.method)) list.push(d.method)
  return list.map((m) => ({ value: m, label: m }))
}

/** 粘贴识别与显式导入共用的浮条：导入成功可撤销，识别失败只报一句。 */
type ComposeToast =
  | { kind: 'imported'; restore: { drafts: Draft[]; activeId: string } }
  | { kind: 'error'; message: string }

export function ComposeView() {
  const { t } = useTranslation()
  const [drafts, setDrafts] = useState<Draft[]>(() => [blankDraft()])
  const [activeId, setActiveId] = useState<string>(() => '')
  // 分栏占比用窗口本地 state：usePrefs 的那份是所有窗口共享的，拖这里不该动主窗布局。
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
  /** 本窗口开着的出站连接：连接活在后端，前端不主动断就一直挂着。 */
  const openWs = useRef<Set<string>>(new Set())
  /** 同上，但 SSE 更要紧：后端对它既没有读超时也没有总超时。 */
  const openStreams = useRef<Set<string>>(new Set())

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

  /**
   * 连接状态与发帧都要在异步回调里改 ws 分片，必须走这里而不是 patch：
   * patch 整片替换 ws，调用方闭包里的旧快照会把期间键入的待发消息盖掉。
   * p 给函数形态时还能读到合并那一刻的最新分片（见 WsPatch）。
   */
  const patchWs = useCallback((id: string, p: WsPatch) => {
    setDrafts((prev) => prev.map((d) => (d.id === id ? applyWsPatch(d, p) : d)))
  }, [])

  const openDraft = useCallback((d: Draft) => {
    // 顶替要求「唯一一张、同类型、从没动过」。类型也得对：从「+」菜单开的空 WS/SSE 页签
    // 被 http 蓝本顶掉的话，用户选的类型就这么没了，而且没有撤销入口。
    const takeOver = (prev: Draft[]) => prev.length === 1 && prev[0].kind === d.kind && isPristine(prev[0])
    setDrafts((prev) => (takeOver(prev) ? [d] : [...prev, d]))
    setActiveId(d.id)
  }, [])

  const closeDraft = useCallback((id: string) => {
    setDrafts((prev) => {
      const next = prev.filter((d) => d.id !== id)
      // 关掉最后一个页签留一份空白草稿：空窗口没有任何可操作的东西。
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
      // 队列里可能不止一条：窗口挂载之前连点了几下，那几次的事件都没人接（见 lib/windows.ts）。
      for (const req of pending) void takeSeed(req)
    } else {
      // 冷启动兜底：localStorage 不可用时仍能从窗口 URL 拿到蓝本。
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

  // 关掉页签或再发一次之后，旧会话就没人看了。构造器窗口可能开很久，而长连接的时间线
  // 是在前端逐帧累加起来的（见 data/delta），不清理就是实打实的泄漏。
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
        /* 对端可能先断了，此处无从补救 */
      })
    }
    for (const id of openStreams.current) {
      if (live.has(id)) continue
      openStreams.current.delete(id)
      void Bridge.stopStream(id).catch(() => {
        /* 流可能刚好自己结束了 */
      })
    }
    retain(live)
  }, [drafts, retain])

  // 非 Windows 上关窗即销毁 WebView，pagehide 是前端唯一还能跑的时机；
  // Go 侧的 WindowClosing 钩子是更可靠的第二道。
  useEffect(() => {
    const onHide = () => {
      for (const id of openWs.current) {
        void Bridge.closeWebSocket(id).catch(() => {
          /* 窗口正在销毁，结果没人看 */
        })
      }
      openWs.current.clear()
      for (const id of openStreams.current) {
        void Bridge.stopStream(id).catch(() => {
          /* 同上 */
        })
      }
      openStreams.current.clear()
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
          /* 对端可能先断了，回填会说明真相 */
        }
        openWs.current.delete(id)
        patchWs(d.id, { conn: 'closed' })
        track(id, 'ws')
        return
      }
      if (!url) return
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

    if (d.kind === 'sse' && d.sentFlowId && sessionsRef.current.stream[d.sentFlowId]?.status === 'open') {
      const id = d.sentFlowId
      try {
        await Bridge.stopStream(id)
      } catch {
        /* 流可能刚好自己结束了 */
      }
      openStreams.current.delete(id)
      track(id, 'sse')
      return
    }

    if (!url || d.sending) return
    const session = d.sentFlowId ? sessionsRef.current.http[d.sentFlowId] : undefined
    // SSE 的 flow 在流关闭前一直是 pending，这条重入保护对它无意义。
    if (d.kind !== 'sse' && d.sentFlowId && (session?.status ?? 'pending') === 'pending') return
    if (d.kind === 'graphql' && gqlOf(d).query.trim() === '') return
    if (resolveWire(d).varsError) return

    patch(d.id, { sending: true, sendError: undefined })
    try {
      const flowId = await Bridge.sendRequest(toRequestSpec(d))
      if (d.kind === 'sse') openStreams.current.add(flowId)
      track(flowId, d.kind)
      // 只有一次性往返才留上一次的 flow 做对照；流没有「上一次的响应」可比。
      patch(d.id, { sentFlowId: flowId, prevFlowId: d.kind === 'sse' ? undefined : d.sentFlowId, sending: false })
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
    // 识别错了也不能把粘贴吞掉：原文按普通文本落到光标处。
    const el = e.currentTarget
    const start = el.selectionStart ?? active.url.length
    const end = el.selectionEnd ?? start
    patch(active.id, { url: active.url.slice(0, start) + text + active.url.slice(end) })
    setToast({ kind: 'error', message: t(`compose.curl.err.${result.error.code}`, result.error.params) })
  }

  /* ── 派生状态 ── */

  const diff = useMemo(() => draftDiff(active), [active])
  const varsError = useMemo(() => resolveWire(active).varsError, [active])
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
      : active.kind === 'sse'
        ? active.sending || (!streamSession && httpPending)
        : active.sending || httpPending

  const currentRow = useMemo(() => (httpSession?.response ? toRowFromHttp(httpSession, 1) : undefined), [httpSession])
  const prevRow = useMemo(() => (prevSession?.response ? toRowFromHttp(prevSession, 1) : undefined), [prevSession])
  // 新的一次还在路上时留着上一次的响应：换成空白会让「改一处、比一次」的来回失去参照。
  const paneRow = currentRow ?? (waiting ? prevRow : undefined)

  // 被阻断 / 转发出错的 flow 没有响应可看，错误原因才是用户要的信息。
  // 失败优先于上一次的响应：把陈旧的 200 留在那儿会让人以为这次也成功了。
  const flowFailed = !waiting && !!httpSession && !httpSession.response && !streamSession
  // 超上限 / 超时 / 读到一半断了，后端会留下部分内容并把原因写进 Flow.Error（见 internal/app/resend.go）。
  // 这类失败不能按 flowFailed 处理：正文照常展示，但必须在它上方说清「这份是不完整的」——
  // 否则界面上只剩一个笼统的 ERR，用户无从判断少了多少。
  const partialFailure =
    !waiting && !flowFailed && httpSession?.status === 'error' ? (httpSession.error ?? '') : undefined
  const wsConnError = wsOf(active).connError
  const failure =
    active.kind === 'ws'
      ? wsConnError !== undefined
        ? t('compose.ws.connectFailed', { reason: wsConnError })
        : active.sendError
      : (active.sendError ?? (flowFailed && httpSession ? httpSession.error || '' : undefined))

  const gqlReady = active.kind !== 'graphql' || (!varsError && gqlOf(active).query.trim() !== '')
  const primary = mainButton(active.kind, { waiting, sseOpen, wsConn, hasUrl: !!active.url.trim(), gqlReady, t })

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
          icon={<Send className="h-3.5 w-3.5" />}
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
    t: (key: string) => string
  },
): { label: string; disabled: boolean } {
  switch (kind) {
    case 'ws':
      if (s.wsConn === 'open') return { label: s.t('compose.ws.disconnect'), disabled: false }
      if (s.wsConn === 'connecting') return { label: s.t('compose.ws.connecting'), disabled: true }
      return { label: s.t('compose.ws.connect'), disabled: !s.hasUrl }
    case 'sse':
      if (s.sseOpen) return { label: s.t('compose.sse.stop'), disabled: false }
      return { label: s.waiting ? s.t('compose.sending') : s.t('compose.sse.connect'), disabled: !s.hasUrl || s.waiting }
    case 'graphql':
      return { label: s.waiting ? s.t('compose.sending') : s.t('compose.send'), disabled: !s.hasUrl || s.waiting || !s.gqlReady }
    case 'http':
      return { label: s.waiting ? s.t('compose.sending') : s.t('compose.send'), disabled: !s.hasUrl || s.waiting }
  }
}

/* ───────────────────────── 左右分栏 ───────────────────────── */

function SplitPanes({ left, right }: { left: ReactNode; right: ReactNode }) {
  const { ref, width } = useElementSize<HTMLDivElement>()
  const [frac, setFrac] = useState(0.5)
  // 两侧各留最小可视宽度；容器尚未测量时按比例给个估算值，避免首帧塌成 0。
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
