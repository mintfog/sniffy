// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"io/fs"
	"runtime"

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

	// 启动期占位 UI 按机器语言渲染（见 locale.go）；前端就绪后由 SetMenu 下发用户实际选择的语言。
	labels := labelsFor(uiLang())

	wapp := application.New(application.Options{
		Name:        "Sniffy",
		Description: labels.description,
		// Icon 用于 Windows 任务栏/窗口图标与关于框、macOS Dock。Windows 上 Wails 优先读取
		// 编译进二进制的 .syso 资源(本仓库构建流程不生成)，缺失时回退到此处的 Icon，故必须显式设置，
		// 否则任务栏显示系统默认图标。
		Icon: appIcon,
		Services: []application.Service{
			application.NewService(bridge),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(dist),
		},
	})

	// macOS：启动期先装最小占位菜单，否则 Wails 会在前端挂载前装默认英文菜单
	// （App/File/Edit/View/Window/Help）并闪现；前端就绪后经 Bridge.SetMenu 整树替换。
	// 同时阻止 AppKit 向“编辑”菜单尾部自动追加英文系统项（听写/表情/自动填充/写作工具），
	// 拦不住的由 pruneMenuTail 在菜单装好后兜底修剪。
	if runtime.GOOS == "darwin" {
		suppressAutomaticMenuItems()
		menu, editLabel, editCount := startupMacMenu(labels)
		wapp.Menu.SetApplicationMenu(menu)
		pruneMenuTail(editLabel, editCount)
	}

	winOpts := application.WebviewWindowOptions{
		Name:             mainWindowName,
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
	mainWin := wapp.Window.NewWithOptions(winOpts)

	// 常驻系统托盘：Windows 通知区域 / macOS 菜单栏 / Linux 状态区，支持显示主窗口与退出。
	setupSystemTray(wapp, mainWin, labels)

	return wapp.Run()
}
