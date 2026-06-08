import { type ReactNode, useMemo, useState } from 'react'
import { Check, Copy } from 'lucide-react'
import type { ContentKind } from '../lib/types'
import { prettyJson } from '../lib/format'
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
          <span className="sticky left-0 w-10 shrink-0 select-none border-r border-line/60 bg-inset/40 px-2 text-right text-fg-faint tabular-nums">
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
  if (!text) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">无内容</div>
  return (
    <div className="wb-scroll h-full overflow-auto">
      <CodeLines text={text} highlight={highlight} />
    </div>
  )
}

/* ───────────────────────── Hex 视图 ───────────────────────── */

function hexDump(text: string): string {
  const bytes = new TextEncoder().encode(text)
  const rows: string[] = []
  for (let off = 0; off < bytes.length; off += 16) {
    const slice = bytes.slice(off, off + 16)
    const hex = Array.from(slice).map((b) => b.toString(16).padStart(2, '0')).join(' ')
    const ascii = Array.from(slice).map((b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : '.')).join('')
    rows.push(`${off.toString(16).padStart(8, '0')}  ${hex.padEnd(48, ' ')}  ${ascii}`)
  }
  return rows.join('\n') || '（空）'
}

/* ───────────────────────── 复制按钮 ───────────────────────── */

function CopyBtn({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      title="复制"
      onClick={() => navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1100) })}
      className="flex h-6 w-6 items-center justify-center rounded-wb-sm text-fg-faint transition hover:bg-elevated hover:text-fg"
    >
      {done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  )
}

/* ───────────────────────── BodyViewer ───────────────────────── */

export function BodyViewer({ body, kind }: { body?: string; kind: ContentKind }) {
  const isJson = kind === 'json' || kind === 'form'
  // 查看模式持久化于统一偏好（跨行/跨重启记忆）。非 JSON 时 Tree 不可用，
  // 则展示 Raw，但不覆盖用户偏好（下次遇到 JSON 仍回到 Tree）。
  const stored = usePrefs((s) => s.bodyMode)
  const setPref = usePrefs((s) => s.set)
  const mode: BodyMode = !isJson && stored === 'tree' ? 'raw' : stored
  const setMode = (m: BodyMode) => setPref({ bodyMode: m })

  if (!body) return <div className="px-3 py-6 text-center text-2xs text-fg-faint">无内容</div>

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
        {/* 搜索按钮已移除：body 内查找尚未实现，保留死按钮会误导用户 */}
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
