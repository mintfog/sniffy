import { type CSSProperties, type ReactNode, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Copy } from 'lucide-react'
import i18n from '@/i18n'
import { Bridge, type SessionBody } from '@/lib/bridge'
import type { ContentKind } from '../lib/types'
import { formatSize, prettyJson } from '../lib/format'
import { SegTabs } from '../ui/controls'
import { usePrefs, type BodyMode } from '../prefs'
import { JsonViewer } from './JsonViewer'

/* ───────────────────────── URL 语法高亮 ───────────────────────── */

export function UrlHighlight({ url }: { url: string }) {
  const parts = useMemo(() => {
    const m = /^([a-z]+:\/\/)([^/?#]*)([^?#]*)(\?[^#]*)?(#.*)?$/i.exec(url)
    if (!m) return null
    return { scheme: m[1], host: m[2], path: m[3] || '', query: m[4] || '', hash: m[5] || '' }
  }, [url])

  if (!parts) return <span className="break-all font-mono text-[12px] text-fg">{url}</span>

  const queryNodes: ReactNode[] = []
  if (parts.query) {
    queryNodes.push(<span key="q" className="text-fg-faint">?</span>)
    const pairs = parts.query.slice(1).split('&')
    pairs.forEach((p, i) => {
      const eq = p.indexOf('=')
      if (i > 0) queryNodes.push(<span key={`amp${i}`} className="text-fg-faint">&</span>)
      if (eq >= 0) {
        queryNodes.push(<span key={`k${i}`} className="text-fg-muted">{p.slice(0, eq)}</span>)
        queryNodes.push(<span key={`e${i}`} className="text-fg-faint">=</span>)
        queryNodes.push(<span key={`v${i}`} className="text-ok">{p.slice(eq + 1)}</span>)
      } else {
        queryNodes.push(<span key={`k${i}`} className="text-fg-muted">{p}</span>)
      }
    })
  }

  return (
    <span className="break-all font-mono text-[12px] leading-relaxed">
      <span className="text-fg-faint">{parts.scheme}</span>
      <span className="text-iris">{parts.host}</span>
      <span className="text-info">{parts.path}</span>
      {queryNodes}
      {parts.hash && <span className="text-fg-faint">{parts.hash}</span>}
    </span>
  )
}

/* ───────────────────────── JSON 行内语法高亮 ───────────────────────── */

const JSON_RE = /("(?:[^"\\]|\\.)*"\s*:)|("(?:[^"\\]|\\.)*")|(-?\d+\.?\d*(?:[eE][+-]?\d+)?)|(true|false)|(null)/g

function highlightJsonLine(line: string): ReactNode[] {
  const nodes: ReactNode[] = []
  let last = 0
  let m: RegExpExecArray | null
  JSON_RE.lastIndex = 0
  let idx = 0
  while ((m = JSON_RE.exec(line)) !== null) {
    if (m.index > last) nodes.push(<span key={`p${idx}`} className="text-fg-faint">{line.slice(last, m.index)}</span>)
    if (m[1]) nodes.push(<span key={`k${idx}`} className="text-iris">{m[1]}</span>)
    else if (m[2]) nodes.push(<span key={`s${idx}`} className="text-ok">{m[2]}</span>)
    else if (m[3]) nodes.push(<span key={`n${idx}`} className="text-method-put">{m[3]}</span>)
    else if (m[4]) nodes.push(<span key={`b${idx}`} className="text-method-patch">{m[4]}</span>)
    else if (m[5]) nodes.push(<span key={`u${idx}`} className="text-fg-faint">{m[5]}</span>)
    last = m.index + m[0].length
    idx++
  }
  if (last < line.length) nodes.push(<span key={`t${idx}`} className="text-fg-muted">{line.slice(last)}</span>)
  return nodes
}

/* ───────────────────────── 行号代码块 ───────────────────────── */

function CodeLines({ text, highlight }: { text: string; highlight?: boolean }) {
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  return (
    <div className="min-w-full font-mono text-[12px] leading-[1.55]">
      {lines.map((line, i) => (
        <div key={i} className="flex hover:bg-elevated/30">
          <span
            data-find-skip
            className="sticky left-0 w-10 shrink-0 select-none border-r border-line/60 bg-inset/40 px-2 text-right text-fg-faint tabular-nums"
          >
            {i + 1}
          </span>
          <span className="whitespace-pre-wrap break-all px-3 text-fg-muted">
            {highlight ? highlightJsonLine(line) : line || ' '}
          </span>
        </div>
      ))}
    </div>
  )
}

/** 原始文本（行号，可选 JSON 高亮）—— 用于 详情「原始」子页签 */
export function RawCode({ text, highlight }: { text: string; highlight?: boolean }) {
  const { t } = useTranslation()
  if (!text) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('body.empty')}</div>
  return (
    <div className="wb-scroll h-full overflow-auto">
      <CodeLines text={text} highlight={highlight} />
    </div>
  )
}

/* ───────────────────────── Hex 视图 ───────────────────────── */

function hexDumpBytes(bytes: Uint8Array): string {
  const rows: string[] = []
  for (let off = 0; off < bytes.length; off += 16) {
    const slice = bytes.slice(off, off + 16)
    const hex = Array.from(slice).map((b) => b.toString(16).padStart(2, '0')).join(' ')
    const ascii = Array.from(slice).map((b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : '.')).join('')
    rows.push(`${off.toString(16).padStart(8, '0')}  ${hex.padEnd(48, ' ')}  ${ascii}`)
  }
  return rows.join('\n') || i18n.t('body.hexEmpty')
}

function hexDump(text: string): string {
  return hexDumpBytes(new TextEncoder().encode(text))
}

/* ───────────────────────── 复制按钮 ───────────────────────── */

function CopyBtn({ text }: { text: string }) {
  const { t } = useTranslation()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      title={t('body.copy')}
      onClick={() => navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1100) })}
      className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg"
    >
      {done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  )
}

