import './theme/tokens.css'
import { useTranslation } from 'react-i18next'
import { MiniTitleBar } from './shell/MiniTitleBar'
import { SettingsView } from './views/SettingsView'
import { ToolboxView } from './views/ToolboxView'
import { AboutView } from './views/AboutView'
import { PluginsView } from './views/PluginsView'

export type StandaloneKind = 'settings' | 'tools' | 'about' | 'plugins'

export function isStandaloneKind(v: string | null): v is StandaloneKind {
  return v === 'settings' || v === 'tools' || v === 'about' || v === 'plugins'
}

/** 独立系统窗口的外壳：精简标题栏 + 单一页面内容。主题/强调色由 App 的 usePrefsBridge 应用。 */
export default function StandaloneWindow({ kind }: { kind: StandaloneKind }) {
  const { t } = useTranslation()
  const titles: Record<StandaloneKind, string> = {
    settings: t('standalone.title.settings'),
    tools: t('standalone.title.tools'),
    about: t('standalone.title.about'),
    plugins: t('standalone.title.plugins'),
  }
  return (
    <div className="wb-root flex h-screen w-screen flex-col overflow-hidden">
      {/* 各平台都渲染：mac 是托住红绿灯的拖拽条（系统标题已隐藏），见 MiniTitleBar 内分流。 */}
      <MiniTitleBar title={titles[kind]} />
      <div className="flex min-h-0 flex-1 flex-col">
        {kind === 'settings' && <SettingsView />}
        {kind === 'tools' && <ToolboxView />}
        {kind === 'about' && <AboutView />}
        {kind === 'plugins' && <PluginsView />}
      </div>
    </div>
  )
}
