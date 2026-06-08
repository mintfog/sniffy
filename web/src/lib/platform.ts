/**
 * 运行平台检测。用于标题栏分流：
 *   - windows：Wails 无边框，标题栏（含窗口按钮）由前端自绘。
 *   - mac：Wails 原生集成标题栏（系统红绿灯），前端只需给左上留白。
 *   - linux：Wails 原生窗口装饰，前端不自绘。
 *   - web：浏览器 / headless，无窗口外观需求。
 *
 * 同步检测（基于 window.runtime + userAgent），首帧即正确，无闪烁。
 */
export type Platform = 'windows' | 'mac' | 'linux' | 'web'

export function detectPlatform(): Platform {
  if (typeof window === 'undefined') return 'web'
  const rt = (window as unknown as { runtime?: { WindowMinimise?: unknown } }).runtime
  // 非 Wails 桌面端（浏览器/headless）：window.runtime 不存在
  if (typeof rt?.WindowMinimise !== 'function') return 'web'
  const ua = navigator.userAgent
  if (/Macintosh|Mac OS X/i.test(ua)) return 'mac'
  if (/Windows/i.test(ua)) return 'windows'
  return 'linux'
}

/** 是否运行在 Wails 桌面端（任意 OS） */
export function isDesktop(): boolean {
  return detectPlatform() !== 'web'
}
