import { headerEntries } from './format'
import type { TrafficRow } from './types'
import { saveFile } from './download'

/** 时间戳 → 文件名片段（YYYYMMDD-HHmmss） */
function stamp(): string {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
}

function headersArray(h?: Record<string, string>, rawB64?: Record<string, string>): { name: string; value: string }[] {
  if (!h) return []
  return headerEntries(h, rawB64).map(([name, value]) => ({ name, value }))
}

function queryArray(url: string): { name: string; value: string }[] {
  const qi = url.indexOf('?')
  if (qi < 0) return []
  // URL 参数导出时单独处理 #fragment。
  const hi = url.indexOf('#', qi)
  const raw = url.slice(qi + 1, hi < 0 ? undefined : hi)
  const out: { name: string; value: string }[] = []
  for (const pair of raw.split('&')) {
    if (!pair) continue
    const eq = pair.indexOf('=')
    const name = eq >= 0 ? pair.slice(0, eq) : pair
    const value = eq >= 0 ? pair.slice(eq + 1) : ''
    try {
      out.push({ name: decodeURIComponent(name), value: decodeURIComponent(value) })
    } catch {
      out.push({ name, value })
    }
  }
  return out
}

/** 把 HTTP 流量行序列化为 HAR 1.2 并触发下载。 */
export function exportHar(rows: TrafficRow[], version: string): void {
  const httpRows = rows.filter((r) => r.kind === 'http')
  const entries = httpRows.map((r) => {
    const reqBodyBytes = r.reqBody ? new TextEncoder().encode(r.reqBody).length : 0
    const resBodyBytes = r.sizeBytes ?? (r.resBody ? new TextEncoder().encode(r.resBody).length : 0)
    return {
      startedDateTime: new Date(r.startedAt).toISOString(),
      time: r.durationMs ?? 0,
      request: {
        method: r.method,
        url: r.url,
        httpVersion: 'HTTP/1.1',
        cookies: [],
        headers: headersArray(r.reqHeaders, r.reqHeadersB64),
        queryString: queryArray(r.url),
        headersSize: -1,
        bodySize: reqBodyBytes,
        ...(r.reqBody
          ? { postData: { mimeType: r.contentType || 'text/plain', text: r.reqBody } }
          : {}),
      },
      response: {
        status: r.status ?? 0,
        statusText: r.statusText ?? '',
        httpVersion: 'HTTP/1.1',
        cookies: [],
        headers: headersArray(r.resHeaders, r.resHeadersB64),
        content: {
          size: resBodyBytes,
          mimeType: r.contentType || 'application/octet-stream',
          ...(r.resBody ? { text: r.resBody } : {}),
        },
        redirectURL: '',
        headersSize: -1,
        bodySize: resBodyBytes,
      },
      cache: {},
      timings: { send: 0, wait: r.durationMs ?? 0, receive: 0 },
      // clientIP 表示下游客户端地址，使用自定义字段保留该语义。
      ...(r.clientIP ? { _clientIPAddress: r.clientIP } : {}),
    }
  })

  const har = {
    log: {
      version: '1.2',
      creator: { name: 'Sniffy', version },
      entries,
    },
  }
  void saveFile(JSON.stringify(har, null, 2), `sniffy-${stamp()}.har`)
}

/** 把流量行序列化为简洁 JSON 并触发下载（含 HTTP 与 WS）。 */
export function exportJson(rows: TrafficRow[]): void {
  const data = rows.map((r) => ({
    seq: r.seq,
    kind: r.kind,
    method: r.method,
    scheme: r.scheme,
    host: r.host,
    path: r.path,
    url: r.url,
    status: r.status,
    statusText: r.statusText,
    state: r.state,
    contentType: r.contentType,
    durationMs: r.durationMs,
    sizeBytes: r.sizeBytes,
    clientIP: r.clientIP,
    process: r.process,
    startedAt: new Date(r.startedAt).toISOString(),
    reqHeaders: r.reqHeaders && Object.fromEntries(headerEntries(r.reqHeaders, r.reqHeadersB64)),
    resHeaders: r.resHeaders && Object.fromEntries(headerEntries(r.resHeaders, r.resHeadersB64)),
    reqBody: r.reqBody,
    resBody: r.resBody,
  }))
  void saveFile(JSON.stringify(data, null, 2), `sniffy-${stamp()}.json`)
}
