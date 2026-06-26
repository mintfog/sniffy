import { Events } from '@wailsio/runtime'
import { Bridge } from '@/lib/bridge'
import type { ToolId } from '../views/ToolboxView'

const TOOLBOX_TOOL_KEY = 'sniffy-toolbox-tool'

/**
 * 打开设置窗口。返回 Promise；非 Wails 环境（浏览器预览）会 reject，
 * 调用方可据此回退到主窗口内嵌渲染。
 */
export function openSettingsWindow(): Promise<void> {
  return Bridge.openWindow('settings')
}

/** 打开关于窗口。 */
export function openAboutWindow(): Promise<void> {
  return Bridge.openWindow('about')
}

/** 打开（或聚焦）插件工作室窗口。 */
export function openPluginsWindow(): Promise<void> {
  return Bridge.openWindow('plugins')
}

/** 打开（或聚焦）重写规则窗口。 */
export function openRulesWindow(): Promise<void> {
  return Bridge.openWindow('rules')
}

/**
 * 打开工具箱窗口并选中指定工具。
 * - 写入 localStorage：新开窗口挂载时据此定位工具；
 * - 触发 toolbox_select 事件：已打开的窗口实时切换。
 */
export function openToolboxWindow(tool?: ToolId): Promise<void> {
  if (tool) {
    try {
      window.localStorage.setItem(TOOLBOX_TOOL_KEY, tool)
    } catch {
      /* ignore */
    }
  }
  const p = Bridge.openWindow('tools', tool ? `tool=${tool}` : '')
  if (tool) {
    // 已打开的窗口经事件切换（新窗口走 localStorage / URL）
    p.then(() => {
      try {
        void Events.Emit('toolbox_select', tool)
      } catch {
        /* ignore */
      }
    }).catch(() => {})
  }
  return p
}

/** 从子窗口请求主窗口切换视图并置前（如设置里的「管理证书」）。 */
export function requestMainNav(view: string): void {
  try {
    void Events.Emit('main_nav', view)
  } catch {
    /* ignore */
  }
  Bridge.focusMain().catch(() => {})
}
