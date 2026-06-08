import { Browser } from '@wailsio/runtime'

export const REPO_URL = 'https://github.com/mintfog/sniffy'
export const DOCS_URL = 'https://github.com/mintfog/sniffy#readme'
export const RELEASES_URL = 'https://github.com/mintfog/sniffy/releases'
export const APP_VERSION = '0.1.0'

/** 用系统默认浏览器打开外部链接（Wails 桌面运行时）。 */
export function openExternal(url: string): void {
  Browser.OpenURL(url).catch(() => {})
}
