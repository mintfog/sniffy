// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

// Wails 桌面入口。此文件供 `wails dev` / `wails build` 使用。
// 系统需具备 webview 依赖:
//   - Windows: WebView2 Runtime
//   - macOS:   WKWebView(系统自带)
//   - Linux:   libwebkit2gtk-4.0-dev
//
// 开发模式: wails dev -tags desktop
// 生产构建: wails build -tags desktop
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/mintfog/sniffy/internal/api"
	"github.com/mintfog/sniffy/internal/app"
	"github.com/mintfog/sniffy/internal/desktop"
)

//go:embed all:web2/dist
var assets embed.FS

func main() {
	cfg := app.DefaultConfig()

	application, err := app.Build(cfg, false)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
	if err := application.Start(); err != nil {
		log.Fatalf("启动引擎失败: %v", err)
	}

	// 桌面进程内同时跑本地管理 API（仅监听回环），嵌入的前端经 HTTP/WS 访问它。
	// 这样前端代码在浏览器 / headless / 桌面三种场景完全一致。
	apiServer := api.New(application.Service, application.Pipeline, application.Plugins, "127.0.0.1:8888")
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("本地 API 退出: %v", err)
		}
	}()

	bridge := desktop.New(application)

	sub, err := fs.Sub(assets, "web2/dist")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}

	err = wails.Run(&options.App{
		Title:  "Sniffy",
		Width:  1280,
		Height: 820,
		AssetServer: &assetserver.Options{
			Assets: sub,
		},
		OnStartup:  bridge.Startup,
		OnShutdown: bridge.Shutdown,
		Bind:       []interface{}{bridge},
	})
	if err != nil {
		log.Fatalf("Wails 运行失败: %v", err)
	}
}
