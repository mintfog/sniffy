/** 新建插件时可选的起始模板。键 blank 表示空白脚本。 */
export interface PluginTemplate {
  key: string
  /** i18n 标签键（plugins.new.tpl.*）。 */
  labelKey: string
  source: string
}

export const PLUGIN_TEMPLATES: PluginTemplate[] = [
  {
    key: 'blank',
    labelKey: 'plugins.new.tpl.blank',
    source: '',
  },
  {
    key: 'log',
    labelKey: 'plugins.new.tpl.log',
    source: `// 打印每个请求的方法与 URL
function onRequest(flow) {
  console.log(flow.method, flow.url)
}
`,
  },
  {
    key: 'header',
    labelKey: 'plugins.new.tpl.header',
    source: `// 给匹配的请求注入一个自定义头
function onRequest(flow) {
  header.set(flow.headers, 'X-Sniffy', 'hello')
}
`,
  },
  {
    key: 'mock',
    labelKey: 'plugins.new.tpl.mock',
    source: `// 直接返回伪造响应，不发往上游
function onRequest(flow) {
  if (flow.path === '/ping') {
    mock({
      status: 200,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ok: true, ts: time.now() }),
    })
  }
}
`,
  },
  {
    key: 'sign',
    labelKey: 'plugins.new.tpl.sign',
    source: `// 用 HMAC-SHA256 给请求加签名头
function onRequest(flow) {
  var ts = String(time.unix())
  var sign = crypto.hmac('sha256', settings.appSecret || '', flow.path + ts)
  header.set(flow.headers, 'X-Timestamp', ts)
  header.set(flow.headers, 'X-Sign', sign)
}
`,
  },
]
