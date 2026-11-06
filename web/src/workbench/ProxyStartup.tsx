import { useEffect, useState, type ReactNode } from 'react'
import { Application } from '@wailsio/runtime'
import { AlertCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Bridge, type ProxyStatus } from '@/lib/bridge'
import { hasWailsWebView } from '@/lib/platform'
import { usePrefs } from './prefs'
import { TitleBar } from './shell/TitleBar'
import { Button, TextInput } from './ui/controls'

const cardClass =
  'w-full max-w-md rounded-lg border border-line bg-popover p-6 shadow-dialog'

function CardTitle({ children }: { children: ReactNode }) {
  return (
    <h1 className="flex items-center gap-2 text-base font-semibold text-fg">
      <AlertCircle className="h-5 w-5 text-danger" />
      {children}
    </h1>
  )
}

/** 恢复页外壳：工作台未加载时仍保留标题栏的窗口控制与主题切换。 */
function StartupShell({ children }: { children: ReactNode }) {
  const theme = usePrefs(s => s.theme)
  return (
    <div className="wb-root flex h-screen w-screen flex-col bg-base">
      <TitleBar
        menus={[]}
        isDark={theme === 'dark'}
        onToggleTheme={() =>
          usePrefs
            .getState()
            .set({ theme: theme === 'dark' ? 'light' : 'dark' })
        }
        connected={false}
      />
      <main className="flex flex-1 items-center justify-center p-6">
        {children}
      </main>
    </div>
  )
}

/** 主窗口入口：代理监听正常时直接渲染工作台，否则显示启动失败的恢复页。 */
export function ProxyStartup({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [status, setStatus] = useState<ProxyStatus | null>(null)
  // 浏览器预览没有桌面 Bridge，直接进入工作台的演示入口；其余查询失败都要留在恢复页。
  const [preview] = useState(() => !hasWailsWebView())
  const [queryError, setQueryError] = useState('')
  // 递增即触发重新查询状态。
  const [checks, setChecks] = useState(0)
  const [port, setPort] = useState('')
  const [busy, setBusy] = useState(false)
  const [retryError, setRetryError] = useState('')
  const parsedPort = Number(port)
  const valid =
    Number.isInteger(parsedPort) && parsedPort >= 1 && parsedPort <= 65535

  useEffect(() => {
    if (preview) return
    let active = true
    Bridge.getProxyStatus()
      .then(next => {
        if (!active) return
        setStatus(next)
        setPort(String(next.port))
      })
      .catch(err => {
        if (active) setQueryError(String(err))
      })
    return () => {
      active = false
    }
  }, [preview, checks])

  const recheck = () => {
    setQueryError('')
    setChecks(n => n + 1)
  }

  const retry = async () => {
    if (!valid || busy) return
    setBusy(true)
    setRetryError('')
    try {
      const next = await Bridge.retryProxy(parsedPort)
      setStatus(next)
      if (next.running) {
        usePrefs.getState().set({ port: String(next.port) })
        // 监听已就绪；回读开关失败只影响开关显示，不作为启动错误。
        const cfg = await Bridge.getConfig().catch(() => undefined)
        if (cfg) usePrefs.getState().set({ systemProxy: cfg.systemProxy })
      }
    } catch (err) {
      setRetryError(String(err))
    } finally {
      setBusy(false)
    }
  }

  if (preview || status?.running) return children

  if (queryError) {
    return (
      <StartupShell>
        <section className={cardClass}>
          <CardTitle>{t('proxyStartup.queryFailed')}</CardTitle>
          <p role="alert" className="mt-3 break-words text-sm text-fg-muted">
            {queryError}
          </p>
          <footer className="mt-5 flex justify-end gap-2">
            <Button onClick={() => void Application.Quit()}>
              {t('proxyStartup.quit')}
            </Button>
            <Button variant="primary" onClick={recheck}>
              {t('proxyStartup.recheck')}
            </Button>
          </footer>
        </section>
      </StartupShell>
    )
  }

  if (!status) {
    return (
      <StartupShell>
        <p role="status">{t('proxyStartup.checking')}</p>
      </StartupShell>
    )
  }

  return (
    <StartupShell>
      <form
        className={cardClass}
        onSubmit={event => {
          event.preventDefault()
          void retry()
        }}
      >
        <CardTitle>{t('proxyStartup.title')}</CardTitle>
        <p role="alert" className="mt-3 text-sm text-fg-muted">
          {t(`proxyStartup.${status.errorCode ?? 'listen_failed'}`, {
            port: status.port,
          })}
        </p>
        <p className="mt-2 text-sm text-fg-muted">{t('proxyStartup.help')}</p>
        <label className="mt-5 flex items-center gap-3 text-sm text-fg">
          {t('proxyStartup.port')}
          <TextInput
            autoFocus
            type="number"
            min={1}
            max={65535}
            step={1}
            value={port}
            disabled={busy}
            onChange={event => setPort(event.target.value)}
            aria-invalid={!valid}
            aria-describedby="proxy-port-hint"
            width={120}
          />
        </label>
        <p id="proxy-port-hint" className="mt-2 text-2xs text-fg-muted">
          {t(valid ? 'proxyStartup.saveHint' : 'proxyStartup.invalidPort')}
        </p>
        {status.error && (
          <details className="mt-4 text-2xs text-fg-muted">
            <summary className="cursor-pointer">
              {t('proxyStartup.details')}
            </summary>
            <p className="mt-2 break-words whitespace-pre-wrap">
              {status.error}
            </p>
          </details>
        )}
        {retryError && (
          <p role="alert" className="mt-3 break-words text-sm text-danger">
            {retryError}
          </p>
        )}
        <footer className="mt-5 flex justify-end gap-2">
          <Button onClick={() => void Application.Quit()}>
            {t('proxyStartup.quit')}
          </Button>
          <Button type="submit" variant="primary" disabled={!valid || busy}>
            {t(busy ? 'proxyStartup.starting' : 'proxyStartup.retry')}
          </Button>
        </footer>
      </form>
    </StartupShell>
  )
}
