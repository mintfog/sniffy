---
title: 认证与安全
description: 管理 API 的 Bearer token 来源与轮换、TLS 要求，以及 CSRF 与方法白名单等边界。
---

## 认证要求

**所有绑定地址都强制 Bearer token，回环地址也不例外。**

管理 API 可读取抓包内容并修改运行配置。即使绑定回环地址，同机的其他用户和进程也可能访问该端口，因此仍需认证。

## token 从哪来

按优先级：

1. 环境变量 `SNIFFY_API_TOKEN`
2. 配置区的 `api_token` 文件

两者都没有时，Sniffy 启动时自动生成并写入 `api_token`，路径打印在启动日志里：

```
已生成管理 API token 并保存到 /home/you/.config/sniffy/api_token;
请求需携带 Authorization: Bearer <token>,WebSocket 可用 ?token=
```

| 平台    | 文件权限                                |
| ------- | --------------------------------------- |
| POSIX   | `0600`                                  |
| Windows | owner-only DACL（`x/sys/windows` 设置） |

多个实例同时使用同一配置目录启动时，token 文件的创建会协调完成，各实例读取同一个已保存的值。

### token 的存储

token 单独存放在 `api_token` 中。配置接口读写 `config.json` 时，不会返回或更新 token。

## 权限过宽会自动轮换

启动时如果发现 `api_token` 对 group 或 other 可读，说明旧值可能已经泄漏，Sniffy 会：

1. 重新生成 token，启动中的实例使用新值；
2. 把文件权限收紧；
3. 在日志里说明发生了轮换。

```
检测到 API token 文件权限过宽(旧值可能已泄漏),已轮换为新凭证,旧凭证即时失效
```

生成、轮换与权限修改通过跨进程文件锁协调，锁在进程退出时自动释放。

token 生成、读取或权限处理失败时，Sniffy 会拒绝启动，并在日志中报告原因。

:::caution
出现轮换日志后，请重新读取 token 文件并更新调用方的凭据。使用旧值访问新启动的实例会返回 401。
:::

## 怎么带 token

### REST

只认 `Authorization` 请求头：

```bash
export SNIFFY_API_TOKEN="$(cat ~/.config/sniffy/api_token)"

curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

`Bearer ` 前缀不区分大小写，token 首尾空白会被移除，token 内容需完全匹配。

### WebSocket

`/api/ws` 除了请求头，**额外**接受查询参数——浏览器的 `WebSocket` 构造器设不了请求头：

```bash
websocat "ws://127.0.0.1:8888/api/ws?token=$SNIFFY_API_TOKEN"
```

```js
const ws = new WebSocket(`ws://127.0.0.1:8888/api/ws?token=${token}`);
```

查询参数只在 `/api/ws` 这一条路径上生效，其余端点用它会得到 401。

:::caution
URL 中的 token 可能被 shell 历史、进程列表或访问日志记录。支持设置请求头时，优先使用 `Authorization`。
:::

## 传输安全

| 绑定地址 | TLS | 结果                                           |
| -------- | --- | ---------------------------------------------- |
| 回环     | 无  | 正常启动                                       |
| 回环     | 有  | 正常启动，走 `https://` 与 `wss://`            |
| 非回环   | 有  | 正常启动                                       |
| 非回环   | 无  | **拒绝启动**，除非显式加 `-allow-insecure-api` |

```bash
./sniffy \
  -api-addr 0.0.0.0 \
  -api-tls-cert /etc/sniffy/api.crt \
  -api-tls-key /etc/sniffy/api.key
```

两个 TLS 参数必须同时给出，只给一个会报错退出。

`-allow-insecure-api` 适用于已由 TLS 反向代理或 VPN 保护的部署，启用时会打印警告。该选项允许明文 HTTP，管理端口应限制在受保护的网络内。

### 启动检查

启动时会绑定管理端口并加载 TLS 证书。端口被占用、证书无法读取或内容无效时，Sniffy 会终止启动并报告错误。

## 其余边界

以下检查决定请求是否会被端点接受。

### CSRF 与 DNS rebinding

`/api/ws` 的升级请求需与管理端口同源：带 `Origin` 时其 host 必须与 `Host` 一致，否则升级被拒，返回 403。从浏览器页面订阅事件时，请直接以管理端口的地址作为 WebSocket 地址。

把管理 API 嵌进自己的程序、且没有配置 token 时，所有请求都要先过一遍同源检查：

- `Host` 必须是回环地址或 `localhost`
- `Sec-Fetch-Site` 只接受空值、`same-origin`、`none`
- 有 `Origin` 时，其 host 必须与 `Host` 一致

不通过返回 `403 cross-site request forbidden`。

### 方法白名单

每个端点仅接受文档列出的方法，其他方法返回 `405`，并通过 `Allow` 头列出可用方法。修改状态的操作不接受 `GET` 或 `HEAD`。

部分端点的读取与写入共用路径，通过请求方法区分：

| 路径                       | GET    | PUT                |
| -------------------------- | ------ | ------------------ |
| `/api/plugins/{id}/source` | 读脚本 | 写脚本并热重载     |
| `/api/config`              | 读配置 | 写配置（PUT/POST） |

### 路径多余段

未定义的子路径返回 `404 unknown action`，例如 `/api/sessions/{id}/typo`。请使用端点文档中的完整路径。

## 部署清单

远程部署时，检查以下配置：

- [ ] token 通过环境变量或受限权限的文件注入，没有硬编码进镜像或 unit 文件
- [ ] 绑定非回环地址时启用了 TLS，或确认前面有 TLS 反代
- [ ] 管理端口仅对可信网络开放
- [ ] 日志与监控里没有记录带 `?token=` 的 URL
- [ ] 配置目录挂了持久卷，容器重建后不会每次重新生成 token 与根证书

## 相关

- [约定与错误](/docs/api/conventions/) —— 401 / 403 / 405 分别意味着什么
- [无界面版](/docs/headless/) —— 启动参数与部署方式
- [疑难排查](/docs/troubleshooting/#管理-api-相关) —— 认证失败的排查顺序
