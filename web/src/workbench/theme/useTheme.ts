import { useCallback, useEffect, useState } from 'react'

export type ThemeMode = 'dark' | 'light'

const STORAGE_KEY = 'sniffy-theme'

function readStored(): ThemeMode {
  if (typeof window === 'undefined') return 'dark'
  // URL 覆盖（便于直接分享/截图特定主题）：?theme=light|dark
  const url = new URLSearchParams(window.location.search).get('theme')
  if (url === 'light' || url === 'dark') return url
  const v = window.localStorage.getItem(STORAGE_KEY)
  if (v === 'light' || v === 'dark') return v
  // 默认深色（桌面级抓包工具的标志性外观），可手动切换
  return 'dark'
}

function apply(mode: ThemeMode) {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('data-theme', mode)
}

/**
 * 工作台主题：深/亮可切换。
 * 写入 <html data-theme>，tokens.css 据此切换全部 CSS 变量。
 * 仅在工作台挂载期间生效；旧页面用硬编码亮色，不受影响。
 */
export function useTheme() {
  const [mode, setMode] = useState<ThemeMode>(readStored)

  useEffect(() => {
    apply(mode)
    window.localStorage.setItem(STORAGE_KEY, mode)
  }, [mode])

  const toggle = useCallback(() => {
    setMode((m) => (m === 'dark' ? 'light' : 'dark'))
  }, [])

  return { mode, setMode, toggle, isDark: mode === 'dark' }
}
