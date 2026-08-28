/**
 * 表单之下的真相：这份草稿将要写到线上的报文。
 * 淡色的行是构造器替你补的（Host / Content-Length / 协议头），其余是你键入的原文。
 */
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, ChevronRight, Copy } from 'lucide-react'
import { formatSize } from '../../lib/format'
import { cx } from '../../ui/primitives'
import type { Draft } from './model'
import { buildWire, resolveWire, wireText } from './wire'

export function WireReadout({ draft }: { draft: Draft }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(true)
  const [copied, setCopied] = useState(false)
  const wire = useMemo(() => buildWire(draft), [draft])
  const varsError = useMemo(() => resolveWire(draft).varsError, [draft])
  // URL 还没填时报文必然是残缺的（Host 为空、目标是裸 /），显示它只会误导。
  const ready = draft.url.trim() !== ''

  const copy = () => {
    navigator.clipboard?.writeText(wireText(draft)).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1100)
    })
  }

  return (
    <div className="shrink-0 border-t border-line bg-inset">
      <div className="flex h-7 items-center gap-1.5 pl-2 pr-1.5">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left outline-none focus-visible:text-fg"
        >
          <ChevronRight className={cx('h-3.5 w-3.5 shrink-0 text-fg-faint transition-transform', open && 'rotate-90')} />
          <span className="text-2xs font-semibold uppercase tracking-wide text-fg-muted">{t('compose.wire.title')}</span>
        </button>
        {ready && <span className="wb-tnum shrink-0 text-2xs text-fg-faint">{formatSize(wire.totalBytes)}</span>}
        <button
          type="button"
          onClick={copy}
          disabled={!ready}
          title={t('compose.wire.copy')}
          aria-label={t('compose.wire.copy')}
          className="flex h-5 w-5 shrink-0 items-center justify-center rounded-control text-fg-faint transition hover:bg-elevated hover:text-fg disabled:invisible"
        >
          {copied ? <Check className="h-3 w-3 text-ok" /> : <Copy className="h-3 w-3" />}
        </button>
      </div>
      {open && !ready && <div className="px-3 pb-2 text-2xs leading-relaxed text-fg-faint">{t('compose.wire.needUrl')}</div>}
      {open && ready && (
        <div className="max-h-[38vh] overflow-auto px-3 pb-2 font-mono text-[11.5px] leading-[1.55]">
          <div className="break-all text-accent">{wire.requestLine}</div>
          {wire.headers.map((h, i) => (
            <div key={`${h.name}-${i}`} className="break-all">
              <span className={h.origin === 'synthesized' ? 'text-fg-faint' : 'text-iris'}>{h.name}</span>
              <span className="text-fg-faint">: </span>
              <span className={h.origin === 'synthesized' ? 'text-fg-faint' : 'text-fg-muted'}>{h.value}</span>
            </div>
          ))}
          {wire.dropped.length > 0 && (
            <div className="mt-1 font-sans text-2xs text-fg-faint">
              {t('compose.wire.dropped', { names: wire.dropped.join(', ') })}
            </div>
          )}
          {wire.overridden.length > 0 && (
            <div className="mt-1 font-sans text-2xs text-fg-faint">
              {t('compose.wire.overridden', { names: wire.overridden.join(', ') })}
            </div>
          )}
          {varsError ? (
            <div className="mt-1 font-sans text-2xs text-danger">{t('compose.wire.gqlVarsBroken')}</div>
          ) : (
            wire.bodyBytes > 0 && (
              <div className="mt-1 font-sans text-2xs text-fg-faint">
                {t('compose.wire.bodyLine', { size: formatSize(wire.bodyBytes) })}
              </div>
            )
          )}
        </div>
      )}
    </div>
  )
}
