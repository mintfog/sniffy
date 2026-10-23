// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

// 桌面入口(Wails v3)。需以 -tags desktop 构建,且系统需具备 webview 依赖:
//   - Windows: WebView2(go-webview2，无需 CGO)
//   - macOS:   WKWebView(系统自带，需 CGO)
//   - Linux:   libgtk-4-dev + libwebkitgtk-6.0-dev(需 CGO)
//
// 前端资源需先构建并拷入本目录 dist/(见 scripts/build.sh desktop)。
package main

import (
	"embed"
	"io/fs"

	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/desktop"
)

//go:embed all:dist
var assets embed.FS

func main() {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		app.Fatalf("加载前端资源失败: %v", err)
	}

	if err := desktop.Run(dist); err != nil {
		app.Fatalf("桌面应用运行失败: %v", err)
	}
}
