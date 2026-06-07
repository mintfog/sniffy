import { detectContentKind } from '../lib/format'
import type { TrafficRow } from '../lib/types'

/**
 * Demo 流量 —— 当后端无数据时，让工作台直接跑起来并可“实时”追加，
 * 用于验证高密度表格 + 详情面板的观感与性能，不依赖任何后端。
 */

interface Endpoint {
  method: string
  scheme: 'http' | 'https' | 'ws' | 'wss'
  host: string
  path: string
  ct: string
  proc: string
  weightStatus?: number[] // 可能的状态码池
}

const PROCS = ['chrome.exe', 'msedge.exe', 'Code.exe', 'node.exe', 'curl.exe', 'WeChat.exe']

const ENDPOINTS: Endpoint[] = [
  { method: 'GET', scheme: 'https', host: 'api.github.com', path: '/repos/sniffy/sniffy/commits?per_page=30', ct: 'application/json', proc: 'chrome.exe' },
  { method: 'POST', scheme: 'https', host: 'api.github.com', path: '/graphql', ct: 'application/json', proc: 'chrome.exe', weightStatus: [200, 200, 200, 422] },
  { method: 'GET', scheme: 'https', host: 'cdn.jsdelivr.net', path: '/npm/react@18/umd/react.production.min.js', ct: 'application/javascript', proc: 'msedge.exe' },
  { method: 'GET', scheme: 'https', host: 'fonts.gstatic.com', path: '/s/inter/v13/UcCO3Fwr.woff2', ct: 'font/woff2', proc: 'chrome.exe' },
  { method: 'GET', scheme: 'https', host: 'www.google-analytics.com', path: '/g/collect?v=2&tid=G-XXX', ct: 'image/gif', proc: 'chrome.exe', weightStatus: [204, 204, 200] },
  { method: 'GET', scheme: 'https', host: 'avatars.githubusercontent.com', path: '/u/9919?s=64&v=4', ct: 'image/png', proc: 'chrome.exe' },
  { method: 'POST', scheme: 'https', host: 'api.openai.com', path: '/v1/chat/completions', ct: 'application/json', proc: 'Code.exe', weightStatus: [200, 200, 429] },
  { method: 'GET', scheme: 'https', host: 'registry.npmjs.org', path: '/typescript', ct: 'application/json', proc: 'node.exe' },
  { method: 'PUT', scheme: 'https', host: 'storage.example.com', path: '/uploads/asset-9f2c.bin', ct: 'application/octet-stream', proc: 'curl.exe', weightStatus: [200, 201] },
  { method: 'DELETE', scheme: 'https', host: 'api.example.com', path: '/v2/sessions/abc123', ct: 'application/json', proc: 'Code.exe', weightStatus: [200, 204, 404] },
  { method: 'PATCH', scheme: 'https', host: 'api.example.com', path: '/v2/users/42', ct: 'application/json', proc: 'chrome.exe' },
  { method: 'GET', scheme: 'http', host: 'detectportal.firefox.com', path: '/success.txt', ct: 'text/plain', proc: 'chrome.exe', weightStatus: [200, 302] },
  { method: 'GET', scheme: 'https', host: 'static.cloudflareinsights.com', path: '/beacon.min.js', ct: 'application/javascript', proc: 'msedge.exe' },
  { method: 'GET', scheme: 'https', host: 'assets.example.com', path: '/app/main.4f1a2b.css', ct: 'text/css', proc: 'chrome.exe' },
  { method: 'POST', scheme: 'https', host: 'telemetry.example.com', path: '/report', ct: 'application/json', proc: 'Code.exe', weightStatus: [200, 500, 503] },
  { method: 'GET', scheme: 'wss', host: 'gateway.example.com', path: '/socket?token=••••', ct: 'websocket', proc: 'chrome.exe' },
  { method: 'GET', scheme: 'https', host: 'i.ytimg.com', path: '/vi/dQw4/hqdefault.jpg', ct: 'image/jpeg', proc: 'msedge.exe' },
  { method: 'GET', scheme: 'https', host: 'update.code.visualstudio.com', path: '/api/update/win32-x64/stable/latest', ct: 'application/json', proc: 'Code.exe', weightStatus: [200, 204] },
]

