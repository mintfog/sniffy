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

//go:embed appicon.png
var appIcon []byte

func setupSystemTray(wapp *application.App, mainWin application.Window, labels menuLabels) {
	tray := wapp.SystemTray.New()
	if len(appIcon) > 0 {
		// macOS 模板图仅保留 alpha 通道，使用普通图标以保留彩色 logo。
		tray.SetIcon(appIcon)
	}
	tray.SetTooltip("Sniffy")

	tray.SetMenu(buildTrayMenu(mainWin, labels))

	// macOS 设置左键回调后，右键仍由 Wails 默认弹出菜单。
	tray.OnClick(func() { showWindow(mainWin) })

	// 前端首次挂载和切换语言时广播，覆盖启动期的机器语言。
	wapp.Event.On("lang_changed", func(e *application.CustomEvent) {
		lang, ok := e.Data.(string)
		if !ok || lang == "" {
			return
		}
		labels := labelsFor(normalizeLocale(lang))
		tray.SetMenu(buildTrayMenu(mainWin, labels))
	})
}

func buildTrayMenu(mainWin application.Window, labels menuLabels) *application.Menu {
	menu := application.NewMenu()
	menu.Add(labels.show).OnClick(func(*application.Context) { showWindow(mainWin) })
	menu.AddSeparator()
	menu.Add(labels.quit).OnClick(func(*application.Context) {
		if a := application.Get(); a != nil {
			a.Quit()
		}
	})
	return menu
}

func showWindow(win application.Window) {
	if win == nil {
		return
	}
	restoreMinimisedWindow(win)
	win.Show()
	win.Focus()
}
