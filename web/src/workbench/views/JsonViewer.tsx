import React, { useMemo, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cx } from '../ui/primitives'

/** 可折叠 JSON 树查看器（主题感知，无外部依赖）。解析失败回退为原文。 */
export function JsonViewer({ value }: { value: string }) {
  const parsed = useMemo<{ ok: boolean; data?: unknown }>(() => {
    try {
      return { ok: true, data: JSON.parse(value) }
    } catch {
      return { ok: false }
    }
  }, [value])

  if (!parsed.ok) {
    return (
      <pre className="wb-scroll max-h-full overflow-auto whitespace-pre-wrap break-all px-3 py-2 font-mono text-[12px] leading-relaxed text-fg-muted">
        {value}
      </pre>
    )
  }

  return (
    <div className="wb-scroll max-h-full overflow-auto px-3 py-2 font-mono text-[12px] leading-[1.55]">
      <JsonNode name={null} value={parsed.data} depth={0} isLast />
    </div>
  )
}

function Punct({ children }: { children: React.ReactNode }) {
  return <span className="text-fg-faint">{children}</span>
}

function Primitive({ value }: { value: unknown }) {
  if (value === null) return <span className="text-fg-faint">null</span>
  switch (typeof value) {
    case 'string':
      return <span className="text-ok">"{value}"</span>
    case 'number':
      return <span className="text-method-put tabular-nums">{value}</span>
    case 'boolean':
      return <span className="text-method-patch">{String(value)}</span>
    default:
      return <span className="text-fg">{String(value)}</span>
  }
}

function JsonNode({
  name,
  value,
  depth,
  isLast,
}: {
  name: string | null
  value: unknown
  depth: number
  isLast: boolean
}) {
  const { t } = useTranslation()
  const isObject = value !== null && typeof value === 'object'
  const [open, setOpen] = useState(depth < 2)

  const keyLabel =
    name !== null ? (
      <>
        <span className="text-iris">"{name}"</span>
        <Punct>: </Punct>
      </>
    ) : null

  if (!isObject) {
    return (
      <div style={{ paddingLeft: depth * 14 }} className="whitespace-pre-wrap break-all">
        {keyLabel}
        <Primitive value={value} />
        {!isLast && <Punct>,</Punct>}
      </div>
    )
  }

  const isArray = Array.isArray(value)
  const entries = isArray
    ? (value as unknown[]).map((v, i) => [String(i), v] as const)
    : Object.entries(value as Record<string, unknown>)
  const openBrace = isArray ? '[' : '{'
  const closeBrace = isArray ? ']' : '}'

  return (
    <div>
      <div
        style={{ paddingLeft: depth * 14 }}
        className="group/jn flex cursor-pointer items-start rounded-sm hover:bg-elevated/50"
        onClick={() => setOpen((v) => !v)}
      >
        <ChevronRight
          className={cx('mt-[3px] h-3 w-3 shrink-0 text-fg-faint transition-transform', open && 'rotate-90')}
        />
        <span className="min-w-0">
          {keyLabel}
          <Punct>{openBrace}</Punct>
          {!open && (
            <>
              <span className="px-1 text-fg-faint">
                {isArray
                  ? t('jsonViewer.itemCount', { count: entries.length })
                  : t('jsonViewer.keyCount', { count: entries.length })}
              </span>
              <Punct>{closeBrace}</Punct>
              {!isLast && <Punct>,</Punct>}
            </>
          )}
        </span>
      </div>
      {open && (
        <>
          {entries.map(([k, v], i) => (
            <JsonNode
              key={k}
              name={isArray ? null : k}
              value={v}
              depth={depth + 1}
              isLast={i === entries.length - 1}
            />
          ))}
          <div style={{ paddingLeft: depth * 14 + 14 }}>
            <Punct>{closeBrace}</Punct>
            {!isLast && <Punct>,</Punct>}
          </div>
        </>
      )}
    </div>
  )
}
