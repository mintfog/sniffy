import { ExternalLink, Github, RefreshCw, Radar } from 'lucide-react'
import { Button } from '../ui/controls'
import { APP_VERSION, DOCS_URL, RELEASES_URL, REPO_URL, openExternal } from '../lib/links'

/** 关于面板 */
export function AboutView() {
  return (
    <div className="wb-scroll flex h-full min-h-0 flex-col items-center overflow-auto bg-base px-6 py-8 text-center">
      <span className="flex h-16 w-16 items-center justify-center rounded-2xl bg-accent text-accent-fg shadow-wb">
        <Radar className="h-9 w-9" />
      </span>
      <h1 className="mt-4 text-xl font-semibold tracking-tight text-fg">Sniffy</h1>
      <div className="mt-1 font-mono text-[12px] text-fg-muted">版本 {APP_VERSION}</div>
      <p className="mt-3 max-w-sm text-[12.5px] leading-relaxed text-fg-muted">
        HTTP / HTTPS 抓包代理工具，支持 HTTPS MITM 解密、WebSocket、重写规则、断点与插件系统。
      </p>

      <div className="mt-6 w-full max-w-sm divide-y divide-line overflow-hidden rounded-wb border border-line bg-surface text-left">
        <Row label="内核" value="Go · net/http MITM" />
        <Row label="前端" value="React · Vite · Wails v3" />
        <Row label="许可" value="Apache-2.0" />
      </div>

      <div className="mt-6 flex flex-wrap items-center justify-center gap-2">
        <Button icon={<Github className="h-3.5 w-3.5" />} onClick={() => openExternal(REPO_URL)}>
          项目主页
        </Button>
        <Button icon={<ExternalLink className="h-3.5 w-3.5" />} onClick={() => openExternal(DOCS_URL)}>
          文档
        </Button>
        <Button icon={<RefreshCw className="h-3.5 w-3.5" />} onClick={() => openExternal(RELEASES_URL)}>
          检查更新
        </Button>
      </div>

      <div className="mt-8 text-2xs text-fg-faint">© 2026 mintfog · 仅供合法授权的调试与研究</div>
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-2.5">
      <span className="text-[12.5px] text-fg-muted">{label}</span>
      <span className="font-mono text-[12px] text-fg">{value}</span>
    </div>
  )
}
