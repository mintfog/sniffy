import { headerEntries } from './format'
import type { TrafficRow } from './types'
import { saveFile } from './download'

function stamp(): string {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
}

export function exportJson(rows: TrafficRow[]): Promise<boolean> {
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
  return saveFile(JSON.stringify(data, null, 2), `sniffy-${stamp()}.json`)
}
