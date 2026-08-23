import test from 'node:test'
import assert from 'node:assert/strict'
import type { CurlImport, CurlWarning } from './curl.ts'
import { looksLikeCurl, parseCurl, shellQuote, tokenizeCurl } from './curl.ts'
import type { Draft } from './model.ts'

/* ───────────────────────── 断言小工具 ───────────────────────── */

function good(r: CurlImport): { draft: Draft; warnings: CurlWarning[] } {
  assert.equal(r.ok, true, r.ok ? '' : `expected ok, got error ${r.error.code}`)
  const okr = r as Extract<CurlImport, { ok: true }>
  return { draft: okr.draft, warnings: okr.warnings }
}

/** 头行去掉尾部那条永远存在的空行，还原成有序 [名, 值] 列表。 */
function headers(d: Draft): [string, string][] {
  return d.headers.filter((h) => h.name !== '' || h.value !== '').map((h) => [h.name, h.value] as [string, string])
}

function headerValue(d: Draft, name: string): string | undefined {
  const hit = headers(d).find(([n]) => n.toLowerCase() === name.toLowerCase())
  return hit?.[1]
}

const codes = (ws: CurlWarning[]) => ws.map((w) => w.code)

/** DetailPanel 的 rowToCurl 等价物：往返测试的另一半，钉住导出/导入闭环。 */
function rowToCurl(method: string, url: string, hs: [string, string][], body: string): string {
  let curl = `curl -X ${method} ${shellQuote(url)}`
  for (const [k, v] of hs) curl += ` \\\n  -H ${shellQuote(`${k}: ${v}`)}`
  if (body) curl += ` \\\n  --data-raw ${shellQuote(body)}`
  return curl
}

function roundTrip(method: string, url: string, hs: [string, string][], body: string) {
  const { draft } = good(parseCurl(rowToCurl(method, url, hs, body)))
  assert.equal(draft.method, method)
  assert.equal(draft.url, url)
  assert.deepEqual(headers(draft), hs)
  assert.equal(draft.body, body)
}

/* ───────────────────────── A 自吐自吃（往返守卫） ───────────────────────── */

test('A1 基本 POST + JSON 往返', () => {
  roundTrip('POST', 'https://api.example.com/v1/items', [['Content-Type', 'application/json'], ['Accept', 'application/json']], '{"a":1,"b":"x"}')
})

test('A2 体含单引号的往返（shellQuote 回归守卫）', () => {
  roundTrip('POST', 'https://api.example.com/v1/say', [['Content-Type', 'application/json']], '{"msg":"it\'s"}')
})

test('A3 URL 含单引号的往返', () => {
  roundTrip('GET', "https://api.example.com/s?q=it's", [], '')
})

test('A4 头值含双引号与分号的往返', () => {
  roundTrip('GET', 'https://api.example.com/x', [['X-Test', 'a"b; c=d'], ['Cookie', 'a=1; b=2']], '')
})

/* ───────────────────────── B 浏览器形态 ───────────────────────── */

test('B1 Chrome bash 完整样本', () => {
  const cmd = [
    "curl 'https://api.example.com/v1/items?page=2' \\",
    "  -H 'accept: application/json' \\",
    "  -H 'content-type: application/json' \\",
    "  -H 'cookie: sid=abc; theme=dark' \\",
    '  --data-raw \'{"name":"x"}\' \\',
    '  --compressed',
  ].join('\n')
  const { draft, warnings } = good(parseCurl(cmd))
  assert.equal(draft.method, 'POST')
  assert.equal(draft.url, 'https://api.example.com/v1/items?page=2')
  assert.equal(draft.body, '{"name":"x"}')
  assert.equal(headerValue(draft, 'accept'), 'application/json')
  assert.equal(headerValue(draft, 'cookie'), 'sid=abc; theme=dark')
  assert.ok(headerValue(draft, 'accept-encoding'))
  assert.deepEqual(codes(warnings), ['compressedAdded'])
})

test('B2 Chrome cmd 样本：^ 续行 + "" + "%" + 双写反斜杠', () => {
  const cmd = [
    'curl "https://api.example.com/v1/items" ^',
    '  -H "accept: application/json" ^',
    '  -H "x-pct: 50"%"" ^',
    '  --data-raw "{""a"":1,""p"":""C:\\\\tmp""}"',
  ].join('\n')
  const { draft } = good(parseCurl(cmd))
  assert.equal(draft.method, 'POST')
  assert.equal(draft.url, 'https://api.example.com/v1/items')
  assert.equal(headerValue(draft, 'x-pct'), '50%')
  assert.equal(draft.body, '{"a":1,"p":"C:\\tmp"}')
})

