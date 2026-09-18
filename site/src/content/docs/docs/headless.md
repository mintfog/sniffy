---
title: 无界面版
description: 在服务器、容器或 CI 中运行 Sniffy，配置认证、TLS 与远程访问。
---

无界面版（headless）通过 REST 与 WebSocket 接口管理抓包、规则和插件，适合服务器、容器、CI 与脚本自动化场景。

规则、插件与配置与桌面版共用同一个配置目录，同一台机器上两者可以交替使用。

## 启动

用 Releases 里的可执行文件：

```bash
./sniffy-linux-amd64
```

或在仓库里直接跑（需要 Go 1.26+）：

```bash
go run ./cmd/sniffy
```

启动后：

- 代理监听 `0.0.0.0:8080`
- 管理 API 监听 `http://127.0.0.1:8888`
- 首次启动会生成根证书与管理 API token，路径写在启动日志里

按 `Ctrl+C` 优雅关闭，最长等待 30 秒。

## 命令行参数

| 参数                             | 默认        | 用途                                             |
| -------------------------------- | ----------- | ------------------------------------------------ |
| `-addr`                          | `0.0.0.0`   | 代理监听地址                                     |
| `-port`                          | `8080`      | 代理监听端口                                     |
| `-api-addr`                      | `127.0.0.1` | 管理 API 监听地址                                |
| `-api-port`                      | `8888`      | 管理 API（HTTP + WebSocket）端口                  |
| `-api-tls-cert`                  | 空          | 管理 API 的 TLS 证书路径                         |
| `-api-tls-key`                   | 空          | 管理 API 的 TLS 私钥路径                         |
| `-allow-insecure-api`            | 关          | 允许管理 API 在非回环地址上以明文 HTTP 监听      |
| `-v`                             | 关          | 详细日志                                         |
| `-version`                       | —           | 打印版本号后退出                                 |

配置的优先级是**默认值 < `config.json` < 显式给出的命令行参数**。

代理侧的其余设置（上游代理、解密范围、限速、代理认证等）通过 `config.json` 或 `PUT /api/config` 调整，见[配置参考](/docs/configuration/)。

## 认证

管理 API 在所有监听地址上都要求 Bearer token，包括 `127.0.0.1` 等回环地址。

token 有两个来源：

1. 环境变量 `SNIFFY_API_TOKEN`
2. 配置区的 `api_token` 文件（缺失时自动生成）

自动生成的 token 文件仅允许当前用户访问：POSIX 权限为 `0600`，Windows 使用仅所有者可访问的 DACL。

token 单独存放在 `api_token` 中，不通过配置接口读写。

### 权限过宽会自动轮换

启动时若发现 `api_token` 可被其他用户读取，Sniffy 会收紧权限并重新生成 token。该实例启动后使用新值，调用方需要重新读取文件。

token 安全处理失败时，无论绑定什么地址一律拒绝启动。

### 使用 token

```bash
export SNIFFY_API_TOKEN="$(cat ~/.config/sniffy/api_token)"

curl http://127.0.0.1:8888/api/sessions \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN"
```

REST 只认 `Authorization` 请求头。只有 `/api/ws` 额外接受 `?token=` 查询参数，因为有些 WebSocket 客户端设不了请求头：

```bash
websocat "ws://127.0.0.1:8888/api/ws?token=$SNIFFY_API_TOKEN"
```

用查询参数时注意别把带 token 的地址写进日志或分享出去。

## 远程访问

管理 API 绑定非回环地址时，必须配 TLS：

```bash
./sniffy-linux-amd64 \
  -api-addr 0.0.0.0 \
  -api-tls-cert /etc/sniffy/api.crt \
  -api-tls-key /etc/sniffy/api.key
```

管理 API 绑定非回环地址且未启用 TLS 时，默认拒绝启动。已由 TLS 反向代理或 VPN 保护的部署可使用以下参数允许 HTTP：

```bash
./sniffy-linux-amd64 -api-addr 127.0.0.1 -allow-insecure-api
```

启用 HTTPS 后 WebSocket 地址相应变成 `wss://`。

管理端口绑定失败或 TLS 证书加载失败时，Sniffy 会终止启动并报告错误。

### 方法与路径

除认证之外，调用 API 时还需遵循以下两条：

- **请求方法**：各端点仅接受文档列出的方法，其余返回 405，并通过 `Allow` 头列出可用方法。改变状态的操作不接受 `GET` 与 `HEAD`。
- **请求路径**：未定义的子路径返回 404，例如 `/api/sessions/{id}/typo`。

浏览器页面连 `/api/ws` 时，升级请求还需与管理端口同源，详见[认证与安全](/docs/api/auth/)。

## 常见部署方式

### systemd

```ini
[Unit]
Description=Sniffy proxy
After=network.target

[Service]
ExecStart=/usr/local/bin/sniffy -addr 0.0.0.0 -port 8080
Environment=SNIFFY_API_TOKEN=%your-token%
User=sniffy
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

token 通过环境变量注入时，建议放在 systemd 的 `EnvironmentFile` 里并把该文件设为 `0600`，而不是直接写进 unit 文件。

### Docker

```dockerfile
FROM debian:stable-slim
COPY sniffy-linux-amd64 /usr/local/bin/sniffy
EXPOSE 8080 8888
ENTRYPOINT ["/usr/local/bin/sniffy", "-api-addr", "0.0.0.0", "-allow-insecure-api"]
```

```bash
docker run --rm -p 8080:8080 -p 8888:8888 \
  -e SNIFFY_API_TOKEN=... \
  -v sniffy-config:/root/.config/sniffy \
  sniffy
```

把配置目录挂成卷，根证书与规则才能在容器重建后保留。容器里的 `-api-addr 0.0.0.0` 是为了让宿主机能连上，务必只把 8888 端口暴露给可信网络，或在前面放一层 TLS 反代。

### CI

在流水线里跑接口测试并检查实际发出的请求：

```bash
./sniffy -port 8080 &
export SNIFFY_API_TOKEN="$(cat ~/.config/sniffy/api_token)"
export http_proxy=http://127.0.0.1:8080 https_proxy=http://127.0.0.1:8080

npm test

curl -s -X POST http://127.0.0.1:8888/api/export \
  -H "Authorization: Bearer $SNIFFY_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"hosts":["api.example.com"]}' > sessions.json
```

HTTPS 需要让被测程序信任根证书，见 [HTTPS 解密与根证书](/docs/https/)。

## 实时事件

通过 `/api/ws` 订阅抓包事件，实时跟踪流量变化：

```bash
websocat "ws://127.0.0.1:8888/api/ws?token=$SNIFFY_API_TOKEN"
```

消息是 `{"type": "...", "payload": ...}`。事件类型清单见[管理 API 参考](/docs/api/events/)。

客户端消费过慢时可能丢失事件或断开连接。重新连接后，可通过 `/api/sessions` 查询仍在保留范围内的会话；会话保留上限由 `maxFlows` 控制。

## 日志

日志写到配置区的 `logs/`，按天滚动，保留 7 天。`-v` 打开详细日志。

## 相关

- [管理 API 参考](/docs/api/) —— 全部端点
- [配置参考](/docs/configuration/) —— `config.json` 字段
- [HTTPS 解密与根证书](/docs/https/) —— 让被测程序信任根证书
