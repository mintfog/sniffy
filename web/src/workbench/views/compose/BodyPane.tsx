import { useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { FileText } from 'lucide-react'
import { PluginEditor, type PluginEditorHandle } from '../plugins/editor'
import { formatSize, prettyJson } from '../../lib/format'
import { cx } from '../../ui/primitives'
import { Select } from '../../ui/controls'
import { ParameterTable } from './ParameterTable'
import {
  bodyEditorOf,
  editBodyParameters,
  editRawBody,
  isRawBodyMode,
  switchBodyMode,
  type RawBodyMode,
} from './body'
import { appendHeader, type Draft, type DraftDiff } from './model'
import { byteLength } from './wire'

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
  const bodyEditorRef = useRef<PluginEditorHandle>(null)
  const editor = useMemo(() => bodyEditorOf(draft), [draft])
  const { mode } = editor
  const isRaw = isRawBodyMode(mode)
  const rawMode = isRaw ? mode : (editor.lastRawMode ?? 'json')
  const isForm =
    (mode === 'urlencoded' || mode === 'multipart') && !editor.rawMultipart
  const formats = [
    { value: 'none', label: t('compose.body.none') },
    { value: 'multipart', label: 'form-data' },
    { value: 'urlencoded', label: 'x-www-form-urlencoded' },
    { value: 'raw', label: 'raw' },
  ] as const
  const hasContentType = draft.headers.some(
    h => h.name.trim().toLowerCase() === 'content-type'
  )
  const isJson = useMemo(() => {
    if (!isRaw) return false
    try {
      JSON.parse(draft.body)
      return true
    } catch {
      return false
    }
  }, [draft.body, isRaw])

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-line bg-inset/40 px-3 py-2">
        <div
          role="radiogroup"
          aria-label={t('compose.body.type')}
          className="contents"
        >
          {formats.map(({ value, label }) => {
            const selected = value === 'raw' ? isRaw : mode === value
            return (
              <label
                key={value}
                className={cx(
                  'flex cursor-pointer items-center gap-1.5 text-2xs',
                  selected ? 'text-fg' : 'text-fg-muted'
                )}
              >
                <input
                  type="radio"
                  name={`body-type-${draft.id}`}
                  checked={selected}
                  onChange={() =>
                    onPatch(
                      switchBodyMode(draft, value === 'raw' ? rawMode : value)
                    )
                  }
                  className="h-3 w-3 accent-accent"
                />
                {label}
              </label>
            )
          })}
        </div>
        {isRaw && (
          <Select
            aria-label={t('compose.body.rawFormat')}
            value={rawMode}
            options={[
              { value: 'json', label: 'JSON' },
              { value: 'text', label: 'Text' },
              { value: 'xml', label: 'XML' },
              { value: 'html', label: 'HTML' },
            ]}
            onChange={event =>
              onPatch(switchBodyMode(draft, event.target.value as RawBodyMode))
            }
            className="h-6 min-w-[78px] text-2xs"
          />
        )}
        <div className="ml-auto flex items-center gap-1.5">
          {isJson && !hasContentType && (
            <button
              type="button"
              onClick={() =>
                onPatch({
                  headers: appendHeader(
                    draft.headers,
                    'Content-Type',
                    'application/json'
                  ),
                })
              }
              className="rounded-control px-1.5 py-0.5 text-2xs text-warn transition hover:bg-warn/15"
            >
              {t('compose.req.setJsonType')}
            </button>
          )}
          {isJson && (
            <button
              type="button"
              onClick={() => bodyEditorRef.current?.transformText(prettyJson)}
              className="rounded-control px-1.5 py-0.5 text-2xs text-fg-muted transition hover:bg-elevated hover:text-fg"
            >
              {t('compose.req.format')}
            </button>
          )}
          <span className="tabular-nums text-2xs text-fg-faint">
            {formatSize(byteLength(draft.body))}
          </span>
        </div>
      </div>
      {draft.seed?.bodyBinary && (
        <div className="border-b border-line bg-warn/10 px-3 py-1.5 text-2xs leading-relaxed text-warn">
          {t('compose.req.binaryBody', {
            size: formatSize(draft.seed.bodySize),
          })}
        </div>
      )}
      {draft.seed?.bodyTooLarge && (
        <div className="border-b border-line bg-warn/10 px-3 py-1.5 text-2xs leading-relaxed text-warn">
          {t('compose.req.oversizedBody', {
            size: formatSize(draft.seed.bodySize),
          })}
        </div>
      )}
      {editor.rawMultipart && (
        <div className="shrink-0 border-b border-line px-3 py-1.5 text-2xs leading-relaxed text-fg-muted">
          {t('compose.body.multipartRaw')}
        </div>
      )}
      <div className="relative min-h-0 flex-1">
        {diff.body && (
          <span
            aria-hidden
            className="absolute left-0 top-0 z-10 h-full w-[2px] bg-accent"
          />
        )}
        {mode === 'none' ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-center text-fg-faint">
            <FileText className="h-6 w-6" strokeWidth={1.25} />
            <span className="text-xs">{t('compose.body.empty')}</span>
            <span className="text-2xs">{t('compose.body.emptyHint')}</span>
          </div>
        ) : isForm ? (
          <ParameterTable
            rows={editor.rows ?? []}
            onChange={rows => onPatch(editBodyParameters(draft, rows))}
            label={t('compose.body.formFields')}
            hint={t(
              mode === 'multipart'
                ? 'compose.body.multipartHint'
                : 'compose.body.urlencodedHint'
            )}
          />
        ) : (
          <PluginEditor
            ref={bodyEditorRef}
            value={draft.body}
            onChange={value => onPatch(editRawBody(draft, value))}
            language={mode === 'json' ? 'json' : 'text'}
            placeholder={t('compose.req.bodyPlaceholder')}
            ariaLabel={t('compose.req.body')}
            className="h-full"
          />
        )}
      </div>
    </div>
  )
}
