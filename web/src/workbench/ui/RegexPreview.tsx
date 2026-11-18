import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { FlaskConical } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  Bridge,
  type RuleRegexPreviewInput,
  type RuleRegexPreviewResult,
} from '@/lib/bridge'
import { compileSearchRegex } from '../lib/searchFilter'
import { cx, IconButton, Tooltip } from './primitives'

export function RegexPreview({
  pattern,
  engine,
  replacement,
  caseSensitive = false,
  negate = false,
  triggerClassName,
  panelClassName,
}: {
  pattern: string
  engine: 'search' | 'rule'
  replacement?: string
  caseSensitive?: boolean
  negate?: boolean
  triggerClassName?: string
  panelClassName?: string
}) {
  const { t } = useTranslation()
  const id = useId()
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const [open, setOpen] = useState(false)
  const [sample, setSample] = useState<string | null>(null)
  const [response, setResponse] = useState<{
    request: RuleRegexPreviewInput
    result: RuleRegexPreviewResult | null
  }>()
  const isSearch = engine === 'search'
  const isReplacement = replacement !== undefined
  const expression = isSearch ? pattern.trim() : pattern
  const needsPattern = expression === '' && (isSearch || isReplacement)

  const request = useMemo(() => {
    if (!open || isSearch || needsPattern) return null
    return {
      pattern: expression,
      sample: sample ?? '',
      replacement,
      caseSensitive,
    }
  }, [
    open,
    isSearch,
    needsPattern,
    expression,
    sample,
    replacement,
    caseSensitive,
  ])

  useEffect(() => {
    if (!request) return
    let active = true
    const timer = window.setTimeout(() => {
      Bridge.previewRuleRegex(request)
        .catch(() => null)
        .then(result => {
          if (active) setResponse({ request, result })
        })
    }, 120)
    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [request])

  const search = useMemo(() => {
    if (!open || !isSearch || needsPattern) return
    try {
      return { re: compileSearchRegex(expression) }
    } catch (error) {
      return {
        syntaxError: error instanceof Error ? error.message : String(error),
      }
    }
  }, [open, isSearch, needsPattern, expression])

  const searchResult = useMemo(
    () => ({
      matched: !!sample && (search?.re?.test(sample) ?? false),
      syntaxError: search?.syntaxError,
    }),
    [search, sample]
  )
  const ruleResult = response?.request === request ? response.result : undefined
  const result = isSearch ? searchResult : ruleResult
  const output =
    isReplacement && sample !== null && ruleResult?.matched
      ? ruleResult.output
      : null

  let message: string
  let tone = 'text-fg-muted'
  if (needsPattern) message = t('regexPreview.enterPattern')
  else if (result?.syntaxError) {
    message = t('regexPreview.invalid', { error: result.syntaxError })
    tone = 'text-danger'
  } else if (ruleResult?.tooLarge) message = t('regexPreview.tooLarge')
  else if (result === null) message = t('regexPreview.unavailable')
  else if (sample === null) message = t('regexPreview.enterSample')
  else if (!result) message = t('regexPreview.checking')
  else if (output === sample) message = t('regexPreview.unchanged')
  else {
    const matched = result.matched !== negate
    message = negate
      ? t(matched ? 'regexPreview.negatedMatch' : 'regexPreview.negatedNoMatch')
      : t(matched ? 'regexPreview.matches' : 'regexPreview.noMatch')
    if (matched) tone = 'text-ok'
  }

  return (
    <>
      <span className={triggerClassName}>
        <Tooltip label={t('regexPreview.title')}>
          <IconButton
            size="sm"
            active={open}
            aria-label={t('regexPreview.title')}
            aria-expanded={open}
            aria-controls={open ? id : undefined}
            onClick={event => {
              triggerRef.current = event.currentTarget
              setOpen(!open)
            }}
          >
            <FlaskConical className="h-3.5 w-3.5" />
          </IconButton>
        </Tooltip>
      </span>
      {open && (
        <div
          id={id}
          className={cx(
            'min-w-0 space-y-1.5 rounded-control border border-line bg-inset px-2.5 py-2 text-2xs',
            panelClassName
          )}
          onKeyDown={event => {
            if (event.key !== 'Escape' || event.nativeEvent.isComposing) return
            event.stopPropagation()
            setOpen(false)
            triggerRef.current?.focus()
          }}
        >
          <label htmlFor={`${id}-sample`} className="text-fg-muted">
            {t('regexPreview.sample')}
          </label>
          <textarea
            id={`${id}-sample`}
            autoFocus
            rows={2}
            spellCheck={false}
            value={sample ?? ''}
            onChange={event => setSample(event.target.value)}
            className="block w-full resize-y rounded-control border border-line-strong bg-surface px-2 py-1 font-mono text-fg outline-none focus:shadow-focus"
          />
          <div role="status" className={cx('min-h-4 break-words', tone)}>
            {message}
          </div>
          {output !== null && (
            <>
              <div id={`${id}-output`} className="text-fg-muted">
                {t('regexPreview.output')}
              </div>
              <pre
                aria-labelledby={`${id}-output`}
                className="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-control border border-line bg-surface px-2 py-1 font-mono text-fg"
              >
                {output || t('regexPreview.emptyOutput')}
              </pre>
            </>
          )}
        </div>
      )}
    </>
  )
}
