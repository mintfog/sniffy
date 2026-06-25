// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package plugin

import (
	"os"
	"path/filepath"
)

const exampleManifest = `{
  "id": "example-add-header",
  "name": "示例:注入响应头",
  "version": "1.0.0",
  "author": "sniffy",
  "description": "演示插件 API 与配置项:给响应加自定义头;开启改写时把命中 /api/ 的响应体中的 foo 替换为 bar。默认禁用。",
  "runtime": "js",
  "entry": "index.js",
  "enabled": false,
  "priority": 100,
  "whitelist": [],
  "blacklist": [],
  "settings": {
    "headerName": "X-Sniffy",
    "headerValue": "hello",
    "rewriteBody": true
  },
  "settingsSchema": [
    { "key": "headerName", "type": "string", "label": "响应头名称", "default": "X-Sniffy", "description": "注入到响应里的头名" },
    { "key": "headerValue", "type": "string", "label": "响应头值", "default": "hello" },
    { "key": "rewriteBody", "type": "boolean", "label": "改写响应体", "default": true, "description": "命中 /api/ 时把 foo 替换为 bar" }
  ]
}
`

const exampleScript = `// Sniffy 示例插件
// 可用钩子: onRequest(flow) / onResponse(flow) / onWebSocketMessage(msg) / onStreamMessage(msg)
// flow 字段: id, method, url, host, path, headers{}, body, response{status,statusText,headers,body}
// 处置助手: mock({status,headers,body}) / abort({status,reason}) / setBreakpoint()
// 宿主 API: console.log/info/warn/error, store.get/set, settings, notify(title,msg)
// 助手函数: base64.encode/decode, hex.encode/decode, url.parse, query.parse/stringify,
//           header.get/set/del/has, uuid(), randomId(n)

function onResponse(flow) {
  if (flow.response && flow.response.headers) {
    header.set(flow.response.headers, settings.headerName || 'X-Sniffy', settings.headerValue || 'hello');
  }
  if (settings.rewriteBody && flow.url && flow.url.indexOf('/api/') !== -1 && flow.response && flow.response.body) {
    flow.response.body = flow.response.body.split('foo').join('bar');
    console.log('rewrote body for', flow.url);
  }
}
`

// newPluginTemplate 是「页面内新建插件」时的起始脚本。
const newPluginTemplate = `// Sniffy 插件 —— 在此实现你的钩子。
// 钩子:onRequest(flow) / onResponse(flow) / onWebSocketMessage(msg) / onStreamMessage(msg)
// 处置:mock({status,headers,body}) / abort({status,reason}) / setBreakpoint()
// 宿主:console.*, store.get/set, settings, notify(title,msg)
// 助手:base64.*, hex.*, url.parse, query.*, header.*, uuid(), randomId(n)

function onRequest(flow) {
  // 例:给所有请求加一个标记头
  header.set(flow.headers, 'X-Plugin', 'hello');
}

function onResponse(flow) {
  // 例:打印响应状态
  if (flow.response) {
    console.log(flow.method, flow.url, '->', flow.response.status);
  }
}
`

// seedExamples 在插件目录为空时写入一个示例插件,便于用户上手。
func (m *Manager) seedExamples() {
	if m.dir == "" {
		return
	}
	entries, err := os.ReadDir(m.dir)
	if err == nil && len(entries) > 0 {
		return
	}
	dir := filepath.Join(m.dir, "example-add-header")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(exampleManifest), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "index.js"), []byte(exampleScript), 0o644)
}
