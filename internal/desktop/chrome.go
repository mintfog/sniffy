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

// ApplyPlatformChrome 按平台设置窗口标题栏外观。前端 TitleBar/MiniTitleBar 与之配套：
//   - Windows：无边框，标题栏（品牌/菜单/窗口按钮）全部由前端自绘合为一行。
//     WebView2 无法在系统原生 caption 内嵌入自定义内容，故只能自绘。
//   - macOS：原生标题栏透明化（HiddenInset：保留红绿灯并稍内收，Web 内容延伸到整窗）。
//     不透明的系统标题栏跟随的是系统外观而非应用主题（系统浅色 + 应用深色 = 白条），
//     透明化后标题栏区域露出的是网页自身底色，深/亮主题都天然一致。前端在 mac 上渲染
//     一条可拖拽标题条托住红绿灯（TitleBar/MiniTitleBar 的 mac 模式，拖拽走
//     --wails-draggable CSS）。菜单不在窗口内自绘，而在顶部系统菜单栏（见 menu.go /
//     Bridge.SetMenu，前端 nativeMenu.ts 把菜单模型推过来）。
//   - Linux：交给窗口管理器画原生装饰（CSD 在各桌面环境表现不一，原生最稳），前端仍把
//     TitleBar 当普通菜单栏自绘在窗口内。
func ApplyPlatformChrome(opts *application.WebviewWindowOptions) {
	switch runtime.GOOS {
	case "windows":
		opts.Frameless = true
	case "darwin":
		opts.Mac.TitleBar = application.MacTitleBarHiddenInset
	}
	// linux：保持默认（非 Frameless，原生窗口装饰）
}
