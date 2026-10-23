// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

// Wails v3 桌面入口。此文件供 `wails3 dev` / `go build -tags desktop` 使用。
// 系统需具备 webview 依赖:
//   - Windows: WebView2 Runtime（go-webview2，无需 CGO）
//   - macOS:   WKWebView(系统自带，需 CGO)
//   - Linux:   libgtk-4-dev + libwebkitgtk-6.0-dev（需 CGO）
//
// 开发模式: 先 `cd web && npm run dev`，再以 FRONTEND_DEVSERVER_URL 指向 Vite 运行本程序；
//
//	或直接 `go run -tags desktop .`（使用已构建的 web/dist）。
//
// 生产构建: scripts/build.sh desktop。
package main

import (
	"embed"
	"io/fs"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/desktop"
)

//go:embed all:web/dist
var assets embed.FS

func main() {
	dist, err := fs.Sub(assets, "web/dist")
	if err != nil {
		app.Fatalf("加载前端资源失败: %v", err)
	}

	if err := desktop.Run(dist); err != nil {
		app.Fatalf("桌面应用运行失败: %v", err)
	}
}
