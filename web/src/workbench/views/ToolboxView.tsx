import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
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

type ToolGroup = 'encode' | 'decode' | 'digest' | 'generate'

interface ToolDef {
  id: ToolId
  /** i18n key 后缀，渲染时经 t('toolbox.tool.' + labelKey) 取值 */
  labelKey: string
  group: ToolGroup
  icon: typeof Code2
}

const TOOLS: ToolDef[] = [
  { id: 'base64enc', labelKey: 'base64enc', group: 'encode', icon: Braces },
  { id: 'urlenc', labelKey: 'urlenc', group: 'encode', icon: Braces },
  { id: 'base64dec', labelKey: 'base64dec', group: 'decode', icon: Code2 },
  { id: 'urldec', labelKey: 'urldec', group: 'decode', icon: Code2 },
  { id: 'jwt', labelKey: 'jwt', group: 'decode', icon: KeyRound },
  { id: 'md5', labelKey: 'md5', group: 'digest', icon: Fingerprint },
  { id: 'sha1', labelKey: 'sha1', group: 'digest', icon: Fingerprint },
  { id: 'sha256', labelKey: 'sha256', group: 'digest', icon: Fingerprint },
  { id: 'timestamp', labelKey: 'timestamp', group: 'generate', icon: Clock },
  { id: 'uuid', labelKey: 'uuid', group: 'generate', icon: Hash },
  { id: 'qr', labelKey: 'qr', group: 'generate', icon: QrIcon },
]

const GROUP_ORDER: ToolGroup[] = ['encode', 'decode', 'digest', 'generate']

const STORAGE_KEY = 'sniffy-toolbox-tool'

function isToolId(v: string | null): v is ToolId {
  return !!v && TOOLS.some((t) => t.id === v)
}

/** 工具箱根组件（既可独立窗口承载，也可内嵌）。 */
export function ToolboxView() {
  const { t } = useTranslation()
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

  const groups = useMemo(
    () =>
      GROUP_ORDER.map((g) => ({
        group: g,
        title: t('toolbox.group.' + g),
        items: TOOLS.filter((tool) => tool.group === g),
      })),
    [t],
  )

  return (
    <div className="flex h-full min-h-0 bg-base">
      {/* 工具列表 */}
      <aside className="flex w-44 shrink-0 flex-col gap-3 overflow-auto border-r border-line bg-surface p-2">
        {groups.map(({ group, title, items }) => (
          <div key={group}>
            <div className="px-2 pb-1 text-[10px] font-semibold uppercase tracking-wide text-fg-faint">{title}</div>
            <div className="flex flex-col gap-0.5">
              {items.map((tool) => {
                const Icon = tool.icon
                const isActive = tool.id === active
                return (
                  <button
                    key={tool.id}
                    type="button"
                    onClick={() => setActive(tool.id)}
                    className={cx(
                      'flex items-center gap-2 rounded-wb px-2 py-1.5 text-left text-[12.5px] transition-colors',
                      isActive ? 'wb-row-selected bg-accent' : 'text-fg-muted hover:bg-elevated hover:text-fg',
                    )}
                  >
                    <Icon className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{t('toolbox.tool.' + tool.labelKey)}</span>
                  </button>
                )
              })}
            </div>
          </div>
        ))}
      </aside>

      {/* 工具面板 */}
      <main className="min-w-0 flex-1 overflow-auto p-4">
        <ToolPanel id={active} />
      </main>
    </div>
  )
}

