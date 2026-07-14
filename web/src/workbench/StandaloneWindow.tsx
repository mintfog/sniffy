import './theme/tokens.css'
import { Suspense, lazy } from 'react'
import { useTranslation } from 'react-i18next'
import { MiniTitleBar } from './shell/MiniTitleBar'

// 各视图按需切块：一个独立窗口只解析自己承载的那个视图，
// 尤其把 ~464K 的 CodeMirror 隔离到插件视图，不再随「设置/关于」等窗口一起加载。
const SettingsView = lazy(() => import('./views/SettingsView').then((m) => ({ default: m.SettingsView })))
const ToolboxView = lazy(() => import('./views/ToolboxView').then((m) => ({ default: m.ToolboxView })))
const AboutView = lazy(() => import('./views/AboutView').then((m) => ({ default: m.AboutView })))
const PluginsView = lazy(() => import('./views/PluginsView').then((m) => ({ default: m.PluginsView })))
const RulesView = lazy(() => import('./views/RulesView').then((m) => ({ default: m.RulesView })))

export type StandaloneKind = 'settings' | 'tools' | 'about' | 'plugins' | 'rules'

export function isStandaloneKind(v: string | null): v is StandaloneKind {
  return v === 'settings' || v === 'tools' || v === 'about' || v === 'plugins' || v === 'rules'
}

/** 独立系统窗口的外壳：精简标题栏 + 单一页面内容。主题/强调色由 App 的 usePrefsBridge 应用。 */
export default function StandaloneWindow({ kind }: { kind: StandaloneKind }) {
  const { t } = useTranslation()
  const titles: Record<StandaloneKind, string> = {
    settings: t('standalone.title.settings'),
    tools: t('standalone.title.tools'),
    about: t('standalone.title.about'),
    plugins: t('standalone.title.plugins'),
    rules: t('standalone.title.rules'),
  }
  return (
    <div className="wb-root flex h-screen w-screen flex-col overflow-hidden">
      {/* 标题栏不切块，随外壳同步渲染：窗口一挂载即出现，视图切块再流式补入。 */}
      {/* 各平台都渲染：mac 是托住红绿灯的拖拽条（系统标题已隐藏），见 MiniTitleBar 内分流。 */}
      <MiniTitleBar title={titles[kind]} />
      <div className="flex min-h-0 flex-1 flex-col">
        <Suspense fallback={<div className="min-h-0 flex-1 bg-surface" />}>
          {kind === 'settings' && <SettingsView />}
          {kind === 'tools' && <ToolboxView />}
          {kind === 'about' && <AboutView />}
          {kind === 'plugins' && <PluginsView />}
          {kind === 'rules' && <RulesView />}
        </Suspense>
      </div>
    </div>
  )
}
