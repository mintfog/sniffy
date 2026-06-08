import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  ArrowRightLeft,
  Braces,
  Check,
  Clock,
  Code2,
  Copy,
  Fingerprint,
  Hash,
  KeyRound,
  QrCode as QrIcon,
  RefreshCw,
} from 'lucide-react'
import { Events } from '@wailsio/runtime'
import { Button } from '../ui/controls'
import { cx } from '../ui/primitives'
import {
  base64Decode,
  base64Encode,
  describeJwtClaims,
  digest,
  genUuid,
  parseJwt,
  parseTimestamp,
  timestampNow,
  urlDecode,
  urlEncode,
  type TimestampInfo,
} from '../lib/tools'
import { encodeQrText } from '../lib/qrcode'
import { saveFile } from '../lib/download'

export type ToolId =
  | 'base64enc'
  | 'base64dec'
  | 'urlenc'
  | 'urldec'
  | 'jwt'
  | 'md5'
  | 'sha1'
  | 'sha256'
  | 'timestamp'
  | 'uuid'
  | 'qr'

interface ToolDef {
  id: ToolId
  label: string
  group: string
  icon: typeof Code2
}

const TOOLS: ToolDef[] = [
  { id: 'base64enc', label: 'Base64 编码', group: '编码', icon: Braces },
  { id: 'urlenc', label: 'URL 编码', group: '编码', icon: Braces },
  { id: 'base64dec', label: 'Base64 解码', group: '解码', icon: Code2 },
  { id: 'urldec', label: 'URL 解码', group: '解码', icon: Code2 },
  { id: 'jwt', label: 'JWT 解析', group: '解码', icon: KeyRound },
  { id: 'md5', label: 'MD5', group: '消息摘要', icon: Fingerprint },
  { id: 'sha1', label: 'SHA-1', group: '消息摘要', icon: Fingerprint },
  { id: 'sha256', label: 'SHA-256', group: '消息摘要', icon: Fingerprint },
  { id: 'timestamp', label: '时间戳', group: '生成', icon: Clock },
  { id: 'uuid', label: 'UUID', group: '生成', icon: Hash },
  { id: 'qr', label: '二维码', group: '生成', icon: QrIcon },
]

const STORAGE_KEY = 'sniffy-toolbox-tool'

function isToolId(v: string | null): v is ToolId {
  return !!v && TOOLS.some((t) => t.id === v)
}

/** 工具箱根组件（既可独立窗口承载，也可内嵌）。 */
export function ToolboxView() {
  const [active, setActive] = useState<ToolId>(() => {
    const fromUrl = new URLSearchParams(window.location.search).get('tool')
    if (isToolId(fromUrl)) return fromUrl
    const stored = (() => {
      try {
        return window.localStorage.getItem(STORAGE_KEY)
      } catch {
        return null
      }
    })()
    return isToolId(stored) ? stored : 'base64enc'
  })

  // 已打开的工具箱窗口收到菜单的「选择工具」事件后切换
  useEffect(() => {
    let off = () => {}
    try {
      off = Events.On('toolbox_select', (e: { data?: string }) => {
        if (isToolId(e?.data ?? null)) setActive(e!.data as ToolId)
      })
    } catch {
      /* ignore */
    }
    return () => {
      try {
        off()
      } catch {
        /* ignore */
      }
    }
  }, [])

  const groups = useMemo(() => {
    const order = ['编码', '解码', '消息摘要', '生成']
    return order.map((g) => ({ group: g, items: TOOLS.filter((t) => t.group === g) }))
  }, [])

  return (
    <div className="flex h-full min-h-0 bg-base">
      {/* 工具列表 */}
      <aside className="wb-scroll flex w-44 shrink-0 flex-col gap-3 overflow-auto border-r border-line bg-surface p-2">
        {groups.map(({ group, items }) => (
          <div key={group}>
            <div className="px-2 pb-1 text-[10px] font-semibold uppercase tracking-wide text-fg-faint">{group}</div>
            <div className="flex flex-col gap-0.5">
              {items.map((t) => {
                const Icon = t.icon
                const isActive = t.id === active
                return (
                  <button
                    key={t.id}
                    type="button"
                    onClick={() => setActive(t.id)}
                    className={cx(
                      'flex items-center gap-2 rounded-wb px-2 py-1.5 text-left text-[12.5px] transition-colors',
                      isActive ? 'bg-accent/15 text-accent' : 'text-fg-muted hover:bg-elevated hover:text-fg',
                    )}
                  >
                    <Icon className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{t.label}</span>
                  </button>
                )
              })}
            </div>
          </div>
        ))}
      </aside>

      {/* 工具面板 */}
      <main className="wb-scroll min-w-0 flex-1 overflow-auto p-4">
        <ToolPanel id={active} />
      </main>
    </div>
  )
}

