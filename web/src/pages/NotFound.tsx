import { Link } from 'react-router-dom'
import { Home, ArrowLeft } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function NotFound() {
  const { t } = useTranslation()
  return (
    <div className="min-h-screen bg-gray-50 flex items-center justify-center">
      <div className="text-center">
        {/* 404 图标 */}
        <div className="mb-8">
          <h1 className="text-9xl font-bold text-gray-300">404</h1>
        </div>

        {/* 错误信息 */}
        <div className="mb-8">
          <h2 className="text-3xl font-bold text-gray-900 mb-4">{t('notFound.title')}</h2>
          <p className="text-lg text-gray-600 max-w-md mx-auto">
            {t('notFound.description')}
          </p>
        </div>

        {/* 操作按钮 */}
        <div className="space-y-4 sm:space-y-0 sm:space-x-4 sm:flex sm:justify-center">
          <button
            onClick={() => window.history.back()}
            className="flex items-center justify-center w-full sm:w-auto px-6 py-3 bg-gray-600 hover:bg-gray-700 text-white rounded-md font-medium transition-colors"
          >
            <ArrowLeft className="h-4 w-4 mr-2" />
            {t('notFound.back')}
          </button>

          <Link
            to="/"
            className="flex items-center justify-center w-full sm:w-auto px-6 py-3 bg-primary-600 hover:bg-primary-700 text-white rounded-md font-medium transition-colors"
          >
            <Home className="h-4 w-4 mr-2" />
            {t('notFound.home')}
          </Link>
        </div>

        {/* 帮助链接 */}
        <div className="mt-12">
          <p className="text-sm text-gray-500">
            {t('notFound.helpPrefix')}{' '}
            <Link to="/?view=settings" className="text-primary-600 hover:text-primary-500">
              {t('notFound.settingsLink')}
            </Link>{' '}
            {t('notFound.helpSuffix')}
          </p>
        </div>
      </div>
    </div>
  )
}
