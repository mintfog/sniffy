// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

// Wails v3 桌面入口。此文件供 `wails3 dev` / `go build -tags desktop` 使用。
// 系统需具备 webview 依赖:
//   - Windows: WebView2 Runtime（go-webview2，无需 CGO）
//   - macOS:   WKWebView(系统自带，需 CGO)
//   - Linux:   libwebkit2gtk-4.1-dev（需 CGO）
//
// 开发模式: 先 `cd web && npm run dev`，再以 FRONTEND_DEVSERVER_URL 指向 Vite 运行本程序；
//          或直接 `go run -tags desktop .`（使用已构建的 web/dist）。
// 生产构建: scripts/build.sh desktop。
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/desktop"
)

//go:embed all:web/dist
var assets embed.FS

func main() {
	cfg := app.DefaultConfig()

	sniffyApp, err := app.Build(cfg, false)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
	if err := sniffyApp.Start(); err != nil {
		log.Fatalf("启动引擎失败: %v", err)
	}

	dist, err := fs.Sub(assets, "web/dist")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}

	if err := desktop.Run(sniffyApp, dist); err != nil {
		log.Fatalf("Wails 运行失败: %v", err)
	}
}
