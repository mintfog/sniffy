import { Link } from 'react-router-dom'
import { Home, ArrowLeft } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function NotFound() {
  const { t } = useTranslation()
  return (
    <div className="wb-root flex min-h-screen items-center justify-center bg-base">
      <div className="text-center">
        {/* 404 图标 */}
        <div className="mb-8">
          <h1 className="font-mono text-6xl font-semibold text-fg-faint">404</h1>
        </div>

        {/* 错误信息 */}
        <div className="mb-8">
          <h2 className="mb-2 text-[15px] font-semibold leading-[22px] text-fg">{t('notFound.title')}</h2>
          <p className="mx-auto max-w-md text-[13px] text-fg-muted">
            {t('notFound.description')}
          </p>
        </div>

        {/* 操作按钮 */}
        <div className="flex justify-center gap-2">
          <button
            onClick={() => window.history.back()}
            className="inline-flex h-control-md items-center justify-center gap-1.5 rounded-control border border-line px-2.5 text-[13px] font-medium text-fg outline-none transition-colors hover:bg-hover focus-visible:shadow-focus"
          >
            <ArrowLeft className="h-4 w-4" />
            {t('notFound.back')}
          </button>

          <Link
            to="/"
            className="inline-flex h-control-md items-center justify-center gap-1.5 rounded-control bg-sel px-2.5 text-[13px] font-medium text-sel-fg outline-none transition-colors hover:bg-sel-hover focus-visible:shadow-focus"
          >
            <Home className="h-4 w-4" />
            {t('notFound.home')}
          </Link>
        </div>

        {/* 帮助链接 */}
        <div className="mt-12">
          <p className="text-[12px] text-fg-muted">
            {t('notFound.helpPrefix')}{' '}
            <Link to="/?view=settings" className="text-accent hover:text-accent-hover">
              {t('notFound.settingsLink')}
            </Link>{' '}
            {t('notFound.helpSuffix')}
          </p>
        </div>
      </div>
    </div>
  )
}
