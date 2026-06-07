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

/** headers 对象 → "Key: Value" 多行文本 */
export function headersToText(headers?: Record<string, string>): string {
  return Object.entries(headers ?? {})
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n')
}

const shQuote = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`

/** 由行模型构造 cURL 命令（headers/body 缺省时只含 method+url） */
export function buildCurl(row: TrafficRow): string {
  const parts = [`curl ${shQuote(row.url)}`]
  const method = row.method.toUpperCase()
  if (method !== 'GET' && method !== 'WS') parts.push(`-X ${method}`)
  for (const [k, v] of Object.entries(row.reqHeaders ?? {})) {
    parts.push(`-H ${shQuote(`${k}: ${v}`)}`)
  }
  if (row.reqBody) parts.push(`--data-raw ${shQuote(row.reqBody)}`)
  return parts.join(' \\\n  ')
}
