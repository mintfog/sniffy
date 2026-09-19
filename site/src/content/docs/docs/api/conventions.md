---
title: 约定与错误
description: 管理 API 的响应包络、分页结构、状态码语义与调用时的通用规则。
---

本页介绍响应格式、分页、错误码和字段更新约定。具体字段与限制见各端点文档。

## 响应包络

大多数 JSON 端点使用以下响应包络；分页、文件下载与流式导出的格式见后文：

```json
{
  "data": { },
  "success": true,
  "timestamp": "2026-09-17T10:00:00+08:00"
}
```

| 字段        | 类型    | 说明                                         |
| ----------- | ------- | -------------------------------------------- |
| `data`      | any     | 实际载荷。无内容时（如删除成功）整个字段缺省 |
| `success`   | boolean | 是否成功                                     |
| `message`   | string  | 失败原因，成功时缺省                         |
| `timestamp` | string  | 服务端时间，RFC3339                          |

失败时：

```json
{
  "success": false,
  "message": "session not found",
  "timestamp": "2026-09-17T10:00:00+08:00"
}
```

先检查 HTTP 状态码，再按对应格式解析响应。反向代理等中间层返回的错误可能使用其他格式。

### 不走包络的端点

| 端点                                   | 返回                                    |
| -------------------------------------- | --------------------------------------- |
| `/api/certificate/ca`                  | PEM 文件，`application/x-pem-file`      |
| `/api/certificate/ios-profile`         | 描述文件，`application/x-apple-aspen-config` |
| `/api/certificate/export`              | 证书文件，格式随请求参数                |
| `/api/sessions/{id}/body/raw`          | 正文原始字节，支持 Range                |
| `/api/export`                          | 流式 JSON 数组                          |
| 分页列表端点                           | 分页结构，见下                          |

## 分页

会话、WebSocket 会话、流式会话与重写规则列表使用以下分页结构。插件、断点等列表采用各自端点说明中的格式：

```json
{
  "data": [],
  "total": 128,
  "page": 1,
  "pageSize": 50,
  "hasNext": true,
  "hasPrev": false
}
```

| 查询参数   | 默认 | 说明                       |
| ---------- | ---- | -------------------------- |
| `page`     | `1`  | 页码，从 1 开始            |
| `pageSize` | `50` | 每页条数                   |

两个参数都必须是正整数，非法值（负数、零、非数字）被忽略并回退到默认值，不会报错。

页码超出范围时，`data` 返回空数组，`total` 仍表示总条数。

遍历全部数据：

```bash
page=1
while :; do
  resp=$(curl -s "$SNIFFY_API/api/sessions?page=$page&pageSize=100" \
           -H "Authorization: Bearer $SNIFFY_API_TOKEN")
  echo "$resp" | jq -c '.data[]'
  [ "$(echo "$resp" | jq -r '.hasNext')" = "true" ] || break
  page=$((page + 1))
done
```

:::caution
抓包期间会话列表会持续变化，分页查询可能出现重复或遗漏。批量获取可使用 [`/api/export`](/docs/api/export/) 减少分页带来的影响；该接口逐条读取会话，也不提供事务快照。
:::

## 状态码

| 状态码 | 含义                                     | 典型原因                                       |
| ------ | ---------------------------------------- | ---------------------------------------------- |
| 200    | 成功                                     | —                                              |
| 400    | 请求有问题                               | JSON 畸形、必填字段缺失、取值超范围、未知字段  |
| 401    | 认证失败                                 | token 缺失、不匹配，或刚被轮换                 |
| 403    | 跨站请求被拒                             | WebSocket 升级的 `Origin` 与 `Host` 不同源             |
| 404    | 资源不存在                               | id 不存在，或路径含未知 / 多余的段             |
| 405    | 方法不允许                               | 该端点不放行这个方法，响应带 `Allow` 头        |
| 413    | 请求体过大                               | 超过该端点的上限                               |
| 500    | 服务端处理失败                           | 证书生成失败、磁盘写入失败等                   |
| 501    | 能力不可用                               | 当前装配没有插件系统、断点管理器或证书管理     |
| 503    | 构造器未装配                             | `/api/compose` 系列没有接上发送入口            |

### 请求体上限

超过上限返回 413，与 400 区分：前者要减少内容，后者要改字段或格式。

| 端点                     | 上限     |
| ------------------------ | -------- |
| `/api/compose`           | 8 MiB    |
| `/api/compose/ws/*/send` | 12 MiB   |
| `/api/breakpoints/*`     | 9 MiB    |
| `/api/certificate/export`| 64 KiB   |
| `/api/certificate/import`| 11 MiB（证书文件本身 10 MiB） |
| `/api/export`            | 64 KiB   |

### 500 与重试

`500` 表示服务端处理失败，不保证操作已回滚。重试前请查询资源状态，并确认操作是否幂等。例如，`DELETE /api/plugins/{id}` 可能已停用插件，但未成功删除目录；此时重启后插件可能重新加载。

## 严格解码

以下端点拒绝未知字段，字段名不在请求结构中时返回 400：

| 端点                             | 说明                                                     |
| -------------------------------- | -------------------------------------------------------------- |
| `/api/breakpoints/{id}/resume`   | 接受编辑补丁。暂停快照的正文使用 base64，补丁的 `body` 使用文本；请按补丁结构提交 |
| `/api/export`                    | 拒绝未知过滤字段，避免按错误的条件导出数据                 |

这两个端点还要求请求体**恰好是一个 JSON 值**，尾随内容会报错。

## 补丁式修改

`/api/config` 与 `/api/plugins/{id}/manifest` 接受部分字段，只写要改的：

```bash
curl -X PUT $SNIFFY_API/api/config \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"throttle": true}'
```

未出现的字段保持不变。这与 `PUT /api/intercept/rules/{id}` 不同——后者是**整条替换**，没给的字段会变成零值。

数组字段是整体替换而非追加：`{"decryptAllow": ["a.com"]}` 会让名单里只剩 `a.com`。

## 头部的字节旁路

会话与断点接口里，凡是头部都可能带一个 `headersB64` 旁路字段。

原因是头值允许非 UTF-8 字节（最常见的是 `Content-Disposition` 里的 Latin-1 文件名），而 JSON 字符串必须是合法 UTF-8。这类字节在主字段里被替换成 `U+FFFD`，原始字节放在旁路里。

| 场景                   | 怎么处理                                                    |
| ---------------------- | ----------------------------------------------------------- |
| 只是读取和展示         | 用主字段，忽略旁路                                           |
| 需要字节保真           | 旁路项非空时以它为准                                         |
| 回写（断点、构造器）   | 旁路是与主列表**下标对齐的全量列表**，文本位置填空串，长度必须一致 |

## 时间与编码

- 所有时间戳是 RFC3339 字符串。
- 耗时字段（`duration`、`responseTime`）是毫秒整数，`uptime` 是秒。
- 请求体一律 UTF-8 JSON，除 `/api/certificate/import` 用 `multipart/form-data`。
- 响应默认 `Content-Type: application/json`。

## 并发

接口没有乐观锁与 ETag。同一份规则或配置被两个客户端同时改时，后写入的胜出。

需要串行化的场合（例如 CI 里多个作业共用一个 Sniffy），在调用方自己加锁。

## 相关

- [认证与安全](/docs/api/auth/) —— 401 与 403 的由来
- [管理 API 概览](/docs/api/) —— 端点索引
