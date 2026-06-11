// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

// 桌面入口(Wails v3)。需以 -tags desktop 构建,且系统需具备 webview 依赖:
//   - Windows: WebView2(go-webview2，无需 CGO)
//   - macOS:   WKWebView(系统自带，需 CGO)
//   - Linux:   libwebkit2gtk-4.1-dev(需 CGO)
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
	cfg := app.DefaultConfig()
	// 应用持久化配置(config.json)中保存的监听地址/端口,UI 中的修改重启后生效。
	cfg.Address, cfg.Port = app.ResolveListen(cfg.Address, cfg.Port)

	sniffyApp, err := app.Build(cfg, false)
	if err != nil {
		app.Fatalf("初始化失败: %v", err)
	}
	if err := sniffyApp.Start(); err != nil {
		app.Fatalf("启动引擎失败: %v", err)
	}

	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		app.Fatalf("加载前端资源失败: %v", err)
	}

	if err := desktop.Run(sniffyApp, dist); err != nil {
		app.Fatalf("Wails 运行失败: %v", err)
	}
}
