import { useCallback } from 'react'
import { usePrefs, type ThemeMode } from '../prefs'

export type { ThemeMode }

/**
 * 工作台主题（深/亮）。
 *
 * 已并入统一偏好 store（见 ../prefs）：主题与强调色/密度等一同持久化、并跨窗口同步。
 * 本 hook 保留为兼容旧调用点的薄封装；CSS 变量的实际应用由 usePrefsBridge() 负责。
 */
export function useTheme() {
  const mode = usePrefs((s) => s.theme)
  const set = usePrefs((s) => s.set)

  const setMode = useCallback((m: ThemeMode) => set({ theme: m }), [set])
  const toggle = useCallback(() => set({ theme: mode === 'dark' ? 'light' : 'dark' }), [set, mode])

  return { mode, setMode, toggle, isDark: mode === 'dark' }
}
