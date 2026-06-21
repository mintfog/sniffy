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
// 托盘菜单文案跟随应用内语言：初始按启动期机器语言渲染(labelsFor(uiLang()),避免英文闪现),
// 随后订阅前端广播的 lang_changed 事件(见 web/src/i18n/bridge.ts),在用户切换语言或前端启动初次
// 广播时重建菜单,使托盘与界面语言保持一致。tray.SetMenu 内部已 InvokeSync 到主线程,故可在事件
// 回调的 goroutine 中直接调用。
func setupSystemTray(wapp *application.App, mainWin application.Window, lb menuLabels) {
	tray := wapp.SystemTray.New()
	if len(appIcon) > 0 {
		tray.SetIcon(appIcon)
	}
	tray.SetTooltip("Sniffy") // Windows 悬浮提示;macOS 忽略

	tray.SetMenu(buildTrayMenu(mainWin, lb))

	// 左键单击显示主窗口。设了 clickHandler 后,macOS 左键不再自动弹菜单(走此 handler),
	// 右键仍由智能默认弹出上面的菜单;Windows 左键显示窗口、右键弹菜单。
	tray.OnClick(func() { showWindow(mainWin) })

	// 跟随应用内语言：前端切换语言(设置页 changeLang)或启动期初次广播当前语言时都会发此事件,
	// 据其语言码用对应文案重建托盘菜单。
	wapp.Event.On("lang_changed", func(e *application.CustomEvent) {
		lang := langFromEvent(e)
		if lang == "" {
			return
		}
		tray.SetMenu(buildTrayMenu(mainWin, labelsFor(normalizeLocale(lang))))
	})
}

// buildTrayMenu 按给定语言标签构建托盘右键菜单(显示主窗口 / 退出)。
func buildTrayMenu(mainWin application.Window, lb menuLabels) *application.Menu {
	menu := application.NewMenu()
	menu.Add(lb.show).OnClick(func(*application.Context) { showWindow(mainWin) })
	menu.AddSeparator()
	menu.Add(lb.quit).OnClick(func(*application.Context) {
		if a := application.Get(); a != nil {
			a.Quit()
		}
	})
	return menu
}

// langFromEvent 从 lang_changed 事件取出语言码。前端 Events.Emit('lang_changed', lang) 的负载
// 在 Go 侧可能是字符串或单元素数组(与 i18n/bridge.ts 中 Array.isArray 的解析对称),两者都兼容。
func langFromEvent(e *application.CustomEvent) string {
	switch v := e.Data.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
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
