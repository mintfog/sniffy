// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ApplyPlatformChrome 按平台设置窗口标题栏外观。前端 TitleBar 与之配套：
//   - Windows：无边框，标题栏（品牌/菜单/窗口按钮）全部由前端自绘合为一行。
//     WebView2 无法在系统原生 caption 内嵌入自定义内容，故只能自绘。
//   - macOS：完全用系统原生标题栏（系统画窗口标题 + 红绿灯，Web 内容下移）。
//     菜单不再在窗口内自绘，而是搬到顶部系统菜单栏（见 menu.go / Bridge.SetMenu，
//     前端 nativeMenu.ts 把菜单模型推过来）。前端在 mac 上不渲染 TitleBar/MiniTitleBar。
//   - Linux：交给窗口管理器画原生装饰（CSD 在各桌面环境表现不一，原生最稳），前端仍把
//     TitleBar 当普通菜单栏自绘在窗口内。
//
// 这样窗口内自绘只剩 Windows（整条）与 Linux（仅菜单栏）两处，mac 全交给系统。
func ApplyPlatformChrome(opts *application.WebviewWindowOptions) {
	switch runtime.GOOS {
	case "windows":
		opts.Frameless = true
	case "darwin":
		opts.Mac.TitleBar = application.MacTitleBarDefault
	}
	// linux：保持默认（非 Frameless，原生窗口装饰）
}