test('B3 ANSI-C 引用 $\'\\x7b…\' 还原为 JSON', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' --data-raw $'\\x7b\\x22a\\x22\\x3a1\\x7d'"))
  assert.equal(draft.body, '{"a":1}')
  assert.ok(!codes(warnings).includes('binaryBodyLossy'))
})

test('B4 ANSI-C 里的高位字节标记为有损', () => {
  const { warnings } = good(parseCurl("curl 'https://x.test/' --data-raw $'\\xe4\\xbd\\xa0'"))
  assert.ok(codes(warnings).includes('binaryBodyLossy'))
})

test('B5 Windows 形态 "abc"^换行"def" 产出字面换行', () => {
  const cmd = 'curl "https://x.test/" ^\n  --data-raw "abc"^\n"def"'
  const { draft } = good(parseCurl(cmd))
  assert.equal(draft.body, 'abc\ndef')
})

test('B5b 新版 Chrome cmd 形态：每个引号都写成 ^"，与 bash 形态解析结果一致', () => {
  // cmd 的 ^ 先于程序的 argv 解析被吃掉，^" 是真定界引号、^\^" 还原成 \" 才读成字面引号。
  // 这两条命令是同一次请求的两种复制形态，解析结果必须逐字相同。
  const win = [
    'curl --url ^"https://x.test/api?page=1^&limit=10^" ^',
    '  -H ^"accept: */*^" ^',
    '  -H ^"sec-ch-ua: ^\\^"Not=A?Brand^\\^";v=^\\^"99^\\^", ^\\^"Chromium^\\^";v=^\\^"151^\\^"^" ^',
    '  -H ^"sec-ch-ua-platform: ^\\^"Windows^\\^"^" ^',
    '  -H ^"priority: u=1, i^" ^',
    '  -H ^"xid;^"',
  ].join('\n')
  const posix = [
    "curl --url 'https://x.test/api?page=1&limit=10' \\",
    "  -H 'accept: */*' \\",
    '  -H \'sec-ch-ua: "Not=A?Brand";v="99", "Chromium";v="151"\' \\',
    '  -H \'sec-ch-ua-platform: "Windows"\' \\',
    "  -H 'priority: u=1, i' \\",
    "  -H 'xid;'",
  ].join('\n')
  const shape = (c: string) => {
    const r = good(parseCurl(c))
    return { url: r.draft.url, method: r.draft.method, headers: headers(r.draft), body: r.draft.body }
  }
  const got = shape(win)
  assert.deepEqual(got, shape(posix))
  assert.equal(got.url, 'https://x.test/api?page=1&limit=10')
  // ^\^" 这一层如果没还原对，值里会残留反斜杠或提前断句。
  assert.equal(got.headers.find(([n]) => n === 'sec-ch-ua')?.[1], '"Not=A?Brand";v="99", "Chromium";v="151"')
  assert.equal(got.headers.find(([n]) => n === 'xid')?.[1], '')
})

test('B5c 单行的新版 cmd 形态也能被认成 windows 方言', () => {
  // 没有续行脱字号可认时，靠 ^" 判定方言；漏判会让 ^ 原样留在值里。
  const { draft } = good(parseCurl('curl ^"https://x.test/a^" -H ^"accept: */*^"'))
  assert.equal(draft.url, 'https://x.test/a')
  assert.deepEqual(headers(draft), [['accept', '*/*']])
})

test('B6 Firefox 样本：-H Cookie 与 -b 并存时不重复', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/a' -X POST -H 'Cookie: sid=1' -b 'sid=2' --data-raw 'a=1'"))
  const cookies = headers(draft).filter(([n]) => n.toLowerCase() === 'cookie')
  assert.equal(cookies.length, 1)
  assert.equal(cookies[0][1], 'sid=1')
})

test('B7 HTTP/2 伪头被丢弃', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -H ':authority: x.test' -H ':method: GET' -H 'accept: */*'"))
  assert.deepEqual(headers(draft), [['accept', '*/*']])
  assert.ok(codes(warnings).includes('pseudoHeaderDropped'))
})

/* ───────────────────────── C 选项形态 ───────────────────────── */

test('C1 -XPOST 粘连值', () => {
  const { draft } = good(parseCurl("curl -XPOST 'https://x.test/' -d 'a=1'"))
  assert.equal(draft.method, 'POST')
  assert.equal(draft.body, 'a=1')
})

test('C2 -sSL 聚簇解析,并就 -L 单独提示', () => {
  const { draft, warnings } = good(parseCurl("curl -sSL 'https://x.test/'"))
  assert.equal(draft.url, 'https://x.test/')
  // -s/-S 与本地行为一致故静默;-L 相反——上游客户端一律不代跟随，请求会停在 30x。
  assert.deepEqual(codes(warnings), ['locationIgnored'])
})

