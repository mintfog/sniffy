import type { Platform } from '@/lib/platform'

/** 应用名单的条目上限，与后端一致。 */
export const MAX_TUN_APPS = 256

/** 单项的长度上限（UTF-8 字节数），与后端一致。 */
export const MAX_TUN_APP_BYTES = 32767

export type TunAppsProblem =
  | { kind: 'relative'; value: string }
  | { kind: 'foreign'; value: string }
  | { kind: 'invalid'; value: string }
  | { kind: 'tooMany'; max: number }

export type TunAppsParse = { list: string[]; problem?: undefined } | { list?: undefined; problem: TunAppsProblem }

const WINDOWS_SEPARATOR = /[\\/]/
// 与后端的 unicode.IsControl 一致：C0、DEL 与 C1 控制字符。
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f-\u009f]/

/**
 * 把输入框的内容拆成应用名单：每行一项，程序名或本机写法的绝对路径（程序名里可以有空格，如 Google
 * Chrome）。规则与后端在 platform 上的一致：Windows 上反斜杠与斜杠都是分隔符，其它平台只认斜杠，
 * 带反斜杠的是 Windows 的写法。返回去掉首尾空白、空行与重复行后的各项（路径的规范化由后端完成），
 * 或第一处问题。
 */
export function parseTunApps(text: string, platform: Platform): TunAppsParse {
  const windows = platform === 'windows'
  const items = [...new Set(text.split(/\r?\n/).map((s) => s.trim()).filter(Boolean))]
  for (const value of items) {
    if (CONTROL.test(value) || value === '.' || value === '..' || utf8Length(value) > MAX_TUN_APP_BYTES) {
      return { problem: { kind: 'invalid', value } }
    }
    if (!windows && value.includes('\\')) return { problem: { kind: 'foreign', value } }
    const path = windows ? windowsPath(value) : unixPath(value)
    if (path && !path.absolute) return { problem: { kind: 'relative', value } }
  }
  if (items.length > MAX_TUN_APPS) return { problem: { kind: 'tooMany', max: MAX_TUN_APPS } }
  return { list: items }
}

/**
 * 按 Go 在 Windows 上的 filepath.VolumeName 与 IsAbs 判断：带盘符或分隔符的是路径，不是程序名。盘符后
 * 紧跟分隔符、或以两个分隔符开头（UNC、设备路径）的是绝对路径；带盘符而不带分隔符的（C:app.exe）是
 * 相对于该盘当前目录的路径，斜杠开头而不带盘符的（Git Bash 的 /c/Users/…）同样不是绝对路径。
 */
function windowsPath(value: string): { absolute: boolean } | undefined {
  // Go 按字节看第 2 个是不是冒号，第 1 个是任意 ASCII 字符都算盘符（1:\x 也是）；第 1 个字符不是 ASCII
  // 时，第 2 个字节落在它的 UTF-8 编码里面。
  const drive = value[1] === ':' && value.charCodeAt(0) < 0x80
  if (drive) return { absolute: WINDOWS_SEPARATOR.test(value.charAt(2)) }
  if (!WINDOWS_SEPARATOR.test(value)) return undefined
  return { absolute: /^[\\/]{2}/.test(value) }
}

function unixPath(value: string): { absolute: boolean } | undefined {
  return value.includes('/') ? { absolute: value.startsWith('/') } : undefined
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length
}
