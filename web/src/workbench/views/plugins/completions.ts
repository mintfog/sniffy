/**
 * sniffy 插件宿主 API 的代码补全数据与 CodeMirror 补全源。
 *
 * 词条与 internal/plugin/js/plugin.go 严格对应:
 *   - 顶层 API（钩子、决策函数、console/store/settings/notify、助手命名空间）来自 hostSetup;
 *   - flow 字段来自 jsFlow/jsResponse/jsProcess 与 requestToJS / OnWebSocketMessage / OnStreamMessage,
 *     各字段标注其生效钩子(phases),编辑时按光标所在钩子过滤,避免在 onRequest 里提示 WS 专属字段等误导。
 * 改宿主 API 时同步此处,编辑器提示才不至于误导作者。
 */
import { snippetCompletion, type Completion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { syntaxTree } from '@codemirror/language'
import type { SyntaxNode } from '@lezer/common'

interface Member {
  name: string
  detail: string
  info?: string
  type?: Completion['type']
  /** 片段体仅含方法部分(不带对象前缀);留空则按 detail 的签名自动生成。 */
  snip?: string
}

const fn = (name: string, detail: string, info?: string): Member => ({ name, detail, info, type: 'function' })
const prop = (name: string, detail: string, info?: string): Member => ({ name, detail, info, type: 'property' })
/** header.* 的 snip 把 flow.headers 排到最后一个 tab 位,光标先落在 name;name 必是字面量故加引号,value 常为变量故不加。 */
const hfn = (name: string, detail: string, info: string, snip: string): Member => ({ name, detail, info, type: 'function', snip })

/** `obj.` 后的成员补全数据(键为命名空间对象名)。 */
const NAMESPACES: Record<string, Member[]> = {
  console: [
    fn('log', '(...args)', '输出一条 log 级日志'),
    fn('info', '(...args)', 'info 级日志'),
    fn('warn', '(...args)', 'warn 级日志'),
    fn('error', '(...args)', 'error 级日志'),
    fn('debug', '(...args)', 'debug 级日志'),
  ],
  store: [
    fn('get', '(key) → any', '读取持久化 KV'),
    fn('set', '(key, value)', '写入持久化 KV(落盘,改插件/重启不丢)'),
  ],
  header: [
    hfn('get', '(headers, name) → string', '读取头部值(名字大小写无关)', "get(${2:flow.headers}, '${1:name}')"),
    hfn('has', '(headers, name) → bool', '判断头部是否存在(名字大小写无关)', "has(${2:flow.headers}, '${1:name}')"),
    hfn('set', '(headers, name, value)', '写头部,已存在则覆盖(名字大小写无关)', "set(${3:flow.headers}, '${1:name}', ${2:value})"),
    hfn('del', '(headers, name)', '删除头部(名字大小写无关)', "del(${2:flow.headers}, '${1:name}')"),
  ],
  json: [
    fn('safeParse', '(str, fallback?) → any', '解析失败返回 fallback,不抛错'),
    fn('stringify', '(value, pretty?) → string', 'pretty=true 时 2 空格缩进'),
    fn('get', '(objOrStr, "a.b.0.c") → any', '点路径取值'),
  ],
  base64: [
    fn('encode', '(str) → string', '标准 base64 编码'),
    fn('decode', '(str) → string', '标准 base64 解码'),
    fn('urlEncode', '(str) → string', 'URL-safe base64(无填充)'),
    fn('urlDecode', '(str) → string', 'URL-safe base64 解码'),
    fn('encodeBytes', '(bytes[]) → string', '对字节数组做 base64'),
    fn('decodeBytes', '(str) → number[]', 'base64 解码为字节数组'),
  ],
  hex: [
    fn('encode', '(str) → string', '十六进制编码'),
    fn('decode', '(str) → string', '十六进制解码'),
  ],
  url: [{ name: 'parse', detail: '(str) → object', info: '解析为 {protocol, host, hostname, port, path, query, hash}', type: 'function', snip: 'parse(${1:flow.url})' }],
  query: [
    fn('parse', '(str) → object', '解析 query string 为对象'),
    fn('stringify', '(obj) → string', '对象编码为 query string'),
  ],
  crypto: [
    fn('md5', '(str) → hex', 'MD5(弱,勿用于安全场景)'),
    fn('sha1', '(str) → hex', 'SHA-1(弱,勿用于安全场景)'),
    fn('sha256', '(str) → hex', 'SHA-256'),
    fn('sha512', '(str) → hex', 'SHA-512'),
    fn('md5Base64', '(str) → base64', 'MD5,base64 输出'),
    fn('sha1Base64', '(str) → base64', 'SHA-1,base64 输出'),
    fn('sha256Base64', '(str) → base64', 'SHA-256,base64 输出'),
    fn('sha512Base64', '(str) → base64', 'SHA-512,base64 输出'),
    fn('hashBytes', '(algo, bytes[]) → hex', '对原始字节(number[])哈希,避免 UTF-8 二次编码'),
    fn('hmac', '(algo, key, msg) → hex', 'HMAC;algo 取 md5/sha1/sha256/sha512'),
    fn('hmacBase64', '(algo, key, msg) → base64', 'HMAC,base64 输出'),
    fn('hmacBase64Url', '(algo, key, msg) → base64url', 'HMAC,URL-safe base64(JWT 签名用)'),
    fn('randomBytes', '(n) → number[]', '密码学安全随机字节,n∈[1,4096]'),
    fn('randomInt', '(min, max) → number', '[min, max) 均匀随机整数'),
    fn('randomString', '(n, alphabet?) → string', '随机串,默认 base62,可传自定义字母表'),
  ],
  utf8: [
    fn('toBytes', '(str) → number[]', '字符串 → UTF-8 字节'),
    fn('fromBytes', '(bytes[]) → string', 'UTF-8 字节 → 字符串'),
  ],
  time: [
    fn('now', '() → number', '当前 Unix 毫秒'),
    fn('unix', '() → number', '当前 Unix 秒'),
    fn('iso', '() → string', '当前 RFC3339(UTC)'),
    fn('format', '(ms, layout?) → string', 'layout: datetime|date|iso'),
  ],
  jwt: [
    fn('decode', '(token) → {header,payload,signature}', '不验签,仅拆段'),
    fn('signHS256', '(payload, secret) → string', 'HS256 签发'),
    fn('verifyHS256', '(token, secret) → bool', '仅校验签名'),
  ],
}

/** 命名空间在顶层候选里的一句话作用说明(候选 detail)。 */
const NAMESPACE_DESC: Record<string, string> = {
  console: '日志输出(log/info/warn/error/debug)',
  store: '持久化 KV(落盘,改插件/重启不丢)',
  header: '大小写无关读写头',
  json: '容错解析与点路径取值',
  base64: 'Base64 编解码',
  hex: '十六进制编解码',
  url: 'URL 解析',
  query: 'query string 解析/编码',
  crypto: '哈希 / HMAC / 随机数',
  utf8: 'UTF-8 与字节数组互转',
  time: '时间戳与格式化',
  jwt: 'JWT 拆解与 HS256 签验',
}

/** flow 上的字段。phases 标明该字段在哪些钩子里存在,用于按光标所在钩子过滤。 */
interface FlowField {
  name: string
  ty: string
  info: string
  phases: readonly string[]
  /** 仅在无法判定钩子、混合展示时附加的来源标签(如「请求」「WS/流」)。 */
  tag?: string
  /** 对象字段(response/process),可继续 `.` 下钻。 */
  nested?: boolean
}

const ALL_PHASES = ['request', 'response', 'ws', 'stream'] as const

const FLOW_FIELDS: FlowField[] = [
  { name: 'id', ty: 'string', info: '本次请求/响应的流 ID(只读);WS/流消息钩子里恒为空', phases: ['request', 'response'] },
  { name: 'url', ty: 'string', info: '完整 URL;HTTP 阶段可改写,WS/流中只读', phases: ALL_PHASES },
  { name: 'method', ty: 'string', info: 'HTTP 方法,可改写', phases: ['request', 'response'], tag: '请求' },
  { name: 'host', ty: 'string', info: '目标主机,可改写', phases: ['request', 'response'], tag: '请求' },
  { name: 'path', ty: 'string', info: '请求路径(含 query),可改写', phases: ['request', 'response'], tag: '请求' },
  { name: 'headers', ty: 'object', info: '请求头扁平首值视图;配合 header.get/set 大小写无关读写', phases: ['request', 'response'], tag: '请求' },
  { name: 'body', ty: 'string', info: '请求体文本,可改写', phases: ['request', 'response'], tag: '请求' },
  { name: 'response', ty: 'object', info: '响应对象,onResponse 中可读改;构造伪造响应用 mock()', phases: ['response'], tag: '响应', nested: true },
  { name: 'process', ty: 'object', info: '发起进程 {name, pid, path},可能为空', phases: ['request', 'response'], tag: '进程', nested: true },
  { name: 'direction', ty: 'string', info: 'client->server | server->client', phases: ['ws', 'stream'], tag: 'WS/流' },
  { name: 'type', ty: 'string', info: 'WS 帧类型:text|binary|close|ping|pong', phases: ['ws'], tag: 'WS' },
  { name: 'data', ty: 'string', info: '消息负载文本,可就地改写', phases: ['ws', 'stream'], tag: 'WS/流' },
  { name: 'kind', ty: 'string', info: '流类型:sse|grpc|chunk', phases: ['stream'], tag: '流' },
  { name: 'eventType', ty: 'string', info: 'SSE 的 event 名;其余为空', phases: ['stream'], tag: 'SSE' },
]

const RESPONSE_FIELDS: Member[] = [
  prop('status', 'number', 'HTTP 状态码,可改写'),
  prop('statusText', 'string', '状态文本(如 OK)'),
  prop('headers', 'object', '响应头扁平视图;配合 header.* 读写'),
  prop('body', 'string', '响应体文本,可改写'),
  prop('reason', 'string', '仅 mock 时作为原因标注(abort 的原因走 abort({reason}))'),
]

const PROCESS_FIELDS: Member[] = [
  prop('name', 'string', '进程名'),
  prop('pid', 'number', '进程 PID'),
  prop('path', 'string', '可执行文件路径'),
]

/** 钩子函数名 → 内部阶段标识(与 plugin.go 的 __PHASE__ 一致)。 */
const HOOK_PHASE: Record<string, string> = {
  onRequest: 'request',
  onResponse: 'response',
  onWebSocketMessage: 'ws',
  onStreamMessage: 'stream',
}

const HOOK_SNIPPETS: Completion[] = [
  snippetCompletion('function onRequest(flow) {\n\t${}\n}', {
    label: 'onRequest',
    detail: '钩子 (flow)',
    info: '每个请求发出前调用;可改 flow、abort()/mock()/setBreakpoint()',
    type: 'function',
    boost: 20,
  }),
  snippetCompletion('function onResponse(flow) {\n\t${}\n}', {
    label: 'onResponse',
    detail: '钩子 (flow)',
    info: '收到响应后调用;可改 flow.response、abort()/setBreakpoint()',
    type: 'function',
    boost: 19,
  }),
  snippetCompletion('function onWebSocketMessage(flow) {\n\t${}\n}', {
    label: 'onWebSocketMessage',
    detail: '钩子 (flow)',
    info: '每条 WebSocket 帧调用',
    type: 'function',
    boost: 18,
  }),
  snippetCompletion('function onStreamMessage(flow) {\n\t${}\n}', {
    label: 'onStreamMessage',
    detail: '钩子 (flow)',
    info: '每条流式消息(SSE/gRPC/分块)调用',
    type: 'function',
    boost: 17,
  }),
]

/**
 * goja 运行时(ES5.1 + 大部分 ES2015 内置)确实支持的标准库全局,用于补全「完整 JS」。
 * 刻意不含 fetch/setTimeout/Promise/window/document/require 等——goja 是嵌入式引擎而非浏览器/Node,
 * 补出它们只会诱导作者写出跑不起来的代码。新增项前请确认 goja 支持。
 */
const ES_GLOBALS: Record<string, Member[]> = {
  Math: [
    fn('abs', '(x) → number'),
    fn('ceil', '(x) → number'),
    fn('floor', '(x) → number'),
    fn('round', '(x) → number'),
    fn('trunc', '(x) → number'),
    fn('sign', '(x) → number'),
    fn('max', '(...n) → number'),
    fn('min', '(...n) → number'),
    fn('pow', '(x, y) → number'),
    fn('sqrt', '(x) → number'),
    fn('cbrt', '(x) → number'),
    fn('hypot', '(...n) → number'),
    fn('exp', '(x) → number'),
    fn('log', '(x) → number'),
    fn('log2', '(x) → number'),
    fn('log10', '(x) → number'),
    fn('random', '() → number', '[0,1) 伪随机;安全场景用 crypto.randomInt'),
    prop('PI', 'number'),
    prop('E', 'number'),
  ],
  JSON: [
    fn('parse', '(str, reviver?) → any'),
    fn('stringify', '(value, replacer?, space?) → string'),
  ],
  Object: [
    fn('keys', '(obj) → string[]'),
    fn('values', '(obj) → any[]'),
    fn('entries', '(obj) → [k,v][]'),
    fn('assign', '(target, ...sources) → object'),
    fn('freeze', '(obj) → object'),
    fn('create', '(proto, props?) → object'),
    fn('getOwnPropertyNames', '(obj) → string[]'),
    fn('getPrototypeOf', '(obj) → object'),
    fn('defineProperty', '(obj, key, desc) → object'),
  ],
  Array: [
    fn('isArray', '(v) → bool'),
    fn('from', '(iterable, mapFn?) → any[]'),
    fn('of', '(...items) → any[]'),
  ],
  Number: [
    fn('isInteger', '(v) → bool'),
    fn('isFinite', '(v) → bool'),
    fn('isNaN', '(v) → bool'),
    fn('parseFloat', '(str) → number'),
    fn('parseInt', '(str, radix?) → number'),
    prop('MAX_SAFE_INTEGER', 'number'),
    prop('MIN_SAFE_INTEGER', 'number'),
  ],
  String: [
    fn('fromCharCode', '(...codes) → string'),
    fn('fromCodePoint', '(...points) → string'),
  ],
  Date: [
    fn('now', '() → number', '当前 Unix 毫秒(= time.now)'),
    fn('parse', '(str) → number'),
    fn('UTC', '(year, month, ...) → number'),
  ],
}

/** ES 全局对象在顶层候选里的说明。 */
const ES_GLOBAL_DESC: Record<string, string> = {
  Math: '数学函数与常量',
  JSON: 'JSON 解析/序列化',
  Object: '对象工具(keys/assign/freeze…)',
  Array: '数组工具(isArray/from/of)',
  Number: '数值工具与常量',
  String: '字符串静态方法',
  Date: '日期/时间戳',
}

/** goja 支持的全局函数(非对象成员)。 */
const ES_GLOBAL_FNS: Completion[] = [
  snippetCompletion('parseInt(${1:str}, ${2:10})', { label: 'parseInt', detail: '(str, radix?) → number', type: 'function' }),
  snippetCompletion('parseFloat(${1:str})', { label: 'parseFloat', detail: '(str) → number', type: 'function' }),
  snippetCompletion('isNaN(${1:v})', { label: 'isNaN', detail: '(v) → bool', type: 'function' }),
  snippetCompletion('isFinite(${1:v})', { label: 'isFinite', detail: '(v) → bool', type: 'function' }),
  snippetCompletion('encodeURIComponent(${1:str})', { label: 'encodeURIComponent', detail: '(str) → string', type: 'function' }),
  snippetCompletion('decodeURIComponent(${1:str})', { label: 'decodeURIComponent', detail: '(str) → string', type: 'function' }),
  snippetCompletion('encodeURI(${1:str})', { label: 'encodeURI', detail: '(str) → string', type: 'function' }),
  snippetCompletion('decodeURI(${1:str})', { label: 'decodeURI', detail: '(str) → string', type: 'function' }),
]

/** 标准 JS 关键字与字面量;无 boost,只在前缀匹配时垫在宿主 API 之后。 */
const JS_KEYWORDS: Completion[] = [
  'var', 'let', 'const', 'function', 'return', 'if', 'else', 'for', 'while', 'do',
  'switch', 'case', 'default', 'break', 'continue', 'try', 'catch', 'finally', 'throw',
  'typeof', 'instanceof', 'new', 'delete', 'void', 'in', 'of', 'this',
  'null', 'true', 'false', 'undefined', 'NaN', 'Infinity',
].map((k): Completion => ({ label: k, type: 'keyword' }))

/** 顶层标识符:全局函数、决策函数、命名空间对象。 */
const TOP_LEVEL: Completion[] = [
  ...HOOK_SNIPPETS,
  // 数字默认值要用编号占位符 ${1:403}:纯 ${403} 会被当成 tab 序号而丢失字面值
  snippetCompletion("abort({ status: ${1:403}, reason: ${2:'blocked'} })", { label: 'abort', detail: '决策', info: '拦截请求/响应并以指定状态码返回', type: 'function' }),
  snippetCompletion('mock({ status: ${1:200}, headers: {${2}}, body: ${3} })', { label: 'mock', detail: '决策 (onRequest)', info: '直接返回伪造响应,不发往上游', type: 'function' }),
  { label: 'setBreakpoint', detail: '决策 (onRequest/onResponse)', info: '挂起到断点,等 UI 放行', type: 'function', apply: 'setBreakpoint()' },
  snippetCompletion('notify(${1:title}, ${2:msg})', { label: 'notify', detail: '(title, msg)', info: '向 UI 推送通知', type: 'function' }),
  { label: 'uuid', detail: '() → string', info: 'UUID v4', type: 'function', apply: 'uuid()' },
  snippetCompletion('randomId(${1:8})', { label: 'randomId', detail: '(n?) → string', info: 'n 字节随机 hex(默认 8)', type: 'function' }),
  snippetCompletion('btoa(${1:str})', { label: 'btoa', detail: '(str) → string', info: '= base64.encode', type: 'function' }),
  snippetCompletion('atob(${1:str})', { label: 'atob', detail: '(str) → string', info: '= base64.decode', type: 'function' }),
  { label: 'flow', detail: '当前流', info: '请求/响应/帧的可变对象;输入 flow. 查看字段', type: 'variable' },
  { label: 'settings', detail: '只读配置', info: 'plugin.json 里的 settings 对象', type: 'variable' },
  ...Object.keys(NAMESPACES).map((ns): Completion => ({
    label: ns,
    detail: NAMESPACE_DESC[ns] ?? '助手命名空间',
    info: NAMESPACES[ns].map((m) => m.name).join(' · '),
    type: 'namespace',
  })),
  ...Object.keys(ES_GLOBALS).map((g): Completion => ({
    label: g,
    detail: ES_GLOBAL_DESC[g] ?? '内置对象',
    info: ES_GLOBALS[g].map((m) => m.name).join(' · '),
    type: g === 'Math' || g === 'JSON' ? 'namespace' : 'class',
  })),
  ...ES_GLOBAL_FNS,
  ...JS_KEYWORDS,
]

/** 占位符默认值:仅收录「绝大多数情况下就是这个值」的参数,避免预填错误反而误导。 */
const ARG_DEFAULTS: Record<string, string> = {
  algo: "'sha256'",
}

/** 由 detail 的签名生成带 tab 占位符的片段体。 */
function memberSnippet(m: Member): string {
  const sig = /^\(([^)]*)\)/.exec(m.detail.trim())
  const raw = sig ? sig[1].trim() : ''
  if (!raw) return `${m.name}()`
  const fields: string[] = []
  let i = 1
  for (const p of raw.split(',').map((s) => s.trim()).filter(Boolean)) {
    if (p.endsWith('?')) continue
    if (p.startsWith('...')) {
      fields.push('${' + i++ + '}')
      continue
    }
    const name = p.replace(/\[\]$/, '')
    fields.push('${' + i++ + ':' + (ARG_DEFAULTS[name] ?? name) + '}')
  }
  return `${m.name}(${fields.join(', ')})`
}

