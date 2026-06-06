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
  "description": "演示插件 API:给响应加 X-Sniffy 头;命中 /api/ 时把响应体中的 foo 替换为 bar。默认禁用。",
  "runtime": "js",
  "entry": "index.js",
  "enabled": false,
  "priority": 100,
  "whitelist": [],
  "blacklist": []
}
`

const exampleScript = `// Sniffy 示例插件
// 可用钩子: onRequest(flow) / onResponse(flow) / onWebSocketMessage(msg)
// flow 字段: id, method, url, host, path, headers{}, body, response{status,statusText,headers,body}
// 处置助手: mock({status,headers,body}) / abort({status,reason}) / setBreakpoint()
// 宿主 API: console.log/info/warn/error, store.get/set, settings, notify(title,msg)

function onResponse(flow) {
  if (flow.response && flow.response.headers) {
    flow.response.headers['X-Sniffy'] = 'hello';
  }
  if (flow.url && flow.url.indexOf('/api/') !== -1 && flow.response && flow.response.body) {
    flow.response.body = flow.response.body.split('foo').join('bar');
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