test('C2b --location 与 --location-trusted 同样提示,且不混进 optionIgnored', () => {
  for (const flag of ['--location', '--location-trusted']) {
    const { warnings } = good(parseCurl(`curl ${flag} 'https://x.test/'`))
    assert.deepEqual(codes(warnings), ['locationIgnored'], flag)
  }
})

test('C2c 没写 -L 就不该冒出重定向提示', () => {
  const { warnings } = good(parseCurl("curl 'https://x.test/'"))
  assert.ok(!codes(warnings).includes('locationIgnored'))
})

test('C3 --data-raw= 等号形式', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' --data-raw='{\"a\":1}'"))
  assert.equal(draft.body, '{"a":1}')
})

test('C4 --url 与 -X PUT', () => {
  const { draft } = good(parseCurl("curl --url 'https://x.test/p' -X PUT"))
  assert.equal(draft.url, 'https://x.test/p')
  assert.equal(draft.method, 'PUT')
})

test('C5 -X post 归一为 POST 且不提示', () => {
  const { draft, warnings } = good(parseCurl("curl -X post 'https://x.test/'"))
  assert.equal(draft.method, 'POST')
  assert.deepEqual(warnings, [])
})

test('C6 -X PROPFIND 原样保留并提示', () => {
  const { draft, warnings } = good(parseCurl("curl -X PROPFIND 'https://x.test/'"))
  assert.equal(draft.method, 'PROPFIND')
  const w = warnings.find((x) => x.code === 'unusualMethod')
  assert.equal(w?.params?.method, 'PROPFIND')
})

/* ───────────────────────── D data / -G ───────────────────────── */

test('D1 多个 -d 以 & 相连并补 urlencoded 头', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' -d a=1 -d b=2"))
  assert.equal(draft.method, 'POST')
  assert.equal(draft.body, 'a=1&b=2')
  assert.equal(headerValue(draft, 'content-type'), 'application/x-www-form-urlencoded')
})

test('D2 -G 把 data 整体挪到查询串且不编码、不补 Content-Type', () => {
  const { draft } = good(parseCurl("curl -G 'https://x.test/s' -d a=1 -d 'b= c'"))
  assert.equal(draft.method, 'GET')
  assert.equal(draft.url, 'https://x.test/s?a=1&b= c')
  assert.equal(draft.body, '')
  assert.equal(headerValue(draft, 'content-type'), undefined)
})

test('D3 -G --data-urlencode 按 curl_easy_escape 编码', () => {
  const { draft } = good(parseCurl("curl -G 'https://x.test/s' --data-urlencode 'q=a b&c'"))
  assert.equal(draft.url, 'https://x.test/s?q=a%20b%26c')
})

test('D4 --data-urlencode 四形式', () => {
  const plain = good(parseCurl("curl -G 'https://x.test/s' --data-urlencode 'a b'"))
  assert.equal(plain.draft.url, 'https://x.test/s?a%20b')

  const eq = good(parseCurl("curl -G 'https://x.test/s' --data-urlencode '=a b'"))
  assert.equal(eq.draft.url, 'https://x.test/s?a%20b')

  const named = good(parseCurl("curl -G 'https://x.test/s' --data-urlencode 'n a=a b'"))
  assert.equal(named.draft.url, 'https://x.test/s?n a=a%20b')

  const file = good(parseCurl("curl -G 'https://x.test/s' --data-urlencode '@in.txt'"))
  assert.equal(file.draft.url, 'https://x.test/s')
  assert.ok(codes(file.warnings).includes('dataFileUnreadable'))
})

test('D5 --json 补 Content-Type 与 Accept 两条头', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' --json '{\"a\":1}'"))
  assert.equal(draft.body, '{"a":1}')
  assert.equal(headerValue(draft, 'content-type'), 'application/json')
  assert.equal(headerValue(draft, 'accept'), 'application/json')
})

test('D6 --json 与 -d 混用时提示 mixedDataKinds', () => {
  const { warnings } = good(parseCurl("curl 'https://x.test/' --json '{\"a\":1}' -d 'b=2'"))
  assert.ok(codes(warnings).includes('mixedDataKinds'))
})

test('D7 -d @file 无法读取：体为空但仍是 POST', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -d @body.json"))
  assert.equal(draft.method, 'POST')
  assert.equal(draft.body, '')
  const w = warnings.find((x) => x.code === 'dataFileUnreadable')
  assert.equal(w?.level, 'error')
  assert.equal(w?.params?.name, 'body.json')
})

