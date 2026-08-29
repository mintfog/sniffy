# 插件助手函数参考

sniffy 的 JS 插件运行在 goja 上,宿主预置了一批**纯计算**助手函数,覆盖编码、哈希、签名、时间等常见抓包改包场景,无需自带依赖即可像 Postman 脚本那样处理数据。

所有助手都是全局可用的,在 `onRequest` / `onResponse` / `onWebSocketMessage` / `onStreamMessage` 任意钩子里直接调用。

## 运行约束(先读)

助手的能力边界由插件的执行模型决定:

- **同步、立即返回**:每个插件独占一个 goja 运行时,单线程串行执行,无事件循环、无 `Promise`/`setTimeout`、无法发起网络请求。助手都是纯 CPU 计算,调用即返回。
- **100ms 超时**:每次钩子调用默认 100ms 超时,超时即放行原始流量。别在助手里处理超大 body(例如对几 MB 的响应体反复哈希),会拖过预算。
- **二进制走 hex/base64**:哈希、HMAC 等产物以十六进制或 base64 字符串返回；原始字节使用 `number[]`（每项 0–255），见 [`utf8`](#utf8--字节版-base64)。非 UTF-8 的请求、响应和消息载荷使用 base64 侧通道，见[载荷通道](#载荷通道body-与-bodyb64)；头值使用单值字符串视图，见[头值字节语义](#头值字节语义)。
- **容错而非抛错**:解析类助手(`base64.decode`、`json.safeParse` 等)遇到非法输入返回空值/兜底值,不抛异常打断钩子。

> Postman 的 `pm.sendRequest`(异步发请求)、可视化、测试运行器等依赖事件循环或专有 UI 的能力**不在**助手范围内。

---

## 载荷通道:body 与 bodyB64

载荷跨 VM 边界以 JSON 传递,而 JSON 字符串必须是合法 UTF-8。载荷因此拆成文本与 base64 两个字段:

| 字段 | 何时有值 | 说明 |
|---|---|---|
| `flow.body` / `flow.data` / `flow.response.body` | 载荷是合法 UTF-8 | 文本原文,直接读写 |
| `flow.bodyB64` / `flow.dataB64` / `flow.response.bodyB64` | 载荷不是合法 UTF-8 | 原始字节的标准 base64;此时文本字段为空串 |

规则:

- **载荷回程**:文本字段非空时采用文本值；文本为空且 b64 非空时解码 b64。未修改的 b64 载荷逐字节回传。
- **通道识别**:根据 b64 字段是否存在选择载荷通道，消息类型字段只描述帧类型。
- **修改二进制**:`base64.decodeBytes` → 修改 `number[]` → `base64.encodeBytes` 写回 b64 字段；文本字段保持空串。
- **修改文本或清空**:写入文本字段并 `delete` 同名 b64 字段；清空二进制载荷使用 `delete` b64 字段。
- **mock 二进制响应**:`mock({ status: 200, bodyB64: base64.encodeBytes(bytes) })`，与 `body` 二选一。

```js
function onRequest(f) {
  if (!f.bodyB64) return                    // 文本载荷走 f.body
  var bytes = base64.decodeBytes(f.bodyB64)
  bytes[0] = 0x01
  f.bodyB64 = base64.encodeBytes(bytes)
}
```

```js
// 把二进制载荷换成文本：清理 b64 字段后写入文本字段
function onWebSocketMessage(msg) {
  delete msg.dataB64
  msg.data = 'replaced'
}
```

> `base64.decodeBytes(undefined)` 返回空数组；文本载荷应先判断 `f.bodyB64` 再读取字节。

### 头值字节语义

`f.headers` / `f.response.headers` 是名字到首值的扁平对象,没有 `headersB64` 这样的旁路。
头值同样允许非 UTF-8 字节(最常见的是 `Content-Disposition` 里的 Latin-1 文件名),这些
字节跨 VM 边界时被逐个换成 U+FFFD(`\uFFFD`,界面上的「�」),脚本看到的就是这个形态。

- **原样回传**:宿主以送入 VM 的视图作为比较基准；值保持一致时写回原始字节。
- **改写头值**:`f.headers['Content-Disposition'] += '!'` 按脚本看到的字符串生成新的头值，U+FFFD 也作为该字符串的一部分写线。
- **删除头值**:`delete f.headers['X-Foo']` 按头名移除对应字段。

头值采用扁平字符串视图；宿主通过回程比较恢复未改动值的原始字节，脚本改写时按新的字符串值写线。

---

## 编码 / 解码

### base64

| 调用 | 返回 | 说明 |
|---|---|---|
| `base64.encode(str)` | string | 标准 base64 编码 |
| `base64.decode(str)` | string | 标准 base64 解码,非法输入返回 `""` |
| `base64.urlEncode(str)` | string | URL-safe base64,**无填充**(`=`) |
| `base64.urlDecode(str)` | string | URL-safe base64 解码 |
| `base64.encodeBytes(bytes[])` | string | 对 `number[]` 原始字节做 base64 |
| `base64.decodeBytes(str)` | `number[]` | base64 解码为原始字节数组 |
| `btoa(str)` / `atob(str)` | string | Web 习惯别名,等价 `base64.encode` / `decode` |

### hex

| 调用 | 返回 | 说明 |
|---|---|---|
| `hex.encode(str)` | string | 十六进制编码 |
| `hex.decode(str)` | string | 十六进制解码,非法输入返回 `""` |

### url / query

| 调用 | 返回 | 说明 |
|---|---|---|
| `url.parse(str)` | object | 解析为 `{protocol, host, hostname, port, path, query{}, hash}` |
| `query.parse(str)` | object | 解析 query string(可带前导 `?`)为对象 |
| `query.stringify(obj)` | string | 对象编码为 query string |

```js
function onRequest(f) {
  var u = url.parse(f.url)
  console.log(u.path, query.stringify({ a: 1, b: 'x' }))  // /api  a=1&b=x
}
```

---

## crypto

哈希、HMAC、随机数。哈希/HMAC 默认输出十六进制小写;带 `Base64` 后缀的输出标准 base64。

| 调用 | 返回 | 说明 |
|---|---|---|
| `crypto.md5(str)` | hex | MD5 |
| `crypto.sha1(str)` | hex | SHA-1 |
| `crypto.sha256(str)` | hex | SHA-256 |
| `crypto.sha512(str)` | hex | SHA-512 |
| `crypto.md5Base64(str)` 等 | base64 | 上述各算法的 base64 输出 |
| `crypto.hashBytes(algo, bytes[])` | hex | 对 `number[]` 原始字节哈希,`algo` ∈ `md5/sha1/sha256/sha512` |
| `crypto.hmac(algo, key, msg)` | hex | HMAC,`algo` 同上 |
| `crypto.hmacBase64(algo, key, msg)` | base64 | HMAC,标准 base64 输出 |
| `crypto.hmacBase64Url(algo, key, msg)` | base64url | HMAC,URL-safe 无填充(JWT 用) |
| `crypto.randomBytes(n)` | `number[]` | `n` 个随机字节(0–255),`n` ∈ [1, 4096] |
| `crypto.randomInt(min, max)` | number | `[min, max)` 区间均匀随机整数 |
| `crypto.randomString(n, alphabet?)` | string | 随机串,默认 base62 字母表 |

随机数均来自 `crypto/rand`(密码学安全)。

```js
// 给请求加一个 HMAC 签名头(接口鉴权常见套路)
function onRequest(f) {
  var ts = String(time.unix())
  var sign = crypto.hmac('sha256', settings.appSecret, f.path + ts)
  header.set(f.headers, 'X-Timestamp', ts)
  header.set(f.headers, 'X-Sign', sign)
}
```

> 哈希的字符串入参按字节直接计算。对纯文本无歧义;若要对**真正的二进制**(例如先 `base64.decodeBytes` 得到的字节)哈希,请走 `crypto.hashBytes(algo, bytes)`,避免 UTF-8 二次编码。

---

## jwt

最小 JWT 工具:解码不验签,签发/验签仅支持 HS256。

| 调用 | 返回 | 说明 |
|---|---|---|
| `jwt.decode(token)` | `{header, payload, signature}` 或 `null` | 仅拆三段并 JSON 解析,**不验签** |
| `jwt.signHS256(payloadObj, secret)` | string | 用 HS256 签发(头部固定 `{alg:'HS256',typ:'JWT'}`) |
| `jwt.verifyHS256(token, secret)` | bool | 仅校验签名,**不校验 `exp` 等声明** |

```js
function onRequest(f) {
  var auth = header.get(f.headers, 'Authorization') || ''
  var claims = jwt.decode(auth.replace('Bearer ', ''))
  if (claims && claims.payload.role !== 'admin') abort({ status: 403, reason: 'not admin' })
}
```

---

## json

`JSON.parse` / `JSON.stringify` 原生可用;`json.*` 提供容错与点路径取值。

| 调用 | 返回 | 说明 |
|---|---|---|
| `json.safeParse(str, fallback?)` | any | 解析失败返回 `fallback`(默认 `null`),不抛错 |
| `json.stringify(value, pretty?)` | string | `pretty=true` 时 2 空格缩进;失败返回 `""` |
| `json.get(objOrStr, "a.b.0.c")` | any | 点路径取值(数字段当数组下标),缺失返回 `undefined` |

```js
function onResponse(f) {
  var token = json.get(f.response.body, 'data.token')
  if (token) store.set('lastToken', token)
}
```

---

## time

| 调用 | 返回 | 说明 |
|---|---|---|
| `time.now()` | number | 当前 Unix **毫秒** |
| `time.unix()` | number | 当前 Unix **秒** |
| `time.iso()` | string | 当前时间 RFC3339(UTC) |
| `time.format(ms, layout?)` | string | 格式化毫秒时间戳(UTC) |

`layout` 取值:`"datetime"`(默认,`2006-01-02 15:04:05`)、`"date"`(`2006-01-02`)、`"iso"`(RFC3339),或自定义 Go 参考时间布局串。

---

## utf8 / 字节版 base64

原始字节以 `number[]`(每项 0–255)在 JS 侧承载,避免 latin1 字符串歧义。

| 调用 | 返回 | 说明 |
|---|---|---|
| `utf8.toBytes(str)` | `number[]` | 字符串 → UTF-8 字节数组 |
| `utf8.fromBytes(bytes[])` | string | UTF-8 字节数组 → 字符串 |

配合 `base64.encodeBytes` / `decodeBytes`、`crypto.hashBytes` 处理二进制数据。

---

## 其它常用

| 调用 | 返回 | 说明 |
|---|---|---|
| `uuid()` | string | UUID v4 |
| `randomId(n?)` | string | `n` 个随机字节的十六进制串(默认 8 字节) |
| `header.get/has/set/del(headers, name)` | — | 对扁平头对象做**大小写无关**读写 |
| `console.log/info/warn/error/debug(...)` | — | 写插件日志(对象自动 JSON 化) |
| `store.get(k)` / `store.set(k, v)` | — | 每插件持久化 KV(可落盘,改插件/重启不丢);持久化值需可 JSON 序列化,NaN/Infinity 与循环引用路径会被跳过并记录 error,其余字段照常保留 |
| `settings` | object | 插件 `plugin.json` 里的只读配置 |
| `notify(title, msg)` | — | 向 UI 推送通知 |

钩子内可用的决策函数:`abort({status,reason})`、`mock({status,headers,body})` 或 `mock({status,headers,bodyB64})`(仅 `onRequest`)、`setBreakpoint()`(仅 `onRequest`/`onResponse`)。