function ToolPanel({ id }: { id: ToolId }) {
  switch (id) {
    case 'base64enc':
      return <TransformTool key={id} title="Base64 编码" run={(s) => base64Encode(s)} placeholder="输入要编码的文本…" />
    case 'base64dec':
      return <TransformTool key={id} title="Base64 解码" run={(s) => base64Decode(s)} placeholder="粘贴 Base64 字符串…" mono />
    case 'urlenc':
      return <TransformTool key={id} title="URL 编码" run={(s) => urlEncode(s)} placeholder="输入要编码的文本…" />
    case 'urldec':
      return <TransformTool key={id} title="URL 解码" run={(s) => urlDecode(s)} placeholder="粘贴 URL 编码字符串…" mono />
    case 'jwt':
      return <JwtTool key={id} />
    case 'md5':
      return <TransformTool key={id} title="MD5 摘要" run={(s) => digest('MD5', s)} placeholder="输入要计算摘要的文本…" monoOut />
    case 'sha1':
      return <TransformTool key={id} title="SHA-1 摘要" run={(s) => digest('SHA-1', s)} placeholder="输入要计算摘要的文本…" monoOut />
    case 'sha256':
      return <TransformTool key={id} title="SHA-256 摘要" run={(s) => digest('SHA-256', s)} placeholder="输入要计算摘要的文本…" monoOut />
    case 'timestamp':
      return <TimestampTool key={id} />
    case 'uuid':
      return <UuidTool key={id} />
    case 'qr':
      return <QrTool key={id} />
    default:
      return null
  }
}

/* ───────────────────────── 复制按钮 ───────────────────────── */

function CopyButton({ text, label = '复制' }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      size="sm"
      disabled={!text}
      onClick={() => {
        if (!text) return
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true)
          setTimeout(() => setDone(false), 1100)
        })
      }}
      icon={done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />}
    >
      {done ? '已复制' : label}
    </Button>
  )
}

function PanelHead({ title, icon: Icon, right }: { title: string; icon: typeof Code2; right?: ReactNode }) {
  return (
    <div className="mb-3 flex items-center gap-2">
      <Icon className="h-4 w-4 text-accent" />
      <h2 className="text-[13px] font-semibold text-fg">{title}</h2>
      <div className="ml-auto flex items-center gap-1.5">{right}</div>
    </div>
  )
}

const ioCls =
  'wb-scroll w-full resize-none rounded-wb border border-line bg-inset px-2.5 py-2 text-[12.5px] text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface'

/* ───────────────────────── 通用 转换工具 ───────────────────────── */

function TransformTool({
  title,
  run,
  placeholder,
  mono,
  monoOut,
}: {
  title: string
  run: (input: string) => string | Promise<string>
  placeholder?: string
  /** 输入用等宽字体 */
  mono?: boolean
  /** 输出用等宽字体（摘要类） */
  monoOut?: boolean
}) {
  const [input, setInput] = useState('')
  const [output, setOutput] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    let alive = true
    if (!input) {
      setOutput('')
      setError('')
      return
    }
    Promise.resolve()
      .then(() => run(input))
      .then((out) => {
        if (!alive) return
        setOutput(out)
        setError('')
      })
      .catch((e) => {
        if (!alive) return
        setOutput('')
        setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      alive = false
    }
  }, [input, run])

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead title={title} icon={ArrowRightLeft} right={<CopyButton text={output} />} />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">输入</label>
      <textarea
        spellCheck={false}
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder={placeholder}
        rows={6}
        className={cx(ioCls, mono && 'font-mono')}
      />
      <label className="mb-1 mt-3 block text-2xs font-medium text-fg-muted">输出</label>
      <textarea
        readOnly
        value={error ? '' : output}
        rows={6}
        placeholder="结果将在此显示"
        className={cx(ioCls, (mono || monoOut) && 'font-mono', 'cursor-text')}
      />
      {error && <div className="mt-2 rounded-wb bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{error}</div>}
    </div>
  )
}

