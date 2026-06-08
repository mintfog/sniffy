import { Bridge } from '@/lib/bridge'

/**
 * 保存文本到文件：弹出系统原生「保存」对话框，由 Go 直接写盘（不触发 WebView 下载栏）。
 * 返回是否已保存（用户取消返回 false）。
 *
 * 仅面向 Wails 桌面运行时——不提供浏览器回退路径。
 */
export function saveFile(content: string, filename: string): Promise<boolean> {
  return Bridge.saveTextFile(filename, content)
}
