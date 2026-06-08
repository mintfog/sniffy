// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"io/fs"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mintfog/sniffy/internal/app"
)

// Run 装配并运行 Wails v3 桌面应用:把 Bridge 注册为 Service、嵌入前端资源、按平台设置窗口外观,
// 然后阻塞运行直到窗口关闭。两个桌面入口(根 main.go 与 cmd/sniffy-desktop)共用此装配。
//
// dist 应为前端构建产物(web/dist)的子文件系统。AssetFileServerFS 会自动挂载
// Wails 运行时 IPC(/wails/runtime),前端经 @wailsio/runtime 调用 Bridge 方法、接收事件。
func Run(sniffyApp *app.App, dist fs.FS) error {
	bridge := New(sniffyApp)

	wapp := application.New(application.Options{
		Name:        "Sniffy",
		Description: "HTTP/HTTPS 抓包代理工具",
		Services: []application.Service{
			application.NewService(bridge),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(dist),
		},
	})

	winOpts := application.WebviewWindowOptions{
		Title:            "Sniffy",
		Width:            1280,
		Height:           820,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(17, 17, 23),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
	}
	ApplyPlatformChrome(&winOpts)
	wapp.Window.NewWithOptions(winOpts)

	return wapp.Run()
}
