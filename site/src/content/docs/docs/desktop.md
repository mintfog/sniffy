---
title: 桌面版快速开始
description: 安装 Sniffy、设置代理、配置 HTTPS 证书并查看第一条会话。
---

## 下载与安装

在 [GitHub Releases](https://github.com/mintfog/sniffy/releases) 中选择对应平台的安装包。

| 操作系统 | 架构       | 安装包                                       |
| -------- | ---------- | -------------------------------------------- |
| Windows  | x64        | `sniffy-desktop-windows-amd64-installer.exe` |
| macOS    | Apple 芯片 | `sniffy-desktop-darwin-arm64-installer.dmg`  |
| macOS    | Intel      | `sniffy-desktop-darwin-amd64-installer.dmg`  |
| Linux    | x64        | `sniffy-desktop-linux-amd64-installer.deb`   |

## 接入流量

打开 Sniffy，开启「系统代理」，或将目标应用的 HTTP / HTTPS 代理设为 `127.0.0.1:8080`。如果修改过监听端口，请使用设置中的实际值。

## 配置 HTTPS

打开「证书管理」，按照应用内的平台引导安装并信任 Sniffy 根证书。抓取其他设备的 HTTPS 流量时，也需要在对应设备上完成证书信任配置。

## 查看第一条会话

1. 在目标应用中发起一次请求。
2. 回到流量列表，按域名或关键字筛选会话。
3. 选择会话，查看请求与响应的头部、正文、状态码和耗时。
4. 使用请求重发、规则改写或断点验证接口行为。

## 编写插件

打开「插件」页面，新建插件并选择内置模板。编辑脚本后保存并启用，已启用的插件支持保存后热重载。

```js
function onRequest(flow) {
  if (flow.path !== "/api/profile") return;

  mock({
    status: 200,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: "Sniffy", plan: "pro" }),
  });
}
```

可通过插件白名单限定目标域名。完整宿主 API 见[插件助手函数参考](https://github.com/mintfog/sniffy/blob/main/docs/plugins-helpers.md)。