function ToolPanel({ id }: { id: ToolId }) {
  const { t } = useTranslation()
  switch (id) {
    case 'base64enc':
      return <TransformTool key={id} title={t('toolbox.tool.base64enc')} run={(s) => base64Encode(s)} placeholder={t('toolbox.transform.phEncode')} />
    case 'base64dec':
      return <TransformTool key={id} title={t('toolbox.tool.base64dec')} run={(s) => base64Decode(s)} placeholder={t('toolbox.transform.phBase64')} mono />
    case 'urlenc':
      return <TransformTool key={id} title={t('toolbox.tool.urlenc')} run={(s) => urlEncode(s)} placeholder={t('toolbox.transform.phEncode')} />
    case 'urldec':
      return <TransformTool key={id} title={t('toolbox.tool.urldec')} run={(s) => urlDecode(s)} placeholder={t('toolbox.transform.phUrlEncoded')} mono />
    case 'jwt':
      return <JwtTool key={id} />
    case 'md5':
      return <TransformTool key={id} title={t('toolbox.transform.md5Title')} run={(s) => digest('MD5', s)} placeholder={t('toolbox.transform.phDigest')} monoOut />
    case 'sha1':
      return <TransformTool key={id} title={t('toolbox.transform.sha1Title')} run={(s) => digest('SHA-1', s)} placeholder={t('toolbox.transform.phDigest')} monoOut />
    case 'sha256':
      return <TransformTool key={id} title={t('toolbox.transform.sha256Title')} run={(s) => digest('SHA-256', s)} placeholder={t('toolbox.transform.phDigest')} monoOut />
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

function CopyButton({ text, label }: { text: string; label?: string }) {
  const { t } = useTranslation()
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
      {done ? t('toolbox.common.copied') : label ?? t('toolbox.common.copy')}
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
  'w-full resize-none rounded-wb border border-line bg-inset px-2.5 py-2 text-[12.5px] text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-accent focus:bg-surface'

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
  const { t } = useTranslation()
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
      <label className="mb-1 block text-2xs font-medium text-fg-muted">{t('toolbox.transform.input')}</label>
      <textarea
        spellCheck={false}
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder={placeholder}
        rows={6}
        className={cx(ioCls, mono && 'font-mono')}
      />
      <label className="mb-1 mt-3 block text-2xs font-medium text-fg-muted">{t('toolbox.transform.output')}</label>
      <textarea
        readOnly
        value={error ? '' : output}
        rows={6}
        placeholder={t('toolbox.transform.resultHint')}
        className={cx(ioCls, (mono || monoOut) && 'font-mono', 'cursor-text')}
      />
      {error && <div className="mt-2 rounded-wb bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{error}</div>}
    </div>
  )
}

/* ───────────────────────── JWT ───────────────────────── */

function JwtTool() {
  const { t } = useTranslation()
  const [input, setInput] = useState('')
  const parsed = useMemo(() => {
    if (!input.trim()) return null
    try {
      const r = parseJwt(input)
      return { ok: true as const, ...r, notes: describeJwtClaims(r.payload) }
    } catch (e) {
      return { ok: false as const, error: e instanceof Error ? e.message : String(e) }
    }
    // t 在依赖中：parseJwt / describeJwtClaims 经 i18n.t 取文案，切换语言时需重算
  }, [input, t])

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead title={t('toolbox.tool.jwt')} icon={KeyRound} />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">Token</label>
      <textarea
        spellCheck={false}
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder={t('toolbox.jwt.tokenPlaceholder')}
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
              {parsed.signature || t('toolbox.jwt.noSignature')}
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
      <pre className="max-h-72 overflow-auto rounded-wb border border-line bg-inset px-2.5 py-2 font-mono text-[12px] leading-relaxed text-fg">
        {text}
      </pre>
    </div>
  )
}

/* ───────────────────────── 时间戳 ───────────────────────── */

function TimestampTool() {
  const { t } = useTranslation()
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
    [t('toolbox.timestamp.unixSec'), String(info.unix)],
    [t('toolbox.timestamp.unixMs'), String(info.unixMs)],
    ['ISO 8601', info.iso],
    [t('toolbox.timestamp.local'), info.local],
    ['UTC', info.utc],
  ]

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead
        title={t('toolbox.tool.timestamp')}
        icon={Clock}
        right={
          <Button size="sm" icon={<RefreshCw className="h-3.5 w-3.5" />} onClick={refreshNow}>
            {t('toolbox.timestamp.now')}
          </Button>
        }
      />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">{t('toolbox.timestamp.parseLabel')}</label>
      <input
        spellCheck={false}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder={t('toolbox.timestamp.parsePlaceholder')}
        className={cx(ioCls, 'font-mono')}
      />
      {error && <div className="mt-2 rounded-wb bg-danger/10 px-2.5 py-1.5 text-2xs text-danger">{error}</div>}
      <div className="mt-3 divide-y divide-line overflow-hidden rounded-wb border border-line">
        {rows.map(([k, v]) => (
          <button
            key={k}
            type="button"
            onClick={() => void navigator.clipboard?.writeText(v)}
            title={t('toolbox.common.clickToCopy')}
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
  const { t } = useTranslation()
  const [list, setList] = useState<string[]>(() => [genUuid()])
  const gen = (n: number) => setList(Array.from({ length: n }, () => genUuid()))

  return (
    <div className="mx-auto max-w-[760px]">
      <PanelHead
        title={t('toolbox.uuid.title')}
        icon={Hash}
        right={
          <>
            <Button size="sm" onClick={() => gen(1)}>
              {t('toolbox.uuid.gen1')}
            </Button>
            <Button size="sm" onClick={() => gen(10)}>
              {t('toolbox.uuid.gen10')}
            </Button>
            <CopyButton text={list.join('\n')} label={t('toolbox.uuid.copyAll')} />
          </>
        }
      />
      <div className="flex flex-col gap-1">
        {list.map((u, i) => (
          <button
            key={`${u}-${i}`}
            type="button"
            onClick={() => void navigator.clipboard?.writeText(u)}
            title={t('toolbox.common.clickToCopy')}
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
  const { t } = useTranslation()
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
        title={t('toolbox.qr.title')}
        icon={QrIcon}
        right={
          svgString ? (
            <Button size="sm" onClick={() => void saveFile(svgString, 'sniffy-qrcode.svg')}>
              {t('toolbox.qr.downloadSvg')}
            </Button>
          ) : undefined
        }
      />
      <label className="mb-1 block text-2xs font-medium text-fg-muted">{t('toolbox.qr.contentLabel')}</label>
      <textarea
        spellCheck={false}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder={t('toolbox.qr.contentPlaceholder')}
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
          <span className="text-2xs text-fg-faint">
            {t('toolbox.qr.ecLevel', { level: 'M' })} · {result?.ok ? result.matrix.length : 0}×
            {result?.ok ? result.matrix.length : 0}
          </span>
        </div>
      )}
    </div>
  )
}