/** 方法 → 带参数占位符的片段补全,属性 → 原样插入。 */
function memberCompletion(m: Member): Completion {
  if (m.type === 'function' || m.type === undefined) {
    return snippetCompletion(m.snip ?? memberSnippet(m), { label: m.name, detail: m.detail, info: m.info, type: 'function' })
  }
  return { label: m.name, detail: m.detail, info: m.info, type: m.type, apply: m.name }
}

/** flow 字段补全:已知钩子则按 phases 过滤,未知则全展示并附来源标签。 */
function flowMembers(phase: string | null): Completion[] {
  const fields = phase ? FLOW_FIELDS.filter((f) => f.phases.includes(phase)) : FLOW_FIELDS
  return fields.map((f) => ({
    label: f.name,
    detail: !phase && f.tag ? `${f.ty} · ${f.tag}` : f.ty,
    info: f.info,
    type: f.nested ? 'variable' : 'property',
  }))
}

/** 沿语法树向上找到光标所在的钩子函数,返回其阶段标识;判定不出则 null。 */
function enclosingPhase(context: CompletionContext): string | null {
  for (let node: SyntaxNode | null = syntaxTree(context.state).resolveInner(context.pos, -1); node; node = node.parent) {
    let name: string | null = null
    if (node.name === 'FunctionDeclaration') {
      const def = node.getChild('VariableDefinition')
      if (def) name = context.state.sliceDoc(def.from, def.to)
    } else if (node.name === 'ArrowFunction' || node.name === 'FunctionExpression') {
      name = assignedName(node, context)
    }
    if (name && HOOK_PHASE[name]) return HOOK_PHASE[name]
  }
  return null
}

