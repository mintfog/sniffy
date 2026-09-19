import { Download, ExternalLink, FolderOpen, Github, PackageCheck, RefreshCw, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { UpdateState } from '@/lib/bridge'
import { SniffyMark, cx } from '../ui/primitives'
import { Button } from '../ui/controls'
import { DOWNLOAD_URL, REPO_URL, docsUrl, openExternal } from '../lib/links'
import { useBackendVersion } from '../lib/version'
import { formatMB, useUpdate, type UpdateActions } from '../lib/update'

/** 关于面板 */
export function AboutView() {
  const { t, i18n } = useTranslation()
  const version = useBackendVersion()
  const { state, actions, installError } = useUpdate()
  return (
    <div className="flex h-full min-h-0 flex-col items-center overflow-auto bg-base px-6 py-8 text-center">
      <span className="flex h-16 w-16 shrink-0 items-center justify-center rounded-wb border border-line bg-surface text-accent shadow-wb">
        <SniffyMark className="h-11 w-11" />
      </span>
      <h1 className="mt-4 font-mono text-xl font-semibold uppercase tracking-[0.22em] text-fg">Sniffy</h1>
      <div className="mt-1 font-mono text-[12px] text-fg-muted">{t('about.version', { version })}</div>
      <p className="mt-3 max-w-sm text-[12.5px] leading-relaxed text-fg-muted">{t('about.description')}</p>

      <UpdatePanel state={state} actions={actions} installError={installError} />

      <div className="mt-6 w-full max-w-sm shrink-0 divide-y divide-line overflow-hidden rounded-wb border border-line bg-surface text-left">
        <Row label={t('about.row.kernel')} value="Go · net/http MITM" />
        <Row label={t('about.row.frontend')} value="React · Vite · Wails v3" />
        <Row label={t('about.row.license')} value="Apache-2.0" />
      </div>

      <div className="mt-6 flex shrink-0 flex-wrap items-center justify-center gap-2">
        <Button icon={<Github className="h-3.5 w-3.5" />} onClick={() => openExternal(REPO_URL)}>
          {t('about.repo')}
        </Button>
        <Button icon={<ExternalLink className="h-3.5 w-3.5" />} onClick={() => openExternal(docsUrl(i18n.language))}>
          {t('about.docs')}
        </Button>
      </div>

      <div className="mt-8 shrink-0 text-2xs text-fg-faint">{t('about.copyright')}</div>
    </div>
  )
}

function UpdatePanel({
  state,
  actions,
  installError,
}: {
  state: UpdateState
  actions: UpdateActions
  installError: string
}) {
  const { t } = useTranslation()
  const skipped = !!state.latest && state.skippedVersion === state.latest
  const description = detail(state, t)
  const downloadFailed = state.status === 'error' && state.errorStage === 'download'
  const showDownloadActions = state.status === 'available' || downloadFailed
  const downloadLabel = t(downloadFailed ? 'about.update.retryDownload' : 'about.update.download')

  return (
    <div className="mt-6 w-full max-w-sm shrink-0 rounded-wb border border-line bg-surface px-4 py-3.5 text-left">
      <div className="flex items-center gap-2">
        <span className={cx('h-1.5 w-1.5 shrink-0 rounded-full', statusDot(state))} />
        <span className="text-[12.5px] text-fg">{headline(state, t)}</span>
      </div>
      {description && <p className="mt-1.5 text-2xs leading-relaxed text-fg-faint">{description}</p>}
      {/* 已下载状态仍可带检查错误，详情行需保留安装包路径。 */}
      {state.status !== 'error' && state.error && (
        <p className="mt-1.5 text-2xs leading-relaxed text-warn">
          {t('about.update.checkFailed', { reason: state.error })}
        </p>
      )}

      {state.status === 'downloading' && (
        <div className="mt-2.5 h-1 w-full overflow-hidden rounded-full bg-elevated">
          <div
            className="h-full bg-accent transition-[width]"
            style={{ width: `${Math.round(((state.downloaded ?? 0) / state.total!) * 100)}%` }}
          />
        </div>
      )}

      <div className="mt-3 flex flex-wrap gap-2">
        {showDownloadActions && state.asset && (
          <Button variant="primary" icon={<Download className="h-3.5 w-3.5" />} onClick={actions.download}>
            {downloadLabel}
          </Button>
        )}
        {showDownloadActions && !state.asset && (
          <Button
            variant="primary"
            icon={<ExternalLink className="h-3.5 w-3.5" />}
            onClick={() => openExternal(DOWNLOAD_URL)}
          >
            {t('about.update.openDownloadPage')}
          </Button>
        )}
        {state.status === 'downloading' && (
          <Button icon={<X className="h-3.5 w-3.5" />} onClick={actions.cancel}>
            {t('about.update.cancel')}
          </Button>
        )}
        {state.status === 'downloaded' && state.installAction !== 'reveal' && (
          <Button variant="primary" icon={<PackageCheck className="h-3.5 w-3.5" />} onClick={actions.install}>
            {t(state.installAction === 'run' ? 'about.update.install' : 'about.update.openImage')}
          </Button>
        )}
        {state.status === 'downloaded' && (
          <Button
            variant={state.installAction === 'reveal' ? 'primary' : 'secondary'}
            icon={<FolderOpen className="h-3.5 w-3.5" />}
            onClick={actions.reveal}
          >
            {t('about.update.reveal')}
          </Button>
        )}
        {state.status !== 'downloading' && (
          <Button
            icon={<RefreshCw className={cx('h-3.5 w-3.5', state.status === 'checking' && 'animate-spin')} />}
            disabled={state.status === 'checking'}
            onClick={actions.check}
          >
            {t('about.update.check')}
          </Button>
        )}
        {state.notesUrl && state.status === 'available' && (
          <Button icon={<ExternalLink className="h-3.5 w-3.5" />} onClick={() => openExternal(state.notesUrl!)}>
            {t('about.update.notes')}
          </Button>
        )}
        {state.status === 'available' && !skipped && <Button onClick={actions.skip}>{t('about.update.skip')}</Button>}
        {skipped && <Button onClick={actions.unskip}>{t('about.update.unskip')}</Button>}
      </div>

      {state.status === 'downloaded' && state.installAction === 'run' && (
        <p className="mt-2 text-2xs leading-relaxed text-fg-faint">{t('about.update.installHint')}</p>
      )}
      {installError && <p className="mt-2 text-2xs leading-relaxed text-warn">{installError}</p>}
    </div>
  )
}

function statusDot(state: UpdateState): string {
  switch (state.status) {
    case 'available':
    case 'downloaded':
      return 'bg-accent'
    case 'error':
      return 'bg-warn'
    case 'checking':
    case 'downloading':
      return 'bg-fg-muted animate-pulse'
    default:
      return 'bg-ok'
  }
}

type Translate = (key: string, opts?: Record<string, unknown>) => string

function headline(state: UpdateState, t: Translate): string {
  if (state.devBuild) return t('about.update.devBuild')
  switch (state.status) {
    case 'checking':
      return t('about.update.checking')
    case 'available':
      return t('about.update.available', { version: state.latest })
    case 'downloading':
      return t('about.update.downloading')
    case 'downloaded':
      return t('about.update.downloaded', { version: state.latest })
    case 'error':
      return t(state.errorStage === 'download' ? 'about.update.downloadFailed' : 'about.update.failed')
    case 'latest':
      return t('about.update.latest')
    default:
      return t('about.update.idle')
  }
}

function detail(state: UpdateState, t: Translate): string {
  switch (state.status) {
    case 'error':
      return state.error ?? ''
    case 'downloaded':
      return state.downloadedPath ?? ''
    case 'downloading':
      return `${formatMB(state.downloaded ?? 0)} / ${formatMB(state.total!)}`
    case 'available': {
      const size = state.asset ? formatMB(state.asset.size) : ''
      const parts = [state.publishedAt, size, state.asset?.name].filter(Boolean)
      return state.asset ? parts.join(' · ') : t('about.update.noAsset')
    }
    case 'latest':
      return state.checkedAt ? t('about.update.checkedAt', { time: formatTime(state.checkedAt) }) : ''
    default:
      return t('about.update.source')
  }
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-2.5">
      <span className="text-[12.5px] text-fg-muted">{label}</span>
      <span className="font-mono text-[12px] text-fg">{value}</span>
    </div>
  )
}