/* ───────────────────────── JWT ───────────────────────── */

function JwtTool() {
  const [input, setInput] = useState('')
  const parsed = useMemo(() => {
    if (!input.trim()) return null
    try {
      const r = parseJwt(input)
      return { ok: true as const, ...r, notes: describeJwtClaims(r.payload) }
    } catch (e) {
      return { ok: false as const, error: e instanceof Error ? e.message : String(e) }
    }
  }, [input])

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead title="JWT 解析" icon={KeyRound} />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">Token</label>
      <textarea
        spellCheck={false}
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="粘贴 JWT（header.payload.signature）…"
        rows={5}
        className={cx(ioCls, 'font-mono')}
      />
      {parsed && !parsed.ok && (
        <div className="mt-2 rounded-wb bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{parsed.error}</div>
      )}
      {parsed && parsed.ok && (
        <div className="mt-3 flex flex-col gap-3">
          <JsonBlock title="Header" value={parsed.header} />
          <JsonBlock title="Payload" value={parsed.payload} />
          {parsed.notes.length > 0 && (
            <div className="rounded-wb border border-line bg-surface px-3 py-2 text-2xs leading-relaxed text-fg-muted">
              {parsed.notes.map((n, i) => (
                <div key={i}>· {n}</div>
              ))}
            </div>
          )}
          <div>
            <div className="mb-1 text-2xs font-medium text-fg-muted">Signature</div>
            <div className="break-all rounded-wb border border-line bg-inset px-2.5 py-2 font-mono text-2xs text-fg-faint">
              {parsed.signature || '（无）'}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function JsonBlock({ title, value }: { title: string; value: unknown }) {
  const text = useMemo(() => JSON.stringify(value, null, 2), [value])
  return (
    <div>
      <div className="mb-1 flex items-center gap-2">
        <span className="text-2xs font-medium text-fg-muted">{title}</span>
        <span className="ml-auto">
          <CopyButton text={text} />
        </span>
      </div>
      <pre className="wb-scroll max-h-72 overflow-auto rounded-wb border border-line bg-inset px-2.5 py-2 font-mono text-[12px] leading-relaxed text-fg">
        {text}
      </pre>
    </div>
  )
}

/* ───────────────────────── 时间戳 ───────────────────────── */

function TimestampTool() {
  const [input, setInput] = useState('')
  const [info, setInfo] = useState<TimestampInfo>(() => timestampNow())
  const [error, setError] = useState('')

  const refreshNow = useCallback(() => {
    setInput('')
    setError('')
    setInfo(timestampNow())
  }, [])

  useEffect(() => {
    if (!input.trim()) return
    try {
      setInfo(parseTimestamp(input))
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [input])

  const rows: [string, string][] = [
    ['Unix（秒）', String(info.unix)],
    ['Unix（毫秒）', String(info.unixMs)],
    ['ISO 8601', info.iso],
    ['本地时间', info.local],
    ['UTC', info.utc],
  ]

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead
        title="时间戳"
        icon={Clock}
        right={
          <Button size="sm" icon={<RefreshCw className="h-3.5 w-3.5" />} onClick={refreshNow}>
            当前时间
          </Button>
        }
      />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">解析（Unix 秒/毫秒 或 日期字符串）</label>
      <input
        spellCheck={false}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="如 1700000000 / 2026-06-08T10:00:00Z"
        className={cx(ioCls, 'font-mono')}
      />
      {error && <div className="mt-2 rounded-wb bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{error}</div>}
      <div className="mt-3 divide-y divide-line overflow-hidden rounded-wb border border-line">
        {rows.map(([k, v]) => (
          <button
            key={k}
            type="button"
            onClick={() => void navigator.clipboard?.writeText(v)}
            title="点击复制"
            className="grid w-full grid-cols-[140px_1fr] items-center text-left transition-colors hover:bg-elevated/50"
          >
            <span className="border-r border-line bg-inset/50 px-3 py-2 text-2xs text-fg-muted">{k}</span>
            <span className="break-all px-3 py-2 font-mono text-[12px] text-fg">{v}</span>
          </button>
        ))}
      </div>
    </div>
  )
}

/* ───────────────────────── UUID ───────────────────────── */

function UuidTool() {
  const [list, setList] = useState<string[]>(() => [genUuid()])
  const gen = (n: number) => setList(Array.from({ length: n }, () => genUuid()))

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead
        title="UUID 生成"
        icon={Hash}
        right={
          <>
            <Button size="sm" onClick={() => gen(1)}>
              生成 1 个
            </Button>
            <Button size="sm" onClick={() => gen(10)}>
              生成 10 个
            </Button>
            <CopyButton text={list.join('\n')} label="全部复制" />
          </>
        }
      />
      <div className="flex flex-col gap-1">
        {list.map((u, i) => (
          <button
            key={`${u}-${i}`}
            type="button"
            onClick={() => void navigator.clipboard?.writeText(u)}
            title="点击复制"
            className="rounded-wb border border-line bg-inset px-2.5 py-1.5 text-left font-mono text-[12.5px] text-fg transition-colors hover:bg-elevated"
          >
            {u}
          </button>
        ))}
      </div>
    </div>
  )
}

/* ───────────────────────── 二维码 ───────────────────────── */

function QrTool() {
  const [input, setInput] = useState('https://github.com/mintfog/sniffy')

  const result = useMemo(() => {
    if (!input.trim()) return null
    try {
      return { ok: true as const, matrix: encodeQrText(input, 'M') }
    } catch (e) {
      return { ok: false as const, error: e instanceof Error ? e.message : String(e) }
    }
  }, [input])

  const svgString = useMemo(() => {
    if (!result?.ok) return ''
    const m = result.matrix
    const n = m.length
    const border = 4 // ISO/IEC 18004 要求至少 4 模块静默区
    const dim = n + border * 2
    let path = ''
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        if (m[y][x]) path += `M${x + border},${y + border}h1v1h-1z`
      }
    }
    return `<svg xmlns="http://www.w3.org/2000/svg" width="100%" height="100%" viewBox="0 0 ${dim} ${dim}" shape-rendering="crispEdges" style="display:block"><rect width="${dim}" height="${dim}" fill="#ffffff"/><path d="${path}" fill="#000000"/></svg>`
  }, [result])

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead
        title="二维码生成"
        icon={QrIcon}
        right={
          svgString ? (
            <Button size="sm" onClick={() => void saveFile(svgString, 'sniffy-qrcode.svg')}>
              下载 SVG
            </Button>
          ) : undefined
        }
      />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">内容（文本 / URL）</label>
      <textarea
        spellCheck={false}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="输入要编码为二维码的内容…"
        rows={3}
        className={cx(ioCls, 'font-mono')}
      />
      {result && !result.ok && (
        <div className="mt-2 rounded-wb bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{result.error}</div>
      )}
      {svgString && (
        <div className="mt-4 flex flex-col items-center gap-2">
          <div
            className="rounded-wb border border-line bg-white p-3"
            style={{ width: 264, height: 264 }}
            // eslint-disable-next-line react/no-danger
            dangerouslySetInnerHTML={{ __html: svgString }}
          />
          <span className="text-2xs text-fg-faint">纠错等级 M · {result?.ok ? result.matrix.length : 0}×{result?.ok ? result.matrix.length : 0}</span>
        </div>
      )}
    </div>
  )
}
