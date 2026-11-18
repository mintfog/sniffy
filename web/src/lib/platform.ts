export type Platform = 'windows' | 'mac' | 'linux'

/** 同步读取 WebView 的平台标识，标题栏可在首帧选择布局。 */
export function detectPlatform(): Platform {
  const ua = typeof navigator !== 'undefined' ? navigator.userAgent : ''
  if (/Macintosh|Mac OS X/i.test(ua)) return 'mac'
  if (/Windows/i.test(ua)) return 'windows'
  return 'linux'
}

/** Windows / Linux：无边框窗口，标题栏与窗口按钮由前端自绘。 */
export function usesSelfDrawnChrome(): boolean {
  const p = detectPlatform()
  return p === 'windows' || p === 'linux'
}

/**
 * 是否运行在 Wails 原生 WebView 中。与 Wails 运行时判定浏览器环境的依据一致：WebView2 与
 * WKWebView / WebKitGTK 在页面脚本执行前注入消息通道，浏览器直开预览时不存在。
 */
export function hasWailsWebView(): boolean {
  const w = window as Window & {
    chrome?: { webview?: { postMessage?: unknown } }
    webkit?: { messageHandlers?: { external?: { postMessage?: unknown } } }
  }
  return Boolean(
    w.chrome?.webview?.postMessage ||
      w.webkit?.messageHandlers?.external?.postMessage
  )
}

/** 是否运行在桌面端。纯桌面形态下恒为 true（保留以兼容调用点）。 */
export function isDesktop(): boolean {
  return true
}
