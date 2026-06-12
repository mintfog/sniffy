/**
 * 运行平台检测。用于标题栏/菜单分流：
 *   - windows：Wails v3 无边框，标题栏（含窗口按钮）由前端自绘。
 *   - mac：Wails v3 透明原生标题栏（HiddenInset，红绿灯悬浮）；TitleBar/MiniTitleBar
 *     以 mac 模式渲染为托住红绿灯的主题色拖拽条，菜单走顶部系统菜单栏
 *     （见 workbench/shell/nativeMenu.ts）。
 *   - linux：Wails v3 原生窗口装饰，前端把 TitleBar 当普通菜单栏自绘在窗口内。
 *
 * 应用已是纯桌面形态（Wails v3，不再考虑网页版），平台仅区分三种桌面 OS。
 * 基于 navigator.userAgent 同步检测（WebView2 / WKWebView / WebKitGTK 的 UA 均含 OS 标识），
 * 首帧即正确、无闪烁、无需等待运行时 IPC。
 */
export type Platform = 'windows' | 'mac' | 'linux'

export function detectPlatform(): Platform {
  const ua = typeof navigator !== 'undefined' ? navigator.userAgent : ''
  if (/Macintosh|Mac OS X/i.test(ua)) return 'mac'
  if (/Windows/i.test(ua)) return 'windows'
  return 'linux'
}

/** 是否运行在桌面端。纯桌面形态下恒为 true（保留以兼容调用点）。 */
export function isDesktop(): boolean {
  return true
}