test('D8 --data-raw 不解释 @', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' --data-raw '@body.json'"))
  assert.equal(draft.body, '@body.json')
  assert.ok(!codes(warnings).includes('dataFileUnreadable'))
})

test("D9 -d '' 体为空但仍是 POST 且补 Content-Type", () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' -d ''"))
  assert.equal(draft.method, 'POST')
  assert.equal(draft.body, '')
  assert.equal(headerValue(draft, 'content-type'), 'application/x-www-form-urlencoded')
})

test('D10 URL 已带查询串时用 & 追加', () => {
  const { draft } = good(parseCurl("curl -G 'https://x.test/s?x=1' -d 'y=2'"))
  assert.equal(draft.url, 'https://x.test/s?x=1&y=2')
})

/* ───────────────────────── E 头 / 认证 / cookie ───────────────────────── */

test('E1 -u 只切第一个冒号', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' -u 'user:pa:ss'"))
  assert.equal(headerValue(draft, 'authorization'), 'Basic dXNlcjpwYTpzcw==')
})

test('E2 -u 非 ASCII 走 UTF-8 base64 且不抛', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' -u '用户:密码'"))
  assert.equal(headerValue(draft, 'authorization'), 'Basic 55So5oi3OuWvhueggQ==')
})

test('E3 -u 无密码时补空密码并提示', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -u user"))
  assert.equal(headerValue(draft, 'authorization'), 'Basic dXNlcjo=')
  assert.ok(codes(warnings).includes('userNoPassword'))
})

test('E4 已有 Authorization 时 -H 胜出', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -H 'Authorization: Bearer t' -u 'a:b'"))
  assert.equal(headerValue(draft, 'authorization'), 'Bearer t')
  assert.ok(codes(warnings).includes('authHeaderConflict'))
})

test('E5 -b 带等号是 cookie 数据', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -b 'a=1; b=2'"))
  assert.equal(headerValue(draft, 'cookie'), 'a=1; b=2')
  assert.ok(!codes(warnings).includes('cookieFile'))
})

test('E6 -b 不带等号是 cookie 文件，不合成头', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -b cookies.txt"))
  assert.equal(headerValue(draft, 'cookie'), undefined)
  assert.ok(codes(warnings).includes('cookieFile'))
})

test('E7 -A / -e 合成 User-Agent 与 Referer', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' -A 'sniffy/1' -e 'https://ref.test/'"))
  assert.equal(headerValue(draft, 'user-agent'), 'sniffy/1')
  assert.equal(headerValue(draft, 'referer'), 'https://ref.test/')
})

test('E8 -H Name; 与 -H Name: 都产出空值头，-H Accept 被跳过', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -H 'X-A;' -H 'X-B:' -H 'Accept'"))
  assert.deepEqual(headers(draft), [['X-A', ''], ['X-B', '']])
  const w = warnings.find((x) => x.code === 'headerSkipped')
  assert.equal(w?.params?.line, 'Accept')
})

test('E9 --compressed 不覆盖已有的 accept-encoding', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -H 'accept-encoding: identity' --compressed"))
  const enc = headers(draft).filter(([n]) => n.toLowerCase() === 'accept-encoding')
  assert.equal(enc.length, 1)
  assert.equal(enc[0][1], 'identity')
  assert.ok(!codes(warnings).includes('compressedAdded'))
})

/* ───────────────────────── F 忽略与失败 ───────────────────────── */

test('F1 代理被忽略，-k/-s 静默而 -L 单独提示', () => {
  const { draft, warnings } = good(parseCurl("curl -x http://127.0.0.1:8080 -k -L -s 'https://x.test/'"))
  assert.equal(draft.url, 'https://x.test/')
  assert.deepEqual(codes(warnings), ['proxyIgnored', 'locationIgnored'])
})

test('F2 无对应实现的选项归并成一条 optionIgnored', () => {
  const { warnings } = good(parseCurl("curl --http2 --resolve x.test:443:127.0.0.1 -m 30 'https://x.test/'"))
  const w = warnings.find((x) => x.code === 'optionIgnored')
  const names = String(w?.params?.names ?? '')
  assert.ok(names.includes('--http2') && names.includes('--resolve') && names.includes('-m'))
  assert.equal(warnings.filter((x) => x.code === 'optionIgnored').length, 1)
})

test('F3 管道之后的内容被丢弃', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/a' | jq ."))
  assert.equal(draft.url, 'https://x.test/a')
  assert.ok(codes(warnings).includes('pipedCommand'))
})