/** 取赋给箭头/函数表达式的变量名,覆盖 `const onRequest = …` 与 `onRequest = …` 两种写法。 */
function assignedName(fn: SyntaxNode, context: CompletionContext): string | null {
  const parent = fn.parent
  if (!parent) return null
  if (parent.name === 'VariableDeclaration') {
    const def = parent.getChild('VariableDefinition')
    if (def) return context.state.sliceDoc(def.from, def.to)
  } else if (parent.name === 'AssignmentExpression') {
    // 钩子必须是全局函数,只认 `onRequest = function/arrow`(VariableName);obj.x= 形式不算钩子。
    const lhs = parent.firstChild
    if (lhs && lhs.name === 'VariableName') {
      return context.state.sliceDoc(lhs.from, lhs.to)
    }
  }
  return null
}

/** 解析 `base.partial` 中的 base,给出其成员候选;base 未知则返回 null(交还给 lang-js 补全)。 */
function membersForBase(base: string, context: CompletionContext): Completion[] | null {
  if (base === 'flow') return flowMembers(enclosingPhase(context))
  if (base === 'flow.response') return RESPONSE_FIELDS.map(memberCompletion)
  if (base === 'flow.process') return PROCESS_FIELDS.map(memberCompletion)
  const ns = NAMESPACES[base]
  if (ns) return ns.map(memberCompletion)
  const es = ES_GLOBALS[base]
  return es ? es.map(memberCompletion) : null
}

/**
 * sniffy 宿主 API 补全源。挂到 javascriptLanguage 的 language data 上,
 * 与 lang-javascript 自带的局部作用域补全并存、合并展示。
 */
export function sniffyCompletionSource(context: CompletionContext): CompletionResult | null {
  // 支持多级成员路径(flow.response.…),取最后一个 `.` 之前的整段作为 base。
  const chain = context.matchBefore(/[\w$]+(?:\.[\w$]+)*\.[\w$]*$/)
  if (chain) {
    const dot = chain.text.lastIndexOf('.')
    const base = chain.text.slice(0, dot)
    const partial = chain.text.slice(dot + 1)
    const options = membersForBase(base, context)
    if (!options) return null
    return { from: chain.to - partial.length, options, validFor: /^[\w$]*$/ }
  }
  const word = context.matchBefore(/[\w$]+/)
  if (!word && !context.explicit) return null
  return {
    from: word ? word.from : context.pos,
    options: TOP_LEVEL,
    validFor: /^[\w$]*$/,
  }
}
