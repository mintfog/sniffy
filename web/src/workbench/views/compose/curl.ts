/**
 * cURL 命令 → 构造器草稿。
 *
 * 主要来源是浏览器开发者工具的「复制为 cURL」：Chrome/Firefox 的 bash 版与 Windows cmd 版
 * 转义规则完全不同，所以分词器带方言。解析器只产出 { level, code, params }，
 * 面向用户的文案在渲染时才查 i18n——同一份结果要同时喂给三种语言。
 *
 * 注意：本文件被 `node --test` 直接加载（curl.test.ts），因此它与它 import 的 ./model
 * 都不能在运行期依赖 `@/` 路径别名或浏览器环境，否则 node 解析不了模块。
 */
import type { Draft } from './model.ts'
import { newDraft, newHeaderRow, normalizeHeaders } from './model.ts'

export type CurlWarnLevel = 'info' | 'warn' | 'error'

export interface CurlWarning {
  level: CurlWarnLevel
  /** i18n key 后缀，渲染时 t('compose.curl.warn.' + code)。 */
  code: string
  params?: Record<string, string | number>
}

export interface CurlError {
  code: string
  params?: Record<string, string | number>
}

export type CurlImport =
  | { ok: true; draft: Draft; warnings: CurlWarning[] }
  | { ok: false; error: CurlError; warnings: CurlWarning[] }

/** 这几个 code 一旦出现就没有可用结果，由 parseCurl 转成 ok:false。 */
const FATAL_CODES = ['powershellDialect', 'unterminatedQuote']

/* ───────────────────────── Phase 0：预处理 ───────────────────────── */

/**
 * 剥 BOM、markdown 围栏与首行提示符。
 * 刻意不做 `\r\n → \n` 全局归一：那会连引号内的字面 CR 一起吃掉。
 */
function preprocess(text: string): string {
  let s = text
  if (s.charCodeAt(0) === 0xfeff) s = s.slice(1)
  // 剥 NUL：cmdUnescape 拿它当哨兵传递换行，输入里留着就能被伪造成一个假换行。
  // 用 split/join 而非正则——控制字符进正则会被 eslint 的 no-control-regex 拦下。
  s = s.split(CMD_NEWLINE).join('')
  s = s.replace(/^[ \t]*```[^\n]*\r?\n/, '').replace(/\r?\n[ \t]*```\s*$/, '')
  s = s.replace(/^[ \t\r\n]*(?:\$|PS [^\r\n>]*>|>)[ \t]+/, '')
  return s
}