test('F4 多个裸 URL 只取第一个', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://a.test/' 'https://b.test/'"))
  assert.equal(draft.url, 'https://a.test/')
  assert.ok(codes(warnings).includes('multipleUrls'))
})

test('F5 未闭合引号硬失败', () => {
  const r = parseCurl("curl 'https://x.test/")
  assert.equal(r.ok, false)
  assert.equal(r.ok ? '' : r.error.code, 'unterminatedQuote')
})

test('F6 纯 URL 不是 curl 命令', () => {
  const r = parseCurl('https://x.test/')
  assert.equal(r.ok, false)
  assert.equal(r.ok ? '' : r.error.code, 'notCurl')
})

test('F7 空串与无 URL 各自报错', () => {
  const empty = parseCurl('   ')
  assert.equal(empty.ok ? '' : empty.error.code, 'empty')
  const noUrl = parseCurl('curl -X POST')
  assert.equal(noUrl.ok ? '' : noUrl.error.code, 'noUrl')
})

test('F8 PowerShell 方言硬失败', () => {
  const r = parseCurl('$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession\nInvoke-WebRequest -Uri "https://x.test/"')
  assert.equal(r.ok, false)
  assert.equal(r.ok ? '' : r.error.code, 'powershellDialect')
})

test('F9 -T 变 PUT 且体为空', () => {
  const { draft, warnings } = good(parseCurl("curl -T upload.bin 'https://x.test/up'"))
  assert.equal(draft.method, 'PUT')
  assert.equal(draft.body, '')
  assert.ok(codes(warnings).includes('uploadFileUnreadable'))
})

test('F10 -F 字面 part 合成完整 multipart，boundary 与体内一致', () => {
  const { draft } = good(parseCurl("curl 'https://x.test/' -F 'a=1' -F 'b=2'"))
  assert.equal(draft.method, 'POST')
  const ct = headerValue(draft, 'content-type') ?? ''
  const boundary = /boundary=(.+)$/.exec(ct)?.[1] ?? ''
  assert.ok(boundary.length > 0)
  assert.equal(draft.body.split(`--${boundary}\n`).length - 1, 2)
  assert.ok(draft.body.endsWith(`--${boundary}--\n`))
  assert.ok(draft.body.includes('Content-Disposition: form-data; name="a"\n\n1\n'))
  assert.ok(draft.body.includes('Content-Disposition: form-data; name="b"\n\n2\n'))
  assert.ok(!draft.body.includes('\r\n'))
})

test('F11 -F 文件 part 只合成骨架并报文件名', () => {
  const { draft, warnings } = good(parseCurl("curl 'https://x.test/' -F 'f=@a.png'"))
  const w = warnings.find((x) => x.code === 'formFileUnreadable')
  assert.equal(w?.level, 'error')
  assert.equal(w?.params?.name, 'a.png')
  assert.ok(draft.body.includes('filename="a.png"'))
  assert.ok(draft.body.includes('Content-Type: application/octet-stream'))
})

/* ───────────────────────── G looksLikeCurl 专项 ───────────────────────── */

test('G1 像 curl 的形态全部命中', () => {
  const yes = [
    "curl 'https://x.test/'",
    '   curl https://x.test/',
    '\n  curl https://x.test/',
    'CURL https://x.test/',
    '$ curl https://x.test/',
    'C:\\tools\\curl.exe "https://x.test/"',
    '/usr/bin/curl https://x.test/',
    '```bash\ncurl https://x.test/\n```',
  ]
  for (const s of yes) assert.equal(looksLikeCurl(s), true, s)
})

test('G2 不像 curl 的形态全部落空', () => {
  const no = ['curly braces', 'https://curl.se/docs', '', '   ', 'POST /x HTTP/1.1', 'echo curl', 'curling']
  for (const s of no) assert.equal(looksLikeCurl(s), false, s)
})

test('G3 markdown 围栏内的命令可被解析', () => {
  const { draft } = good(parseCurl('```bash\ncurl https://x.test/a -H \'accept: */*\'\n```'))
  assert.equal(draft.url, 'https://x.test/a')
  assert.equal(headerValue(draft, 'accept'), '*/*')
})

/* ───────────────────────── H 分词器直测 ───────────────────────── */

test('H1 tokenizeCurl 保留空 token 与引号内换行', () => {
  const { argv } = tokenizeCurl("curl '' 'a\nb'")
  assert.deepEqual(argv, ['curl', '', 'a\nb'])
})

test('H2 POSIX 双引号里的 \\n 是反斜杠加字母', () => {
  const { argv } = tokenizeCurl('curl "a\\nb"')
  assert.deepEqual(argv, ['curl', 'a\\nb'])
})
