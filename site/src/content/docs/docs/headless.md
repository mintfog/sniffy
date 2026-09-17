---
title: 无界面版与 API 接入
description: 启动 Sniffy Headless，配置 Bearer token 并调用 REST API。
---

Headless 模式适合服务器部署和自动化调试。REST API 用于管理会话、规则和插件，WebSocket 用于订阅实时事件。

## 启动服务

从 [Releases](https://github.com/mintfog/sniffy/releases) 下载对应平台的 Headless 可执行文件，或在仓库根目录使用 Go 1.26+ 启动：

```bash
go run ./cmd/sniffy
```

默认代理端口为 `8080`，管理 API 地址为 `http://127.0.0.1:8888`。

| 参数                             | 用途                             |
| -------------------------------- | -------------------------------- |
| `-addr` / `-port`                | 指定代理监听地址和端口           |
| `-api-addr` / `-api-port`        | 指定管理 API 监听地址和端口      |
| `-api-tls-cert` / `-api-tls-key` | 配置管理 API 的 HTTPS 证书和私钥 |

## Bearer token 认证

所有管理 API 请求均需 Bearer token，包括访问本机回环地址的请求。

启动时可通过环境变量 `SNIFFY_API_TOKEN` 设置 token；未设置时，Sniffy 自动生成并保存到用户配置目录下的 `sniffy/api_token` 文件，具体路径见启动日志。

将实际 token 设为调用终端的 `SNIFFY_API_TOKEN`，然后查询会话：

```bash
curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

REST API 通过 `Authorization` 请求头接收 token。

## WebSocket 实时事件

事件入口为 `/api/ws`，连接地址为 `ws://127.0.0.1:8888/api/ws`。启用 HTTPS 时使用 `wss://`。

连接可通过 `Authorization: Bearer <token>` 请求头认证。对于无法设置该头部的 WebSocket 客户端，`/api/ws` 也支持 `?token=<token>` 查询参数。使用查询参数时，应避免把包含 token 的连接地址写入日志或分享给他人。

## 远程访问

管理 API 绑定非回环地址时，需通过 `-api-tls-cert` 和 `-api-tls-key` 配置 HTTPS。由 TLS 反向代理或 VPN 保护的环境，可通过 `-allow-insecure-api` 显式允许 HTTP。

接口实现与路由定义见仓库的 [internal/api](https://github.com/mintfog/sniffy/tree/main/internal/api)。本文提供启动和接入示例；各资源接口的完整参数与返回结构仍需进一步整理。