const SAMPLE_JSON: Record<string, unknown> = {
  ok: true,
  requestId: 'req_8f2c91a0',
  data: {
    id: 9919,
    name: 'sniffy',
    full_name: 'sniffy/sniffy',
    private: false,
    stargazers_count: 1287,
    language: 'Go',
    topics: ['proxy', 'mitm', 'http', 'websocket'],
    owner: { login: 'sniffy', type: 'Organization' },
  },
  meta: { page: 1, perPage: 30, total: 412, hasNext: true },
}

// 用一个确定性 PRNG，避免在工作流/SSR 环境对 Math.random 的依赖问题（应用内可用，但保持可控）。
function makeRng(seed: number) {
  let s = seed >>> 0
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return s / 0xffffffff
  }
}

function pick<T>(rng: () => number, arr: T[]): T {
  return arr[Math.floor(rng() * arr.length)]
}

function buildRow(seq: number, startedAt: number, rng: () => number): TrafficRow {
  const ep = pick(rng, ENDPOINTS)
  const statusPool = ep.weightStatus ?? [200, 200, 200, 200, 304, 404]
  const isWs = ep.scheme === 'ws' || ep.scheme === 'wss'
  const status = isWs ? undefined : pick(rng, statusPool)
  const roll = rng()
  const state: TrafficRow['state'] = roll < 0.04 ? 'pending' : roll < 0.07 ? 'error' : 'completed'
  const url = `${ep.scheme}://${ep.host}${ep.path}`
  const reqHeaders: Record<string, string> = {
    Host: ep.host,
    'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
    Accept: ep.ct.includes('json') ? 'application/json, */*' : '*/*',
    'Accept-Encoding': 'gzip, deflate, br',
    Connection: 'keep-alive',
  }
  if (ep.method === 'POST' || ep.method === 'PUT' || ep.method === 'PATCH') {
    reqHeaders['Content-Type'] = 'application/json'
  }
  const resHeaders: Record<string, string> = {
    'Content-Type': ep.ct,
    Server: 'cloudflare',
    'Cache-Control': 'max-age=300',
    'Content-Encoding': 'gzip',
  }
  const resBody = ep.ct.includes('json') ? JSON.stringify(SAMPLE_JSON, null, 2) : undefined
  const reqBody =
    ep.method === 'POST' || ep.method === 'PUT' || ep.method === 'PATCH'
      ? JSON.stringify({ name: 'sniffy', enabled: true, ttl: 300 }, null, 2)
      : undefined

  return {
    id: `demo-${seq}-${Math.floor(rng() * 1e6)}`,
    seq,
    kind: isWs ? 'ws' : 'http',
    method: isWs ? 'WS' : ep.method,
    scheme: ep.scheme,
    host: ep.host,
    path: ep.path,
    url,
    status,
    statusText: status === 200 ? 'OK' : status === 204 ? 'No Content' : status === 404 ? 'Not Found' : status === 500 ? 'Internal Server Error' : status === 429 ? 'Too Many Requests' : undefined,
    state: isWs ? 'completed' : state,
    contentType: ep.ct,
    contentKind: detectContentKind(ep.ct, ep.path),
    durationMs: state === 'pending' ? undefined : Math.round(8 + rng() * 1400),
    sizeBytes: isWs ? Math.round(rng() * 8000) : Math.round(120 + rng() * 240_000),
    clientIP: `192.168.${Math.floor(rng() * 4)}.${10 + Math.floor(rng() * 200)}`,
    process: ep.proc || pick(rng, PROCS),
    startedAt,
    reqHeaders,
    resHeaders: isWs ? undefined : resHeaders,
    reqBody,
    resBody,
  }
}

/** 初始 demo 数据（oldest-first：seq 越大越新、越靠近底部） */
export function makeDemoRows(count = 60): TrafficRow[] {
  const rng = makeRng(20260607)
  const now = Date.now()
  const rows: TrafficRow[] = []
  for (let i = 0; i < count; i++) {
    const seq = i + 1
    const startedAt = now - (count - 1 - i) * (300 + Math.floor(rng() * 900))
    rows.push(buildRow(seq, startedAt, rng))
  }
  return rows // 已是 oldest-first（i=count-1 → seq=count → 最新，位于末尾）
}

/** 生成下一条 demo 行（实时流模拟用） */
export function makeDemoRowFactory(startSeq: number) {
  let seq = startSeq
  let tick = startSeq * 7919
  return () => {
    seq += 1
    tick += 1
    return buildRow(seq, Date.now(), makeRng(tick))
  }
}
