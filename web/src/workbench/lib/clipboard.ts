import { headerEntries } from './format'
import type { TrafficRow } from './types'

/** 写剪贴板；Clipboard API 不可用（非安全上下文等）时回退 execCommand */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      ta.remove()
      return ok
    } catch {
      return false
    }
  }
}

/** headers 对象 → "Key: Value" 多行文本；字节旁路值按 Latin-1 渲染 */
export function headersToText(headers?: Record<string, string>, rawB64?: Record<string, string>): string {
  return headerEntries(headers, rawB64)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n')
}

const shQuote = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`
// cmd 下用双引号包裹，内部双引号转义为 \"（curl.exe 的 MSVCRT 参数解析）。
const cmdQuote = (s: string) => `"${s.replace(/"/g, '\\"')}"`

/** cURL 命令的目标 shell：bash 用单引号 + 反斜杠续行；cmd 用双引号 + 脱字符续行。 */
export type CurlShell = 'bash' | 'cmd'

/** 由行模型构造 cURL 命令（headers/body 缺省时只含 method+url） */
export function buildCurl(row: TrafficRow, shell: CurlShell = 'bash'): string {
  const q = shell === 'cmd' ? cmdQuote : shQuote
  // cmd 续行符 ^ 必须是行末最后一个字符，故 ^ 后紧跟换行。
  const joiner = shell === 'cmd' ? ' ^\n  ' : ' \\\n  '
  const parts = [`curl ${q(row.url)}`]
  const method = row.method.toUpperCase()
  if (method !== 'GET' && method !== 'WS') parts.push(`-X ${method}`)
  let hadAcceptEncoding = false
  for (const [k, v] of headerEntries(row.reqHeaders, row.reqHeadersB64)) {
     if (k.toLowerCase() === 'accept-encoding') {
      hadAcceptEncoding = true
      continue
    }
    parts.push(`-H ${q(`${k}: ${v}`)}`)
  }
  if (hadAcceptEncoding) parts.push('--compressed')
  if (row.reqBody) parts.push(`--data-raw ${q(row.reqBody)}`)
  return parts.join(joiner)
}
