// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

// 桌面入口(Wails v2)。需以 -tags desktop 构建,且系统需具备 webview 依赖:
//   - Windows: WebView2
//   - macOS:   WKWebView(系统自带)
//   - Linux:   libwebkit2gtk-4.0-dev
//
// 推荐使用 `wails build` 或 scripts/build.sh desktop。
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

//go:embed all:dist
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

	// 桌面进程内同时跑本地管理 API(仅监听回环),嵌入的前端经 HTTP/WS 访问它。
	// 这样前端代码在浏览器 / headless / 桌面三种场景完全一致。
	apiServer := api.New(application.Service, application.Pipeline, application.Plugins, "127.0.0.1:8888")
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("本地 API 退出: %v", err)
		}
	}()

	bridge := desktop.New(application)

	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}

	appOpts := &options.App{
		Title:  "Sniffy",
		Width:  1280,
		Height: 820,
		AssetServer: &assetserver.Options{
			Assets: sub,
		},
		OnStartup:  bridge.Startup,
		OnShutdown: bridge.Shutdown,
		Bind:       []interface{}{bridge},
	}
	// 按平台设置标题栏外观：Windows 自绘 / macOS 原生集成 / Linux 原生装饰。
	desktop.ApplyPlatformChrome(appOpts)

	if err = wails.Run(appOpts); err != nil {
		log.Fatalf("Wails 运行失败: %v", err)
	}
}