/* ───────────────────────── 图片预览 ───────────────────────── */

// 棋盘格背景:透明 PNG/SVG 在其上更易辨认真实形状。半透明灰在明暗主题下都可见。
const CHECKER = 'rgba(127,127,127,0.16)'
const checkerStyle: CSSProperties = {
  backgroundImage: `linear-gradient(45deg, ${CHECKER} 25%, transparent 25%), linear-gradient(-45deg, ${CHECKER} 25%, transparent 25%), linear-gradient(45deg, transparent 75%, ${CHECKER} 75%), linear-gradient(-45deg, transparent 75%, ${CHECKER} 75%)`,
  backgroundSize: '16px 16px',
  backgroundPosition: '0 0, 0 8px, 8px -8px, -8px 0',
}

type ImgStatus = 'loading' | 'ready' | 'empty' | 'toolarge' | 'error'

/**
 * 图片响应预览:DTO 里二进制体被 BodyPreview 丢成空串,故按需经 bridge 拉取原始字节
 * (base64),组装 Blob URL 交 <img> 渲染;另存原始字节供 Hex 视图。fit/actual 切换缩放。
 */
function ImageBodyViewer({ rowId, source }: { rowId: string; source: 'request' | 'response' }) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'preview' | 'hex'>('preview')
  const [status, setStatus] = useState<ImgStatus>('loading')
  const [info, setInfo] = useState<SessionBody | null>(null)
  const [url, setUrl] = useState('')
  const [bytes, setBytes] = useState<Uint8Array | null>(null)
  const [dims, setDims] = useState<{ w: number; h: number } | null>(null)
  const [zoom, setZoom] = useState<'fit' | 'actual'>('fit')

  useEffect(() => {
    let alive = true
    let objectUrl = ''
    setStatus('loading')
    setInfo(null)
    setBytes(null)
    setDims(null)
    setZoom('fit')
    Bridge.getSessionBody(rowId, source)
      .then((b) => {
        if (!alive) return
        if (!b || b.size === 0) {
          setStatus('empty')
          return
        }
        setInfo(b)
        if (b.tooLarge || !b.base64) {
          setStatus('toolarge')
          return
        }
        try {
          const raw = Uint8Array.from(atob(b.base64), (c) => c.charCodeAt(0))
          objectUrl = URL.createObjectURL(new Blob([raw], { type: b.mime || 'application/octet-stream' }))
          setBytes(raw)
          setUrl(objectUrl)
          setStatus('ready')
        } catch {
          setStatus('error')
        }
      })
      .catch(() => {
        if (alive) setStatus('error')
      })
    return () => {
      alive = false
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [rowId, source])

  const center = (node: ReactNode) => (
    <div className="flex h-full items-center justify-center px-3 py-6 text-center text-2xs text-fg-faint">{node}</div>
  )

  // 非就绪态占位文案;preview 与 hex 共用,使两种视图都如实反映加载/过大/失败/空状态。
  const placeholder =
    status === 'loading'
      ? t('body.imageLoading')
      : status === 'toolarge'
        ? t('body.imageTooLarge', { size: formatSize(info?.size ?? 0) })
        : status === 'error'
          ? t('body.imageError')
          : t('body.empty')

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b border-line/60 px-2 py-1.5">
        <SegTabs<'preview' | 'hex'>
          value={mode}
          onChange={setMode}
          options={[
            { key: 'preview', label: t('body.preview') },
            { key: 'hex', label: 'Hex' },
          ]}
        />
        {info && status === 'ready' && (
          <div className="ml-auto flex items-center gap-2 text-2xs text-fg-faint">
            <span className="font-mono">{info.mime}</span>
            {dims && <span className="tabular-nums">{dims.w}×{dims.h}</span>}
            <span className="tabular-nums">{formatSize(info.size)}</span>
            {mode === 'preview' && (
              <button
                type="button"
                onClick={() => setZoom((z) => (z === 'fit' ? 'actual' : 'fit'))}
                className="rounded-wb-sm px-1.5 py-0.5 text-fg-muted transition hover:bg-elevated hover:text-fg"
              >
                {zoom === 'fit' ? t('body.zoomActual') : t('body.zoomFit')}
              </button>
            )}
          </div>
        )}
      </div>
      <div className="wb-scroll min-h-0 flex-1 overflow-auto">
        {mode === 'hex' ? (
          bytes ? <CodeLines text={hexDumpBytes(bytes)} /> : center(placeholder)
        ) : status !== 'ready' ? (
          center(placeholder)
        ) : (
          // actual 时按自然尺寸渲染:用 w-max 让容器随图收缩,从左上角起滚动(居中会令溢出区无法滚到)。
          <div
            className={zoom === 'fit' ? 'flex min-h-full items-center justify-center p-4' : 'w-max min-w-full p-4'}
            style={checkerStyle}
          >
            <img
              src={url}
              alt=""
              onLoad={(e) => setDims({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
              onError={() => setStatus('error')}
              className={zoom === 'fit' ? 'max-h-full max-w-full object-contain' : 'max-w-none'}
            />
          </div>
        )}
      </div>
    </div>
  )
}

/* ───────────────────────── BodyViewer ───────────────────────── */

export function BodyViewer({
  body,
  kind,
  rowId,
  source = 'response',
}: {
  body?: string
  kind: ContentKind
  /** 二进制体(图片)需按需拉取原始字节:提供会话 id 才启用图片预览。 */
  rowId?: string
  source?: 'request' | 'response'
}) {
  const { t } = useTranslation()
  // 查看模式持久化于统一偏好（跨行/跨重启记忆）。非 JSON 时 Tree 不可用，
  // 则展示 Raw，但不覆盖用户偏好（下次遇到 JSON 仍回到 Tree）。
  // hook 须全部先于条件 return：同一实例的 kind 可能翻转为 image，提前 return 会改变 hook 数量。
  const stored = usePrefs((s) => s.bodyMode)
  const setPref = usePrefs((s) => s.set)
  if (kind === 'image' && rowId) return <ImageBodyViewer rowId={rowId} source={source} />
  const isJson = kind === 'json' || kind === 'form'
  const mode: BodyMode = !isJson && stored === 'tree' ? 'raw' : stored
  const setMode = (m: BodyMode) => setPref({ bodyMode: m })

  if (!body) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">{t('body.empty')}</div>

  const pretty = isJson ? prettyJson(body) : body

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b border-line/60 px-2 py-1.5">
        <SegTabs<BodyMode>
          value={mode}
          onChange={setMode}
          options={
            isJson
              ? [
                  { key: 'tree', label: 'Tree' },
                  { key: 'raw', label: 'Raw' },
                  { key: 'hex', label: 'Hex' },
                ]
              : [
                  { key: 'raw', label: 'Raw' },
                  { key: 'hex', label: 'Hex' },
                ]
          }
        />
        <div className="ml-auto flex items-center gap-0.5">
          <CopyBtn text={pretty} />
        </div>
      </div>
      <div className="wb-scroll min-h-0 flex-1 overflow-auto">
        {mode === 'tree' && <JsonViewer value={body} />}
        {mode === 'raw' && <CodeLines text={pretty} highlight={isJson} />}
        {mode === 'hex' && <CodeLines text={hexDump(body)} />}
      </div>
    </div>
  )
}