/** 粘贴内容是否像一条 curl 命令（容忍前导空白/提示符/路径前缀/curl.exe/大小写/markdown 围栏）。 */
export function looksLikeCurl(text: string): boolean {
  // 路径前缀允许 `:`，否则 Windows 的 `C:\tools\curl.exe` 认不出来。
  return /^\s*(?:\$[ \t]+)?(?:[\w.\-:]*[\\/])*curl(?:\.exe)?["']?(?=\s|$)/i.test(preprocess(text))
}

/* ───────────────────────── Phase 1：分词 ───────────────────────── */

type Dialect = 'posix' | 'windows'
type State = 'NORMAL' | 'SQ' | 'DQ' | 'ANSI'

function detectDialect(s: string): Dialect | 'powershell' {
  if (/`[ \t]*\r?\n/.test(s) || /Invoke-WebRequest|Invoke-RestMethod|\$session\s*=/.test(s)) return 'powershell'
  if (/\^[ \t]*\r?\n/.test(s)) return 'windows'
  // 新版 Chrome 的「复制为 cURL (cmd)」把每个引号都写成 ^"，整条命令可以只有一行，
  // 此时没有续行的脱字号可认；而 ^" 在 POSIX shell 里没有任何用处，用它判定不会误伤。
  if (s.includes('^"')) return 'windows'
  // 误判成 windows 的代价远小于反向：posix 被当成 windows 只是少认几个转义，
  // 而 windows 被当成 posix 会让 `\` 吃掉后一个字符，把 {\"a\":1} 直接毁掉。
  if (s.includes('""') && !s.includes("'")) return 'windows'
  return 'posix'
}

/**
 * cmd 把值里的换行写成 `"^⏎"`（闭引号 + 脱字号 + 换行 + 开引号）。真跑一遍的话续行会把换行
 * 连同它一起吃掉，得到 `abc"def` —— 等于把用户原始请求里的换行丢了。这里用一个哨兵把它带过
 * 第二阶段（preprocess 已剥掉输入中所有 NUL，故无法被伪造）。
 */
const CMD_NEWLINE = '\u0000'

/**
 * 还原 cmd 的转义。它先于程序自己的 argv 解析发生：`^` 只被 cmd 吃掉，后一个字符原样送进程序，
 * 由程序的引号规则决定它是不是定界符 —— 所以 `^"` 是一个真定界引号，而 `^\^"` 还原成 `\"`
 * 才会被程序读成字面引号。
 *
 * 必须单独走一遍而不能并进分词器：cmd 的 `^` 在程序看来的引号内部照样生效，而 cmd 自身的
 * 引号状态只由**未转义**的 `"` 切换。两套引号状态错位，单遍状态机表达不了。
 */
function cmdUnescape(src: string): string {
  let out = ''
  let quoted = false
  let i = 0
  while (i < src.length) {
    const ch = src[i]
    if (ch === '^' && !quoted) {
      let j = i + 1
      while (src[j] === ' ' || src[j] === '\t') j++
      const afterNewline = src[j] === '\n' ? j + 1 : src[j] === '\r' && src[j + 1] === '\n' ? j + 2 : -1
      if (afterNewline >= 0) {
        // 闭引号 + ^⏎ + 开引号 是换行的编码；`^` 前有空白的那些才是纯续行。
        if (out.endsWith('"') && src[afterNewline] === '"') out += CMD_NEWLINE
        i = afterNewline
        continue
      }
      if (i + 1 < src.length) {
        out += src[i + 1]
        i += 2
        continue
      }
      i++
      continue
    }
    if (ch === '"') quoted = !quoted
    out += ch
    i++
  }
  return out
}

const ANSI_SIMPLE: Record<string, string> = {
  '\\': '\\',
  "'": "'",
  '"': '"',
  '`': '`',
  '?': '?',
  a: '\x07',
  b: '\b',
  e: '\x1b',
  E: '\x1b',
  f: '\f',
  n: '\n',
  r: '\r',
  t: '\t',
  v: '\v',
}

/** 导出供单测与将来其它导入入口使用。 */
export function tokenizeCurl(text: string): { argv: string[]; warnings: CurlWarning[] } {
  const warnings: CurlWarning[] = []
  const pre = preprocess(text)
  const dialect = detectDialect(pre)
  if (dialect === 'powershell') {
    warnings.push({ level: 'error', code: 'powershellDialect' })
    return { argv: [], warnings }
  }
  const src = dialect === 'windows' ? cmdUnescape(pre) : pre

  const argv: string[] = []
  let buf = ''
  // token 是否已开启：既区分 '' 空 token 与「根本没有 token」，
  // 又用来判定 Windows 的 `^`——紧跟闭引号（open）是字面换行，前有空白（!open）才是续行。
  let open = false
  let state: State = 'NORMAL'
  let lossyBytes = false
  let i = 0

  const flush = () => {
    if (open) {
      argv.push(buf)
      buf = ''
      open = false
    }
  }
  const take = (s: string) => {
    buf += s
    open = true
  }

  const isNewline = (n: number) => src[n] === '\n' || (src[n] === '\r' && src[n + 1] === '\n')
  const skipNewline = (n: number) => (src[n] === '\r' ? n + 2 : n + 1)

  while (i < src.length) {
    const ch = src[i]

    if (state === 'NORMAL') {
      if (ch === ' ' || ch === '\t') {
        flush()
        i++
        continue
      }
      if (isNewline(i)) {
        flush()
        i = skipNewline(i)
        continue
      }
      if (ch === CMD_NEWLINE) {
        take('\n')
        i++
        continue
      }
      if (dialect === 'posix' && ch === '#' && !open) {
        while (i < src.length && !isNewline(i)) i++
        continue
      }
      if (!open && (ch === '|' || ch === ';' || ch === '&' || ch === '>' || ch === '<')) {
        warnings.push({ level: 'warn', code: 'pipedCommand' })
        break
      }
      if (dialect === 'posix') {
        if (ch === '$' && src[i + 1] === "'") {
          state = 'ANSI'
          open = true
          i += 2
          continue
        }
        if (ch === '$' && src[i + 1] === '"') {
          state = 'DQ'
          open = true
          i += 2
          continue
        }
        if (ch === "'") {
          state = 'SQ'
          open = true
          i++
          continue
        }
        if (ch === '\\') {
          if (isNewline(i + 1)) {
            i = skipNewline(i + 1)
            continue
          }
          if (i + 1 < src.length) {
            take(src[i + 1])
            i += 2
            continue
          }
          i++
          continue
        }
      }
      if (ch === '"') {
        state = 'DQ'
        open = true
        i++
        continue
      }
      take(ch)
      i++
      continue
    }

    if (state === 'SQ') {
      if (ch === "'") {
        state = 'NORMAL'
        i++
      } else {
        take(ch)
        i++
      }
      continue
    }

    if (state === 'DQ') {
      if (dialect === 'posix') {
        if (ch === '"') {
          state = 'NORMAL'
          i++
          continue
        }
        if (ch === '\\') {
          const nx = src[i + 1]
          if (isNewline(i + 1)) {
            i = skipNewline(i + 1)
            continue
          }
          if (nx === '"' || nx === '\\' || nx === '$' || nx === '`') {
            take(nx)
            i += 2
            continue
          }
          // bash 语义："a\nb" 里的 \n 是反斜杠加字母 n，不是换行。
          take('\\')
          i++
          continue
        }
        take(ch)
        i++
        continue
      }
      // Windows：宽进——同时认 cmd 的 "" 与 Chromium 无条件加倍出来的 \\ / \"。
      if (ch === '"') {
        if (src[i + 1] === '"') {
          take('"')
          i += 2
        } else {
          state = 'NORMAL'
          i++
        }
        continue
      }
      if (ch === '\\' && (src[i + 1] === '"' || src[i + 1] === '\\')) {
        take(src[i + 1])
        i += 2
        continue
      }
      take(ch)
      i++
      continue
    }

    // ANSI：$'...'
    if (ch === "'") {
      state = 'NORMAL'
      i++
      continue
    }
    if (ch !== '\\' || i + 1 >= src.length) {
      take(ch)
      i++
      continue
    }
    const nx = src[i + 1]
    if (ANSI_SIMPLE[nx] !== undefined) {
      take(ANSI_SIMPLE[nx])
      i += 2
      continue
    }
    if (nx === 'x' || nx === 'u' || nx === 'U') {
      const max = nx === 'x' ? 2 : nx === 'u' ? 4 : 8
      const m = /^[0-9a-fA-F]+/.exec(src.slice(i + 2, i + 2 + max))
      if (m) {
        const code = parseInt(m[0], 16)
        // bash 的 \xHH(≥0x80) 送出的是裸字节，而我们只能给出一个 UTF-16 码元，
        // 经 UTF-8 编码后是两个字节，与原命令实际写到线上的字节不同。
        if (nx === 'x' && code >= 0x80) lossyBytes = true
        take(String.fromCodePoint(code))
        i += 2 + m[0].length
        continue
      }
      take(nx)
      i += 2
      continue
    }
    if (nx >= '0' && nx <= '7') {
      const m = /^[0-7]{1,3}/.exec(src.slice(i + 1))!
      const code = parseInt(m[0], 8)
      if (code >= 0o200) lossyBytes = true
      take(String.fromCharCode(code))
      i += 1 + m[0].length
      continue
    }
    if (nx === 'c' && i + 2 < src.length) {
      take(String.fromCharCode(src.charCodeAt(i + 2) & 0x1f))
      i += 3
      continue
    }
    take('\\')
    i++
  }

  if (state !== 'NORMAL') {
    warnings.push({ level: 'error', code: 'unterminatedQuote' })
    return { argv: [], warnings }
  }
  flush()
  if (lossyBytes) warnings.push({ level: 'error', code: 'binaryBodyLossy' })
  return { argv, warnings }
}

/* ───────────────────────── shell 转义（导出侧） ───────────────────────── */

/** POSIX shell 单引号转义；DetailPanel 的 rowToCurl 反向复用，使导出/导入闭环。 */
export function shellQuote(s: string): string {
  return `'${s.split("'").join("'\\''")}'`
}

/* ───────────────────────── 选项表 ───────────────────────── */

/** 取值选项：名 → 归一化后的语义键。短选项与长选项在同一张表里。 */
const VALUE_OPTS: Record<string, string> = {
  '-X': 'request',
  '--request': 'request',
  '-H': 'header',
  '--header': 'header',
  '-d': 'data',
  '--data': 'data',
  '--data-ascii': 'data',
  '--data-raw': 'data-raw',
  '--data-binary': 'data',
  '--data-urlencode': 'data-urlencode',
  '--json': 'json',
  '-F': 'form',
  '--form': 'form',
  '--form-string': 'form-string',
  '-u': 'user',
  '--user': 'user',
  '-b': 'cookie',
  '--cookie': 'cookie',
  '-A': 'user-agent',
  '--user-agent': 'user-agent',
  '-e': 'referer',
  '--referer': 'referer',
  '-T': 'upload-file',
  '--upload-file': 'upload-file',
  '-r': 'range',
  '--range': 'range',
  '--url': 'url',
  '--url-query': 'url-query',
  '--oauth2-bearer': 'oauth2-bearer',
}

/** 取值但被忽略的代理类选项，合并成一条 proxyIgnored。 */
const PROXY_VALUE_OPTS = new Set([
  '-x',
  '--proxy',
  '--preproxy',
  '--proxy-user',
  '--proxy-header',
  '--socks4',
  '--socks4a',
  '--socks5',
  '--socks5-hostname',
  '--proxy1.0',
])

/** 取值且静默忽略的选项。 */
const SILENT_VALUE_OPTS = new Set([
  '-o',
  '--output',
  '-w',
  '--write-out',
  '--max-redirs',
  '--stderr',
  '--trace',
  '--trace-ascii',
])

/** 取值、忽略并提示的选项，与 IGNORED_FLAGS 一起归并成一条 optionIgnored。 */
const NOTICE_VALUE_OPTS = new Set([
  '-m',
  '--max-time',
  '--connect-timeout',
  '--retry',
  '--retry-delay',
  '--retry-max-time',
  '-c',
  '--cookie-jar',
  '-E',
  '--cert',
  '--cert-type',
  '--key',
  '--key-type',
  '--pass',
  '--cacert',
  '--capath',
  '--pinnedpubkey',
  '--resolve',
  '--connect-to',
  '--interface',
  '--limit-rate',
  '--local-port',
  '--dns-servers',
  '-K',
  '--config',
  '--ciphers',
  '--aws-sigv4',
  '-C',
  '--continue-at',
  '--happy-eyeballs-timeout-ms',
  '-z',
  '--time-cond',
])

const SILENT_FLAGS = new Set([
  '-s',
  '--silent',
  '-S',
  '--show-error',
  '-v',
  '--verbose',
  '-i',
  '--include',
  '-O',
  '--remote-name',
  '-J',
  '--remote-header-name',
  '-f',
  '--fail',
  '--fail-with-body',
  '-N',
  '--no-buffer',
  '--raw',
  '-g',
  '--globoff',
  '--path-as-is',
  '-q',
  '--disable',
  '-j',
  '--junk-session-cookies',
  '-#',
  '--progress-bar',
  '--no-progress-meter',
  '-Z',
  '--parallel',
  '-a',
  '--append',
  '--basic',
  '--tcp-nodelay',
  '--no-keepalive',
  '-R',
  '--remote-time',
  '--styled-output',
  '--no-styled-output',
])

/**
 * 跟随重定向。单列出来是因为它值得一句专门的提示，而不是混进 optionIgnored 那串名字里：
 * 上游客户端一律不代跟随（CheckRedirect 返回 ErrUseLastResponse，见 internal/core/engine.go），
 * 导入后请求会停在 30x。这与 curl 的行为正好相反，不说清楚用户只会以为是站点变了。
 */
const LOCATION_FLAGS = new Set(['-L', '--location', '--location-trusted'])

/** 不取值、忽略并提示的开关。 */
const NOTICE_FLAGS = new Set([
  '-k',
  '--insecure',
  '--http1.0',
  '--http1.1',
  '--http2',
  '--http2-prior-knowledge',
  '--http3',
  '--http3-only',
  '-4',
  '--ipv4',
  '-6',
  '--ipv6',
  '--tlsv1',
  '--tlsv1.0',
  '--tlsv1.1',
  '--tlsv1.2',
  '--tlsv1.3',
  '--ssl',
  '--ssl-reqd',
  '--digest',
  '--ntlm',
  '--negotiate',
  '--anyauth',
  '--no-alpn',
  '--no-npn',
])

const METHOD_SET = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

/** 与 curl_easy_escape 一致：未保留集 A-Za-z0-9-._~，空格是 %20。 */
const curlEscape = (s: string) =>
  encodeURIComponent(s).replace(/[!'()*]/g, (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase())

/** UTF-8 安全的 base64（btoa 直接吃非 ASCII 会抛 InvalidCharacterError）。 */
function base64Utf8(input: string): string {
  const bytes = new TextEncoder().encode(input)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin)
}

/* ───────────────────────── Phase 2：解析 ───────────────────────── */

interface DataChunk {
  kind: 'raw' | 'json' | 'query'
  text: string
}

interface FormPart {
  name: string
  /** 字面内容；file 非空时恒为空串。 */
  value: string
  file?: string
  filename?: string
  type?: string
}

/** 解析一条 curl 命令为构造器草稿。headers 已过 normalizeHeaders，可直接交给 openDraft。 */
export function parseCurl(text: string): CurlImport {
  const warnings: CurlWarning[] = []
  if (!text.trim()) return { ok: false, error: { code: 'empty' }, warnings }

  const tok = tokenizeCurl(text)
  warnings.push(...tok.warnings)
  const fatal = warnings.find((w) => FATAL_CODES.includes(w.code))
  if (fatal) return { ok: false, error: { code: fatal.code, params: fatal.params }, warnings }

  const argv = tok.argv
  if (argv.length === 0 || !/(?:^|[\\/])curl(?:\.exe)?$/i.test(argv[0])) {
    return { ok: false, error: { code: 'notCurl' }, warnings }
  }

  let url = ''
  let sawExtraUrl = false
  let requested = ''
  let headFlag = false
  let uploadFlag = false
  let getFlag = false
  let compressed = false
  let user: string | undefined
  let bearer = ''
  let range = ''
  let cookie = ''
  let userAgent = ''
  let referer = ''
  let hasDataOption = false
  const chunks: DataChunk[] = []
  const queryChunks: string[] = []
  const forms: FormPart[] = []
  const headerRows: [string, string][] = []
  const ignored = new Set<string>()
  let proxyIgnored = false
  let pseudoDropped = false
  let sawLocation = false

  const setUrl = (v: string) => {
    if (url) sawExtraUrl = true
    else url = v
  }

  const addHeaderLine = (line: string) => {
    const trimmed = line.trim()
    if (!trimmed) return
    // HTTP/2 伪头以冒号开头，名字里的那个冒号不是分隔符。
    const sep = trimmed.startsWith(':') ? trimmed.indexOf(':', 1) : trimmed.indexOf(':')
    if (sep < 0) {
      if (trimmed.endsWith(';')) {
        headerRows.push([trimmed.slice(0, -1).trim(), ''])
        return
      }
      warnings.push({ level: 'warn', code: 'headerSkipped', params: { line: trimmed } })
      return
    }
    const name = trimmed.slice(0, sep).trim()
    if (!name) return
    if (name.startsWith(':')) {
      pseudoDropped = true
      return
    }
    headerRows.push([name, trimmed.slice(sep + 1).trim()])
  }

  const addDataFile = (spec: string) => {
    warnings.push({ level: 'error', code: 'dataFileUnreadable', params: { name: spec.slice(1) || '-' } })
  }

  const urlencodeSpec = (spec: string): string | null => {
    if (spec.startsWith('@')) return null
    const eq = spec.indexOf('=')
    const at = spec.indexOf('@')
    // `=content` 形式只送出编码后的内容，等号本身不出现在结果里。
    if (eq === 0) return curlEscape(spec.slice(1))
    if (eq > 0) {
      // name=content：name 不编码，只编码等号右边。
      return spec.slice(0, eq) + '=' + curlEscape(spec.slice(eq + 1))
    }
    if (at >= 0) return null
    return curlEscape(spec)
  }

  const addForm = (spec: string, literal: boolean) => {
    const eq = spec.indexOf('=')
    if (eq < 0) {
      ignored.add('-F')
      return
    }
    const name = spec.slice(0, eq)
    const rest = spec.slice(eq + 1)
    if (literal || (rest[0] !== '@' && rest[0] !== '<')) {
      forms.push({ name, value: rest })
      return
    }
    const segs = rest.slice(1).split(';')
    const part: FormPart = { name, value: '', file: segs[0] }
    for (const seg of segs.slice(1)) {
      const kv = seg.trim()
      if (kv.toLowerCase().startsWith('type=')) part.type = kv.slice(5)
      else if (kv.toLowerCase().startsWith('filename=')) part.filename = kv.slice(9)
    }
    forms.push(part)
  }

  const apply = (key: string, value: string) => {
    switch (key) {
      case 'request':
        requested = value
        break
      case 'header':
        addHeaderLine(value)
        break
      case 'url':
        setUrl(value)
        break
      case 'data':
        hasDataOption = true
        if (value.startsWith('@')) addDataFile(value)
        else chunks.push({ kind: 'raw', text: value })
        break
      case 'data-raw':
        // --data-raw 永不解释 @。
        hasDataOption = true
        chunks.push({ kind: 'raw', text: value })
        break
      case 'data-urlencode': {
        hasDataOption = true
        const enc = urlencodeSpec(value)
        if (enc === null) addDataFile(value.startsWith('@') ? value : '@' + value.slice(value.indexOf('@') + 1))
        else chunks.push({ kind: 'query', text: enc })
        break
      }
      case 'json':
        hasDataOption = true
        chunks.push({ kind: 'json', text: value })
        break
      case 'form':
        addForm(value, false)
        break
      case 'form-string':
        addForm(value, true)
        break
      case 'user':
        user = value
        break
      case 'cookie':
        if (value.includes('=')) cookie = value
        else warnings.push({ level: 'warn', code: 'cookieFile' })
        break
      case 'user-agent':
        userAgent = value
        break
      case 'referer':
        referer = value
        break
      case 'upload-file':
        uploadFlag = true
        warnings.push({ level: 'error', code: 'uploadFileUnreadable' })
        break
      case 'range':
        range = value
        break
      case 'url-query': {
        const enc = urlencodeSpec(value)
        if (enc !== null) queryChunks.push(enc)
        break
      }
      case 'oauth2-bearer':
        bearer = value
        break
    }
  }

  /** 取值选项的值：粘连形式优先，否则吃下一个 argv。 */
  const valueOf = (glued: string, next: () => string | undefined): string => {
    if (glued !== '') return glued
    return next() ?? ''
  }

  for (let i = 1; i < argv.length; i++) {
    const arg = argv[i]
    const next = () => argv[++i]

    if (arg === '--') {
      for (let j = i + 1; j < argv.length; j++) setUrl(argv[j])
      break
    }

    if (arg.startsWith('--')) {
      const eq = arg.indexOf('=')
      const name = eq >= 0 ? arg.slice(0, eq) : arg
      const glued = eq >= 0 ? arg.slice(eq + 1) : ''
      if (name === '--compressed') {
        compressed = true
        continue
      }
      if (name === '--get') {
        getFlag = true
        continue
      }
      if (name === '--head') {
        headFlag = true
        continue
      }
      if (VALUE_OPTS[name]) {
        apply(VALUE_OPTS[name], valueOf(glued, next))
        continue
      }
      if (PROXY_VALUE_OPTS.has(name)) {
        proxyIgnored = true
        if (eq < 0) next()
        continue
      }
      if (SILENT_VALUE_OPTS.has(name)) {
        if (eq < 0) next()
        continue
      }
      if (NOTICE_VALUE_OPTS.has(name)) {
        ignored.add(name)
        if (eq < 0) next()
        continue
      }
      if (LOCATION_FLAGS.has(name)) {
        sawLocation = true
        continue
      }
      if (SILENT_FLAGS.has(name)) continue
      if (NOTICE_FLAGS.has(name)) {
        ignored.add(name)
        continue
      }
      // 未知长选项：不敢吞掉后一个 argv——猜错会把它当成裸 URL。
      ignored.add(name)
      continue
    }

    if (arg.length > 1 && arg[0] === '-') {
      let consumed = false
      for (let k = 1; k < arg.length && !consumed; k++) {
        const short = '-' + arg[k]
        const glued = arg.slice(k + 1)
        if (short === '-G') {
          getFlag = true
          continue
        }
        if (short === '-I') {
          headFlag = true
          continue
        }
        if (VALUE_OPTS[short]) {
          apply(VALUE_OPTS[short], valueOf(glued, next))
          consumed = true
          continue
        }
        if (PROXY_VALUE_OPTS.has(short)) {
          proxyIgnored = true
          if (glued === '') next()
          consumed = true
          continue
        }
        if (SILENT_VALUE_OPTS.has(short)) {
          if (glued === '') next()
          consumed = true
          continue
        }
        if (NOTICE_VALUE_OPTS.has(short)) {
          ignored.add(short)
          if (glued === '') next()
          consumed = true
          continue
        }
        if (LOCATION_FLAGS.has(short)) {
          sawLocation = true
          continue
        }
        if (SILENT_FLAGS.has(short)) continue
        if (NOTICE_FLAGS.has(short)) {
          ignored.add(short)
          continue
        }
        ignored.add(short)
      }
      continue
    }

    setUrl(arg)
  }

  if (sawExtraUrl) warnings.push({ level: 'warn', code: 'multipleUrls' })
  if (pseudoDropped) warnings.push({ level: 'info', code: 'pseudoHeaderDropped' })
  if (proxyIgnored) warnings.push({ level: 'warn', code: 'proxyIgnored' })
  if (sawLocation) warnings.push({ level: 'warn', code: 'locationIgnored' })
  if (ignored.size > 0) {
    warnings.push({ level: 'warn', code: 'optionIgnored', params: { names: [...ignored].join(', ') } })
  }

  if (!url) return { ok: false, error: { code: 'noUrl' }, warnings }

  /* 体：data 合并 → 表单 → -G 挪查询 */
  let body = ''
  let synthContentType = ''
  if (chunks.length > 0) {
    const hasJson = chunks.some((c) => c.kind === 'json')
    const allJson = chunks.every((c) => c.kind === 'json')
    // & 会毁掉 JSON，所以纯 --json 直接首尾相接；一旦与别的 data 选项混用就只能按 & 拼，并如实提示。
    body = allJson ? chunks.map((c) => c.text).join('') : chunks.map((c) => c.text).join('&')
    if (hasJson && !allJson) warnings.push({ level: 'warn', code: 'mixedDataKinds' })
    synthContentType = allJson ? 'application/json' : 'application/x-www-form-urlencoded'
  } else if (hasDataOption) {
    synthContentType = 'application/x-www-form-urlencoded'
  }
  const jsonMode = chunks.length > 0 && chunks.every((c) => c.kind === 'json')

  if (getFlag) {
    if (body) queryChunks.unshift(body)
    body = ''
    synthContentType = ''
  }

  let formBoundary = ''
  if (forms.length > 0) {
    const built = buildMultipart(forms, warnings)
    body = built.body
    formBoundary = built.boundary
    synthContentType = `multipart/form-data; boundary=${formBoundary}`
  }

  if (uploadFlag) body = ''

  const query = queryChunks.join('&')
  if (query) url += (url.includes('?') ? '&' : '?') + query

  /* 方法推断：-X > -I > -T > -G > 有体 > GET */
  let method = 'GET'
  if (requested) method = requested.toUpperCase()
  else if (headFlag) method = 'HEAD'
  else if (uploadFlag) method = 'PUT'
  else if (getFlag) method = 'GET'
  else if (hasDataOption || forms.length > 0) method = 'POST'
  if (!METHOD_SET.includes(method)) warnings.push({ level: 'info', code: 'unusualMethod', params: { method } })

  /* 合成头：同名已由 -H 给出时一律让 -H 胜出 */
  const has = (name: string) => headerRows.some(([n]) => n.toLowerCase() === name)

  if (cookie && !has('cookie')) headerRows.push(['Cookie', cookie])
  if (userAgent && !has('user-agent')) headerRows.push(['User-Agent', userAgent])
  if (referer && !has('referer')) headerRows.push(['Referer', referer])
  if (range && !has('range')) headerRows.push(['Range', /=/.test(range) ? range : `bytes=${range}`])
  if (bearer && !has('authorization')) headerRows.push(['Authorization', `Bearer ${bearer}`])
  if (user !== undefined) {
    if (has('authorization')) {
      warnings.push({ level: 'warn', code: 'authHeaderConflict' })
    } else {
      // 密码里可以有冒号，只切第一个。
      const sep = user.indexOf(':')
      const name = sep < 0 ? user : user.slice(0, sep)
      const pass = sep < 0 ? '' : user.slice(sep + 1)
      if (sep < 0) warnings.push({ level: 'warn', code: 'userNoPassword' })
      headerRows.push(['Authorization', `Basic ${base64Utf8(`${name}:${pass}`)}`])
    }
  }
  if (compressed && !has('accept-encoding')) {
    headerRows.push(['Accept-Encoding', 'gzip, deflate, br'])
    warnings.push({ level: 'info', code: 'compressedAdded' })
  }
  if (jsonMode && !getFlag) {
    if (!has('accept')) headerRows.push(['Accept', 'application/json'])
  }
  if (synthContentType && !has('content-type')) headerRows.push(['Content-Type', synthContentType])

  const draft: Draft = {
    ...newDraft(),
    method,
    url,
    headers: normalizeHeaders(headerRows.map(([n, v]) => newHeaderRow(n, v))),
    body,
  }
  return { ok: true, draft, warnings }
}

/* ───────────────────────── multipart 合成 ───────────────────────── */

/**
 * Draft 只有一个文本 body，后端原样出站，故 -F 只能整段合成出来。
 * 行尾用 LF 而非 CRLF：编辑器（CM6）没设 lineSeparator，会把 CRLF 静默压成 LF，
 * 那样预览与实发就对不上了，宁可一开始就是 LF 并如实提示。
 */
function buildMultipart(parts: FormPart[], warnings: CurlWarning[]): { body: string; boundary: string } {
  const contents = parts.map((p) => `${p.name}${p.value}${p.file ?? ''}${p.filename ?? ''}${p.type ?? ''}`).join('\n')
  let boundary = ''
  for (let attempt = 0; attempt < 8; attempt++) {
    const rand = Math.random().toString(16).slice(2).padEnd(12, '0').slice(0, 12)
    boundary = `------------------------${rand}${attempt}`
    if (!contents.includes(boundary)) break
  }

  const lines: string[] = []
  let anyFile = false
  for (const p of parts) {
    lines.push(`--${boundary}`)
    let disp = `Content-Disposition: form-data; name="${p.name}"`
    if (p.file) disp += `; filename="${p.filename ?? p.file}"`
    lines.push(disp)
    if (p.file) {
      lines.push(`Content-Type: ${p.type ?? 'application/octet-stream'}`)
      anyFile = true
      warnings.push({ level: 'error', code: 'formFileUnreadable', params: { name: p.file } })
    } else if (p.type) {
      lines.push(`Content-Type: ${p.type}`)
    }
    lines.push('')
    lines.push(p.file ? '' : p.value)
  }
  lines.push(`--${boundary}--`)
  lines.push('')

  if (!anyFile) warnings.push({ level: 'info', code: 'formSynthesized' })
  warnings.push({ level: 'warn', code: 'formLfOnly' })
  return { body: lines.join('\n'), boundary }
}
