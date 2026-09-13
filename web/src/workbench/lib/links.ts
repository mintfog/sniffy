import { Browser } from '@wailsio/runtime'

export const REPO_URL = 'https://github.com/mintfog/sniffy'
export const DOCS_URL = 'https://github.com/mintfog/sniffy#readme'
export const RELEASES_URL = 'https://github.com/mintfog/sniffy/releases'
/** 浏览器演示版本，由 Vite 从 package.json 注入。 */
export const APP_VERSION = __APP_VERSION__

/** 用系统默认浏览器打开外部链接（Wails 桌面运行时）。 */
export function openExternal(url: string): void {
  Browser.OpenURL(url).catch(() => {})
}
