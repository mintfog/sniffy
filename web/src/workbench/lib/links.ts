import { Browser } from '@wailsio/runtime'

export const REPO_URL = 'https://github.com/mintfog/sniffy'
export const DOWNLOAD_URL = 'https://gosniffy.com/#download'
/** 浏览器演示版本，由 Vite 从 package.json 注入。 */
export const APP_VERSION = __APP_VERSION__

/** 文档站地址；英文界面直接落在英文文档上。 */
export function docsUrl(lang: string): string {
  return lang.startsWith('zh') ? 'https://gosniffy.com/docs/' : 'https://gosniffy.com/en/docs/'
}

/** 用系统默认浏览器打开外部链接（Wails 桌面运行时）。 */
export function openExternal(url: string): void {
  Browser.OpenURL(url).catch(() => {})
}
