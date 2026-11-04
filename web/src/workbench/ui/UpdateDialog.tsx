import { useEffect, useMemo, useRef, type MouseEvent } from 'react'
import { createPortal } from 'react-dom'
import { Download, FolderOpen, PackageCheck, RotateCw, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import type { UpdateState } from '@/lib/bridge'
import {
  downloadPercent,
  formatMB,
  INSTALL_HINT_KEYS,
  INSTALL_LABEL_KEYS,
  type UpdateActions,
} from '../lib/update'
import { DOWNLOAD_URL, openExternal } from '../lib/links'
import { Button } from './controls'
import { IconButton, SniffyMark, cx } from './primitives'

interface UpdateDialogProps {
  state: UpdateState
  actions: UpdateActions
  installError: string
  installing: boolean
  onClose: () => void
}

export function UpdateDialog({
  state,
  actions,
  installError,
  installing,
  onClose,
}: UpdateDialogProps) {
  const { t } = useTranslation()
  const notes = state.notes?.trim() ?? ''
  const dialogRef = useRef<HTMLDivElement>(null)
  // 底栏随状态整体替换，聚焦的按钮被卸载后焦点会落到 body，Escape 便不再到达弹窗。
  useEffect(() => {
    const dialog = dialogRef.current
    if (dialog && !dialog.contains(document.activeElement)) dialog.focus()
  }, [state.status])
  const notesHTML = useMemo(() => {
    const parsed = marked.parse(notes, { async: false })
    return DOMPurify.sanitize(parsed, {
      // 图片会在展示更新说明时额外发起网络请求。
      FORBID_TAGS: ['img', 'style', 'input'],
    })
  }, [notes])

  const openNotesLink = (event: MouseEvent<HTMLDivElement>) => {
    if (!(event.target instanceof Element)) return
    const link = event.target.closest('a[href]')
    if (!link) return
    event.preventDefault()
    try {
      const url = new URL(link.getAttribute('href') ?? '', state.notesUrl)
      if (url.protocol === 'https:' || url.protocol === 'http:') {
        openExternal(url.href)
      }
    } catch {
      // 清单可能未提供 notesUrl，相对链接此时无法解析。
    }
  }

  // 有安装包时留在弹窗内跟随下载与安装进度；没有时只能去下载页，弹窗随之关闭。
  const downloadUpdate = () => {
    if (state.asset) {
      actions.download()
    } else {
      openExternal(DOWNLOAD_URL)
      onClose()
    }
  }

  return createPortal(
    <div
      role="presentation"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        ref={dialogRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={t('about.update.dialogTitle')}
        onClick={event => event.stopPropagation()}
        onKeyDown={event => {
          event.stopPropagation()
          if (event.key === 'Escape') onClose()
        }}
        className="flex max-h-[calc(100dvh-2rem)] w-full max-w-[720px] flex-col rounded-xl border border-line-strong bg-surface p-6 text-left text-fg shadow-2xl outline-none select-text"
      >
        <header className="flex shrink-0 items-center gap-4 border-b border-line pb-5">
          <SniffyMark className="h-14 w-14" />
          <div className="min-w-0 flex-1">
            <h2 className="text-xl font-semibold">
              {t('about.update.dialogTitle')}
            </h2>
            <p className="mt-1 text-[13px] text-fg-muted">
              {state.current} → {state.latest}
            </p>
          </div>
          <IconButton
            aria-label={t('about.update.close')}
            onClick={onClose}
            className="self-start"
          >
            <X className="h-5 w-5" />
          </IconButton>
        </header>
        <div
          onClick={openNotesLink}
          className={cx(
            'my-5 min-h-0 max-h-[52vh] overflow-auto text-[13px] leading-7 text-fg-muted [overflow-wrap:anywhere]',
            '[&_h1]:text-lg [&_h2]:text-[16px] [&_h3]:text-sm [&_:is(h1,h2,h3)]:font-semibold [&_:is(h1,h2,h3)]:text-fg [&_:is(h1,h2,h3)]:mb-2 [&_:is(h1,h2,h3)]:mt-5 [&>div>:first-child]:mt-0',
            '[&_ul]:list-disc [&_ol]:list-decimal [&_:is(ul,ol)]:pl-6 [&_p]:my-2 [&_a]:text-accent [&_a]:underline [&_pre]:overflow-auto'
          )}
        >
          {notes ? (
            <div dangerouslySetInnerHTML={{ __html: notesHTML }} />
          ) : (
            <p>
              {t('about.update.notesUnavailable')}{' '}
              {state.notesUrl && (
                <a href={state.notesUrl}>{t('about.update.notes')}</a>
              )}
            </p>
          )}
        </div>
        <footer className="shrink-0 border-t border-line pt-3">
          {/* 安装包失效时后端会把状态退回 available，安装错误因此不归属任何状态分支。 */}
          {installError && (
            <p className="mb-2 text-[12px] text-warn">{installError}</p>
          )}
          <UpdateFooter
            state={state}
            actions={actions}
            installing={installing}
            onDownload={downloadUpdate}
            onClose={onClose}
          />
        </footer>
      </div>
    </div>,
    document.body
  )
}

function UpdateFooter({
  state,
  actions,
  installing,
  onDownload,
  onClose,
}: {
  state: UpdateState
  actions: UpdateActions
  installing: boolean
  onDownload: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()

  if (state.status === 'downloading') {
    const percent = downloadPercent(state)
    return (
      <>
        <div className="mb-1.5 flex items-center justify-between text-[12px] text-fg-muted">
          <span>{t('about.update.downloading')}</span>
          <span className="tabular-nums">
            {formatMB(state.downloaded ?? 0)} / {formatMB(state.total!)} ·{' '}
            {percent}%
          </span>
        </div>
        <div className="mb-3 h-1.5 w-full overflow-hidden rounded-full bg-elevated">
          <div
            className="h-full bg-accent transition-[width]"
            style={{ width: `${percent}%` }}
          />
        </div>
        <div className="flex justify-end gap-2">
          <Button icon={<X className="h-4 w-4" />} onClick={actions.cancel}>
            {t('about.update.cancel')}
          </Button>
          <Button onClick={onClose}>{t('about.update.later')}</Button>
        </div>
      </>
    )
  }

  if (state.status === 'downloaded') {
    const action = state.installAction
    const canInstall = action !== 'reveal'
    const hintKey = canInstall ? INSTALL_HINT_KEYS[action] : undefined
    return (
      <>
        <p className="mb-1 text-[13px] text-fg">
          {t('about.update.downloaded', { version: state.latest })}
        </p>
        {hintKey && (
          <p className="mb-1 text-[12px] text-fg-faint">{t(hintKey)}</p>
        )}
        <div className="mt-2 flex flex-wrap justify-end gap-2">
          <Button onClick={onClose}>{t('about.update.later')}</Button>
          <Button
            variant={canInstall ? 'secondary' : 'primary'}
            icon={<FolderOpen className="h-4 w-4" />}
            onClick={actions.reveal}
          >
            {t('about.update.reveal')}
          </Button>
          {canInstall && (
            <Button
              variant="primary"
              icon={<PackageCheck className="h-4 w-4" />}
              disabled={installing}
              onClick={actions.install}
            >
              {t(INSTALL_LABEL_KEYS[action])}
            </Button>
          )}
        </div>
      </>
    )
  }

  if (state.status === 'error' && state.errorStage === 'download') {
    return (
      <>
        <div className="mb-3 text-[12px]">
          <p className="text-warn">{t('about.update.downloadFailed')}</p>
          {state.error && (
            <p className="mt-1 text-fg-faint [overflow-wrap:anywhere]">
              {state.error}
            </p>
          )}
        </div>
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>{t('about.update.later')}</Button>
          <Button
            variant="primary"
            icon={<RotateCw className="h-4 w-4" />}
            onClick={onDownload}
          >
            {t('about.update.retryDownload')}
          </Button>
        </div>
      </>
    )
  }

  return (
    <>
      <p className="mb-3 text-[12px] text-fg-faint">
        {t('about.update.downloadHint')}
      </p>
      <div className="flex flex-wrap items-center justify-end gap-2">
        {state.skippedVersion !== state.latest && (
          <button
            type="button"
            className="mr-auto text-[13px] text-accent underline"
            onClick={() => {
              actions.skip()
              onClose()
            }}
          >
            {t('about.update.skip')}
          </button>
        )}
        <Button onClick={onClose}>{t('about.update.later')}</Button>
        <Button
          autoFocus
          variant="primary"
          icon={<Download className="h-4 w-4" />}
          onClick={onDownload}
        >
          {t(
            state.asset
              ? 'about.update.downloadUpdate'
              : 'about.update.openDownloadPage'
          )}
        </Button>
      </div>
    </>
  )
}
