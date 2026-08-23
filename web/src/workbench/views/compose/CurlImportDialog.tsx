/**
 * 显式的 cURL 导入入口。
 *
 * URL 框的粘贴识别可发现性不足，所以「新建」下拉里另留一条明路；
 * 解析失败留在弹窗里就地报错，不关窗，用户可以直接改了再试。
 */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal, X } from 'lucide-react'
import { Button } from '../../ui/controls'
import { parseCurl, type CurlWarning } from './curl'
import type { Draft } from './model'

export function CurlImportDialog({
  onClose,
  onImport,
}: {
  onClose: () => void
  onImport: (draft: Draft, warnings: CurlWarning[]) => void
}) {
  const { t } = useTranslation()
  const [text, setText] = useState('')
  const [error, setError] = useState<string | undefined>(undefined)
  const areaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    areaRef.current?.focus()
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const submit = () => {
    const result = parseCurl(text)
    if (!result.ok) {
      setError(t(`compose.curl.err.${result.error.code}`, result.error.params))
      return
    }
    onImport(result.draft, result.warnings)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose} role="presentation">
      <div
        className="flex w-full max-w-2xl flex-col overflow-hidden rounded-wb border border-line bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <header className="flex items-center gap-2 border-b border-line bg-inset/50 px-4 py-2.5">
          <Terminal className="h-4 w-4 text-accent" />
          <span className="text-[13px] font-semibold text-fg">{t('compose.curl.title')}</span>
          <button type="button" onClick={onClose} className="ml-auto text-fg-faint hover:text-fg">
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="flex flex-col gap-2 px-4 py-3">
          <label className="text-[12.5px] text-fg-muted" htmlFor="curl-import-text">
            {t('compose.curl.paste')}
          </label>
          <textarea
            id="curl-import-text"
            ref={areaRef}
            value={text}
            spellCheck={false}
            onChange={(e) => {
              setText(e.target.value)
              setError(undefined)
            }}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
                e.preventDefault()
                e.stopPropagation()
                submit()
              }
            }}
            placeholder={t('compose.curl.placeholder')}
            className="h-56 w-full resize-none rounded-wb border border-line bg-inset px-3 py-2 font-mono text-[11.5px] leading-[1.55] text-fg outline-none transition-colors placeholder:font-sans placeholder:text-fg-faint focus:border-accent focus:bg-surface"
          />
          {error && <span className="text-2xs leading-relaxed text-danger">{error}</span>}
        </div>
        <footer className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
          <Button onClick={onClose}>{t('compose.curl.cancel')}</Button>
          <Button variant="primary" onClick={submit} disabled={!text.trim()}>
            {t('compose.curl.import')}
          </Button>
        </footer>
      </div>
    </div>
  )
}
