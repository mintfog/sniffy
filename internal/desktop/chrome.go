// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

//go:build desktop

package desktop

import (
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// ApplyPlatformChrome 按平台设置窗口标题栏外观。前端 TitleBar 与之配套：
//   - Windows：无边框，标题栏（品牌/菜单/窗口按钮）全部由前端自绘合为一行。
//     WebView2 无法在系统原生 caption 内嵌入自定义内容，故只能自绘。
//   - macOS：保留系统原生红绿灯，标题栏隐藏内嵌、Web 内容铺满整窗（前端给左上留白避开红绿灯）。
//     这是 macOS 原生支持的“集成标题栏”，按钮仍是系统画的。
//   - Linux：交给窗口管理器画原生装饰（CSD 在各桌面环境表现不一，原生最稳），前端不自绘。
//
// 这样自绘代码只存在 Windows 一处，跨平台维护面收敛、不发散。
func ApplyPlatformChrome(opts *options.App) {
	switch runtime.GOOS {
	case "windows":
		opts.Frameless = true
	case "darwin":
		opts.Mac = &mac.Options{TitleBar: mac.TitleBarHiddenInset()}
	}
	// linux：保持默认（Frameless=false，原生窗口装饰）
}
