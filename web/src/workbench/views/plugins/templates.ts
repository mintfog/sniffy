import type { SettingField } from './model'

/** 模板携带的配置项声明：labelKey/descKey 为 i18n 键，创建时解析为当前语言写入 manifest。 */
export interface TemplateField {
  key: string
  type?: SettingField['type']
  default?: unknown
  labelKey: string
  descKey?: string
  placeholder?: string
  options?: { value: unknown; labelKey?: string }[]
}

/** 新建插件时可选的起始模板。键 blank 表示空白脚本。 */
export interface PluginTemplate {
  key: string
  /** 下拉项标签 i18n 键（plugins.new.tpl.*）。 */
  labelKey: string
  /** 选中后展示的一句话说明 i18n 键（plugins.new.tplDesc.*）。 */
  descKey?: string
  source: string
  /** 声明配置项；创建时按 default 生成 settings，并把 schema 写入 manifest 驱动配置页表单。 */
  schema?: TemplateField[]
}

export const PLUGIN_TEMPLATES: PluginTemplate[] = [
  {
    key: 'blank',
    labelKey: 'plugins.new.tpl.blank',
    descKey: 'plugins.new.tplDesc.blank',
    source: `// 在此实现你的钩子。可用钩子与 API 见文档 docs/plugins-helpers.md。
// onRequest(flow) / onResponse(flow) / onWebSocketMessage(msg) / onStreamMessage(msg)
// 处置：mock({status,headers,body|bodyB64}) / abort({status,reason}) / setBreakpoint()
// 载荷：合法 UTF-8 走 body/data；其余载荷文本字段为空、字节在 bodyB64/dataB64（标准 base64）
// 宿主：console.*, store.get/set, settings, notify(title,msg)
// 助手：base64.*, hex.*, crypto.*, jwt.*, json.*, time.*, url.parse, query.*, header.*, uuid()

function onRequest(flow) {
}
`,
  },
  {
    key: 'log',
    labelKey: 'plugins.new.tpl.log',
    descKey: 'plugins.new.tplDesc.log',
    source: `// 打印每条请求与响应的概要，便于快速观察流量
function onRequest(flow) {
  console.log('→', flow.method, flow.url)
}

function onResponse(flow) {
  if (flow.response) {
    console.log('←', flow.response.status, flow.method, flow.url)
  }
}
`,
  },
  {
    key: 'header',
    labelKey: 'plugins.new.tpl.header',
    descKey: 'plugins.new.tplDesc.header',
    schema: [
      { key: 'name', type: 'string', default: '', labelKey: 'plugins.new.tplField.header.name', placeholder: 'X-Sniffy' },
      { key: 'value', type: 'string', default: '', labelKey: 'plugins.new.tplField.header.value', placeholder: 'hello' },
    ],
    source: `// 给命中的请求注入/覆盖一个请求头（用配置项设定头名与值）。
// name 配置后向命中请求注入该头；name 为空时保持请求头集合不变。
function onRequest(flow) {
  if (!settings.name) return
  header.set(flow.headers, settings.name, settings.value || '')
}
`,
  },
  {
    key: 'mock',
    labelKey: 'plugins.new.tpl.mock',
    descKey: 'plugins.new.tplDesc.mock',
    schema: [
      { key: 'path', type: 'string', default: '', labelKey: 'plugins.new.tplField.mock.path', descKey: 'plugins.new.tplField.mock.pathDesc', placeholder: '/api/user' },
      { key: 'status', type: 'number', default: 200, labelKey: 'plugins.new.tplField.mock.status' },
      { key: 'body', type: 'text', default: '{"ok":true}', labelKey: 'plugins.new.tplField.mock.body' },
    ],
    source: `// 命中指定路径时直接返回伪造响应（仅 onRequest 生效）。
// path 配置后按请求路径匹配；path 为空时匹配集合为空。
function onRequest(flow) {
  if (settings.path && flow.path === settings.path) {
    mock({
      status: Number(settings.status) || 200,
      headers: { 'Content-Type': 'application/json' },
      body: settings.body || '{"ok":true}',
    })
  }
}
`,
  },
  {
    key: 'rewriteResponse',
    labelKey: 'plugins.new.tpl.rewriteResponse',
    descKey: 'plugins.new.tplDesc.rewriteResponse',
    schema: [
      { key: 'urlContains', type: 'string', default: '', labelKey: 'plugins.new.tplField.common.urlContains', descKey: 'plugins.new.tplField.common.urlContainsDesc', placeholder: '/api/' },
      { key: 'search', type: 'string', default: '', labelKey: 'plugins.new.tplField.rewriteResponse.search' },
      { key: 'replace', type: 'string', default: '', labelKey: 'plugins.new.tplField.rewriteResponse.replace' },
    ],
    source: `// 改写命中响应的文本内容。
// 响应体在进插件前已按 Content-Encoding 解压为明文，可直接字符串替换；
// 改动后引擎按明文重算 Content-Length 并去掉压缩头，无需手动处理。
// search 配置后执行替换；search 为空时保留响应体。
function onResponse(flow) {
  if (!flow.response || !flow.response.body) return
  if (settings.urlContains && flow.url.indexOf(settings.urlContains) === -1) return
  if (settings.search) {
    flow.response.body = flow.response.body.split(settings.search).join(settings.replace || '')
  }
}
`,
  },
  {
    key: 'binary',
    labelKey: 'plugins.new.tpl.binary',
    descKey: 'plugins.new.tplDesc.binary',
    source: `// 观察 / 改写二进制请求体与响应体（protobuf、图片、压缩包等）。
// 非 UTF-8 载荷的原始字节在 flow.bodyB64 / flow.response.bodyB64（标准 base64），对应文本字段为空。
// 改二进制写回 b64 字段；改成文本要把 b64 字段置空 —— 两个字段同时有值会按文本为准并记一条错误日志。
function onRequest(flow) {
  if (!flow.bodyB64) return
  var bytes = base64.decodeBytes(flow.bodyB64)
  console.log('→', flow.method, flow.url, bytes.length, 'bytes', crypto.hashBytes('sha256', bytes))

  // 改写示例：改完写回 bodyB64
  // bytes[0] = 0x01
  // flow.bodyB64 = base64.encodeBytes(bytes)
}

function onResponse(flow) {
  if (!flow.response || !flow.response.bodyB64) return
  var bytes = base64.decodeBytes(flow.response.bodyB64)
  console.log('←', flow.response.status, bytes.length, 'bytes')
}
`,
  },
  {
    key: 'redirect',
    labelKey: 'plugins.new.tpl.redirect',
    descKey: 'plugins.new.tplDesc.redirect',
    schema: [
      { key: 'fromHost', type: 'string', default: '', labelKey: 'plugins.new.tplField.redirect.fromHost', placeholder: 'old.example.com' },
      { key: 'toHost', type: 'string', default: '', labelKey: 'plugins.new.tplField.redirect.toHost', placeholder: 'new.example.com' },
    ],
    source: `// 把命中主机的请求整体重定向到另一个主机（HTTP/HTTPS 均可跨主机）。
// 转发目标由 flow.url 决定；跨主机时同步 flow.host 以更新 Host 头。
function onRequest(flow) {
  var from = settings.fromHost
  var to = settings.toHost
  if (from && to && flow.host === from) {
    // 仅替换 scheme 后的主机部分，保留 query/path 中的同名文本。
    flow.url = flow.url.split('://' + from).join('://' + to)
    flow.host = to
  }
}
`,
  },
  {
    key: 'block',
    labelKey: 'plugins.new.tpl.block',
    descKey: 'plugins.new.tplDesc.block',
    schema: [
      { key: 'urlContains', type: 'string', default: '', labelKey: 'plugins.new.tplField.common.urlContains', descKey: 'plugins.new.tplField.block.urlContainsDesc', placeholder: 'ads.example.com' },
      { key: 'status', type: 'number', default: 403, labelKey: 'plugins.new.tplField.block.status', descKey: 'plugins.new.tplField.block.statusDesc' },
      { key: 'reason', type: 'string', default: 'blocked by sniffy', labelKey: 'plugins.new.tplField.block.reason' },
    ],
    source: `// 屏蔽命中的请求：status 非 0 时回一个错误响应，为 0 时直接断开连接。
// urlContains 配置后启用 URL 匹配；为空时匹配集合为空。
function onRequest(flow) {
  var kw = settings.urlContains || ''
  if (kw && flow.url.indexOf(kw) !== -1) {
    // status 为空时使用 403，显式 0 表示断开连接。
    var s = settings.status
    var code = (s === 0 || s === '0') ? 0 : (Number(s) || 403)
    abort({ status: code, reason: settings.reason || 'blocked by sniffy' })
  }
}
`,
  },
  {
    key: 'cors',
    labelKey: 'plugins.new.tpl.cors',
    descKey: 'plugins.new.tplDesc.cors',
    schema: [
      { key: 'origin', type: 'string', default: '*', labelKey: 'plugins.new.tplField.cors.origin', descKey: 'plugins.new.tplField.cors.originDesc', placeholder: '*' },
    ],
    source: `// 放开跨域：回显请求 Origin 并给响应补 CORS 头,预检 OPTIONS 直接放行。
// 会作用于命中的每个响应,建议在插件配置里设白名单限定站点。
function onRequest(flow) {
  // CORS 预检由带 Origin 的 OPTIONS 请求表示。
  if (flow.method === 'OPTIONS' && header.has(flow.headers, 'Origin')) {
    var h = {}
    applyCors(h, flow)
    mock({ status: 204, headers: h })
  }
}

function onResponse(flow) {
  if (!flow.response) return
  // 响应头对象按需创建后写入 CORS 字段。
  applyCors(flow.response.headers || (flow.response.headers = {}), flow)
}

function applyCors(h, flow) {
  // 配置具体 Origin 时回显该值；通配配置按请求 Origin 回显，缺少时使用 *。
  var want = settings.origin || '*'
  var origin = want !== '*' ? want : (header.get(flow.headers, 'Origin') || '*')
  header.set(h, 'Access-Control-Allow-Origin', origin)
  header.set(h, 'Access-Control-Allow-Methods', 'GET,POST,PUT,PATCH,DELETE,OPTIONS')
  // 预检响应回显请求声明的头，普通响应使用 *。
  header.set(h, 'Access-Control-Allow-Headers', header.get(flow.headers, 'Access-Control-Request-Headers') || '*')
  if (origin !== '*') {
    header.set(h, 'Access-Control-Allow-Credentials', 'true')
    header.set(h, 'Vary', 'Origin')
  } else {
    header.del(h, 'Access-Control-Allow-Credentials')
  }
}
`,
  },
  {
    key: 'authToken',
    labelKey: 'plugins.new.tpl.authToken',
    descKey: 'plugins.new.tplDesc.authToken',
    schema: [
      { key: 'loginPath', type: 'string', default: '', labelKey: 'plugins.new.tplField.authToken.loginPath', placeholder: '/api/login' },
      { key: 'tokenField', type: 'string', default: 'data.token', labelKey: 'plugins.new.tplField.authToken.tokenField', descKey: 'plugins.new.tplField.authToken.tokenFieldDesc', placeholder: 'data.token' },
      { key: 'headerName', type: 'string', default: 'Authorization', labelKey: 'plugins.new.tplField.authToken.headerName' },
    ],
    source: `// 从登录响应里捕获 token 存入 store，后续请求自动带上鉴权头。
// loginPath 配置后启用登录响应捕获；store 落盘，重载与重启后继续使用已存的 token。
// 插件配置中的白名单可限定作用站点，避免向无关主机注入鉴权头。
function onResponse(flow) {
  if (!flow.response || !settings.loginPath) return
  if (flow.path === settings.loginPath) {
    var token = json.get(flow.response.body, settings.tokenField || 'data.token')
    if (token) {
      store.set('token:' + flow.host, token)
      console.log('captured token for', flow.host)
    }
  }
}

function onRequest(flow) {
  var name = settings.headerName || 'Authorization'
  if (header.has(flow.headers, name)) return
  var token = store.get('token:' + flow.host)
  if (token) {
    header.set(flow.headers, name, 'Bearer ' + token)
  }
}
`,
  },
  {
    key: 'sign',
    labelKey: 'plugins.new.tpl.sign',
    descKey: 'plugins.new.tplDesc.sign',
    schema: [
      { key: 'appSecret', type: 'string', default: '', labelKey: 'plugins.new.tplField.sign.appSecret', descKey: 'plugins.new.tplField.sign.appSecretDesc' },
    ],
    source: `// 用 HMAC-SHA256 给请求加时间戳与签名头（接口鉴权常见套路）。
// appSecret 配置后注入签名头；为空时保持请求头集合不变。
function onRequest(flow) {
  if (!settings.appSecret) return
  var ts = String(time.unix())
  var sign = crypto.hmac('sha256', settings.appSecret, flow.path + ts)
  header.set(flow.headers, 'X-Timestamp', ts)
  header.set(flow.headers, 'X-Sign', sign)
}
`,
  },
  {
    key: 'breakpoint',
    labelKey: 'plugins.new.tpl.breakpoint',
    descKey: 'plugins.new.tplDesc.breakpoint',
    schema: [
      { key: 'urlContains', type: 'string', default: '', labelKey: 'plugins.new.tplField.common.urlContains', descKey: 'plugins.new.tplField.breakpoint.urlContainsDesc', placeholder: '/api/order' },
    ],
    source: `// 命中 URL 时挂起请求，等 UI 手动放行 / 改包 / 丢弃（仅 onRequest/onResponse 有效）。
// urlContains 配置后启用 URL 匹配；为空时不产生断点。
function onRequest(flow) {
  var kw = settings.urlContains || ''
  if (kw && flow.url.indexOf(kw) !== -1) {
    setBreakpoint()
  }
}
`,
  },
  {
    key: 'websocket',
    labelKey: 'plugins.new.tpl.websocket',
    descKey: 'plugins.new.tplDesc.websocket',
    source: `// 观察 / 改写 WebSocket 消息。
// msg.direction: 'client->server' | 'server->client'；msg.type: 'text' | 'binary'。
// 载荷通道由字节决定而非帧类型：合法 UTF-8 在 msg.data，其余字节在 msg.dataB64（标准 base64），
// msg.type 只描述帧类型，判断通道要看 msg.dataB64 是否存在。
function onWebSocketMessage(msg) {
  var arrow = msg.direction === 'client->server' ? '↑' : '↓'
  if (msg.dataB64) {
    var bytes = base64.decodeBytes(msg.dataB64)
    console.log(arrow, '[binary]', bytes.length, 'bytes')

    // 改写二进制示例：写回 dataB64
    // bytes[0] = 0x01
    // msg.dataB64 = base64.encodeBytes(bytes)
    return
  }
  console.log(arrow, msg.data)

  // 改写示例：把文本里的 foo 换成 bar（帧长度/掩码由引擎按新载荷重算）
  // msg.data = msg.data.split('foo').join('bar')
}
`,
  },
  {
    key: 'stream',
    labelKey: 'plugins.new.tpl.stream',
    descKey: 'plugins.new.tplDesc.stream',
    source: `// 观察 / 改写流式响应（SSE / gRPC / 分块 JSON）。
// msg.kind: 'sse' | 'grpc' | 'chunk'；msg.data 是去掉协议外壳的纯载荷。
// SSE 的 msg.eventType 仅在事件带 event: 字段时非空。
// gRPC 等非 UTF-8 载荷在 msg.dataB64（标准 base64），用 base64.decodeBytes/encodeBytes 读写。
function onStreamMessage(msg) {
  if (msg.dataB64) {
    console.log('[' + msg.kind + ']', base64.decodeBytes(msg.dataB64).length, 'bytes')
    return
  }
  console.log('[' + msg.kind + ']', msg.eventType || '', msg.data)

  // 改写示例（SSE 写入纯载荷，event:/data: 外壳由引擎补全）：
  // msg.data = msg.data.split('foo').join('bar')
}
`,
  },
]
