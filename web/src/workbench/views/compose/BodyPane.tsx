/** 构造器的请求体编辑区，以及请求体页签右侧的动作。 */
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { PluginEditor } from '../plugins/editor'
import { formatSize, prettyJson } from '../../lib/format'
import { appendHeader, type Draft, type DraftDiff } from './model'
import { byteLength } from './wire'

/** 请求体页签右侧的动作：格式化 JSON、补 Content-Type。两者都只在真的能帮上忙时出现。 */
export function BodyActions({ draft, onPatch }: { draft: Draft; onPatch: (patch: Partial<Draft>) => void }) {
  const { t } = useTranslation()
  const hasContentType = draft.headers.some((h) => h.name.trim().toLowerCase() === 'content-type')
  const isJson = useMemo(() => {
    const s = draft.body.trim()
    if (!s || !/^[[{]/.test(s)) return false
    try {
      JSON.parse(s)
      return true
    } catch {
      return false
    }
  }, [draft.body])

  const setJsonType = () => {
    onPatch({ headers: appendHeader(draft.headers, 'Content-Type', 'application/json') })
  }

  return (
    <>
      {isJson && !hasContentType && (
        <button
          type="button"
          onClick={setJsonType}
          className="rounded-control px-1.5 py-0.5 text-2xs text-warn transition hover:bg-warn/15"
        >
          {t('compose.req.setJsonType')}
        </button>
      )}
      {isJson && (
        <button
          type="button"
          onClick={() => onPatch({ body: prettyJson(draft.body) })}
          className="rounded-control px-1.5 py-0.5 text-2xs text-fg-muted transition hover:bg-elevated hover:text-fg"
        >
          {t('compose.req.format')}
        </button>
      )}
      <span className="tabular-nums text-2xs text-fg-faint">{formatSize(byteLength(draft.body))}</span>
    </>
  )
}

export function BodyPane({
  draft,
  diff,
  onPatch,
}: {
  draft: Draft
  diff: DraftDiff
  onPatch: (patch: Partial<Draft>) => void
}) {
  const { t } = useTranslation()
  return (
    <div className="flex h-full min-h-0 flex-col">
      {draft.seed?.bodyBinary && (
        <div className="border-b border-line bg-warn/10 px-3 py-1.5 text-2xs leading-relaxed text-warn">
          {t('compose.req.binaryBody', { size: formatSize(draft.seed.bodySize) })}
        </div>
      )}
      {draft.seed?.bodyTooLarge && (
        <div className="border-b border-line bg-warn/10 px-3 py-1.5 text-2xs leading-relaxed text-warn">
          {t('compose.req.oversizedBody', { size: formatSize(draft.seed.bodySize) })}
        </div>
      )}
      <div className="relative min-h-0 flex-1">
        {diff.body && <span aria-hidden className="absolute left-0 top-0 z-10 h-full w-[2px] bg-accent" />}
        <PluginEditor
          value={draft.body}
          onChange={(v) => onPatch({ body: v })}
          language="json"
          placeholder={t('compose.req.bodyPlaceholder')}
          ariaLabel={t('compose.req.body')}
          className="h-full"
        />
      </div>
    </div>
  )
}
