// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	_ "embed"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appIcon 是嵌入的应用图标（源自 build/appicon.png，1024×1024 PNG）。
// 同时用于三处：application.Options.Icon（macOS Dock / Windows 窗口与任务栏 / 关于框）与系统托盘。
// 各平台由 Wails 缩放到对应尺寸（Windows 取小图标度量、macOS 取菜单栏厚度），故单张大图即可。
//
//go:embed appicon.png
var appIcon []byte

// setupSystemTray 创建常驻系统托盘图标:Windows 通知区域 / macOS 菜单栏 / Linux 状态区。
//   - 左键单击:显示并聚焦主窗口(Windows、macOS 行为一致)。
//   - 右键单击:弹出菜单(显示主窗口 / 退出);macOS 上由 Wails 智能默认把右键映射到 ShowMenu。
//
// 托盘图标复用 appIcon(彩色图)。注意 macOS 不用模板图(SetTemplateIcon)——模板图只取 alpha
// 通道渲染成单色,彩色 logo 会变成纯黑块,故这里用 SetIcon 保留原色。
//
// 托盘菜单标签取启动期机器语言(labelsFor(uiLang())),不随应用内语言切换实时更新——这与启动占位
// 菜单(menu.go)同源,属可接受的低频静态文案。
func setupSystemTray(wapp *application.App, mainWin application.Window, lb menuLabels) {
	tray := wapp.SystemTray.New()
	if len(appIcon) > 0 {
		tray.SetIcon(appIcon)
	}
	tray.SetTooltip("Sniffy") // Windows 悬浮提示;macOS 忽略

	menu := application.NewMenu()
	menu.Add(lb.show).OnClick(func(*application.Context) { showWindow(mainWin) })
	menu.AddSeparator()
	menu.Add(lb.quit).OnClick(func(*application.Context) {
		if a := application.Get(); a != nil {
			a.Quit()
		}
	})
	tray.SetMenu(menu)

	// 左键单击显示主窗口。设了 clickHandler 后,macOS 左键不再自动弹菜单(走此 handler),
	// 右键仍由智能默认弹出上面的菜单;Windows 左键显示窗口、右键弹菜单。
	tray.OnClick(func() { showWindow(mainWin) })
}

// showWindow 还原(若最小化)并把窗口带到前台。
func showWindow(win application.Window) {
	if win == nil {
		return
	}
	if win.IsMinimised() {
		win.Restore()
	}
	win.Show()
	win.Focus()
}
